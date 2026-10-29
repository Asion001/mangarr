// Package refresh synchronizes chapter lists from source modules into the
// database (the equivalent of Sonarr's RSS sync + series refresh).
package refresh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/chapternum"
	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/history"
	"github.com/Asion001/mangarr/internal/jobs"
	"github.com/Asion001/mangarr/internal/library"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/sourcecache"
	"github.com/Asion001/mangarr/internal/sourcegov"
)

type Refresher struct {
	db       *db.DB
	bus      *events.Bus
	mods     *modules.Manager
	settings *settings.Store
	searcher *downloads.Searcher
	lib      *library.Library
	log      *slog.Logger

	sourceLocks sync.Map // sourceID -> *sync.Mutex
	seriesLocks sync.Map // seriesID -> *sync.Mutex

	// Gov (optional) paces checks per catalog and reports cooldowns.
	Gov *sourcegov.Governor
	// Cache/Gen (optional) let the first check of a new series reuse the
	// details fetched while adding it.
	Cache *sourcecache.Cache
	Gen   func() int64
}

func New(d *db.DB, bus *events.Bus, mods *modules.Manager, st *settings.Store, searcher *downloads.Searcher, lib *library.Library, log *slog.Logger) *Refresher {
	return &Refresher{db: d, bus: bus, mods: mods, settings: st, searcher: searcher, lib: lib, log: log}
}

type Result struct {
	NewChapters int `json:"newChapters"`
	NewReleases int `json:"newReleases"`
	Grabbed     int `json:"grabbed"`
	Failed      int `json:"failed"`
}

