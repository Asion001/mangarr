package app

import (
	"context"
	"strings"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/config"
	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/upscaler"
)

// wireUpscalers offers the built-in upscaler when this image has the tools
// for it. Upscaling on another machine is a worker with the upscale role
// (System → Workers), which brings its own module with it.
func (a *App) wireUpscalers(ctx context.Context) error {
	if a.Cfg.Mode == config.ModeIntegrated {
		if err := a.offerLocalUpscaler(ctx); err != nil {
			return err
		}
		return a.ShareUpscalePriority(ctx)
	}
	return nil
}

// LocalUpscalePriority is where the built-in upscaler starts: after the
// workers (which start at 100), since a worker is usually the machine with
// the better GPU.
const LocalUpscalePriority = 200

// sharedPriorityKey remembers that the built-in upscaler was moved into the
// workers' priority list.
const sharedPriorityKey = "upscale_priority_shared"

// ShareUpscalePriority moves the built-in upscaler into the workers' priority
// list, once. It used to be ordered against the "Workers" module as a whole;
// now the workers module ranks as its best online worker, so this keeps the
// order an install already had: after every upscale worker when the workers
// came first, before them otherwise.
func (a *App) ShareUpscalePriority(ctx context.Context) error {
	var done bool
	_ = a.Settings.Get(ctx, sharedPriorityKey, &done)
	if done {
		return nil
	}
	var defs []model.ProviderDefinition
	if err := a.DB.NewSelect().Model(&defs).Where("kind = ? AND user_id IS NULL", string(modules.KindUpscale)).Scan(ctx); err != nil {
		return err
	}
	var local, pool *model.ProviderDefinition
	for i := range defs {
		switch defs[i].Implementation {
		case "local":
			local = &defs[i]
		case "workers":
			pool = &defs[i]
		}
	}
	if local != nil {
		var workers []model.Worker
		if err := a.DB.NewSelect().Model(&workers).Scan(ctx); err != nil {
			return err
		}
		lowest, highest, found := 0, 0, false
		for _, w := range workers {
			if !w.HasRole(model.RoleUpscale) {
				continue
			}
			if !found || w.Priority < lowest {
				lowest = w.Priority
			}
			if !found || w.Priority > highest {
				highest = w.Priority
			}
			found = true
		}
		want := local.Priority
		if pool == nil || pool.Priority <= local.Priority {
			// the workers went first: keep the server behind all of them
			// (and behind the workers still to come, which start at 100)
			floor := 110
			if found {
				floor = max(floor, highest+10)
			}
			want = max(want, floor)
		} else if found {
			want = min(want, lowest-10)
		}
		if want != local.Priority {
			local.Priority = want
			if err := a.Modules.Update(ctx, local); err != nil {
				return err
			}
			a.Log.Info("moved the built-in upscaler into the workers' priority list", "priority", want)
		}
	}
	return a.Settings.Set(ctx, sharedPriorityKey, true)
}

// localOfferedKey remembers that the built-in upscaler was set up once (so
// deleting it in the UI sticks).
const localOfferedKey = "local_upscaler_offered"

// offerLocalUpscaler adds the built-in upscaler when the image has the tools.
func (a *App) offerLocalUpscaler(ctx context.Context) error {
	var offered bool
	_ = a.Settings.Get(ctx, localOfferedKey, &offered)
	dir := "/opt/upscalers"
	if offered || !upscaler.ToolsAvailable(dir) {
		return nil
	}
	// only enabled by default with a real GPU (lavapipe on the CPU is very slow)
	gpu := false
	for _, d := range upscaler.Devices() {
		if !strings.Contains(strings.ToLower(d), "llvmpipe") && !strings.Contains(strings.ToLower(d), "lavapipe") {
			gpu = true
		}
	}
	def := &model.ProviderDefinition{Kind: string(modules.KindUpscale), Implementation: "local", Name: "Built-in (this server)",
		Enabled: gpu, Priority: LocalUpscalePriority, Settings: map[string]any{"toolsDir": dir, "gpu": "auto"}}
	if err := a.Modules.Create(ctx, def); err != nil {
		return err
	}
	a.Log.Info("added the built-in upscaler", "gpu", gpu)
	return a.Settings.Set(ctx, localOfferedKey, true)
}

// OfferWorkersUpscaler adds the module that upscales on the workers, the
// first time a worker with that role dials in — so a GPU box only has to be
// given a key.
func (a *App) OfferWorkersUpscaler(ctx context.Context) {
	n, err := a.DB.NewSelect().Model((*model.ProviderDefinition)(nil)).
		Where("kind = ? AND implementation = ?", string(modules.KindUpscale), "workers").Count(ctx)
	if err != nil || n > 0 {
		return
	}
	def := &model.ProviderDefinition{Kind: string(modules.KindUpscale), Implementation: "workers", Name: "Workers",
		Enabled: true, Priority: 20, Settings: map[string]any{}}
	if err := a.Modules.Create(ctx, def); err != nil {
		a.Log.Warn("could not add the workers upscaler", "err", err)
		return
	}
	a.Log.Info("added the upscaler that hands batches to the workers")
	a.PushProcessBacklog("worker-online") // chapters that were waiting for one
}

// NameWorkers says which machine is doing each queued job, for the queue
// page: a job with a task out at a worker is not being done here.
func (a *App) NameWorkers(ctx context.Context, items []downloads.JobView) {
	if a.Tasks == nil || len(items) == 0 {
		return
	}
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	var rows []struct {
		JobID int64  `bun:"job_id"`
		Name  string `bun:"name"`
	}
	err := a.DB.NewSelect().TableExpr("worker_tasks AS t").ColumnExpr("t.job_id, w.name").
		Join("JOIN workers AS w ON w.id = t.worker_id").
		Where("t.state = ? AND t.job_id IN (?)", model.TaskLeased, bun.List(ids)).Scan(ctx, &rows)
	if err != nil || len(rows) == 0 {
		return
	}
	byJob := make(map[int64]string, len(rows))
	for _, r := range rows {
		byJob[r.JobID] = r.Name
	}
	for i := range items {
		items[i].Worker = byJob[items[i].ID]
	}
}
