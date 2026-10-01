package worktasks_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/worktasks"
)

// each runs a test against both dialects: the hand-out is a
// compare-and-set, and it has to be one on both.
func each(t *testing.T, run func(t *testing.T, d *db.DB)) {
	t.Run("sqlite", func(t *testing.T) { run(t, dbtest.SQLite(t)) })
	t.Run("postgres", func(t *testing.T) { run(t, dbtest.Postgres(t)) })
}

func ledger(d *db.DB) *worktasks.Ledger {
	return worktasks.New(d, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// seedJob makes the download job tasks hang off, and the series and chapter
// it needs to exist at all.
func seedJob(t *testing.T, d *db.DB) int64 {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	root := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
	profile := &model.Profile{Name: "test", CreatedAt: now, UpdatedAt: now}
	for _, m := range []any{root, profile} {
		if _, err := d.NewInsert().Model(m).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	ser := &model.Series{Title: "Series", SortTitle: "series", RootFolderID: root.ID, ProfileID: profile.ID,
		Path: "Series", AddedAt: now, UpdatedAt: now}
	if _, err := d.NewInsert().Model(ser).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	ch := &model.Chapter{SeriesID: ser.ID, NumberKey: "1", NumberSort: 1, State: model.ChapterMissing,
		FirstSeenAt: now, UpdatedAt: now}
	if _, err := d.NewInsert().Model(ch).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	job := &model.DownloadJob{Kind: model.JobKindDownload, Status: model.JobQueued, SeriesID: ser.ID, ChapterID: ch.ID,
		NotBefore: now, CreatedAt: now, UpdatedAt: now}
	if _, err := d.NewInsert().Model(job).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return job.ID
}

func seedWorker(t *testing.T, d *db.DB, name string) int64 {
	t.Helper()
	w := &model.Worker{Name: name, KeyHash: "hash-" + name, Prefix: "mgw_" + name, Roles: []string{model.RoleDownload},
		Enabled: true, Info: map[string]any{}, CreatedAt: time.Now().UTC()}
	if _, err := d.NewInsert().Model(w).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	return w.ID
}

// TestOneWorkerWins: two workers reaching for the same task at the same
// moment, fifty times over — exactly one gets each task.
func TestOneWorkerWins(t *testing.T) {
	each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		l := ledger(d)
		job := seedJob(t, d)
		a, b := seedWorker(t, d, "a"), seedWorker(t, d, "b")
		const tasks = 50
		for i := range tasks {
			if err := l.Add(ctx, &model.WorkerTask{JobID: job, Kind: model.TaskDownload, Seq: i}); err != nil {
				t.Fatal(err)
			}
		}
		var mu sync.Mutex
		got := map[int64]int{}
		var wg sync.WaitGroup
		for _, id := range []int64{a, b} {
			wg.Add(1)
			go func(worker int64) {
				defer wg.Done()
				for {
					task, err := l.Claim(ctx, worker, []string{model.TaskDownload})
					if err != nil {
						t.Error(err)
						return
					}
					if task == nil {
						return
					}
					mu.Lock()
					got[task.ID]++
					mu.Unlock()
				}
			}(id)
		}
		wg.Wait()
		if len(got) != tasks {
			t.Fatalf("%d of %d tasks were handed out", len(got), tasks)
		}
		for id, n := range got {
			if n != 1 {
				t.Fatalf("task %d went to %d workers", id, n)
			}
		}
	})
}

func TestClaimHonorsCapacityAndPriority(t *testing.T) {
	each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		l := ledger(d)
		job := seedJob(t, d)
		high, low := seedWorker(t, d, "high"), seedWorker(t, d, "low")
		now := time.Now().UTC()
		if _, err := d.NewUpdate().Model((*model.Worker)(nil)).
			Set("last_seen_at = ?", now).Set("priority = CASE WHEN id = ? THEN 10 ELSE 20 END", high).
			Where("id IN (?)", bun.In([]int64{high, low})).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		for i := range 3 {
			if err := l.Add(ctx, &model.WorkerTask{JobID: job, Kind: model.TaskDownload, Seq: i}); err != nil {
				t.Fatal(err)
			}
		}
		if task, err := l.Claim(ctx, low, []string{model.TaskDownload}, 2, 1); err != nil || task != nil {
			t.Fatalf("lower priority claimed while higher had capacity: %v %+v", err, task)
		}
		first, err := l.Claim(ctx, high, []string{model.TaskDownload}, 2, 1)
		if err != nil || first == nil {
			t.Fatalf("higher priority did not claim: %v %+v", err, first)
		}
		second, err := l.Claim(ctx, low, []string{model.TaskDownload}, 2, 1)
		if err != nil || second == nil {
			t.Fatalf("fallback worker did not claim when higher was full: %v %+v", err, second)
		}
		if task, err := l.Claim(ctx, high, []string{model.TaskDownload}, 2, 1); err != nil || task != nil {
			t.Fatalf("global cap was exceeded: %v %+v", err, task)
		}
		if err := l.Finish(ctx, first.ID, high, worktasks.Progress{}); err != nil {
			t.Fatal(err)
		}
		if task, err := l.Claim(ctx, high, []string{model.TaskDownload}, 2, 1); err != nil || task == nil {
			t.Fatalf("released capacity was not reused: %v %+v", err, task)
		}
	})
}

