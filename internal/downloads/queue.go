// Package downloads owns the download queue: deciding what to grab
// (Searcher), queueing jobs and running the fetch → validate → process →
// import pipeline (Manager).
package downloads

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/history"
	"github.com/Asion001/mangarr/internal/model"
)

// activeStatuses are jobs that still hold their chapter (one per chapter).
var activeStatuses = []string{model.JobQueued, model.JobPaused, model.JobDownloading, model.JobProcessing, model.JobImporting}

// ActiveStatuses returns the statuses of unfinished jobs.
func ActiveStatuses() []string { return append([]string(nil), activeStatuses...) }

type Queue struct {
	db   *db.DB
	bus  *events.Bus
	wake chan struct{}
}

func NewQueue(d *db.DB, bus *events.Bus) *Queue {
	return &Queue{db: d, bus: bus, wake: make(chan struct{}, 1)}
}

// Wake makes the manager look at the queue now.
func (q *Queue) Wake() { q.signal() }

func (q *Queue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Enqueue creates a job unless the chapter already has an active one.
func (q *Queue) Enqueue(ctx context.Context, seriesID, chapterID int64, releaseID *int64, kind string, isUpgrade bool) (*model.DownloadJob, bool, error) {
	return q.EnqueuePriority(ctx, seriesID, chapterID, releaseID, kind, isUpgrade, 0)
}

// EnqueuePriority is Enqueue with a queue priority (higher runs first).
func (q *Queue) EnqueuePriority(ctx context.Context, seriesID, chapterID int64, releaseID *int64, kind string, isUpgrade bool, priority int) (*model.DownloadJob, bool, error) {
	return q.EnqueueConfigured(ctx, seriesID, chapterID, releaseID, kind, isUpgrade, priority, JobOptions{})
}

type JobOptions struct {
	ProfileName    string
	RecycledFileID *int64
	Config         *model.ProfileConfig
	ForceDownload  bool
	PinRelease     bool
}

// EnqueueConfigured persists run-specific input and settings before dispatch can claim it.
func (q *Queue) EnqueueConfigured(ctx context.Context, seriesID, chapterID int64, releaseID *int64, kind string, isUpgrade bool, priority int, options JobOptions) (*model.DownloadJob, bool, error) {
	if preview, err := IsPreview(ctx, q.db, seriesID); err != nil {
		return nil, false, err
	} else if preview {
		return nil, false, ErrPreview
	}
	var existing model.DownloadJob
	err := q.db.NewSelect().Model(&existing).Where("chapter_id = ?", chapterID).Where("status IN (?)", bun.In(activeStatuses)).Limit(1).Scan(ctx)
	if err == nil {
		if err := q.Raise(ctx, chapterID, priority); err != nil {
			return nil, false, err
		}
		if err := q.db.NewSelect().Model(&existing).Where("id = ?", existing.ID).Scan(ctx); err != nil {
			return nil, false, err
		}
		return &existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	now := time.Now().UTC()
	job := &model.DownloadJob{Kind: kind, SeriesID: seriesID, ChapterID: chapterID, ReleaseID: releaseID, Status: model.JobQueued,
		IsUpgrade: isUpgrade, Priority: priority, NotBefore: now, CreatedAt: now, UpdatedAt: now,
		ProfileName: options.ProfileName, RecycledFileID: options.RecycledFileID, ConfigOverride: options.Config, ForceDownload: options.ForceDownload, PinRelease: options.PinRelease}
	err = q.orderTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		ranks, err := insertionRanks(ctx, tx, priority, 1)
		if err != nil {
			return err
		}
		job.Rank = ranks[0]
		if err := bumpOrder(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(job).Exec(ctx); err != nil {
			return err
		}
		if kind == model.JobKindDownload && !isUpgrade {
			_, err := tx.NewUpdate().Model((*model.Chapter)(nil)).Set("state = ?", model.ChapterQueued).Set("updated_at = ?", now).
				Where("id = ?", chapterID).Exec(ctx)
			return err
		}
		return nil
	})
	if err != nil {
		if isUniqueViolation(err) {
			// lost a race with a concurrent enqueue: return the winner
			var winner model.DownloadJob
			if e2 := q.db.NewSelect().Model(&winner).Where("chapter_id = ?", chapterID).Where("status IN (?)", bun.In(activeStatuses)).Limit(1).Scan(ctx); e2 == nil {
				return &winner, false, nil
			}
		}
		return nil, false, err
	}
	q.bus.Changed("queue", "created", job.ID)
	q.bus.Changed("chapter", "updated", chapterID)
	q.signal()
	return job, true, nil
}

// EnqueueReprocessFiles materializes planned processing in bounded bulk
// inserts. Existing active chapter jobs win through the partial unique index.
func (q *Queue) EnqueueReprocessFiles(ctx context.Context, files []model.ChapterFile, priority int) (int, error) {
	const batchSize = 250
	created := 0
	now := time.Now().UTC()
	for start := 0; start < len(files); start += batchSize {
		end := min(start+batchSize, len(files))
		jobs := make([]model.DownloadJob, 0, end-start)
		for _, file := range files[start:end] {
			jobs = append(jobs, model.DownloadJob{Kind: model.JobKindReprocess, SeriesID: file.SeriesID, ChapterID: file.ChapterID,
				ReleaseID: file.ReleaseID, Status: model.JobQueued, IsUpgrade: true, Priority: priority, NotBefore: now, CreatedAt: now, UpdatedAt: now})
		}
		var added int64
		err := q.orderTx(ctx, func(ctx context.Context, tx bun.Tx) error {
			values, err := insertionRanks(ctx, tx, priority, len(jobs))
			if err != nil {
				return err
			}
			for i := range jobs {
				jobs[i].Rank = values[i]
			}
			res, err := tx.NewInsert().Model(&jobs).On("CONFLICT DO NOTHING").Exec(ctx)
			if err != nil {
				return err
			}
			added, _ = res.RowsAffected()
			return bumpOrder(ctx, tx)
		})
		if err != nil {
			return created, err
		}
		created += int(added)
	}
	if created > 0 {
		q.bus.Changed("queue", "sync", 0)
		q.signal()
	}
	return created, nil
}

type JobView struct {
	model.DownloadJob
	SeriesTitle string  `json:"seriesTitle"`
	Chapter     string  `json:"chapter"`
	NumberSort  float64 `json:"numberSort"`
	SourceName  string  `json:"sourceName"`
	Scanlator   string  `json:"scanlator"`
	// Live is the progress of a running job (not stored).
	Live *LiveProgress `json:"live,omitempty" bun:"-"`
	// Worker is the machine doing it, when it isn't this one.
	Worker string `json:"worker,omitempty" bun:"-"`
}

// ListFilter selects queue entries.
type ListFilter struct {
	// Statuses limits to these statuses (empty = active ones, plus recently
	// finished ones when IncludeDone).
	Statuses []string `json:"statuses,omitempty"`
	Kind     string   `json:"kind,omitempty" enum:",download,reprocess"`
	SeriesID int64    `json:"seriesId,omitempty"`
	// Query matches the series title.
	Query       string `json:"q,omitempty"`
	IncludeDone bool   `json:"includeDone,omitempty"`
	// IDs limits to these jobs.
	IDs []int64 `json:"ids,omitempty"`
}

func (q *Queue) base(d bun.IDB, f ListFilter, withStatus bool) *bun.SelectQuery {
	sel := d.NewSelect().TableExpr("download_jobs AS j").
		Join("JOIN series AS s ON s.id = j.series_id").
		Join("JOIN chapters AS c ON c.id = j.chapter_id").
		Join("LEFT JOIN chapter_releases AS r ON r.id = j.release_id").
		Join("LEFT JOIN series_sources AS ss ON ss.id = r.series_source_id")
	if f.Kind != "" {
		sel = sel.Where("j.kind = ?", f.Kind)
	}
	if f.SeriesID > 0 {
		sel = sel.Where("j.series_id = ?", f.SeriesID)
	}
	if len(f.IDs) > 0 {
		sel = sel.Where("j.id IN (?)", bun.In(f.IDs))
	}
	if qs := strings.TrimSpace(f.Query); qs != "" {
		sel = sel.Where("LOWER(s.title) LIKE ?", "%"+strings.ToLower(qs)+"%")
	}
	switch {
	case withStatus && len(f.Statuses) > 0:
		sel = sel.Where("j.status IN (?)", bun.In(f.Statuses))
	case f.IncludeDone:
		sel = sel.Where("(j.status IN (?) OR j.updated_at > ?)", bun.In(activeStatuses), time.Now().UTC().Add(-24*time.Hour))
	default:
		sel = sel.Where("j.status IN (?)", bun.In(activeStatuses))
	}
	return sel
}

// QueuePage is one page of the queue.
type QueuePage struct {
	Revision int64     `json:"revision" doc:"Rank revision; send on later pages to detect intervening rank changes"`
	Items    []JobView `json:"items"`
	Total    int       `json:"total"`
	Page     int       `json:"page"`
	PageSize int       `json:"pageSize"`
	// Counts are per status for the filter without its status part.
	Counts map[string]int `json:"counts"`
}

// ListPage returns a page of the queue: running jobs first, then by durable rank.
func (q *Queue) ListPage(ctx context.Context, f ListFilter, page, pageSize int) (*QueuePage, error) {
	return q.ListPageAt(ctx, f, page, pageSize, nil)
}

var ErrQueueOrderChanged = errors.New("queue order changed; restart pagination")

// ListPageAt optionally rejects a page from a different rank revision. Each
// page's rows, total and counts come from one database snapshot.
func (q *Queue) ListPageAt(ctx context.Context, f ListFilter, page, pageSize int, revision *int64) (*QueuePage, error) {
	var out *QueuePage
	var opts *sql.TxOptions
	if q.db.Kind == db.Postgres {
		opts = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	err := q.db.RunInTx(ctx, opts, func(ctx context.Context, tx bun.Tx) error {
		page, pageSize = max(page, 1), min(max(pageSize, 1), 500)
		out = &QueuePage{Items: []JobView{}, Page: page, PageSize: pageSize, Counts: map[string]int{}}
		if err := tx.NewSelect().Table("download_queue_order").Column("revision").Where("id = 1").Scan(ctx, &out.Revision); err != nil {
			return err
		}
		if revision != nil && *revision != out.Revision {
			return ErrQueueOrderChanged
		}
		total, err := q.base(tx, f, true).Count(ctx)
		if err != nil {
			return err
		}
		out.Total = total
		err = q.base(tx, f, true).
			ColumnExpr("j.*").
			ColumnExpr("s.title AS series_title, c.number_key AS chapter, c.number_sort AS number_sort").
			ColumnExpr("COALESCE(ss.source_name, '') AS source_name, COALESCE(r.scanlator, '') AS scanlator").
			OrderExpr("CASE j.status WHEN 'importing' THEN 0 WHEN 'processing' THEN 1 WHEN 'downloading' THEN 2 WHEN 'queued' THEN 3 WHEN 'paused' THEN 3 WHEN 'failed' THEN 4 ELSE 5 END").
			OrderExpr("j.rank, j.id").
			Limit(pageSize).Offset((page-1)*pageSize).Scan(ctx, &out.Items)
		if err != nil {
			return err
		}
		var counts []struct {
			Status string `bun:"status"`
			N      int    `bun:"n"`
		}
		if err := q.base(tx, f, false).ColumnExpr("j.status AS status, COUNT(*) AS n").GroupExpr("j.status").Scan(ctx, &counts); err != nil {
			return err
		}
		for _, c := range counts {
			out.Counts[c.Status] = c.N
		}
		return nil
	})
	return out, err
}

// List returns active jobs (plus recently failed/completed when includeDone).
func (q *Queue) List(ctx context.Context, includeDone bool) ([]JobView, error) {
	p, err := q.ListPage(ctx, ListFilter{IncludeDone: includeDone}, 1, 500)
	if err != nil {
		return nil, err
	}
	return p.Items, nil
}

// IDs returns the ids of the jobs matching f.
func (q *Queue) IDs(ctx context.Context, f ListFilter) ([]int64, error) {
	var ids []int64
	err := q.base(q.db, f, true).ColumnExpr("j.id").OrderExpr("j.id").Scan(ctx, &ids)
	return ids, err
}

// ActiveChapters returns chapter ids with active jobs for a series.
func (q *Queue) ActiveChapters(ctx context.Context, seriesID int64) (map[int64]bool, error) {
	var ids []int64
	err := q.db.NewSelect().Model((*model.DownloadJob)(nil)).Column("chapter_id").
		Where("series_id = ?", seriesID).Where("status IN (?)", bun.In(activeStatuses)).Scan(ctx, &ids)
	out := map[int64]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out, err
}

// Remove cancels a queued/failed job (running jobs are cancelled by the manager).
func (q *Queue) Remove(ctx context.Context, id int64, blocklist bool) error {
	var job model.DownloadJob
	if err := q.db.NewSelect().Model(&job).Where("id = ?", id).Scan(ctx); err != nil {
		return err
	}
	return q.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if blocklist && job.ReleaseID != nil {
			if err := BlocklistRelease(ctx, tx, *job.ReleaseID, "removed from queue by user"); err != nil {
				return err
			}
		}
		if _, err := tx.NewDelete().Model((*model.DownloadJob)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
			return err
		}
		return resetChapterState(ctx, tx, job.ChapterID)
	})
}

