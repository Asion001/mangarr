package downloads

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/progress"
)

// Hold stops starting jobs (true) or starts again (false).
func (m *Manager) Hold(on bool) {
	m.mu.Lock()
	m.held = on
	m.mu.Unlock()
	m.queue.signal()
}

// CancelAll stops every running job; they're queued again on next start.
func (m *Manager) CancelAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.running {
		c()
	}
}

// Running is the number of jobs running now.
func (m *Manager) Running() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.running)
}

// Cancel stops a running job (used when the user removes it from the queue),
// here and on whichever worker is doing it.
func (m *Manager) Cancel(jobID int64) {
	m.mu.Lock()
	if c, ok := m.running[jobID]; ok {
		c()
	}
	m.mu.Unlock()
	if m.Tasks != nil {
		if err := m.Tasks.Cancel(context.Background(), jobID); err != nil {
			m.log.Warn("could not cancel a job's worker tasks", "job", jobID, "err", err)
		}
	}
}

func (m *Manager) setStatus(ctx context.Context, job *model.DownloadJob, status string, chapterState string) {
	now := time.Now().UTC()
	job.Status, job.UpdatedAt = status, now
	cols := []string{"status", "updated_at"}
	if status == model.JobDownloading && job.StartedAt == nil {
		job.StartedAt = &now
		cols = append(cols, "started_at")
	}
	// compare-and-set: a job paused meanwhile keeps its paused status, and
	// one handed back to the queue (Release on shutdown) stays queued
	res, err := m.db.NewUpdate().Model(job).Column(cols...).WherePK().
		Where("status NOT IN (?)", bun.In([]string{model.JobPaused, model.JobQueued})).Exec(ctx)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return
	}
	if chapterState != "" && !job.IsUpgrade && job.Kind == model.JobKindDownload {
		_, _ = m.db.NewUpdate().Model((*model.Chapter)(nil)).Set("state = ?", chapterState).Set("updated_at = ?", now).Where("id = ?", job.ChapterID).Exec(ctx)
		m.bus.Changed("chapter", "updated", job.ChapterID)
	}
	m.bus.Changed("queue", "updated", job.ID)
}

// progress records how far a download is, for the queue and the live
// stream. bytesIn is what has been pulled from the site so far (0 when the
// caller doesn't count it).
func (m *Manager) progress(job *model.DownloadJob, done, total int, bytesIn int64) {
	m.Live.Update(job.ID, progress.Event{Stage: progress.StageDownload, Done: done, Total: total, BytesIn: bytesIn})
	job.PagesDone, job.PagesTotal = done, total
	if total > 0 {
		job.Progress = done * 100 / total
	}
	m.progressMu.Lock()
	last := m.lastPersist[job.ID]
	persist := time.Since(last) > 2*time.Second || done == total
	if persist {
		m.lastPersist[job.ID] = time.Now()
	}
	m.progressMu.Unlock()
	if persist {
		_, _ = m.db.NewUpdate().Model(job).Column("pages_done", "pages_total", "progress").WherePK().Exec(context.Background())
		m.bus.Changed("queue", "updated", job.ID)
	}
}

// claim atomically moves a queued job to "downloading" so a stale dispatch
// snapshot can never run the same job twice.
func (m *Manager) claim(ctx context.Context, job *model.DownloadJob) bool {
	now := time.Now().UTC()
	res, err := m.db.NewUpdate().Model((*model.DownloadJob)(nil)).
		Set("status = ?", model.JobDownloading).Set("updated_at = ?", now).
		Where("id = ? AND status = ?", job.ID, model.JobQueued).Exec(ctx)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// Bulk applies an action to queue entries and returns how many changed.
// Actions: pause, resume, retry, remove, blocklist, top, bottom.
func (m *Manager) Bulk(ctx context.Context, ids []int64, action string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	now := time.Now().UTC()
	affected := 0
	exec := func(q *bun.UpdateQuery) error {
		res, err := q.Where("id IN (?)", bun.In(ids)).Exec(ctx)
		if err == nil {
			n, _ := res.RowsAffected()
			affected = int(n)
		}
		return err
	}
	var err error
	switch action {
	case "pause":
		err = exec(m.db.NewUpdate().Model((*model.DownloadJob)(nil)).Set("status = ?", model.JobPaused).Set("updated_at = ?", now).
			Where("status IN (?)", bun.In([]string{model.JobQueued, model.JobDownloading, model.JobProcessing})))
		if err == nil {
			for _, id := range ids {
				m.Cancel(id) // stops running ones; their status stays paused
			}
			// chapters of paused downloads show as queued again
			_, err = m.db.NewUpdate().Model((*model.Chapter)(nil)).Set("state = ?", model.ChapterQueued).Set("updated_at = ?", now).
				Where("id IN (SELECT chapter_id FROM download_jobs WHERE id IN (?) AND status = ? AND kind = ? AND is_upgrade = ?)", bun.In(ids), model.JobPaused, model.JobKindDownload, false).
				Where("state IN (?)", bun.In([]string{model.ChapterDownloading, model.ChapterProcessing})).Exec(ctx)
		}
	case "resume":
		err = exec(m.db.NewUpdate().Model((*model.DownloadJob)(nil)).Set("status = ?", model.JobQueued).Set("not_before = ?", now).Set("updated_at = ?", now).
			Where("status = ?", model.JobPaused))
	case "retry":
		affected, _, err = m.queue.RetryFailed(ctx, ids)
	case "remove", "blocklist":
		for _, id := range ids {
			m.Cancel(id)
			if e := m.queue.Remove(ctx, id, action == "blocklist"); e == nil {
				affected++
			} else if !errors.Is(e, sql.ErrNoRows) {
				err = e
			}
		}
	case "top", "bottom":
		return m.queue.Move(ctx, ids, action, 0)
	default:
		return 0, fmt.Errorf("unknown action %q", action)
	}
	m.queue.signal()
	m.bus.Changed("queue", "sync", 0)
	m.bus.Changed("chapter", "updated", 0)
	return affected, err
}