// SyncSeries refreshes every enabled source of a series, applies pending add
// options and evaluates downloads.
func (r *Refresher) SyncSeries(ctx context.Context, seriesID int64, onlyDue bool) (Result, error) {
	// one sync per series at a time (RefreshSources and RefreshSeries may overlap)
	v, _ := r.seriesLocks.LoadOrStore(seriesID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	var res Result
	var s model.Series
	if err := r.db.NewSelect().Model(&s).Where("id = ?", seriesID).Scan(ctx); err != nil {
		return res, err
	}
	var sources []model.SeriesSource
	q := r.db.NewSelect().Model(&sources).Where("series_id = ? AND enabled = ?", seriesID, true).Order("priority", "id")
	if onlyDue {
		q = q.Where("next_check_at <= ?", time.Now().UTC())
	}
	if err := q.Scan(ctx); err != nil {
		return res, err
	}
	var details *source.MangaDetails
	var lastErr error
	succeeded := 0
	for i := range sources {
		ss := &sources[i]
		det, nc, nr, err := r.syncSource(ctx, &s, ss)
		if err != nil {
			res.Failed++
			lastErr = err
			r.log.Warn("source sync failed", "series", s.Title, "source", ss.SourceName, "err", err)
			continue
		}
		succeeded++
		res.NewChapters += nc
		res.NewReleases += nr
		if details == nil {
			details = det
		}
	}
	if succeeded == 0 && len(sources) > 0 {
		return res, lastErr
	}
	if details != nil && sourceOnlyMetadata(&s) {
		md, prov := metadataagg.Merge([]metadata.SeriesMetadata{metadataagg.FromSource(details)})
		if metadataagg.Apply(&s, &metadataagg.Resolved{Metadata: md, Provenance: prov}) {
			s.UpdatedAt = time.Now().UTC()
			if _, err := r.db.NewUpdate().Model(&s).Column("title", "status", "metadata", "reading_direction", "updated_at").WherePK().Exec(ctx); err != nil {
				return res, err
			}
		}
	}
	if s.AddOptions.Pending && succeeded > 0 {
		if err := r.applyAddOptions(ctx, &s); err != nil {
			return res, err
		}
		r.writeSidecars(ctx, &s, sources)
		if !s.AddOptions.SearchMissing {
			r.bus.Changed("series", "updated", s.ID)
			return res, nil
		}
	}
	grabbed, err := r.searcher.Evaluate(ctx, s.ID, nil, false)
	res.Grabbed = grabbed
	r.bus.Changed("series", "updated", s.ID)
	return res, err
}

// sourceOnlyMetadata reports whether no metadata provider filled the
// series in: none is linked, or the linked one was down when it was added.
func sourceOnlyMetadata(s *model.Series) bool {
	return !s.Metadata.FromProviders()
}

func (r *Refresher) lockSource(sourceID string) func() {
	v, _ := r.sourceLocks.LoadOrStore(sourceID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

var backoffSteps = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour}

// Backoff returns the escalating delay after n consecutive failures.
func Backoff(n int) time.Duration {
	if n <= 0 {
		return 0
	}
	if n > len(backoffSteps) {
		n = len(backoffSteps)
	}
	return backoffSteps[n-1]
}

// Interval returns the check interval for a source link.
func (r *Refresher) Interval(ctx context.Context, s *model.Series, ss *model.SeriesSource) time.Duration {
	if ss.CheckIntervalMinutes > 0 {
		return time.Duration(ss.CheckIntervalMinutes) * time.Minute
	}
	dl, _ := r.settings.Downloads(ctx)
	base := time.Duration(dl.DefaultCheckIntervalMinutes) * time.Minute
	if base <= 0 {
		base = 6 * time.Hour
	}
	switch s.Status {
	case model.StatusHiatus:
		return 24 * time.Hour
	case model.StatusCompleted, model.StatusCancelled:
		return 7 * 24 * time.Hour
	}
	return base
}

func jitter(d time.Duration) time.Duration {
	j := time.Duration(float64(d) * 0.1 * (rand.Float64()*2 - 1))
	return d + j
}

var titlePrefix = regexp.MustCompile(`(?i)^\s*(?:(?:vol(?:ume)?\.?\s*[0-9]+(?:\.[0-9]+)?)\s*)?(?:(?:ch(?:apter)?|ep(?:isode)?|#)\.?\s*[0-9]+(?:[.,][0-9]+)?[a-z]?)`)

// ChapterTitle extracts a title from names like "Vol.2 Ch.10 - The Fight".
func ChapterTitle(name string) string {
	loc := titlePrefix.FindStringIndex(name)
	if loc == nil {
		return ""
	}
	rest := strings.TrimSpace(name[loc[1]:])
	explicit := false
	if runes := []rune(rest); len(runes) > 0 && strings.ContainsRune("-:–|", runes[0]) {
		rest = strings.TrimSpace(string(runes[1:]))
		explicit = true
	}
	// Some catalogs spell an alphabetic chapter suffix with a space
	// ("Chapter 78 P"). It is part of the number, not a one-letter title.
	// An explicit separator still permits real titles such as "Ch. 78 - X".
	if !explicit && singleLetter(rest) {
		return ""
	}
	return rest
}

func singleLetter(s string) bool {
	r := []rune(strings.TrimSpace(s))
	return len(r) == 1 && unicode.IsLetter(r[0])
}

// reconciledChapterTitle repairs the old spaced-suffix parsing result when a
// chapter is refreshed. All current releases are considered so a genuine
// one-letter title with an explicit separator is preserved.
func reconciledChapterTitle(current string, releaseNames []string) (string, bool) {
	if !singleLetter(current) {
		return current, false
	}
	next := ""
	for _, name := range releaseNames {
		title := ChapterTitle(name)
		if strings.EqualFold(title, current) {
			return title, title != current
		}
		if next == "" && title != "" {
			next = title
		}
	}
	return next, next != current
}

// recentDetails returns details fetched in the last 10 minutes (while the
// user was adding the series) for the first check of a new series.
func (r *Refresher) recentDetails(s *model.Series, ss *model.SeriesSource) *sourcecache.Details {
	if r.Cache == nil || r.Gen == nil || !s.AddOptions.Pending || ss.LastCheckedAt != nil {
		return nil
	}
	v, ok := r.Cache.Get(sourcecache.DetailsKey(r.Gen(), ss.ModuleID, ss.SourceID, ss.MangaURL))
	if !ok {
		return nil
	}
	d := v.(*sourcecache.Details)
	if d.Details == nil || time.Since(d.FetchedAt) > 10*time.Minute {
		return nil
	}
	return d
}

// syncSource fetches one source and upserts chapters/releases.
func (r *Refresher) syncSource(ctx context.Context, s *model.Series, ss *model.SeriesSource) (*source.MangaDetails, int, int, error) {
	unlock := r.lockSource(ss.SourceID)
	defer unlock()

	now := time.Now().UTC()
	fail := func(err error) (*source.MangaDetails, int, int, error) {
		ss.ConsecutiveFailures++
		b := now.Add(Backoff(ss.ConsecutiveFailures))
		ss.BackoffUntil = &b
		ss.LastCheckedAt = &now
		ss.LastError = err.Error()
		ss.NextCheckAt = b
		_, _ = r.db.NewUpdate().Model(ss).Column("consecutive_failures", "backoff_until", "last_checked_at", "last_error", "next_check_at").WherePK().Exec(ctx)
		r.bus.Changed("seriessource", "updated", ss.ID)
		return nil, 0, 0, err
	}
	// a throttled catalog is checked again when its cooldown ends; that isn't
	// a failure of this series
	postpone := func(until time.Time, err error) (*source.MangaDetails, int, int, error) {
		ss.NextCheckAt, ss.LastError = until, err.Error()
		_, _ = r.db.NewUpdate().Model(ss).Column("next_check_at", "last_error").WherePK().Exec(ctx)
		r.bus.Changed("seriessource", "updated", ss.ID)
		return nil, 0, 0, err
	}
	key := sourcegov.Key{ModuleID: ss.ModuleID, SourceID: ss.SourceID}
	if r.Gov != nil {
		if until, reason := r.Gov.Cooldown(key); !until.IsZero() {
			return postpone(until, &sourcegov.ErrCoolingDown{Key: key, Until: until, Reason: reason})
		}
		if err := r.Gov.Pace(ctx, key, "refresh"); err != nil {
			return nil, 0, 0, err
		}
	}
	mod, _, err := modules.GetAs[source.Module](r.mods, ss.ModuleID)
	if err != nil {
		return fail(err)
	}
	title := ss.Title
	if title == "" {
		title = s.Title
	}
	ref := source.MangaRef{SourceID: ss.SourceID, URL: ss.MangaURL, EngineRef: ss.EngineRef, TitleHint: title}
	var det *source.MangaDetails
	var chs []source.Chapter
	if cached := r.recentDetails(s, ss); cached != nil {
		det, chs = cached.Details, cached.Chapters
	} else {
		sctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		det, chs, err = mod.Manga(sctx, ref, true)
		cancel()
	}
	if err != nil {
		if cd, ok := sourcegov.CoolingDown(err); ok {
			return postpone(cd.Until, err)
		}
		if r.Gov != nil {
			if until, _ := r.Gov.Cooldown(key); !until.IsZero() { // this request triggered it
				return postpone(until, err)
			}
		}
		return fail(err)
	}
	if det.Title != "" {
		title = det.Title
	}

	nextCheck := now.Add(jitter(r.Interval(ctx, s, ss)))
	newChapters, newReleases := 0, 0
	err = r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var chapters []model.Chapter
		if err := tx.NewSelect().Model(&chapters).Where("series_id = ?", s.ID).Scan(ctx); err != nil {
			return err
		}
		byKey := map[string]*model.Chapter{}
		for i := range chapters {
			byKey[chapters[i].NumberKey] = &chapters[i]
		}
		var releases []model.ChapterRelease
		if err := tx.NewSelect().Model(&releases).Where("series_source_id = ?", ss.ID).Scan(ctx); err != nil {
			return err
		}
		byURL := map[string]*model.ChapterRelease{}
		for i := range releases {
			byURL[releases[i].ChapterURL] = &releases[i]
		}
		seen := map[string]bool{}
		touched := map[int64]*model.Chapter{}
		for _, c := range chs {
			if seen[c.URL] {
				continue
			}
			seen[c.URL] = true
			var chapterID *int64
			if n, ok := chapternum.Parse(title, c.Name, c.Number); ok {
				key := chapternum.Key(n)
				ch := byKey[key]
				if ch == nil {
					ch = &model.Chapter{SeriesID: s.ID, NumberKey: key, NumberSort: n, Volume: chapternum.Volume(c.Name),
						Title: ChapterTitle(c.Name), Monitored: s.AddOptions.Pending || s.MonitorNew == model.MonitorAll,
						State: model.ChapterMissing, ReleaseDate: c.UploadDate, FirstSeenAt: now, UpdatedAt: now}
					if _, err := tx.NewInsert().Model(ch).Exec(ctx); err != nil {
						return err
					}
					byKey[key] = ch
					newChapters++
				} else {
					changed := false
					if ch.Title == "" {
						if t := ChapterTitle(c.Name); t != "" {
							ch.Title, changed = t, true
						}
					}
					if ch.Volume == "" {
						if v := chapternum.Volume(c.Name); v != "" {
							ch.Volume, changed = v, true
						}
					}
					if c.UploadDate != nil && (ch.ReleaseDate == nil || c.UploadDate.Before(*ch.ReleaseDate)) {
						ch.ReleaseDate, changed = c.UploadDate, true
					}
					if changed {
						ch.UpdatedAt = now
						if _, err := tx.NewUpdate().Model(ch).Column("title", "volume", "release_date", "updated_at").WherePK().Exec(ctx); err != nil {
							return err
						}
					}
				}
				id := ch.ID
				chapterID = &id
				touched[ch.ID] = ch
			}
			rel := byURL[c.URL]
			if rel == nil {
				rel = &model.ChapterRelease{SeriesID: s.ID, ChapterID: chapterID, SeriesSourceID: ss.ID, ChapterURL: c.URL, WebURL: c.WebURL,
					EngineRef: c.EngineRef, Name: c.Name, Scanlator: c.Scanlator, RawNumber: c.Number, UploadDate: c.UploadDate, CreatedAt: now}
				if _, err := tx.NewInsert().Model(rel).Exec(ctx); err != nil {
					return err
				}
				newReleases++
				if chapterID == nil {
					_ = history.Record(ctx, tx, s.ID, nil, model.HistoryUnparsed, c.Name, map[string]string{"source": ss.SourceName, "url": c.URL})
				}
				continue
			}
			rel.ChapterID, rel.WebURL, rel.EngineRef, rel.Name, rel.Scanlator, rel.RawNumber, rel.UploadDate, rel.Removed =
				chapterID, c.WebURL, c.EngineRef, c.Name, c.Scanlator, c.Number, c.UploadDate, false
			if _, err := tx.NewUpdate().Model(rel).Column("chapter_id", "web_url", "engine_ref", "name", "scanlator", "raw_number", "upload_date", "removed").WherePK().Exec(ctx); err != nil {
				return err
			}
		}
		// releases no longer listed by the source
		for url, rel := range byURL {
			if !seen[url] && !rel.Removed {
				if _, err := tx.NewUpdate().Model(rel).Set("removed = ?", true).WherePK().Exec(ctx); err != nil {
					return err
				}
			}
		}
		// Releases are the source of chapter titles. Earlier versions treated a
		// spaced alphabetic number suffix as a one-letter title and then never
		// revisited the stored value. Repair only that narrow shape, using every
		// still-present release so a legitimate title from another source wins.
		for id, ch := range touched {
			if !singleLetter(ch.Title) {
				continue
			}
			var names []string
			if err := tx.NewSelect().TableExpr("chapter_releases").Column("name").
				Where("chapter_id = ? AND removed = ?", id, false).OrderExpr("id").Scan(ctx, &names); err != nil {
				return err
			}
			if title, changed := reconciledChapterTitle(ch.Title, names); changed {
				ch.Title, ch.UpdatedAt = title, now
				if _, err := tx.NewUpdate().Model(ch).Column("title", "updated_at").WherePK().Exec(ctx); err != nil {
					return err
				}
			}
		}
		ss.EngineRef = det.EngineRef
		if ss.Title == "" {
			ss.Title = det.Title
		}
		if det.WebURL != "" {
			ss.WebURL = det.WebURL
		}
		ss.Rating = nil
		if det.Rating.Valid() {
			ss.Rating = det.Rating
		}
		ss.LastCheckedAt, ss.LastSuccessAt = &now, &now
		ss.ConsecutiveFailures, ss.BackoffUntil, ss.LastError = 0, nil, ""
		ss.NextCheckAt = nextCheck
		_, err := tx.NewUpdate().Model(ss).Column("engine_ref", "title", "web_url", "rating", "last_checked_at", "last_success_at",
			"consecutive_failures", "backoff_until", "last_error", "next_check_at").WherePK().Exec(ctx)
		return err
	})
	if err != nil {
		return nil, 0, 0, err
	}
	r.bus.Changed("seriessource", "updated", ss.ID)
	return det, newChapters, newReleases, nil
}

