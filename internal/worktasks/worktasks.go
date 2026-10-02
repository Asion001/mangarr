// Package worktasks is the ledger of work handed to workers: what is
// waiting, who holds it, and what became of it. Workers pull from it (they
// never listen on a port), so every hand-out is a compare-and-set on one
// row — which both SQLite and PostgreSQL do without row locks.
package worktasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/model"
)

// Lease is how long a worker holds a task before it has to say it is still
// alive, and how long the reaper waits before taking it back.
const Lease = 2 * time.Minute

// MaxAttempts is how often a task is handed out before it is given up on.
// A worker that dies mid-chapter costs one attempt.
const MaxAttempts = 3

// Unclaimed is how long a task may wait for a worker that never comes. It
// only applies while no worker that could do it is online — a queue behind
// busy workers waits as long as it needs to.
const Unclaimed = time.Minute

// OnlineWithin is how long after its last word a worker still counts as
// being here.
const OnlineWithin = 2 * time.Minute

// ErrNotYours is returned when a worker acts on a task it doesn't hold —
// its lease expired and someone else has it now. The worker drops the task
// and asks for another.
var ErrNotYours = errors.New("this task is not leased to you")

// Ledger stores and hands out worker tasks.
type Ledger struct {
	db  *db.DB
	log *slog.Logger
	// claimMu makes capacity checks and the following compare-and-set one
	// scheduling decision, so simultaneous polls cannot overbook a limit.
	claimMu sync.Mutex
	// DataDir is where a task's payload is staged, for the work that trades
	// files rather than page uploads.
	DataDir string
	// Changed (optional) is called whenever a task's state changes, so the
	// download manager can look at the job again without waiting for a tick.
	Changed func(jobID int64)
	// Abandoned (optional) is called when a task is given up on, so the job
	// it belongs to isn't left waiting for a worker that never comes back.
	Abandoned func(task model.WorkerTask)

	waitMu  sync.Mutex
	waiting map[int64]chan error
}

func New(d *db.DB, log *slog.Logger) *Ledger {
	return &Ledger{db: d, log: log}
}

// DB is the database the ledger lives in, for callers that have the ledger
// and nothing else (a module implementation, say).
func (l *Ledger) DB() *db.DB { return l.db }

// theLedger is this process's ledger. Module implementations hand work to
// workers through it, and the core may not import them to pass it in.
var theLedger atomic.Pointer[Ledger]

// SetDefault publishes the ledger for those modules.
func SetDefault(l *Ledger) { theLedger.Store(l) }

// Default is the process's ledger (nil before the server has wired one).
func Default() *Ledger { return theLedger.Load() }

// Add stores a task to be picked up. Kind, JobID and Spec must be set.
func (l *Ledger) Add(ctx context.Context, t *model.WorkerTask) error {
	now := time.Now().UTC()
	t.State = model.TaskPending
	t.CreatedAt = now
	if t.NotBefore.IsZero() {
		t.NotBefore = now
	}
	if t.Spec == nil {
		t.Spec = map[string]any{}
	}
	if _, err := l.db.NewInsert().Model(t).Exec(ctx); err != nil {
		return err
	}
	l.changed(t.JobID)
	return nil
}