// TestPriorityOnlyHoldsBackWorkItCanTake: a better-placed worker with room
// keeps back only the kinds it can do itself. One without an upscaling model
// never asks for upscale work, so it must not starve the GPU box below it.
func TestPriorityOnlyHoldsBackWorkItCanTake(t *testing.T) {
	each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		l := ledger(d)
		job := seedJob(t, d)
		roles := []string{model.RoleDownload, model.RoleUpscale}
		now := time.Now().UTC()
		seed := func(name string, priority int, info map[string]any) int64 {
			w := &model.Worker{Name: name, KeyHash: "hash-" + name, Prefix: "mgw_" + name, Roles: roles, Enabled: true,
				Priority: priority, Info: info, CreatedAt: now, LastSeenAt: &now}
			if _, err := d.NewInsert().Model(w).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			return w.ID
		}
		seed("cpu", 10, map[string]any{"cpus": 8})
		gpu := seed("gpu", 20, map[string]any{"models": []any{map[string]any{"name": "m"}}})
		if err := l.Add(ctx, &model.WorkerTask{JobID: job, Kind: model.TaskUpscale}); err != nil {
			t.Fatal(err)
		}
		task, err := l.Claim(ctx, gpu, roles, 4, 1)
		if err != nil || task == nil || task.Kind != model.TaskUpscale {
			t.Fatalf("upscale work waited on a worker that cannot upscale: %v %+v", err, task)
		}
		if err := l.Add(ctx, &model.WorkerTask{JobID: job, Kind: model.TaskDownload, Seq: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := d.NewUpdate().Model((*model.Worker)(nil)).Set("concurrent = 2").Where("id = ?", gpu).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if task, err := l.Claim(ctx, gpu, roles, 4, 1); err != nil || task != nil {
			t.Fatalf("download work skipped the idle higher-priority worker: %v %+v", err, task)
		}
	})
}