// applyAddOptions sets chapter monitoring after the first successful sync.
func (r *Refresher) applyAddOptions(ctx context.Context, s *model.Series) error {
	var chapters []model.Chapter
	if err := r.db.NewSelect().Model(&chapters).Where("series_id = ?", s.ID).Scan(ctx); err != nil {
		return err
	}
	sort.Slice(chapters, func(i, j int) bool { return chapters[i].NumberSort > chapters[j].NumberSort })
	opt := s.AddOptions
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for i, ch := range chapters {
			mon := false
			switch opt.Monitor {
			case model.MonitorAll, "missing", "":
				mon = true
			case model.MonitorLatest:
				n := opt.LatestCount
				if n <= 0 {
					n = 1
				}
				mon = i < n
			case model.MonitorFrom:
				mon = ch.NumberSort >= opt.FromChapter
			case model.MonitorFuture, model.MonitorNone:
				mon = false
			}
			if mon != ch.Monitored {
				if _, err := tx.NewUpdate().Model((*model.Chapter)(nil)).Set("monitored = ?", mon).Where("id = ?", ch.ID).Exec(ctx); err != nil {
					return err
				}
			}
		}
		s.AddOptions.Pending = false
		_, err := tx.NewUpdate().Model(s).Column("add_options").WherePK().Exec(ctx)
		return err
	})
}