// Claim hands one waiting task to a worker: the first it may do, oldest
// first. It returns nil when there is nothing for it.
func (l *Ledger) Claim(ctx context.Context, workerID int64, kinds []string, limits ...int) (*model.WorkerTask, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	l.claimMu.Lock()
	defer l.claimMu.Unlock()
	// a task kept for this worker (an upscale route's) is its own even when
	// a worker with a better priority would take that kind of work
	mine := kinds
	perWorker := 1
	if len(limits) > 0 {
		global := limits[0]
		if len(limits) > 1 {
			perWorker = max(limits[1], 1)
		}
		var yielded []string
		var err error
		kinds, yielded, err = l.claimableKinds(ctx, workerID, kinds, global, perWorker)
		if err != nil {
			return nil, err
		}
		mine = append(append([]string(nil), kinds...), yielded...)
		if len(mine) == 0 {
			return nil, nil
		}
	}
	free := map[string]bool{}
	for _, k := range kinds {
		free[k] = true
	}
	var worker model.Worker
	if err := l.db.NewSelect().Model(&worker).Where("id = ?", workerID).Scan(ctx); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var waiting []model.WorkerTask
	err := l.db.NewSelect().Model(&waiting).
		Where("state = ? AND not_before <= ?", model.TaskPending, now).
		Where("kind IN (?)", bun.In(mine)).
		Order("id").Limit(100).Scan(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]pinState{} // the workers tasks are kept for
	for _, t := range waiting {
		pin := PinOf(&t)
		switch {
		case len(pin.Workers) > 0 && !l.pinAllows(ctx, pin, workerID, t.Kind, perWorker, seen):
			continue // kept for other workers while they are around
		case !pin.Has(workerID) && !free[t.Kind]:
			continue
		}
		if !upscalesIfNeeded(&worker, &t) {
			continue // pages that need upscaling, and it can't
		}
		until := now.Add(Lease)
		// the compare-and-set: only one worker can move a row out of pending
		res, err := l.db.NewUpdate().Model((*model.WorkerTask)(nil)).
			Set("state = ?", model.TaskLeased).Set("worker_id = ?", workerID).
			Set("lease_until = ?", until).Set("heartbeat_at = ?", now).Set("started_at = COALESCE(started_at, ?)", now).
			Set("attempt = attempt + 1").Set("cancel = ?", false).
			Where("id = ? AND state = ?", t.ID, model.TaskPending).Exec(ctx)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			continue // another worker was quicker
		}
		got := t
		got.State, got.WorkerID, got.LeaseUntil, got.Attempt = model.TaskLeased, workerID, &until, t.Attempt+1
		got.HeartbeatAt, got.Cancel = &now, false
		l.changed(got.JobID)
		return &got, nil
	}
	return nil, nil
}

// claimableKinds enforces the installation and per-worker limits, then
// narrows kinds to those no compatible worker above this one could take right
// now: a lower-priority worker gets a kind only when every online worker with
// a better priority that does that kind is full. Priority is ascending, like
// module priority.
func (l *Ledger) claimableKinds(ctx context.Context, workerID int64, kinds []string, global, defaultPerWorker int) (free, yielded []string, err error) {
	var workers []model.Worker
	if err := l.db.NewSelect().Model(&workers).Where("enabled = ?", true).Scan(ctx); err != nil {
		return nil, nil, err
	}
	var current *model.Worker
	for i := range workers {
		if workers[i].ID == workerID {
			current = &workers[i]
			break
		}
	}
	if current == nil {
		return nil, nil, nil
	}
	var counts []struct {
		WorkerID int64 `bun:"worker_id"`
		Count    int   `bun:"count"`
	}
	if err := l.db.NewSelect().Model((*model.WorkerTask)(nil)).
		ColumnExpr("worker_id, COUNT(*) AS count").Where("state = ?", model.TaskLeased).
		GroupExpr("worker_id").Scan(ctx, &counts); err != nil {
		return nil, nil, err
	}
	held := map[int64]int{}
	total := 0
	for _, row := range counts {
		held[row.WorkerID] = row.Count
		total += row.Count
	}
	if global > 0 && total >= global {
		return nil, nil, nil
	}
	spare := func(w *model.Worker) bool {
		limit := w.Concurrent
		if limit <= 0 {
			limit = defaultPerWorker
		}
		return held[w.ID] < max(limit, 1)
	}
	if !spare(current) {
		return nil, nil, nil
	}
	now := time.Now()
	out := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		if kind == model.TaskEncode && !takes(current, kind) {
			continue // a worker too old to process pages still holds the role
		}
		yield := false
		for i := range workers {
			w := &workers[i]
			if w.ID == current.ID || w.Priority >= current.Priority || w.LastSeenAt == nil || now.Sub(*w.LastSeenAt) >= OnlineWithin {
				continue
			}
			if takes(w, kind) && spare(w) {
				yield = true
				break
			}
		}
		if yield {
			yielded = append(yielded, kind)
		} else {
			out = append(out, kind)
		}
	}
	return out, yielded, nil
}