// TestEqualPriorityTakeTurns: a worker allowed many tasks at once doesn't
// take every chapter while another of the same priority sits idle, and a
// peer that can't upscale doesn't hold back work only an upscaler can do.
func TestEqualPriorityTakeTurns(t *testing.T) {
	each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		l := ledger(d)
		job := seedJob(t, d)
		roles := []string{model.RoleUpscale, model.RoleEncode}
		now := time.Now().UTC()
		gpu := map[string]any{worktasks.InfoProcess: true, "models": []any{map[string]any{"name": "m"}}}
		seed := func(name string) int64 {
			w := &model.Worker{Name: name, KeyHash: "hash-" + name, Prefix: "mgw_" + name, Roles: roles, Enabled: true,
				Priority: 50, Concurrent: 10, Info: gpu, CreatedAt: now, LastSeenAt: &now}
			if _, err := d.NewInsert().Model(w).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			return w.ID
		}
		pc, mac := seed("pc"), seed("mac")
		for i := range 4 {
			if err := l.Add(ctx, &model.WorkerTask{JobID: job, Kind: model.TaskEncode, Seq: i}); err != nil {
				t.Fatal(err)
			}
		}
		if task, err := l.Claim(ctx, pc, roles, 0, 1); err != nil || task == nil {
			t.Fatalf("the first worker got nothing: %v %+v", err, task)
		}
		if task, err := l.Claim(ctx, pc, roles, 0, 1); err != nil || task != nil {
			t.Fatalf("one worker took a second chapter while its peer was idle: %v %+v", err, task)
		}
		if task, err := l.Claim(ctx, mac, roles, 0, 1); err != nil || task == nil {
			t.Fatalf("the idle peer got nothing: %v %+v", err, task)
		}
		if task, err := l.Claim(ctx, pc, roles, 0, 1); err != nil || task == nil {
			t.Fatalf("even workers didn't take the next turn: %v %+v", err, task)
		}

		// a peer without an upscaler is no reason to wait
		if _, err := d.NewUpdate().Model((*model.Worker)(nil)).Set("info = ?", map[string]any{worktasks.InfoProcess: true}).
			Where("id = ?", mac).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := d.NewUpdate().Model((*model.WorkerTask)(nil)).Set("state = ?", model.TaskDone).
			Where("worker_id = ?", mac).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if task, err := l.Claim(ctx, pc, roles, 0, 1); err != nil || task == nil {
			t.Fatalf("the upscaler waited on a peer that can't upscale: %v %+v", err, task)
		}
	})
}

// TestLeaseComesBack: a worker that goes quiet loses its task, and a task
// nobody finishes is given up on rather than handed out forever.
func TestLeaseComesBack(t *testing.T) {
	each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		l := ledger(d)
		job := seedJob(t, d)
		worker := seedWorker(t, d, "quiet")
		if err := l.Add(ctx, &model.WorkerTask{JobID: job, Kind: model.TaskDownload}); err != nil {
			t.Fatal(err)
		}
		expire := func(id int64) {
			t.Helper()
			if _, err := d.NewUpdate().Model((*model.WorkerTask)(nil)).Set("lease_until = ?", time.Now().UTC().Add(-time.Minute)).
				Where("id = ?", id).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		var last *model.WorkerTask
		for attempt := 1; attempt <= worktasks.MaxAttempts; attempt++ {
			task, err := l.Claim(ctx, worker, []string{model.TaskDownload})
			if err != nil || task == nil {
				t.Fatalf("attempt %d: %v %+v", attempt, err, task)
			}
			if task.Attempt != attempt {
				t.Fatalf("attempt counted as %d", task.Attempt)
			}
			last = task
			expire(task.ID)
			requeued, abandoned, err := l.Reap(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if attempt < worktasks.MaxAttempts && (requeued != 1 || abandoned != 0) {
				t.Fatalf("attempt %d: requeued %d abandoned %d", attempt, requeued, abandoned)
			}
			if attempt == worktasks.MaxAttempts && abandoned != 1 {
				t.Fatalf("a task nobody finished was not given up on: requeued %d abandoned %d", requeued, abandoned)
			}
		}
		// and its worker can no longer report on it
		if _, err := l.Heartbeat(ctx, last.ID, worker, worktasks.Progress{}); err != worktasks.ErrNotYours {
			t.Fatalf("heartbeat on an abandoned task: %v", err)
		}
		if err := l.Finish(ctx, last.ID, worker, worktasks.Progress{}); err != worktasks.ErrNotYours {
			t.Fatalf("finishing an abandoned task: %v", err)
		}
	})
}

