// Package cleanup deletes chapters that every required reader has finished
// (read-based cleanup). It is off by default and always explainable through
// a preview. Deleted chapters become "cleaned" and are never re-downloaded
// unless the user restores them.
package cleanup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/fsutil"
	"github.com/Asion001/mangarr/internal/history"
	"github.com/Asion001/mangarr/internal/library"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

// Rules are the effective rules for one series.
type Rules struct {
	Enabled          bool
	Statuses         []string
	RequiredReaders  []int64
	IgnoreNotStarted bool
	KeepLastRead     int
	GraceDays        int
}

type ChapterData struct {
	Chapter model.Chapter
	File    model.ChapterFile
}

// SeriesData is what the rule engine needs about one series.
type SeriesData struct {
	Series   model.Series
	Chapters []ChapterData
	// States: chapter id -> reader id -> state
	States map[int64]map[int64]model.ChapterReadState
}

type Candidate struct {
	SeriesID    int64     `json:"seriesId"`
	SeriesTitle string    `json:"seriesTitle"`
	ChapterID   int64     `json:"chapterId"`
	Chapter     string    `json:"chapter"`
	FileID      int64     `json:"fileId"`
	Path        string    `json:"path"`
	Size        int64     `json:"size"`
	LastReadAt  time.Time `json:"lastReadAt"`
}

type Skip struct {
	SeriesID    int64  `json:"seriesId"`
	SeriesTitle string `json:"seriesTitle"`
	Reason      string `json:"reason"`
}

// Evaluate applies rules to one series and returns deletable chapters (or a
// reason why nothing applies).
func Evaluate(r Rules, sd SeriesData, now time.Time) ([]Candidate, string) {
	if !r.Enabled {
		return nil, "cleanup disabled"
	}
	if len(r.Statuses) > 0 && !contains(r.Statuses, sd.Series.Status) {
		return nil, "series status " + sd.Series.Status + " not in scope"
	}
	required := append([]int64(nil), r.RequiredReaders...)
	if r.IgnoreNotStarted {
		started := map[int64]bool{}
		for _, byReader := range sd.States {
			for rid, st := range byReader {
				if st.Completed || st.Page > 0 {
					started[rid] = true
				}
			}
		}
		kept := required[:0]
		for _, rid := range required {
			if started[rid] {
				kept = append(kept, rid)
			}
		}
		required = kept
	}
	if len(required) == 0 {
		return nil, "no required reader has started this series"
	}
	type done struct {
		cd     ChapterData
		lastAt time.Time
	}
	var completed []done
	for _, cd := range sd.Chapters {
		byReader := sd.States[cd.Chapter.ID]
		all := true
		var last time.Time
		for _, rid := range required {
			st, ok := byReader[rid]
			if !ok || !st.Completed {
				all = false
				break
			}
			at := st.SyncedAt
			if st.ReadAt != nil {
				at = *st.ReadAt
			}
			if at.After(last) {
				last = at
			}
		}
		if all {
			completed = append(completed, done{cd, last})
		}
	}
	if len(completed) == 0 {
		return nil, "no chapter has been finished by all required readers"
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].cd.Chapter.NumberSort < completed[j].cd.Chapter.NumberSort })
	keep := r.KeepLastRead
	if keep < 0 {
		keep = 0
	}
	if keep >= len(completed) {
		return nil, fmt.Sprintf("keeping the last %d read chapters", keep)
	}
	completed = completed[:len(completed)-keep]
	grace := time.Duration(r.GraceDays) * 24 * time.Hour
	var out []Candidate
	for _, c := range completed {
		if now.Sub(c.lastAt) < grace {
			continue
		}
		out = append(out, Candidate{SeriesID: sd.Series.ID, SeriesTitle: sd.Series.Title, ChapterID: c.cd.Chapter.ID,
			Chapter: c.cd.Chapter.NumberKey, FileID: c.cd.File.ID, Size: c.cd.File.Size, LastReadAt: c.lastAt})
	}
	if len(out) == 0 {
		return nil, fmt.Sprintf("finished chapters are still within the %d day grace period", r.GraceDays)
	}
	return out, ""
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// ---- service -------------------------------------------------------------------

type Cleaner struct {
	db       *db.DB
	settings *settings.Store
	lib      *library.Library
	bus      *events.Bus
	log      *slog.Logger
}

func New(d *db.DB, st *settings.Store, lib *library.Library, bus *events.Bus, log *slog.Logger) *Cleaner {
	return &Cleaner{db: d, settings: st, lib: lib, bus: bus, log: log}
}