// takes reports whether a worker can actually do a kind of task. A worker
// without an upscaling model drops the upscale role when it says hello, even
// though its key still carries it, so it never asks for that work.
func takes(w *model.Worker, kind string) bool {
	if !w.Offers(kind) {
		return false
	}
	switch kind {
	case model.TaskUpscale:
		models, _ := w.Info["models"].([]any)
		return len(models) > 0
	case model.TaskEncode:
		// only a worker that says it can process pages: one from before
		// processing moved to workers holds the role but can't do the work
		ok, _ := w.Info[InfoProcess].(bool)
		return ok
	}
	return true
}

// SpecNeedsUpscale marks an encode task whose pages need upscaling: only a
// worker with an upscaling engine can do it.
const SpecNeedsUpscale = "needsUpscale"

// upscalesIfNeeded reports whether a worker can do a task's upscaling, when
// the task has any.
func upscalesIfNeeded(w *model.Worker, t *model.WorkerTask) bool {
	if t.Kind != model.TaskEncode {
		return true
	}
	need, known := t.Spec[SpecNeedsUpscale].(bool)
	if !known {
		// written before the server said: any task whose profile upscales
		profile, _ := t.Spec["profile"].(map[string]any)
		upscale, _ := profile["upscale"].(map[string]any)
		need, _ = upscale["enabled"].(bool)
	}
	if !need {
		return true
	}
	models, _ := w.Info["models"].([]any)
	return len(models) > 0
}

// InfoProcess is what a worker puts in its hello to say it can do the
// processing stage (the encode role).
const InfoProcess = "process"

// CanDo reports whether a worker that can do this kind of task is online.
func (l *Ledger) CanDo(ctx context.Context, kind string) (bool, error) {
	return l.someoneCanDo(ctx, &model.WorkerTask{Kind: kind})
}

// CanTake reports whether a worker that can do this task, with what its
// spec asks for, is online.
func (l *Ledger) CanTake(ctx context.Context, t *model.WorkerTask) (bool, error) {
	return l.someoneCanDo(ctx, t)
}

// Progress is what a worker reports while it works.
type Progress struct {
	PagesDone  int
	PagesTotal int
	BytesIn    int64
	BytesOut   int64
	GPU        string
}

// Heartbeat renews a lease and records progress. It reports whether the
// task has been cancelled meanwhile, which is how a worker is told to stop.
func (l *Ledger) Heartbeat(ctx context.Context, taskID, workerID int64, p Progress) (cancelled bool, err error) {
	now := time.Now().UTC()
	res, err := l.db.NewUpdate().Model((*model.WorkerTask)(nil)).
		Set("heartbeat_at = ?", now).Set("lease_until = ?", now.Add(Lease)).
		Set("pages_done = ?", p.PagesDone).Set("pages_total = ?", p.PagesTotal).
		Set("bytes_in = ?", p.BytesIn).Set("bytes_out = ?", p.BytesOut).
		Where("id = ? AND worker_id = ? AND state = ?", taskID, workerID, model.TaskLeased).Exec(ctx)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return false, ErrNotYours
	}
	var t model.WorkerTask
	if err := l.db.NewSelect().Model(&t).Column("cancel", "job_id").Where("id = ?", taskID).Scan(ctx); err != nil {
		return false, err
	}
	l.changed(t.JobID)
	return t.Cancel, nil
}