// Retry requeues a failed job.
func (q *Queue) Retry(ctx context.Context, id int64) error {
	now := time.Now().UTC()
	_, err := q.db.NewUpdate().Model((*model.DownloadJob)(nil)).
		Set("status = ?", model.JobQueued).Set("error = ''").Set("attempt = 0").Set("not_before = ?", now).Set("updated_at = ?", now).
		Where("id = ? AND status = ?", id, model.JobFailed).Exec(ctx)
	q.signal()
	q.bus.Changed("queue", "updated", id)
	return err
}

// ClearFinished removes completed/failed jobs older than d.
func (q *Queue) ClearFinished(ctx context.Context, d time.Duration) error {
	_, err := q.db.NewDelete().Model((*model.DownloadJob)(nil)).
		Where("status IN (?)", bun.In([]string{model.JobCompleted, model.JobFailed})).
		Where("updated_at < ?", time.Now().UTC().Add(-d)).Exec(ctx)
	return err
}

// resetChapterState sets a chapter back to imported/missing depending on its file.
func resetChapterState(ctx context.Context, db bun.IDB, chapterID int64) error {
	var ch model.Chapter
	if err := db.NewSelect().Model(&ch).Where("id = ?", chapterID).Scan(ctx); err != nil {
		return err
	}
	state := model.ChapterMissing
	switch {
	case ch.FileID != nil:
		state = model.ChapterImported
	case ch.CleanedAt != nil:
		state = model.ChapterCleaned
	}
	_, err := db.NewUpdate().Model((*model.Chapter)(nil)).Set("state = ?", state).Set("updated_at = ?", time.Now().UTC()).
		Where("id = ?", chapterID).Exec(ctx)
	return err
}