// writeSidecars creates the folder, series.json and cover (with the first
// source's thumbnail as cover fallback).
func (r *Refresher) writeSidecars(ctx context.Context, s *model.Series, sources []model.SeriesSource) {
	defer library.RLockSeries(s.ID)()
	var fallback func(ctx context.Context) (io.ReadCloser, error)
	if len(sources) > 0 {
		ss := sources[0]
		fallback = func(ctx context.Context) (io.ReadCloser, error) {
			th, _, err := modules.GetAs[source.Thumbnails](r.mods, ss.ModuleID)
			if err != nil {
				return nil, err
			}
			body, _, err := th.Thumbnail(ctx, source.MangaRef{SourceID: ss.SourceID, URL: ss.MangaURL, EngineRef: ss.EngineRef})
			return body, err
		}
	}
	if err := r.lib.WriteSidecars(ctx, s, fallback); err != nil {
		r.log.Warn("write series sidecars", "series", s.Title, "err", err)
	}
}

// WriteSidecars is exported for metadata refreshes.
func (r *Refresher) WriteSidecars(ctx context.Context, s *model.Series) {
	var sources []model.SeriesSource
	_ = r.db.NewSelect().Model(&sources).Where("series_id = ?", s.ID).Order("priority").Scan(ctx)
	r.writeSidecars(ctx, s, sources)
}