// Held returns a task if this worker still holds it (and nothing else).
func (l *Ledger) Held(ctx context.Context, taskID, workerID int64) (*model.WorkerTask, error) {
	var t model.WorkerTask
	err := l.db.NewSelect().Model(&t).Where("id = ? AND worker_id = ? AND state = ?", taskID, workerID, model.TaskLeased).Scan(ctx)
	if err != nil {
		return nil, ErrNotYours
	}
	return &t, nil
}

// Finish closes a task the worker did, and counts it on the worker.
func (l *Ledger) Finish(ctx context.Context, taskID, workerID int64, p Progress) error {
	return l.close(ctx, taskID, workerID, model.TaskDone, "", p)
}

// Fail closes a task the worker could not do. The job it belongs to decides
// what that means (a retry, or a failed download).
func (l *Ledger) Fail(ctx context.Context, taskID, workerID int64, reason string, p Progress) error {
	return l.close(ctx, taskID, workerID, model.TaskFailed, reason, p)
}

func (l *Ledger) close(ctx context.Context, taskID, workerID int64, state, reason string, p Progress) error {
	now := time.Now().UTC()
	var t model.WorkerTask
	if err := l.db.NewSelect().Model(&t).Where("id = ?", taskID).Scan(ctx); err != nil {
		return err
	}
	err := l.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if p.GPU != "" {
			spec := make(map[string]any, len(t.Spec)+1)
			for k, v := range t.Spec {
				spec[k] = v
			}
			spec["gpu"] = p.GPU
			if _, err := tx.NewUpdate().Model((*model.WorkerTask)(nil)).Set("spec = ?", spec).Where("id = ?", taskID).Exec(ctx); err != nil {
				return err
			}
		}
		res, err := tx.NewUpdate().Model((*model.WorkerTask)(nil)).
			Set("state = ?", state).Set("error = ?", reason).Set("finished_at = ?", now).
			Set("pages_done = ?", p.PagesDone).Set("pages_total = ?", p.PagesTotal).
			Set("bytes_in = ?", p.BytesIn).Set("bytes_out = ?", p.BytesOut).
			Set("lease_until = NULL").
			Where("id = ? AND worker_id = ? AND state = ?", taskID, workerID, model.TaskLeased).Exec(ctx)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrNotYours
		}
		if workerID == 0 {
			return nil
		}
		// the same transaction counts it on the worker, so the totals can't
		// drift from the tasks they come from
		q := tx.NewUpdate().Model((*model.Worker)(nil)).
			Set("pages_done = pages_done + ?", p.PagesDone).
			Set("bytes_in = bytes_in + ?", p.BytesIn).Set("bytes_out = bytes_out + ?", p.BytesOut).
			Where("id = ?", workerID)
		if state == model.TaskDone {
			q = q.Set("tasks_done = tasks_done + 1")
		} else {
			q = q.Set("tasks_failed = tasks_failed + 1")
		}
		if t.StartedAt != nil {
			q = q.Set("busy_seconds = busy_seconds + ?", now.Sub(*t.StartedAt).Seconds())
		}
		_, err = q.Exec(ctx)
		return err
	})
	if err != nil {
		return err
	}
	if state == model.TaskDone {
		l.settle(taskID, nil)
	} else {
		l.settle(taskID, fmt.Errorf("%w: %s", ErrGivenUp, reason))
	}
	l.changed(t.JobID)
	return nil
}

// Cancel asks whoever holds these tasks to stop, and drops the ones nobody
// has started. Used when a job is removed from the queue.
func (l *Ledger) Cancel(ctx context.Context, jobID int64) error {
	now := time.Now().UTC()
	if pending, err := l.OfJob(ctx, jobID); err == nil {
		for _, t := range pending {
			if t.State == model.TaskPending {
				l.settle(t.ID, ErrGivenUp)
			}
		}
	}
	if _, err := l.db.NewUpdate().Model((*model.WorkerTask)(nil)).
		Set("state = ?", model.TaskAbandoned).Set("finished_at = ?", now).Set("error = ?", "cancelled").
		Where("job_id = ? AND state = ?", jobID, model.TaskPending).Exec(ctx); err != nil {
		return err
	}
	_, err := l.db.NewUpdate().Model((*model.WorkerTask)(nil)).Set("cancel = ?", true).
		Where("job_id = ? AND state = ?", jobID, model.TaskLeased).Exec(ctx)
	return err
}

