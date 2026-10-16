package downloads

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/model"
)

const rankStep int64 = 1 << 20

// insertStep is the most a before/after insert takes from its gap.
const insertStep = rankStep >> 10

// Keep ranks exactly representable by JSON clients as well as SQL BIGINT.
const rankLimit int64 = 1 << 52

var pendingStatuses = []string{model.JobQueued, model.JobPaused}

// orderTx serializes rank allocation across processes.
// SQLite begins IMMEDIATE transactions; this singleton also locks PostgreSQL.
func (q *Queue) orderTx(ctx context.Context, fn func(context.Context, bun.Tx) error) error {
	return q.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE download_queue_order SET revision = revision WHERE id = 1"); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}

func bumpOrder(ctx context.Context, tx bun.Tx) error {
	_, err := tx.ExecContext(ctx, "UPDATE download_queue_order SET revision = revision + 1 WHERE id = 1")
	return err
}

// ranks reserves a gap in the global order, including finished jobs so retries
// retain their position. Only exhausted gaps require a full, order-preserving
// renumber. Callers hold orderTx until their inserts/updates have committed.
func ranks(ctx context.Context, tx bun.Tx, action string, anchorID int64, n int) ([]int64, error) {
	for attempt := 0; attempt < 2; attempt++ {
		var low, high int64
		var edge sql.NullInt64
		switch action {
		case "top", "bottom":
			agg := "MIN(rank)"
			if action == "bottom" {
				agg = "MAX(rank)"
			}
			if err := tx.NewSelect().Table("download_jobs").ColumnExpr(agg).Scan(ctx, &edge); err != nil {
				return nil, err
			}
			if action == "top" {
				high = edge.Int64
				low = high - rankStep*int64(n+1)
			} else {
				low = edge.Int64
				high = low + rankStep*int64(n+1)
			}
		case "before", "after":
			var anchor int64
			if err := tx.NewSelect().Table("download_jobs").Column("rank").Where("id = ?", anchorID).Scan(ctx, &anchor); err != nil {
				return nil, err
			}
			if action == "before" {
				high = anchor
				if err := tx.NewSelect().Table("download_jobs").ColumnExpr("MAX(rank)").Where("rank < ?", anchor).Scan(ctx, &edge); err != nil {
					return nil, err
				}
				low = edge.Int64
				if !edge.Valid {
					low = high - rankStep*int64(n+1)
				}
			} else {
				low = anchor
				if err := tx.NewSelect().Table("download_jobs").ColumnExpr("MIN(rank)").Where("rank > ?", anchor).Scan(ctx, &edge); err != nil {
					return nil, err
				}
				high = edge.Int64
				if !edge.Valid {
					high = low + rankStep*int64(n+1)
				}
			}
		default:
			return nil, fmt.Errorf("unknown move %q", action)
		}
		step := (high - low) / int64(n+1)
		if action == "before" || action == "after" {
			// Take a small slice of the gap rather than its middle: chapters
			// queued one at a time land in the same gap, and halving it each
			// time would run out after about twenty of them.
			step = min(step, insertStep)
		}
		if low > -rankLimit && high < rankLimit && step > 0 {
			out := make([]int64, n)
			for i := range out {
				out[i] = low + step*int64(i+1)
			}
			return out, nil
		}
		// A join, not a correlated subquery: Postgres reran the window over the
		// whole table for every row, which took longer than the read timeout
		// once the queue held a few thousand jobs.
		if _, err := tx.ExecContext(ctx, `UPDATE download_jobs SET rank = ordered.new_rank FROM (
   SELECT id, ROW_NUMBER() OVER (ORDER BY rank, id) * ? AS new_rank FROM download_jobs
  ) AS ordered WHERE ordered.id = download_jobs.id`, rankStep); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("queue rank space exhausted")
}

// insertionRanks translates the legacy enqueue priority into a rank once.
// Manual moves subsequently control order independently of that priority.
func insertionRanks(ctx context.Context, tx bun.Tx, priority, n int) ([]int64, error) {
	var anchor int64
	err := tx.NewSelect().Table("download_jobs").Column("id").
		Where("status IN (?) AND priority < ?", bun.In(pendingStatuses), priority).
		OrderExpr("rank, id").Limit(1).Scan(ctx, &anchor)
	if errors.Is(err, sql.ErrNoRows) {
		return ranks(ctx, tx, "bottom", 0, n)
	}
	if err != nil {
		return nil, err
	}
	return ranks(ctx, tx, "before", anchor, n)
}

// Move reorders only queued/paused jobs, preserving their current relative
// order regardless of request ID order. Both kinds share one rank namespace.
// A before/after anchor must be pending and outside the selection. Missing or
// running selected IDs are ignored, as in the legacy bulk endpoints.
func (q *Queue) Move(ctx context.Context, ids []int64, action string, anchorID int64) (int, error) {
	switch action {
	case "top", "bottom":
		if anchorID != 0 {
			return 0, errors.New("top/bottom do not accept an anchor")
		}
	case "before", "after":
		if anchorID <= 0 {
			return 0, errors.New("before/after require anchorId")
		}
		for _, id := range ids {
			if id == anchorID {
				return 0, errors.New("anchor must not be selected")
			}
		}
	default:
		return 0, fmt.Errorf("unknown move %q", action)
	}
	affected := 0
	err := q.orderTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if anchorID != 0 {
			var anchor model.DownloadJob
			sel := tx.NewSelect().Model(&anchor).Where("id = ? AND status IN (?)", anchorID, bun.In(pendingStatuses))
			if q.db.Kind == db.Postgres {
				sel = sel.For("UPDATE")
			}
			if err := sel.Scan(ctx); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return errors.New("anchor must be a pending job")
				}
				return err
			}
		}
		if len(ids) == 0 {
			return nil
		}
		var jobs []model.DownloadJob
		sel := tx.NewSelect().Model(&jobs).Where("id IN (?) AND status IN (?)", bun.In(ids), bun.In(pendingStatuses)).OrderExpr("rank, id")
		if q.db.Kind == db.Postgres {
			sel = sel.For("UPDATE")
		}
		if err := sel.Scan(ctx); err != nil {
			return err
		}
		if len(jobs) == 0 {
			return nil
		}
		values, err := ranks(ctx, tx, action, anchorID, len(jobs))
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		for i, job := range jobs {
			if _, err := tx.NewUpdate().Model((*model.DownloadJob)(nil)).Set("rank = ?", values[i]).Set("updated_at = ?", now).Where("id = ?", job.ID).Exec(ctx); err != nil {
				return err
			}
		}
		affected = len(jobs)
		return bumpOrder(ctx, tx)
	})
	if err != nil {
		return 0, err
	}
	if affected > 0 {
		q.signal()
		q.bus.Changed("queue", "sync", 0)
		q.bus.Changed("chapter", "updated", 0)
	}
	return affected, nil
}

