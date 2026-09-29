package downloads

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/decision"
	"github.com/Asion001/mangarr/internal/history"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/sourcepriority"
)

// Searcher runs the decision engine for chapters and enqueues approved releases.
type Searcher struct {
	db    *db.DB
	queue *Queue
	log   *slog.Logger
}

func NewSearcher(d *db.DB, q *Queue, log *slog.Logger) *Searcher {
	return &Searcher{db: d, queue: q, log: log}
}

// seriesState is everything the decision engine needs for one series.
type seriesState struct {
	series    model.Series
	profile   model.Profile
	chapters  []model.Chapter
	releases  map[int64][]decision.Candidate // by chapter id
	byRelease map[int64]decision.Candidate
	files     map[int64]*model.ChapterFile // by chapter id
	queued    map[int64]bool
	blocked   map[string]bool
}

func (s *Searcher) load(ctx context.Context, seriesID int64, chapterIDs []int64) (*seriesState, error) {
	st := &seriesState{releases: map[int64][]decision.Candidate{}, byRelease: map[int64]decision.Candidate{},
		files: map[int64]*model.ChapterFile{}, blocked: map[string]bool{}}
	if err := s.db.NewSelect().Model(&st.series).Where("id = ?", seriesID).Scan(ctx); err != nil {
		return nil, err
	}
	if err := s.db.NewSelect().Model(&st.profile).Where("id = ?", st.series.ProfileID).Scan(ctx); err != nil {
		return nil, err
	}
	q := s.db.NewSelect().Model(&st.chapters).Where("series_id = ?", seriesID)
	if len(chapterIDs) > 0 {
		q = q.Where("id IN (?)", bun.In(chapterIDs))
	}
	if err := q.Order("number_sort").Scan(ctx); err != nil {
		return nil, err
	}
	// The queue is read before the files on purpose: a job stays active until
	// the transaction that inserts its file completes it, so reading it first
	// means a chapter that finishes importing mid-load is seen as queued (and
	// skipped) rather than as missing (and grabbed a second time).
	var err error
	if st.queued, err = s.queue.ActiveChapters(ctx, seriesID); err != nil {
		return nil, err
	}
	var sources []model.SeriesSource
	if err := s.db.NewSelect().Model(&sources).Where("series_id = ?", seriesID).Scan(ctx); err != nil {
		return nil, err
	}
	if err := sourcepriority.Apply(ctx, s.db, st.series, sources); err != nil {
		return nil, err
	}
	srcByID := map[int64]model.SeriesSource{}
	for _, ss := range sources {
		srcByID[ss.ID] = ss
	}
	var rels []model.ChapterRelease
	if err := s.db.NewSelect().Model(&rels).Where("series_id = ? AND chapter_id IS NOT NULL", seriesID).Scan(ctx); err != nil {
		return nil, err
	}
	for _, r := range rels {
		ss, ok := srcByID[r.SeriesSourceID]
		if !ok {
			continue
		}
		c := decision.Candidate{Release: r, Source: ss}
		st.releases[*r.ChapterID] = append(st.releases[*r.ChapterID], c)
		st.byRelease[r.ID] = c
	}
	var files []model.ChapterFile
	if err := s.db.NewSelect().Model(&files).Where("series_id = ?", seriesID).Scan(ctx); err != nil {
		return nil, err
	}
	for i := range files {
		st.files[files[i].ChapterID] = &files[i]
	}
	var bl []model.Blocklist
	if err := s.db.NewSelect().Model(&bl).Where("series_id = ?", seriesID).Scan(ctx); err != nil {
		return nil, err
	}
	for _, b := range bl {
		st.blocked[blockKey(b.SeriesSourceID, b.ChapterURL)] = true
	}
	return st, nil
}

func blockKey(ss int64, url string) string { return strconv.FormatInt(ss, 10) + "|" + url }

func (st *seriesState) input(ch model.Chapter, explicit bool) decision.Input {
	in := decision.Input{Series: st.series, Profile: st.profile, Chapter: ch, Queued: st.queued[ch.ID], Now: time.Now().UTC(), Search: explicit,
		Blocklisted: func(ss int64, url string) bool { return st.blocked[blockKey(ss, url)] }}
	if f := st.files[ch.ID]; f != nil {
		in.CurrentFile = f
		if f.ReleaseID != nil {
			if c, ok := st.byRelease[*f.ReleaseID]; ok {
				in.CurrentRelease = &c
			}
		}
	}
	return in
}