// HandBack returns tasks a worker is giving up on (it is shutting down), so
// another worker can have them at once instead of after a lease.
func (l *Ledger) HandBack(ctx context.Context, workerID int64, taskIDs []int64) (int, error) {
	q := l.db.NewUpdate().Model((*model.WorkerTask)(nil)).
		Set("state = ?", model.TaskPending).Set("lease_until = NULL").Set("worker_id = NULL").
		Where("worker_id = ? AND state = ?", workerID, model.TaskLeased)
	if len(taskIDs) > 0 {
		q = q.Where("id IN (?)", bun.In(taskIDs))
	}
	res, err := q.Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// Reap takes back tasks whose worker went quiet, and gives up on the ones
// that have been tried too often.
func (l *Ledger) Reap(ctx context.Context) (requeued, abandoned int, err error) {
	now := time.Now().UTC()
	var expired []model.WorkerTask
	if err := l.db.NewSelect().Model(&expired).
		Where("state = ? AND lease_until IS NOT NULL AND lease_until < ?", model.TaskLeased, now).
		Limit(100).Scan(ctx); err != nil {
		return 0, 0, err
	}
	for _, t := range expired {
		state, reason := model.TaskPending, ""
		if t.Attempt >= MaxAttempts {
			state, reason = model.TaskAbandoned, fmt.Sprintf("no worker finished it in %d tries", t.Attempt)
		}
		q := l.db.NewUpdate().Model((*model.WorkerTask)(nil)).
			Set("state = ?", state).Set("lease_until = NULL").Set("worker_id = NULL").Set("error = ?", reason).
			Where("id = ? AND state = ?", t.ID, model.TaskLeased)
		if state == model.TaskAbandoned {
			q = q.Set("finished_at = ?", now)
		}
		res, err := q.Exec(ctx)
		if err != nil {
			return requeued, abandoned, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			continue
		}
		if state == model.TaskAbandoned {
			abandoned++
			l.settle(t.ID, ErrGivenUp)
			t.State, t.Error = model.TaskAbandoned, reason
			l.givenUp(t)
		} else {
			requeued++
		}
		l.log.Info("a worker task came back", "task", t.ID, "job", t.JobID, "attempt", t.Attempt, "state", state)
		l.changed(t.JobID)
	}
	return requeued, abandoned, nil
}

// DropUnclaimed gives up on tasks nobody can do: they were written for a
// worker that was online at the time and has since gone away. It is part of
// the reaper, and separate so a test can run it on its own.
func (l *Ledger) DropUnclaimed(ctx context.Context) (int, error) {
	var waiting []model.WorkerTask
	err := l.db.NewSelect().Model(&waiting).
		Where("state = ? AND created_at < ?", model.TaskPending, time.Now().UTC().Add(-Unclaimed)).
		Limit(100).Scan(ctx)
	if err != nil || len(waiting) == 0 {
		return 0, err
	}
	dropped := 0
	for _, t := range waiting {
		ok, err := l.someoneCanDo(ctx, &t)
		if err != nil {
			return dropped, err
		}
		if ok {
			continue // a worker for this is here; it is just busy
		}
		reason := "no worker that can do this is online"
		res, err := l.db.NewUpdate().Model((*model.WorkerTask)(nil)).
			Set("state = ?", model.TaskAbandoned).Set("finished_at = ?", time.Now().UTC()).Set("error = ?", reason).
			Where("id = ? AND state = ?", t.ID, model.TaskPending).Exec(ctx)
		if err != nil {
			return dropped, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			continue
		}
		dropped++
		l.log.Info("nobody can do this task; handing it back", "task", t.ID, "job", t.JobID, "kind", t.Kind)
		l.settle(t.ID, ErrGivenUp)
		t.State, t.Error = model.TaskAbandoned, reason
		l.givenUp(t)
	}
	return dropped, nil
}

// someoneCanDo reports whether a worker that may do this task has been here
// recently.
func (l *Ledger) someoneCanDo(ctx context.Context, t *model.WorkerTask) (bool, error) {
	var list []model.Worker
	if err := l.db.NewSelect().Model(&list).Where("enabled = ?", true).Scan(ctx); err != nil {
		return false, err
	}
	pin := PinOf(t)
	for _, w := range list {
		if pin.Strict && !pin.Has(w.ID) {
			continue // kept for some workers, which have to be the ones
		}
		if takes(&w, t.Kind) && upscalesIfNeeded(&w, t) && w.LastSeenAt != nil && time.Since(*w.LastSeenAt) < OnlineWithin {
			return true, nil
		}
	}
	return false, nil
}

// OfJob lists a job's tasks, oldest first.
func (l *Ledger) OfJob(ctx context.Context, jobID int64) ([]model.WorkerTask, error) {
	var out []model.WorkerTask
	err := l.db.NewSelect().Model(&out).Where("job_id = ?", jobID).Order("id").Scan(ctx)
	return out, err
}

// OpenJobs is the set of jobs with a task still to be done, of the given
// kinds (any kind when none are given), so the download manager leaves them
// alone.
func (l *Ledger) OpenJobs(ctx context.Context, kinds ...string) (map[int64]bool, error) {
	var ids []int64
	q := l.db.NewSelect().Model((*model.WorkerTask)(nil)).Column("job_id").
		Where("state IN (?)", bun.In([]string{model.TaskPending, model.TaskLeased}))
	if len(kinds) > 0 {
		q = q.Where("kind IN (?)", bun.In(kinds))
	}
	err := q.Scan(ctx, &ids)
	out := map[int64]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out, err
}

// Orphan is the processing task a job left with a worker when the server
// stopped while waiting for it: still open, or done after the job went back
// to the queue (since), with nobody here to collect it.
func (l *Ledger) Orphan(ctx context.Context, jobID int64, since time.Time) (*model.WorkerTask, error) {
	var t model.WorkerTask
	err := l.db.NewSelect().Model(&t).Where("job_id = ? AND kind = ?", jobID, model.TaskEncode).
		WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("state IN (?)", bun.In([]string{model.TaskPending, model.TaskLeased})).
				WhereOr("state = ? AND finished_at > ?", model.TaskDone, since)
		}).
		OrderExpr("id DESC").Limit(1).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Prune deletes finished tasks older than keep, so the ledger stays the