// TestFinishCountsOnTheWorker: what a task did is added to its worker in
// the same transaction that closes it, so the totals can't drift.
func TestFinishCountsOnTheWorker(t *testing.T) {
	each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		l := ledger(d)
		job := seedJob(t, d)
		worker := seedWorker(t, d, "busy")
		for i := range 2 {
			if err := l.Add(ctx, &model.WorkerTask{JobID: job, Kind: model.TaskDownload, Seq: i}); err != nil {
				t.Fatal(err)
			}
		}
		done, _ := l.Claim(ctx, worker, []string{model.TaskDownload})
		failed, _ := l.Claim(ctx, worker, []string{model.TaskDownload})
		if done == nil || failed == nil {
			t.Fatal("expected two tasks")
		}
		// a heartbeat keeps the lease and reports progress
		if cancelled, err := l.Heartbeat(ctx, done.ID, worker, worktasks.Progress{PagesDone: 5, PagesTotal: 20}); err != nil || cancelled {
			t.Fatalf("heartbeat: %v %v", err, cancelled)
		}
		if err := l.Finish(ctx, done.ID, worker, worktasks.Progress{PagesDone: 20, PagesTotal: 20, BytesIn: 1000, BytesOut: 900, GPU: "1"}); err != nil {
			t.Fatal(err)
		}
		var finished model.WorkerTask
		if err := d.NewSelect().Model(&finished).Where("id = ?", done.ID).Scan(ctx); err != nil || finished.Spec["gpu"] != "1" {
			t.Fatalf("GPU report not stored on task: %+v %v", finished, err)
		}
		if err := l.Fail(ctx, failed.ID, worker, "the site said no", worktasks.Progress{PagesDone: 1, BytesIn: 10}); err != nil {
			t.Fatal(err)
		}
		var w model.Worker
		if err := d.NewSelect().Model(&w).Where("id = ?", worker).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if w.TasksDone != 1 || w.TasksFailed != 1 || w.PagesDone != 21 || w.BytesIn != 1010 || w.BytesOut != 900 {
			t.Fatalf("worker totals: %+v", w)
		}
		if w.BusySeconds <= 0 {
			t.Fatalf("no time counted: %+v", w)
		}
		// nothing is left for anyone to pick up
		if open, err := l.OpenJobs(ctx); err != nil || len(open) != 0 {
			t.Fatalf("open jobs: %v %+v", err, open)
		}
	})
}

// TestCancel: removing a job drops what nobody started and asks whoever
// holds the rest to stop.
func TestCancel(t *testing.T) {
	each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		l := ledger(d)
		job := seedJob(t, d)
		worker := seedWorker(t, d, "runner")
		for i := range 2 {
			if err := l.Add(ctx, &model.WorkerTask{JobID: job, Kind: model.TaskDownload, Seq: i}); err != nil {
				t.Fatal(err)
			}
		}
		held, _ := l.Claim(ctx, worker, []string{model.TaskDownload})
		if err := l.Cancel(ctx, job); err != nil {
			t.Fatal(err)
		}
		cancelled, err := l.Heartbeat(ctx, held.ID, worker, worktasks.Progress{})
		if err != nil || !cancelled {
			t.Fatalf("the worker was not told to stop: %v %v", err, cancelled)
		}
		if next, err := l.Claim(ctx, worker, []string{model.TaskDownload}); err != nil || next != nil {
			t.Fatalf("a cancelled job still handed out work: %v %+v", err, next)
		}
	})
}

// TestPrune keeps the ledger to recent history.
func TestPrune(t *testing.T) {
	each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		l := ledger(d)
		job := seedJob(t, d)
		worker := seedWorker(t, d, "old")
		if err := l.Add(ctx, &model.WorkerTask{JobID: job, Kind: model.TaskUpscale}); err != nil {
			t.Fatal(err)
		}
		task, _ := l.Claim(ctx, worker, []string{model.TaskUpscale})
		if err := l.Finish(ctx, task.ID, worker, worktasks.Progress{}); err != nil {
			t.Fatal(err)
		}
		if n, err := l.Prune(ctx, 14*24*time.Hour); err != nil || n != 0 {
			t.Fatalf("a task from just now was pruned: %v %d", err, n)
		}
		if _, err := d.NewUpdate().Model((*model.WorkerTask)(nil)).Set("finished_at = ?", time.Now().UTC().Add(-30*24*time.Hour)).
			Where("id = ?", task.ID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if n, err := l.Prune(ctx, 14*24*time.Hour); err != nil || n != 1 {
			t.Fatalf("prune: %v %d", err, n)
		}
	})
}