type Plan struct {
	Enabled    bool        `json:"enabled"`
	DryRun     bool        `json:"dryRun"`
	Candidates []Candidate `json:"candidates"`
	Skipped    []Skip      `json:"skipped"`
	TotalSize  int64       `json:"totalSize"`
}

// Plan computes what a cleanup run would delete.
func (c *Cleaner) Plan(ctx context.Context) (*Plan, error) {
	cs, err := c.settings.Cleanup(ctx)
	if err != nil {
		return nil, err
	}
	plan := &Plan{Enabled: cs.Enabled, DryRun: cs.DryRun, Candidates: []Candidate{}, Skipped: []Skip{}}

	var readers []model.Reader
	if err := c.db.NewSelect().Model(&readers).Scan(ctx); err != nil {
		return nil, err
	}
	var required []int64
	for _, r := range readers {
		if !r.CountForCleanup {
			continue
		}
		if len(cs.ReaderIDs) > 0 && !containsID(cs.ReaderIDs, r.ID) {
			continue
		}
		required = append(required, r.ID)
	}
	var profiles []model.Profile
	if err := c.db.NewSelect().Model(&profiles).Scan(ctx); err != nil {
		return nil, err
	}
	profileBy := map[int64]model.Profile{}
	for _, p := range profiles {
		profileBy[p.ID] = p
	}
	var tags []model.Tag
	_ = c.db.NewSelect().Model(&tags).Scan(ctx)
	tagLabel := map[int64]string{}
	for _, t := range tags {
		tagLabel[t.ID] = t.Label
	}
	var roots []model.RootFolder
	_ = c.db.NewSelect().Model(&roots).Scan(ctx)
	rootSkip := map[int64]string{}
	if cs.MinFreeSpaceGB > 0 {
		for _, r := range roots {
			if free, err := fsutil.FreeSpace(r.Path); err == nil && free >= uint64(cs.MinFreeSpaceGB)<<30 {
				rootSkip[r.ID] = fmt.Sprintf("%s has more than %d GB free", r.Path, cs.MinFreeSpaceGB)
			}
		}
	}

	var series []model.Series
	if err := c.db.NewSelect().Model(&series).Order("sort_title").Scan(ctx); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for _, s := range series {
		rules := Rules{Enabled: cs.Enabled, Statuses: cs.Statuses, RequiredReaders: required,
			IgnoreNotStarted: cs.IgnoreReadersNotStarted, KeepLastRead: cs.KeepLastRead, GraceDays: cs.GraceDays}
		if p, ok := profileBy[s.ProfileID]; ok {
			o := p.Config.Cleanup
			if o.Enabled != nil {
				rules.Enabled = *o.Enabled
			}
			if o.KeepLastRead != nil {
				rules.KeepLastRead = *o.KeepLastRead
			}
			if o.GraceDays != nil {
				rules.GraceDays = *o.GraceDays
			}
		}
		if !rules.Enabled {
			continue
		}
		if reason, ok := rootSkip[s.RootFolderID]; ok {
			plan.Skipped = append(plan.Skipped, Skip{s.ID, s.Title, reason})
			continue
		}
		if t := excludedTag(s.Tags, tagLabel, cs.ExcludeTags); t != "" {
			plan.Skipped = append(plan.Skipped, Skip{s.ID, s.Title, "tagged " + t})
			continue
		}
		sd, err := c.loadSeries(ctx, s)
		if err != nil {
			return nil, err
		}
		cands, reason := Evaluate(rules, sd, now)
		if reason != "" {
			plan.Skipped = append(plan.Skipped, Skip{s.ID, s.Title, reason})
			continue
		}
		dir, _ := c.lib.SeriesDir(ctx, &s)
		for _, cd := range cands {
			for _, ch := range sd.Chapters {
				if ch.Chapter.ID == cd.ChapterID {
					cd.Path = filepath.Join(dir, ch.File.RelativePath)
				}
			}
			plan.Candidates = append(plan.Candidates, cd)
			plan.TotalSize += cd.Size
		}
	}
	return plan, nil
}