// recent history and not the whole of it.
func (l *Ledger) Prune(ctx context.Context, keep time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-keep)
	res, err := l.db.NewDelete().Model((*model.WorkerTask)(nil)).
		Where("state IN (?) AND finished_at < ?", bun.In([]string{model.TaskDone, model.TaskFailed, model.TaskAbandoned}), cutoff).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// givenUp tells the job's owner that nobody is going to do this task.
func (l *Ledger) givenUp(t model.WorkerTask) {
	if l.Abandoned != nil {
		l.Abandoned(t)
	}
}

func (l *Ledger) changed(jobID int64) {
	if l.Changed != nil && jobID != 0 {
		l.Changed(jobID)
	}
}

// reapEvery is how often expired leases are looked for. It has to be well
// under Lease, or a worker that died holds its task for much longer than
// the lease says.
const reapEvery = 30 * time.Second

// keepFinished is how long finished tasks stay as history.
const keepFinished = 14 * 24 * time.Hour

// Start runs the reaper: tasks whose worker went quiet come back, and old
// finished ones are dropped once a day.
func (l *Ledger) Start(ctx context.Context) error {
	go func() {
		reap := time.NewTicker(reapEvery)
		defer reap.Stop()
		prune := time.NewTicker(24 * time.Hour)
		defer prune.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-reap.C:
				if _, _, err := l.Reap(ctx); err != nil && ctx.Err() == nil {
					l.log.Warn("could not take back expired worker tasks", "err", err)
				}
				if _, err := l.DropUnclaimed(ctx); err != nil && ctx.Err() == nil {
					l.log.Warn("could not hand back unclaimed worker tasks", "err", err)
				}
			case <-prune.C:
				if n, err := l.Prune(ctx, keepFinished); err != nil && ctx.Err() == nil {
					l.log.Warn("could not prune worker tasks", "err", err)
				} else if n > 0 {
					l.log.Info("pruned finished worker tasks", "count", n)
				}
			}
		}
	}()
	return nil
}

