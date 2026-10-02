package worktasks_test

import (
	"context"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/worktasks"
)

// TestPinnedTasksWaitForTheirWorker: an upscale route keeps its batches for
// one worker, which takes them even with a better-placed worker idle; the
// others take them only while it is away, and never when the route waits.
func TestPinnedTasksWaitForTheirWorker(t *testing.T) {
	each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		l := ledger(d)
		job := seedJob(t, d)
		roles := []string{model.RoleUpscale}
		now := time.Now().UTC()
		seed := func(name string, priority int) int64 {
			w := &model.Worker{Name: name, KeyHash: "hash-" + name, Prefix: "mgw_" + name, Roles: roles, Enabled: true,
				Priority: priority, Info: map[string]any{"models": []any{map[string]any{"name": "m"}}}, CreatedAt: now, LastSeenAt: &now}
			if _, err := d.NewInsert().Model(w).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			return w.ID
		}
		fast, good := seed("fast", 10), seed("good", 20)
		add := func(pin worktasks.Pin) *model.WorkerTask {
			task := &model.WorkerTask{JobID: job, Kind: model.TaskUpscale, Spec: map[string]any{}}
			pin.Apply(task.Spec)
			if err := l.Add(ctx, task); err != nil {
				t.Fatal(err)
			}
			return task
		}
		claim := func(worker int64) *model.WorkerTask {
			task, err := l.Claim(ctx, worker, roles, 8, 2)
			if err != nil {
				t.Fatal(err)
			}
			return task
		}

		kept := add(worktasks.Pin{Worker: good})
		if task := claim(fast); task != nil {
			t.Fatalf("the fast worker took a batch kept for another: %+v", task)
		}
		if task := claim(good); task == nil || task.ID != kept.ID {
			t.Fatalf("the route's worker did not get its batch: %+v", task)
		}

		// the route's worker goes away
		gone := now.Add(-time.Hour)
		if _, err := d.NewUpdate().Model((*model.Worker)(nil)).Set("last_seen_at = ?", gone).Where("id = ?", good).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		strict := add(worktasks.Pin{Worker: good, Strict: true})
		if ok, err := l.CanTake(ctx, strict); err != nil || ok {
			t.Fatalf("a batch waiting for an offline worker counted as takeable: %v %v", ok, err)
		}
		loose := add(worktasks.Pin{Worker: good})
		if task := claim(fast); task == nil || task.ID != loose.ID {
			t.Fatalf("a batch whose worker is away did not fall back: %+v", task)
		}
		if task := claim(fast); task != nil {
			t.Fatalf("a batch that waits for its worker went elsewhere: %+v", task)
		}
	})
}