func (c *Cleaner) loadSeries(ctx context.Context, s model.Series) (SeriesData, error) {
	sd := SeriesData{Series: s, States: map[int64]map[int64]model.ChapterReadState{}}
	var rows []struct {
		model.Chapter
		FID          int64  `bun:"fid"`
		RelativePath string `bun:"relative_path"`
		Size         int64  `bun:"size"`
	}
	err := c.db.NewSelect().TableExpr("chapters AS c").
		ColumnExpr("c.*, f.id AS fid, f.relative_path, f.size").
		Join("JOIN chapter_files AS f ON f.id = c.file_id").
		Where("c.series_id = ?", s.ID).Scan(ctx, &rows)
	if err != nil {
		return sd, err
	}
	for _, r := range rows {
		sd.Chapters = append(sd.Chapters, ChapterData{Chapter: r.Chapter, File: model.ChapterFile{ID: r.FID, RelativePath: r.RelativePath, Size: r.Size}})
	}
	var states []model.ChapterReadState
	if err := c.db.NewSelect().Model(&states).Where("series_id = ?", s.ID).Scan(ctx); err != nil {
		return sd, err
	}
	for _, st := range states {
		if sd.States[st.ChapterID] == nil {
			sd.States[st.ChapterID] = map[int64]model.ChapterReadState{}
		}
		sd.States[st.ChapterID][st.ReaderID] = st
	}
	return sd, nil
}

func excludedTag(ids []int64, labels map[int64]string, excluded []string) string {
	for _, id := range ids {
		if contains(excluded, labels[id]) {
			return labels[id]
		}
	}
	return ""
}

func containsID(xs []int64, id int64) bool {
	for _, x := range xs {
		if x == id {
			return true
		}
	}
	return false
}

// Run executes a cleanup (or only plans it when dry run is on).
func (c *Cleaner) Run(ctx context.Context, force bool) (*Plan, int, error) {
	plan, err := c.Plan(ctx)
	if err != nil {
		return nil, 0, err
	}
	if len(plan.Candidates) == 0 || (plan.DryRun && !force) {
		return plan, 0, nil
	}
	cs, _ := c.settings.Cleanup(ctx)
	deleted := 0
	var freed int64
	perSeries := map[string][]string{}
	var errs []error
	for _, cd := range plan.Candidates {
		if err := c.remove(ctx, cd, cs.UseRecycleBin, model.HistoryCleaned); err != nil {
			errs = append(errs, fmt.Errorf("%s ch. %s: %w", cd.SeriesTitle, cd.Chapter, err))
			continue
		}
		deleted++
		freed += cd.Size
		perSeries[cd.SeriesTitle] = append(perSeries[cd.SeriesTitle], cd.Chapter)
	}
	if deleted > 0 {
		var items []string
		for title, chs := range perSeries {
			items = append(items, fmt.Sprintf("%s: %d chapters (%s)", title, len(chs), summarize(chs)))
		}
		sort.Strings(items)
		c.bus.Publish(events.Event{Type: events.CleanupDone, Payload: events.MessagePayload{
			Title: fmt.Sprintf("Cleanup freed %s", humanSize(freed)), Message: fmt.Sprintf("%d read chapters removed", deleted), Items: items}})
	}
	return plan, deleted, errors.Join(errs...)
}

func (c *Cleaner) remove(ctx context.Context, cd Candidate, recycle bool, event string) error {
	defer library.RLockSeries(cd.SeriesID)()
	var s model.Series
	if err := c.db.NewSelect().Model(&s).Where("id = ?", cd.SeriesID).Scan(ctx); err != nil {
		return err
	}
	if _, err := os.Stat(cd.Path); err == nil {
		if recycle {
			var file model.ChapterFile
			if err := c.db.NewSelect().Model(&file).Where("id = ?", cd.FileID).Scan(ctx); err != nil {
				return err
			}
			if _, err := c.lib.Recycle(ctx, cd.Path, s.Path, false, library.RecycleInfo{Series: &s, File: &file, Reason: event}); err != nil {
				return err
			}
		} else if err := os.Remove(cd.Path); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	err := c.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().Model((*model.ChapterFile)(nil)).Where("id = ?", cd.FileID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*model.Chapter)(nil)).Set("file_id = NULL").Set("state = ?", model.ChapterCleaned).
			Set("cleaned_at = ?", now).Set("updated_at = ?", now).Where("id = ?", cd.ChapterID).Exec(ctx); err != nil {
			return err
		}
		chID := cd.ChapterID
		data := map[string]string{"size": fmt.Sprint(cd.Size), "recycled": fmt.Sprint(recycle)}
		if event == model.HistoryDeleted {
			data["reason"] = "manual"
		}
		return history.Record(ctx, tx, cd.SeriesID, &chID, event, filepath.Base(cd.Path),
			data)
	})
	if err != nil {
		return err
	}
	c.bus.Publish(events.Event{Type: downloads.EventFileWritten, SeriesID: cd.SeriesID, Payload: cd.Path})
	c.bus.Changed("chapter", "updated", cd.ChapterID)
	return nil
}