// TestUnclaimedComesBack: a task written while a worker was online, for a
// worker that has since gone away, is handed back instead of waiting
// forever — but one waiting behind a busy worker is left alone.
func TestUnclaimedComesBack(t *testing.T) {
	each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		l := ledger(d)
		job := seedJob(t, d)
		worker := seedWorker(t, d, "gone")
		var given []model.WorkerTask
		l.Abandoned = func(task model.WorkerTask) { given = append(given, task) }
		if err := l.Add(ctx, &model.WorkerTask{JobID: job, Kind: model.TaskDownload}); err != nil {
			t.Fatal(err)
		}
		age := func() {
			t.Helper()
			if _, err := d.NewUpdate().Model((*model.WorkerTask)(nil)).Set("created_at = ?", time.Now().UTC().Add(-time.Hour)).
				Where("state = ?", model.TaskPending).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}

		// the worker is here, just busy: the task waits for it
		seen := time.Now().UTC()
		if _, err := d.NewUpdate().Model((*model.Worker)(nil)).Set("last_seen_at = ?", seen).Where("id = ?", worker).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		age()
		if n, err := l.DropUnclaimed(ctx); err != nil || n != 0 {
			t.Fatalf("a task waiting for a worker that is here was dropped: %v %d", err, n)
		}

		// now it goes away
		gone := time.Now().UTC().Add(-10 * time.Minute)
		if _, err := d.NewUpdate().Model((*model.Worker)(nil)).Set("last_seen_at = ?", gone).Where("id = ?", worker).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if n, err := l.DropUnclaimed(ctx); err != nil || n != 1 {
			t.Fatalf("drop: %v %d", err, n)
		}
		if len(given) != 1 || given[0].JobID != job {
			t.Fatalf("the job wasn't told: %+v", given)
		}
		if _, err := l.Claim(ctx, worker, []string{model.TaskDownload}); err != nil {
			t.Fatal(err)
		}
	})
}

// TestUnofferedRoleComesBack: a worker given the download role whose own
// configuration leaves it out never asks for downloads, so a download
// written for it is handed back even while it is online.
func TestUnofferedRoleComesBack(t *testing.T) {
	each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		l := ledger(d)
		job := seedJob(t, d)
		worker := seedWorker(t, d, "encoder")
		seen := time.Now().UTC()
		info := map[string]any{model.InfoRoles: []string{model.RoleEncode, model.RoleUpscale}}
		if _, err := d.NewUpdate().Model((*model.Worker)(nil)).Set("last_seen_at = ?", seen).Set("info = ?", info).
			Where("id = ?", worker).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if ok, err := l.CanDo(ctx, model.TaskDownload); err != nil || ok {
			t.Fatalf("a worker that doesn't offer downloads counts as able to: %v %v", ok, err)
		}
		var given []model.WorkerTask
		l.Abandoned = func(task model.WorkerTask) { given = append(given, task) }
		if err := l.Add(ctx, &model.WorkerTask{JobID: job, Kind: model.TaskDownload}); err != nil {
			t.Fatal(err)
		}
		if _, err := d.NewUpdate().Model((*model.WorkerTask)(nil)).Set("created_at = ?", time.Now().UTC().Add(-time.Hour)).
			Where("state = ?", model.TaskPending).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if n, err := l.DropUnclaimed(ctx); err != nil || n != 1 || len(given) != 1 {
			t.Fatalf("the download waited for a worker that never asks for one: %v %d %d", err, n, len(given))
		}

		// one that offers it keeps it
		info[model.InfoRoles] = []string{model.RoleDownload}
		if _, err := d.NewUpdate().Model((*model.Worker)(nil)).Set("info = ?", info).Where("id = ?", worker).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if ok, err := l.CanDo(ctx, model.TaskDownload); err != nil || !ok {
			t.Fatalf("a worker offering downloads doesn't count: %v %v", ok, err)
		}
	})
}