type sortJob struct {
	ID         int64   `bun:"id"`
	SeriesID   int64   `bun:"series_id"`
	Rank       int64   `bun:"rank"`
	NumberSort float64 `bun:"number_sort"`
}

// SortByChapter reorders the selected queued/paused jobs by chapter number
// within the places they already hold, so the rest of the queue is untouched.
// Series keep the order of their earliest selected job.
func (q *Queue) SortByChapter(ctx context.Context, ids []int64) (int, error) {
	affected := 0
	err := q.orderTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var jobs []sortJob
		sel := tx.NewSelect().TableExpr("download_jobs AS j").Join("JOIN chapters AS c ON c.id = j.chapter_id").
			ColumnExpr("j.id, j.series_id, j.rank, c.number_sort").
			Where("j.id IN (?) AND j.status IN (?)", bun.In(ids), bun.In(pendingStatuses)).OrderExpr("j.rank, j.id")
		if q.db.Kind == db.Postgres {
			sel = sel.For("UPDATE OF j")
		}
		if len(ids) == 0 {
			return nil
		}
		if err := sel.Scan(ctx, &jobs); err != nil {
			return err
		}
		if len(jobs) < 2 {
			return nil
		}
		slots := make([]int64, len(jobs))
		seriesOrder := map[int64]int{}
		for i, j := range jobs {
			slots[i] = j.Rank
			if _, ok := seriesOrder[j.SeriesID]; !ok {
				seriesOrder[j.SeriesID] = len(seriesOrder)
			}
		}
		sorted := slices.Clone(jobs)
		slices.SortStableFunc(sorted, func(a, b sortJob) int {
			if c := cmp.Compare(seriesOrder[a.SeriesID], seriesOrder[b.SeriesID]); c != 0 {
				return c
			}
			return cmp.Compare(a.NumberSort, b.NumberSort)
		})
		now := time.Now().UTC()
		for i, j := range sorted {
			if j.Rank == slots[i] {
				continue
			}
			if _, err := tx.NewUpdate().Model((*model.DownloadJob)(nil)).Set("rank = ?", slots[i]).Set("updated_at = ?", now).Where("id = ?", j.ID).Exec(ctx); err != nil {
				return err
			}
			affected++
		}
		if affected == 0 {
			return nil
		}
		return bumpOrder(ctx, tx)
	})
	if err != nil {
		return 0, err
	}
	if affected > 0 {
		q.signal()
		q.bus.Changed("queue", "sync", 0)
		q.bus.Changed("chapter", "updated", 0)
	}
	return affected, nil
}