// RefreshDue is the RefreshSources command: sync every source link that is due.
func (r *Refresher) RefreshDue(ctx context.Context, run *jobs.Run) error {
	var seriesIDs []int64
	err := r.db.NewSelect().TableExpr("series_sources AS ss").ColumnExpr("DISTINCT ss.series_id").
		Join("JOIN series AS s ON s.id = ss.series_id").
		Where("ss.enabled = ? AND s.monitored = ? AND ss.next_check_at <= ?", true, true, time.Now().UTC()).
		Limit(500).Scan(ctx, &seriesIDs)
	if err != nil {
		return err
	}
	if len(seriesIDs) == 0 {
		run.Progress("nothing due")
		return nil
	}
	var mu sync.Mutex
	total := Result{}
	done := 0
	var errs []error
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, id := range seriesIDs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res, err := r.SyncSeries(ctx, id, true)
			mu.Lock()
			defer mu.Unlock()
			done++
			total.NewChapters += res.NewChapters
			total.Grabbed += res.Grabbed
			total.Failed += res.Failed
			if err != nil {
				errs = append(errs, err)
			}
			run.Progress("refreshed %d/%d series, %d new chapters, %d grabbed", done, len(seriesIDs), total.NewChapters, total.Grabbed)
		}()
	}
	wg.Wait()
	run.Progress("refreshed %d series: %d new chapters, %d grabbed, %d source failures", len(seriesIDs), total.NewChapters, total.Grabbed, total.Failed)
	if len(errs) == len(seriesIDs) && len(errs) > 0 {
		return fmt.Errorf("all refreshes failed: %w", errors.Join(errs...))
	}
	return nil
}
