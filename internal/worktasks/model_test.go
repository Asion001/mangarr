package worktasks_test

import (
	"context"
	"testing"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/upscale"
	"github.com/Asion001/mangarr/internal/worktasks"
)

// TestWorkerRunsItsOwnModel: a worker set to its own upscaling model gets
// the batch with that model, at a scale the model has; the next worker to
// take the same task after a failed attempt starts from the profile's model
// again.
func TestWorkerRunsItsOwnModel(t *testing.T) {
	each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		l := ledger(d)
		job := seedJob(t, d)
		models := []any{
			map[string]any{"name": "waifu2x-cunet", "scales": []any{2, 4, 8}},
			map[string]any{"name": "realesrgan-x4plus-anime", "scales": []any{4}},
		}
		own := &model.Worker{ID: seedWorker(t, d, "own"), UpscaleModel: "realesrgan-x4plus-anime", Info: map[string]any{"models": models}}
		plain := &model.Worker{ID: seedWorker(t, d, "plain"), Info: map[string]any{"models": models}}
		missing := &model.Worker{ID: seedWorker(t, d, "missing"), UpscaleModel: "realcugan", Info: map[string]any{"models": models}}

		asked := upscale.Params{Model: "waifu2x-cunet", Scale: 2, Noise: 1, Format: "png"}
		task := &model.WorkerTask{JobID: job, Kind: model.TaskUpscale, Spec: map[string]any{"params": asked, "input": "in.zip"}}
		if err := l.Add(ctx, task); err != nil {
			t.Fatal(err)
		}
		lease := func(w *model.Worker) *model.WorkerTask {
			t.Helper()
			got, err := l.Claim(ctx, w.ID, []string{model.TaskUpscale})
			if err != nil || got == nil {
				t.Fatalf("claim: %v %+v", err, got)
			}
			if err := l.UseWorkerModel(ctx, got, w); err != nil {
				t.Fatal(err)
			}
			return got
		}
		handBack := func(w *model.Worker) {
			t.Helper()
			if _, err := l.HandBack(ctx, w.ID, nil); err != nil {
				t.Fatal(err)
			}
		}

		got := lease(own)
		want := upscale.Params{Model: "realesrgan-x4plus-anime", Scale: 4, Noise: 1, Format: "png"}
		if m := l.UsedModel(ctx, task.ID); m != want.Model {
			t.Fatalf("stored model %q, want %q", m, want.Model)
		}
		if p, _ := got.Spec["params"].(upscale.Params); p != want {
			t.Fatalf("the worker was handed %+v, want %+v", got.Spec["params"], want)
		}
		if got.Spec["input"] != "in.zip" {
			t.Fatalf("the rest of the spec was lost: %+v", got.Spec)
		}
		handBack(own)

		lease(plain)
		if m := l.UsedModel(ctx, task.ID); m != asked.Model {
			t.Fatalf("a worker without a model of its own ran %q, want the profile's %q", m, asked.Model)
		}
		handBack(plain)

		lease(missing)
		if m := l.UsedModel(ctx, task.ID); m != asked.Model {
			t.Fatalf("a worker without its chosen model ran %q, want the profile's %q", m, asked.Model)
		}
	})
}

// TestRouteModelBeatsTheWorkers: a model an upscale route chose stays, even
// on a worker set to a model of its own; a worker without it runs as if
// there were no route.
func TestRouteModelBeatsTheWorkers(t *testing.T) {
	each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		l := ledger(d)
		job := seedJob(t, d)
		models := []any{
			map[string]any{"name": "waifu2x-cunet", "scales": []any{2, 4}},
			map[string]any{"name": "realcugan", "scales": []any{2, 3, 4}},
		}
		own := &model.Worker{ID: seedWorker(t, d, "own"), UpscaleModel: "realcugan", Info: map[string]any{"models": models}}
		asked := upscale.Params{Model: "waifu2x-cunet", Scale: 4, Format: "png", Pinned: true}
		task := &model.WorkerTask{JobID: job, Kind: model.TaskUpscale, Spec: map[string]any{"params": asked}}
		if err := l.Add(ctx, task); err != nil {
			t.Fatal(err)
		}
		got, err := l.Claim(ctx, own.ID, []string{model.TaskUpscale})
		if err != nil || got == nil {
			t.Fatalf("claim: %v %+v", err, got)
		}
		if err := l.UseWorkerModel(ctx, got, own); err != nil {
			t.Fatal(err)
		}
		if m := l.UsedModel(ctx, task.ID); m != "waifu2x-cunet" {
			t.Fatalf("the route's model was swapped for %q", m)
		}
		if _, err := l.HandBack(ctx, own.ID, nil); err != nil {
			t.Fatal(err)
		}

		lacks := &model.Worker{ID: seedWorker(t, d, "lacks"), UpscaleModel: "realcugan",
			Info: map[string]any{"models": []any{map[string]any{"name": "realcugan", "scales": []any{2, 3, 4}}}}}
		if got, err = l.Claim(ctx, lacks.ID, []string{model.TaskUpscale}); err != nil || got == nil {
			t.Fatalf("claim: %v %+v", err, got)
		}
		if err := l.UseWorkerModel(ctx, got, lacks); err != nil {
			t.Fatal(err)
		}
		if m := l.UsedModel(ctx, task.ID); m != "realcugan" {
			t.Fatalf("a worker without the route's model ran %q, want its own", m)
		}
	})
}

// TestPinnedModelForEachWorker: routes that list several workers each
// choose a model for theirs; the worker that takes a batch runs its own.
func TestPinnedModelForEachWorker(t *testing.T) {
	each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		l := ledger(d)
		job := seedJob(t, d)
		models := []any{
			map[string]any{"name": "waifu2x-cunet", "scales": []any{2, 4}},
			map[string]any{"name": "realesrgan-x4plus-anime", "scales": []any{4}},
		}
		a := &model.Worker{ID: seedWorker(t, d, "a"), Info: map[string]any{"models": models}}
		b := &model.Worker{ID: seedWorker(t, d, "b"), Info: map[string]any{"models": models}}
		spec := map[string]any{"params": upscale.Params{Model: "waifu2x-cunet", Scale: 2, Format: "png"}}
		worktasks.Pin{Workers: []int64{a.ID, b.ID}, Models: map[int64]string{b.ID: "realesrgan-x4plus-anime"}}.Apply(spec)
		task := &model.WorkerTask{JobID: job, Kind: model.TaskUpscale, Spec: spec}
		if err := l.Add(ctx, task); err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			w    *model.Worker
			want string
		}{{a, "waifu2x-cunet"}, {b, "realesrgan-x4plus-anime"}} {
			got, err := l.Claim(ctx, c.w.ID, []string{model.TaskUpscale})
			if err != nil || got == nil {
				t.Fatalf("claim: %v %+v", err, got)
			}
			if err := l.UseWorkerModel(ctx, got, c.w); err != nil {
				t.Fatal(err)
			}
			if m := l.UsedModel(ctx, task.ID); m != c.want {
				t.Fatalf("worker %d ran %q, want %q", c.w.ID, m, c.want)
			}
			if _, err := l.HandBack(ctx, c.w.ID, nil); err != nil {
				t.Fatal(err)
			}
		}
	})
}
