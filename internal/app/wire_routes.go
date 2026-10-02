package app

import (
	"context"
	"fmt"
	"time"

	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/upscale"
	"github.com/Asion001/mangarr/internal/upscaling"
	"github.com/Asion001/mangarr/internal/worktasks"
)

// upscaleRoutes are the upscale routes set in System → Workers.
func (a *App) upscaleRoutes(ctx context.Context) []model.UpscaleRoute {
	dl, err := a.Settings.Downloads(ctx)
	if err != nil {
		return nil
	}
	return dl.UpscaleRoutes
}

// pickUpscaler is where a route's pages are upscaled here: this server's
// built-in upscaler, or the workers with the batches kept for one of them.
// A target that is away holds the pages back when the route waits for it
// (the chapter is tried again later), and leaves them to the priority order
// when it doesn't.
func (a *App) pickUpscaler(ctx context.Context, r model.UpscaleRoute, chosen upscale.Module) (upscale.Module, func(context.Context) context.Context, error) {
	if r.Target == model.RouteAnyUpscaler {
		return chosen, nil, nil
	}
	impl := "workers"
	if r.Target == model.RouteThisServer {
		impl = "local"
	}
	var target upscale.Module
	for _, t := range modules.ActiveAs[upscale.Module](a.Modules, modules.KindUpscale) {
		if t.Def.Implementation == impl && (a.Processing.Up.Online == nil || a.Processing.Up.Online(t.Def)) {
			target = t.Instance
		}
	}
	if r.Target == model.RouteThisServer {
		switch {
		case target != nil:
			return target, nil, nil
		case r.Wait:
			return nil, nil, upscaling.ErrNoUpscaler{Reason: "an upscale rule waits for this server's upscaler, which is off"}
		}
		return chosen, nil, nil
	}
	if target != nil && a.Tasks != nil && a.Tasks.Ready(ctx, r.Target, model.TaskUpscale) {
		pin := worktasks.Pin{Worker: r.Target, Strict: r.Wait}
		return target, func(ctx context.Context) context.Context { return worktasks.WithPin(ctx, pin) }, nil
	}
	if r.Wait {
		return nil, nil, upscaling.ErrNoUpscaler{Reason: fmt.Sprintf("an upscale rule waits for %s, which is offline or can't upscale", a.workerName(ctx, r.Target))}
	}
	return chosen, nil, nil
}

// routeChapter applies the upscale routes to a chapter processed on a
// worker (MANGARR_PROCESSING=workers). One worker processes the whole
// chapter, so it goes to the target of the route most of its pages fall
// under, when that worker can process it; the worker then upscales each
// page with its route's model.
func (a *App) routeChapter(ctx context.Context, cfg model.ProfileConfig, pages []downloads.PageFile) (worktasks.Pin, []model.UpscaleRoute) {
	routes := a.upscaleRoutes(ctx)
	if len(routes) == 0 || a.Tasks == nil {
		return worktasks.Pin{}, routes
	}
	scales := a.workerScales(ctx, cfg.Upscale.Model)
	count := map[int]int{}
	for _, p := range pages {
		if !upscaling.NeedsUpscale(p, cfg.Upscale.MinWidth) {
			continue
		}
		width := model.PageWidth(p.Width, p.Height)
		if i := model.RouteFor(routes, upscaling.ChooseScale(width, cfg.Upscale.MinWidth, scales), width); i >= 0 && routes[i].Target > 0 {
			count[i]++
		}
	}
	best := -1
	for i := range routes {
		if count[i] > 0 && (best < 0 || count[i] > count[best]) {
			best = i
		}
	}
	if best < 0 {
		return worktasks.Pin{}, routes
	}
	r := routes[best]
	if a.Tasks.Ready(ctx, r.Target, model.TaskEncode) && a.Tasks.Ready(ctx, r.Target, model.TaskUpscale) {
		return worktasks.Pin{Worker: r.Target, Strict: r.Wait}, routes
	}
	if r.Wait {
		return worktasks.Pin{Worker: r.Target, Strict: true}, routes // waits for it
	}
	return worktasks.Pin{}, routes
}

// workerScales are the scales of a model as the online workers have it
// (their first model's when none has it), for working out what scale a
// page needs before a worker has it.
func (a *App) workerScales(ctx context.Context, name string) []int {
	var list []model.Worker
	if err := a.DB.NewSelect().Model(&list).Where("enabled = ?", true).Scan(ctx); err != nil {
		return nil
	}
	var first []int
	for _, w := range list {
		if w.LastSeenAt == nil || time.Since(*w.LastSeenAt) >= worktasks.OnlineWithin {
			continue
		}
		for _, m := range worktasks.WorkerModels(&w) {
			if m.Name == name {
				return m.Scales
			}
			if first == nil {
				first = m.Scales
			}
		}
	}
	return first
}

// workerName names a worker for a message.
func (a *App) workerName(ctx context.Context, id int64) string {
	var w model.Worker
	if err := a.DB.NewSelect().Model(&w).Column("name").Where("id = ?", id).Scan(ctx); err != nil || w.Name == "" {
		return fmt.Sprintf("worker %d", id)
	}
	return w.Name
}
