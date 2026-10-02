package worktasks_test

import (
	"context"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/worktasks"
)

// TestPinnedTasksFollowTheList: upscale routes keep their batches for the
// workers they list, in order. The first of them with room takes a batch
// even with a better-placed worker idle, the next one takes the overflow,
// and the others get it only once none of the listed workers is online —
// and never when a route waits.
func TestPinnedTasksFollowTheList(t *testing.T) {
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
		other, first, second := seed("other", 10), seed("first", 20), seed("second", 30)
		add := func(pin worktasks.Pin) *model.WorkerTask {
			task := &model.WorkerTask{JobID: job, Kind: model.TaskUpscale, Spec: map[string]any{}}
			pin.Apply(task.Spec)
			if err := l.Add(ctx, task); err != nil {
				t.Fatal(err)
			}
			return task
		}
		claim := func(worker int64) *model.WorkerTask {
			task, err := l.Claim(ctx, worker, roles, 8, 1)
			if err != nil {
				t.Fatal(err)
			}
			return task
		}
		away := func(ids ...int64) {
			for _, id := range ids {
				if _, err := d.NewUpdate().Model((*model.Worker)(nil)).Set("last_seen_at = ?", now.Add(-time.Hour)).Where("id = ?", id).Exec(ctx); err != nil {
					t.Fatal(err)
				}
			}
		}
		list := worktasks.Pin{Workers: []int64{first, second}}

		a, b := add(list), add(list)
		if task := claim(other); task != nil {
			t.Fatalf("an unlisted worker took a kept batch: %+v", task)
		}
		if task := claim(second); task != nil {
			t.Fatalf("the second listed worker went ahead of the first, which has room: %+v", task)
		}
		if task := claim(first); task == nil || task.ID != a.ID {
			t.Fatalf("the first listed worker did not get the batch: %+v", task)
		}
		if task := claim(second); task == nil || task.ID != b.ID {
			t.Fatalf("the second listed worker did not take the overflow: %+v", task)
		}

		away(first, second)
		strict := add(worktasks.Pin{Workers: []int64{first, second}, Strict: true})
		if ok, err := l.CanTake(ctx, strict); err != nil || ok {
			t.Fatalf("a batch waiting for offline workers counted as takeable: %v %v", ok, err)
		}
		loose := add(list)
		if task := claim(other); task == nil || task.ID != loose.ID {
			t.Fatalf("a batch whose workers are all away did not fall back: %+v", task)
		}
		if _, err := l.HandBack(ctx, other, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := d.NewUpdate().Model((*model.WorkerTask)(nil)).Set("state = ?", model.TaskDone).Where("id <> ?", strict.ID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if task := claim(other); task != nil {
			t.Fatalf("a batch that waits for its workers went elsewhere: %+v", task)
		}
	})
}