type RemoveResult struct {
	Requested int   `json:"requested"`
	Removed   int   `json:"removed"`
	Skipped   int   `json:"skipped"`
	Freed     int64 `json:"freed"`
}

// RemoveChapters explicitly removes downloaded chapter files and marks the
// chapters cleaned, so a refresh cannot immediately download them again.
func (c *Cleaner) RemoveChapters(ctx context.Context, chapterIDs []int64) (*RemoveResult, error) {
	unique := map[int64]bool{}
	ids := make([]int64, 0, len(chapterIDs))
	for _, id := range chapterIDs {
		if id > 0 && !unique[id] {
			unique[id] = true
			ids = append(ids, id)
		}
	}
	result := &RemoveResult{Requested: len(ids)}
	if len(ids) == 0 {
		return result, nil
	}
	if len(ids) > 1000 {
		return result, errors.New("at most 1000 chapters can be deleted at once")
	}
	type row struct {
		ChapterID   int64  `bun:"chapter_id"`
		Chapter     string `bun:"chapter"`
		SeriesID    int64  `bun:"series_id"`
		SeriesTitle string `bun:"series_title"`
		SeriesPath  string `bun:"series_path"`
		RootID      int64  `bun:"root_id"`
		FileID      int64  `bun:"file_id"`
		Relative    string `bun:"relative_path"`
		Size        int64  `bun:"size"`
	}
	var rows []row
	err := c.db.NewSelect().TableExpr("chapters AS ch").
		ColumnExpr("ch.id AS chapter_id, ch.number_key AS chapter, ch.series_id AS series_id").
		ColumnExpr("s.title AS series_title, s.path AS series_path, s.root_folder_id AS root_id").
		ColumnExpr("f.id AS file_id, f.relative_path AS relative_path, f.size AS size").
		Join("JOIN series AS s ON s.id = ch.series_id").Join("JOIN chapter_files AS f ON f.id = ch.file_id").
		Where("ch.id IN (?)", bun.In(ids)).
		Where("NOT EXISTS (SELECT 1 FROM download_jobs AS j WHERE j.chapter_id = ch.id AND j.status NOT IN (?, ?))", model.JobCompleted, model.JobFailed).
		Scan(ctx, &rows)
	if err != nil {
		return result, err
	}
	result.Skipped = len(ids) - len(rows)
	cs, err := c.settings.Cleanup(ctx)
	if err != nil {
		return result, err
	}
	var errs []error
	for _, r := range rows {
		s := model.Series{ID: r.SeriesID, Title: r.SeriesTitle, Path: r.SeriesPath, RootFolderID: r.RootID}
		dir, err := c.lib.SeriesDir(ctx, &s)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s ch. %s: %w", r.SeriesTitle, r.Chapter, err))
			continue
		}
		cd := Candidate{SeriesID: r.SeriesID, SeriesTitle: r.SeriesTitle, ChapterID: r.ChapterID, Chapter: r.Chapter,
			FileID: r.FileID, Path: filepath.Join(dir, r.Relative), Size: r.Size}
		if err := c.remove(ctx, cd, cs.UseRecycleBin, model.HistoryDeleted); err != nil {
			errs = append(errs, fmt.Errorf("%s ch. %s: %w", r.SeriesTitle, r.Chapter, err))
			continue
		}
		result.Removed++
		result.Freed += r.Size
	}
	result.Skipped += len(errs)
	return result, errors.Join(errs...)
}

// Restore makes a cleaned chapter wanted again.
func (c *Cleaner) Restore(ctx context.Context, chapterID int64) (*model.Chapter, error) {
	var ch model.Chapter
	if err := c.db.NewSelect().Model(&ch).Where("id = ?", chapterID).Scan(ctx); err != nil {
		return nil, err
	}
	if ch.State != model.ChapterCleaned {
		return &ch, nil
	}
	ch.State, ch.CleanedAt, ch.Monitored, ch.UpdatedAt = model.ChapterMissing, nil, true, time.Now().UTC()
	if _, err := c.db.NewUpdate().Model(&ch).Column("state", "cleaned_at", "monitored", "updated_at").WherePK().Exec(ctx); err != nil {
		return nil, err
	}
	chID := ch.ID
	_ = history.Record(ctx, c.db, ch.SeriesID, &chID, model.HistoryRestored, "", nil)
	c.bus.Changed("chapter", "updated", ch.ID)
	return &ch, nil
}

func summarize(chs []string) string {
	if len(chs) <= 3 {
		return strings.Join(chs, ", ")
	}
	return chs[0] + "–" + chs[len(chs)-1]
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d KB", n>>10)
	}
}