// BlocklistRelease adds a release to the blocklist and records history.
func BlocklistRelease(ctx context.Context, db bun.IDB, releaseID int64, reason string) error {
	var r model.ChapterRelease
	if err := db.NewSelect().Model(&r).Where("id = ?", releaseID).Scan(ctx); err != nil {
		return err
	}
	b := &model.Blocklist{SeriesID: r.SeriesID, ChapterID: r.ChapterID, SeriesSourceID: r.SeriesSourceID, ChapterURL: r.ChapterURL,
		Scanlator: r.Scanlator, Reason: reason, CreatedAt: time.Now().UTC()}
	if _, err := db.NewInsert().Model(b).On("CONFLICT (series_source_id, chapter_url) DO NOTHING").Exec(ctx); err != nil {
		return err
	}
	return history.Record(ctx, db, r.SeriesID, r.ChapterID, model.HistoryBlocklist, r.Name, map[string]string{"reason": reason, "scanlator": r.Scanlator})
}

func isUniqueViolation(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate key")
}

// Raise lifts a chapter's queued job to priority when that is higher, so a
// chapter someone just opened passes work queued in the background. It does
// nothing once the job started.
func (q *Queue) Raise(ctx context.Context, chapterID int64, priority int) error {
	if priority <= 0 {
		return nil
	}
	changed := false
	err := q.orderTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var job model.DownloadJob
		err := tx.NewSelect().Model(&job).Where("chapter_id = ? AND status = ? AND priority < ?", chapterID, model.JobQueued, priority).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		values, err := insertionRanks(ctx, tx, priority, 1)
		if err != nil {
			return err
		}
		res, err := tx.NewUpdate().Model((*model.DownloadJob)(nil)).Set("priority = ?", priority).Set("rank = ?", values[0]).Set("updated_at = ?", time.Now().UTC()).Where("id = ? AND status = ?", job.ID, model.JobQueued).Exec(ctx)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		changed = n > 0
		return bumpOrder(ctx, tx)
	})
	if err == nil && changed {
		q.signal()
		q.bus.Changed("queue", "sync", 0)
		q.bus.Changed("chapter", "updated", chapterID)
	}
	return err
}

// ErrPreview refuses downloads for a preview: its chapters only stream.
var ErrPreview = errors.New("this title is a preview: add it to the library to download chapters")

// IsPreview reports whether a series is a preview (see series.Preview).
func IsPreview(ctx context.Context, d bun.IDB, seriesID int64) (bool, error) {
	return d.NewSelect().Model((*model.Series)(nil)).Where("id = ? AND preview = ?", seriesID, true).Exists(ctx)
}