// ---- waiting for a task -----------------------------------------------------

// ErrGivenUp is delivered to whoever is waiting when a task was abandoned:
// no worker finished it, so the caller should do the work itself.
var ErrGivenUp = errors.New("no worker finished this task")

// Await says that someone is waiting for this task's outcome. The channel
// carries nil when a worker finished it and an error when it was given up
// on; nothing is sent while the task is simply passed to another worker.
// The caller must call Forget when it stops waiting.
func (l *Ledger) Await(taskID int64) <-chan error {
	ch := make(chan error, 1)
	l.waitMu.Lock()
	if l.waiting == nil {
		l.waiting = map[int64]chan error{}
	}
	l.waiting[taskID] = ch
	l.waitMu.Unlock()
	return ch
}

// Forget drops the interest registered by Await.
func (l *Ledger) Forget(taskID int64) {
	l.waitMu.Lock()
	delete(l.waiting, taskID)
	l.waitMu.Unlock()
}

// settle tells whoever waits for a task how it ended.
func (l *Ledger) settle(taskID int64, err error) {
	l.waitMu.Lock()
	ch, ok := l.waiting[taskID]
	delete(l.waiting, taskID)
	l.waitMu.Unlock()
	if ok {
		ch <- err
		close(ch)
	}
}

// CancelTask asks whoever holds one task to stop, or drops it if nobody has
// started it.
func (l *Ledger) CancelTask(ctx context.Context, taskID int64) error {
	now := time.Now().UTC()
	res, err := l.db.NewUpdate().Model((*model.WorkerTask)(nil)).
		Set("state = ?", model.TaskAbandoned).Set("finished_at = ?", now).Set("error = ?", "cancelled").
		Where("id = ? AND state = ?", taskID, model.TaskPending).Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		l.settle(taskID, ErrGivenUp)
		return nil
	}
	_, err = l.db.NewUpdate().Model((*model.WorkerTask)(nil)).Set("cancel = ?", true).
		Where("id = ? AND state = ?", taskID, model.TaskLeased).Exec(ctx)
	return err
}

// ---- which job a piece of work belongs to ------------------------------------

type adoptKey struct{}

// WithAdopted marks a context as picking up a processing task a worker
// already has, instead of handing out a new one.
func WithAdopted(ctx context.Context, t *model.WorkerTask) context.Context {
	return context.WithValue(ctx, adoptKey{}, t)
}

// Adopted is the task WithAdopted put in the context, if any.
func Adopted(ctx context.Context) *model.WorkerTask {
	t, _ := ctx.Value(adoptKey{}).(*model.WorkerTask)
	return t
}

type jobKey struct{}

// WithJob marks a context as belonging to a download job, so work started
// deeper in the pipeline (upscaling a batch, say) can be attached to it.
func WithJob(ctx context.Context, jobID int64) context.Context {
	return context.WithValue(ctx, jobKey{}, jobID)
}

// JobFrom is the job a context belongs to (0 when it belongs to none).
func JobFrom(ctx context.Context) int64 {
	id, _ := ctx.Value(jobKey{}).(int64)
	return id
}