// A worker from before processing moved to workers still holds the encode
// role, but only one that says it can process pages gets that work.
func TestEncodeNeedsAWorkerThatProcesses(t *testing.T) {
	each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		l := ledger(d)
		job := seedJob(t, d)
		old, current := seedWorker(t, d, "old"), seedWorker(t, d, "new")
		now := time.Now().UTC()
		for id, info := range map[int64]map[string]any{old: {}, current: {worktasks.InfoProcess: true}} {
			if _, err := d.NewUpdate().Model((*model.Worker)(nil)).Set("roles = ?", []string{model.RoleEncode}).
				Set("info = ?", info).Set("last_seen_at = ?", now).Where("id = ?", id).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if err := l.Add(ctx, &model.WorkerTask{JobID: job, Kind: model.TaskEncode, Spec: map[string]any{}}); err != nil {
			t.Fatal(err)
		}
		if task, err := l.Claim(ctx, old, []string{model.TaskEncode}, 0, 1); err != nil || task != nil {
			t.Fatalf("the old worker got %v, %v", task, err)
		}
		if task, err := l.Claim(ctx, current, []string{model.TaskEncode}, 0, 1); err != nil || task == nil {
			t.Fatalf("the new worker got %v, %v", task, err)
		}
		if ok, err := l.CanDo(ctx, model.TaskEncode); err != nil || !ok {
			t.Fatalf("CanDo = %v, %v", ok, err)
		}
	})
}

// Pages that need upscaling go only to a worker with an upscaler; one that
// only re-encodes still gets the rest.
func TestUpscaledEncodeNeedsAnUpscaler(t *testing.T) {
	each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		l := ledger(d)
		job := seedJob(t, d)
		plain, gpu := seedWorker(t, d, "plain"), seedWorker(t, d, "gpu")
		now := time.Now().UTC()
		infos := map[int64]map[string]any{
			plain: {worktasks.InfoProcess: true},
			gpu:   {worktasks.InfoProcess: true, "models": []any{map[string]any{"name": "realesrgan-x4plus-anime"}}},
		}
		for id, info := range infos {
			if _, err := d.NewUpdate().Model((*model.Worker)(nil)).Set("roles = ?", []string{model.RoleEncode}).
				Set("info = ?", info).Set("last_seen_at = ?", now).Where("id = ?", id).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		upscaled := &model.WorkerTask{Kind: model.TaskEncode, Spec: map[string]any{worktasks.SpecNeedsUpscale: true}}
		if ok, err := l.CanTake(ctx, upscaled); err != nil || !ok {
			t.Fatalf("CanTake with the GPU worker online = %v, %v", ok, err)
		}
		upscaled.JobID = job
		if err := l.Add(ctx, upscaled); err != nil {
			t.Fatal(err)
		}
		if task, err := l.Claim(ctx, plain, []string{model.TaskEncode}, 0, 1); err != nil || task != nil {
			t.Fatalf("the worker without an upscaler got %v, %v", task, err)
		}
		if err := l.Add(ctx, &model.WorkerTask{JobID: job, Kind: model.TaskEncode, Spec: map[string]any{worktasks.SpecNeedsUpscale: false}}); err != nil {
			t.Fatal(err)
		}
		if task, err := l.Claim(ctx, plain, []string{model.TaskEncode}, 0, 1); err != nil || task == nil || task.ID == upscaled.ID {
			t.Fatalf("the worker without an upscaler should get the plain task, got %v, %v", task, err)
		}
		if task, err := l.Claim(ctx, gpu, []string{model.TaskEncode}, 0, 1); err != nil || task == nil || task.ID != upscaled.ID {
			t.Fatalf("the GPU worker should get the upscaled task, got %v, %v", task, err)
		}
		// with only the plain worker around, nobody can take upscaled pages
		if _, err := d.NewUpdate().Model((*model.Worker)(nil)).Set("enabled = ?", false).Where("id = ?", gpu).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if ok, err := l.CanTake(ctx, &model.WorkerTask{Kind: model.TaskEncode, Spec: map[string]any{worktasks.SpecNeedsUpscale: true}}); err != nil || ok {
			t.Fatalf("CanTake without an upscaler = %v, %v", ok, err)
		}
	})
}
