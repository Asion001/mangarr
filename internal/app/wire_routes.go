package app

import (
	"context"
	"fmt"
	"strings"
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

// routeStop is one upscaler in the list the routes a page matches make.
type routeStop struct {
	route model.UpscaleRoute
	up    upscale.Module // this server's, or the workers'
	ready bool
}

// routeStops lists the upscalers of some routes in order, each with
// whether it could take pages now.
func (a *App) routeStops(ctx context.Context, routes []model.UpscaleRoute, chosen upscale.Module) []routeStop {
	var local, pool upscale.Module
	for _, t := range modules.ActiveAs[upscale.Module](a.Modules, modules.KindUpscale) {
		if a.Processing.Up.Online != nil && !a.Processing.Up.Online(t.Def) {
			continue
		}
		switch t.Def.Implementation {
		case "local":
			local = t.Instance
		case "workers":
			pool = t.Instance
		}
	}
	out := make([]routeStop, 0, len(routes))
	for _, r := range routes {
		st := routeStop{route: r}
		switch {
		case r.Target == model.RouteAnyUpscaler:
			st.up, st.ready = chosen, true
		case r.Target == model.RouteThisServer:
			st.up, st.ready = local, local != nil
		default:
			st.up = pool
			st.ready = pool != nil && a.Tasks != nil && a.Tasks.Ready(ctx, r.Target, model.TaskUpscale)
		}
		out = append(out, st)
	}
	return out
}

// pickUpscaler is where pages matching some routes are upscaled here. The
// routes list upscalers in order and the pages go to the first that is
// around: this server's built-in upscaler, any upscaler, or the workers,
// with the batches kept for the workers listed from there on (the first
// with room takes them). With none around the pages wait when a route
// waits (the chapter is tried again later) and go by priority otherwise.
func (a *App) pickUpscaler(ctx context.Context, routes []model.UpscaleRoute, chosen upscale.Module) (upscaling.Lane, error) {
	stops := a.routeStops(ctx, routes, chosen)
	wait := false
	for _, st := range stops {
		wait = wait || st.route.Wait
		if !st.ready {
			continue
		}
		if st.route.Target <= 0 {
			return upscaling.Lane{Up: st.up, Model: st.route.Model}, nil
		}
		// every listed worker up to the first other upscaler that is around,
		// so one that comes back still gets its pages first
		var listed []model.UpscaleRoute
		open := false
		for _, s := range stops {
			if s.route.Target > 0 {
				listed = append(listed, s.route)
			} else if s.ready {
				open = true
				break
			}
		}
		pin := workerPin(listed, open)
		return upscaling.Lane{Up: st.up, Pin: func(ctx context.Context) context.Context { return worktasks.WithPin(ctx, pin) }}, nil
	}
	if wait {
		names := make([]string, 0, len(routes))
		for _, r := range routes {
			names = append(names, a.targetName(ctx, r.Target))
		}
		return upscaling.Lane{}, upscaling.ErrNoUpscaler{Reason: "an upscale rule waits for " + strings.Join(names, " or ") + ", and none is available"}
	}
	return upscaling.Lane{Up: chosen}, nil
}

// workerPin keeps batches for the workers of some routes, in order, with
// each worker's model. It is strict when a route waits and no other
// upscaler is open to the pages after them.
func workerPin(routes []model.UpscaleRoute, open bool) worktasks.Pin {
	pin := worktasks.Pin{Models: map[int64]string{}}
	wait := false
	for _, r := range routes {
		wait = wait || r.Wait
		if !pin.Has(r.Target) {
			pin.Workers = append(pin.Workers, r.Target)
			if r.Model != "" {
				pin.Models[r.Target] = r.Model
			}
		}
	}
	pin.Strict = wait && !open
	return pin
}

// routeChapter applies the upscale routes to a chapter processed on a
// worker (MANGARR_PROCESSING=workers). One worker processes the whole
// chapter, so it is kept for the workers of the routes most of its pages
// match, the first of them with room taking it; that worker then upscales
// each page with its route's model.
func (a *App) routeChapter(ctx context.Context, cfg model.ProfileConfig, pages []downloads.PageFile) (worktasks.Pin, []model.UpscaleRoute) {
	routes := a.upscaleRoutes(ctx)
	if len(routes) == 0 || a.Tasks == nil {
		return worktasks.Pin{}, routes
	}
	scales := a.workerScales(ctx, cfg.Upscale.Model)
	count := map[string]int{}
	chains := map[string][]int{}
	for _, p := range pages {
		if !upscaling.NeedsUpscale(p, cfg.Upscale.MinWidth) {
			continue
		}
		width := model.PageWidth(p.Width, p.Height)
		matched := model.RoutesFor(routes, upscaling.ChooseScale(width, cfg.Upscale.MinWidth, scales), width)
		if len(matched) == 0 {
			continue
		}
		key := fmt.Sprint(matched)
		count[key]++
		chains[key] = matched
	}
	best := ""
	for key, n := range count {
		if best == "" || n > count[best] || (n == count[best] && key < best) {
			best = key
		}
	}
	if best == "" {
		return worktasks.Pin{}, routes
	}
	// this server doesn't process pages here, so only its workers and a
	// route for any upscaler count
	var listed []model.UpscaleRoute
	open := false
	for _, i := range chains[best] {
		if r := routes[i]; r.Target > 0 {
			listed = append(listed, r)
		} else if r.Target == model.RouteAnyUpscaler {
			open = true
			break
		}
	}
	return workerPin(listed, open), routes
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

// targetName names a route's upscaler for a message.
func (a *App) targetName(ctx context.Context, id int64) string {
	switch id {
	case model.RouteAnyUpscaler:
		return "any upscaler"
	case model.RouteThisServer:
		return "this server"
	}
	var w model.Worker
	if err := a.DB.NewSelect().Model(&w).Column("name").Where("id = ?", id).Scan(ctx); err != nil || w.Name == "" {
		return fmt.Sprintf("worker %d", id)
	}
	return w.Name
}
