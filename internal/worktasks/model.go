package worktasks

import (
	"context"
	"encoding/json"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/upscale"
)

// UseWorkerModel points an upscale task at the model its worker is set to
// use, at the nearest scale that model has. The profile's request is kept
// beside it, so a task that goes to another worker after a failed attempt
// starts again from what the profile asked for. A worker without a model
// of its own, or one that doesn't have it, runs the profile's.
func (l *Ledger) UseWorkerModel(ctx context.Context, t *model.WorkerTask, w *model.Worker) error {
	if t == nil || w == nil || t.Kind != model.TaskUpscale {
		return nil
	}
	requested, ok := specParams(t.Spec["requested"])
	if !ok {
		if requested, ok = specParams(t.Spec["params"]); !ok {
			return nil
		}
	}
	params := requested
	if requested.Pinned && !hasModel(w, requested.Model) {
		params.Pinned = false // a route's model this worker lacks: as if there were no route
	}
	if w.UpscaleModel != "" && w.UpscaleModel != requested.Model && !params.Pinned {
		for _, m := range WorkerModels(w) {
			if m.Name == w.UpscaleModel {
				params.Model, params.Scale = m.Name, upscale.FitScale(requested.Scale, m.Scales)
				break
			}
		}
	}
	if current, ok := specParams(t.Spec["params"]); ok && current == params {
		return nil
	}
	spec := make(map[string]any, len(t.Spec)+1)
	for k, v := range t.Spec {
		spec[k] = v
	}
	spec["requested"], spec["params"] = requested, params
	if _, err := l.db.NewUpdate().Model((*model.WorkerTask)(nil)).Set("spec = ?", spec).
		Where("id = ? AND worker_id = ? AND state = ?", t.ID, w.ID, model.TaskLeased).Exec(ctx); err != nil {
		return err
	}
	t.Spec = spec
	return nil
}

// UsedModel is the model an upscale task was run with.
func (l *Ledger) UsedModel(ctx context.Context, taskID int64) string {
	var t model.WorkerTask
	if err := l.db.NewSelect().Model(&t).Column("spec").Where("id = ?", taskID).Scan(ctx); err != nil {
		return ""
	}
	p, _ := specParams(t.Spec["params"])
	return p.Model
}

// specParams reads upscaling parameters however the spec holds them: a
// struct or raw JSON before it is stored, a decoded map after.
func specParams(v any) (upscale.Params, bool) {
	var p upscale.Params
	if v == nil {
		return p, false
	}
	data, err := json.Marshal(v)
	if err != nil || json.Unmarshal(data, &p) != nil {
		return p, false
	}
	return p, true
}

// WorkerModels is the upscaling models a worker said it has.
func WorkerModels(w *model.Worker) []upscale.Model {
	data, err := json.Marshal(w.Info["models"])
	if err != nil {
		return nil
	}
	var out []upscale.Model
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	return out
}

// hasModel reports whether a worker said it has a model.
func hasModel(w *model.Worker, name string) bool {
	for _, m := range WorkerModels(w) {
		if m.Name == name {
			return true
		}
	}
	return false
}