// Evaluate decides and enqueues for chapters of a series (all chapters when
// chapterIDs is empty). explicit=true means a user-initiated search, which
// ignores monitoring and cleaned state.
func (s *Searcher) Evaluate(ctx context.Context, seriesID int64, chapterIDs []int64, explicit bool) (int, error) {
	return s.EvaluateAt(ctx, seriesID, chapterIDs, explicit, 0)
}

// EvaluateAt is Evaluate with a queue priority: what someone is reading now
// goes before the backlog (PriorityReading), background work after it.
func (s *Searcher) EvaluateAt(ctx context.Context, seriesID int64, chapterIDs []int64, explicit bool, priority int) (int, error) {
	if preview, err := IsPreview(ctx, s.db, seriesID); err != nil || preview {
		return 0, err // a preview only streams
	}
	st, err := s.load(ctx, seriesID, chapterIDs)
	if err != nil {
		return 0, err
	}
	grabbed := 0
	for _, ch := range st.chapters {
		if !explicit && ch.FileID == nil && ch.State == model.ChapterCleaned {
			continue
		}
		if st.queued[ch.ID] {
			// already waiting: all this can do is move it up the queue
			if err := s.queue.Raise(ctx, ch.ID, priority); err != nil {
				return grabbed, err
			}
			continue
		}
		d := decision.Decide(st.input(ch, explicit), st.releases[ch.ID])
		if d.Approved == nil {
			continue
		}
		rid := d.Approved.Release.ID
		job, created, err := s.queue.EnqueuePriority(ctx, seriesID, ch.ID, &rid, model.JobKindDownload, d.IsUpgrade, priority)
		if err != nil {
			return grabbed, err
		}
		if created {
			grabbed++
			chID := ch.ID
			_ = history.Record(ctx, s.db, seriesID, &chID, model.HistoryGrabbed, d.Approved.Release.Name, map[string]string{
				"source": d.Approved.Source.SourceName, "scanlator": d.Approved.Release.Scanlator, "jobId": itoa(job.ID), "upgrade": boolStr(d.IsUpgrade)})
		}
	}
	return grabbed, nil
}

// Explain returns decisions for one chapter without enqueueing (UI "why not").
func (s *Searcher) Explain(ctx context.Context, seriesID, chapterID int64) (*decision.Decision, *decision.Candidate, error) {
	st, err := s.load(ctx, seriesID, []int64{chapterID})
	if err != nil || len(st.chapters) == 0 {
		return nil, nil, err
	}
	d := decision.Decide(st.input(st.chapters[0], false), st.releases[chapterID])
	return &d, d.Approved, nil
}

// NextCandidate picks the best remaining release for a chapter after a failure.
func (s *Searcher) NextCandidate(ctx context.Context, seriesID, chapterID int64) (*decision.Candidate, bool, error) {
	return s.NextCandidateConfigured(ctx, seriesID, chapterID, false, nil)
}

func (s *Searcher) NextCandidateConfigured(ctx context.Context, seriesID, chapterID int64, force bool, cfg *model.ProfileConfig) (*decision.Candidate, bool, error) {
	st, err := s.load(ctx, seriesID, []int64{chapterID})
	if err != nil || len(st.chapters) == 0 {
		return nil, false, err
	}
	in := st.input(st.chapters[0], true)
	in.Queued = false
	if force {
		in.CurrentFile, in.CurrentRelease = nil, nil
	}
	if cfg != nil {
		in.Profile.Config = *cfg
	}
	d := decision.Decide(in, st.releases[chapterID])
	return d.Approved, d.IsUpgrade, nil
}

// HasAlternative reports whether another release of the chapter (not
// exclude, not blocklisted, from an enabled source) could be tried instead.
func (s *Searcher) HasAlternative(ctx context.Context, seriesID, chapterID, exclude int64) (bool, error) {
	st, err := s.load(ctx, seriesID, []int64{chapterID})
	if err != nil || len(st.chapters) == 0 {
		return false, err
	}
	var others []decision.Candidate
	for _, c := range st.releases[chapterID] {
		if c.Release.ID != exclude {
			others = append(others, c)
		}
	}
	in := st.input(st.chapters[0], true)
	in.Queued, in.CurrentFile, in.CurrentRelease = false, nil, nil
	return decision.Decide(in, others).Approved != nil, nil
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func boolStr(b bool) string { return strconv.FormatBool(b) }
