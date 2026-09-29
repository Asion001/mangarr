package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Asion001/mangarr/internal/backup"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/health"
	"github.com/Asion001/mangarr/internal/jobs"
	"github.com/Asion001/mangarr/internal/libsync"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/notifications"
	"github.com/Asion001/mangarr/internal/requests"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/sso"
)

// MoreServices are created by wireMore.
type MoreServices struct {
	Notifications *notifications.Dispatcher
	Requests      *requests.Service
	SSO           *sso.Service
	Rescanner     *libsync.Rescanner
	Health        *health.Checker
	Backups       *backup.Service
}

// wireMore registers library sync, notifications, health, backups and
// housekeeping.
func (a *App) wireMore(ctx context.Context) error {
	log := a.Log
	a.Notifications = notifications.New(a.DB, a.Bus, a.Modules, a.Settings, log.With("component", "notifications"))
	a.AddService(a.Notifications)
	a.wireRequests()
	a.SSO = &sso.Service{Settings: a.Settings, Auth: a.Auth, DB: a.DB, HTTP: a.HTTP, Log: log.With("component", "sso")}
	a.Rescanner = libsync.New(a.Modules, a.Bus, log.With("component", "libsync"))
	a.AddService(a.Rescanner)
	a.Backups = backup.New(a.DB, a.Settings, a.Cfg.DataDir)
	a.Health = health.New(a.DB, a.Bus, a.Modules, a.Settings, log.With("component", "health"))
	a.Health.AddStatus("Notifications", func() map[int64]string {
		out := map[int64]string{}
		for id, st := range a.Notifications.Status() {
			out[id] = st.LastError
		}
		return out
	})
	a.Health.AddStatus("Library servers", a.Rescanner.Status)
	a.Health.AddCheck(a.crashHealth)
	a.Health.AddCheck(func(ctx context.Context) []health.Check {
		var out []health.Check
		list, _ := a.Catalogs.List(ctx, false)
		for _, c := range list {
			if c.CooldownUntil != nil {
				out = append(out, health.Check{Source: "Sources", Type: health.Warning, Link: "/sources/catalogs",
					Message: fmt.Sprintf("%s is paused until %s after %s", c.DisplayName, c.CooldownUntil.Local().Format("15:04"), c.CooldownReason)})
			}
		}
		return out
	})

	a.Queue.Register(jobs.Definition{Name: "HealthCheck", Description: "Run health checks",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			res := a.Health.Run(ctx)
			r.Progress("%d issues", len(res))
			return nil
		}})
	a.Queue.Register(jobs.Definition{Name: "DiskScan", Description: "Verify chapter files still exist on disk",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var body struct {
				SeriesID int64 `json:"seriesId"`
			}
			_ = r.Body(&body)
			res, err := a.Library.DiskScan(ctx, body.SeriesID)
			r.Progress("checked %d files in %d series, %d missing", res.Checked, res.Series, res.Missing)
			if err == nil && res.Missing > 0 {
				// missing monitored chapters are wanted again
				_, _ = a.Queue.Push(ctx, "SearchMissing", map[string]any{"seriesId": body.SeriesID}, "diskscan")
			}
			return err
		}})
	a.Queue.Register(jobs.Definition{Name: "Backup", Description: "Back up the database and settings", Exclusive: true,
		Handler: func(ctx context.Context, r *jobs.Run) error {
			typ := "manual"
			if r.Command.Trigger == "scheduled" {
				typ = "scheduled"
			}
			b, err := a.Backups.Create(ctx, typ)
			if err == nil {
				r.Progress("created %s", b.Name)
			}
			return err
		}})
	a.Queue.Register(jobs.Definition{Name: "Housekeeping", Description: "Purge recycle bin, old queue entries, commands and caches",
		Handler: a.housekeeping})
	a.Queue.Register(jobs.Definition{Name: "LibraryRescan", Description: "Ask library servers (Komga/Kavita) to rescan every series folder",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var list []model.Series
			if err := a.DB.NewSelect().Model(&list).Where("preview = ?", false).Scan(ctx); err != nil {
				return err
			}
			var dirs []string
			for i := range list {
				if d, err := a.Library.SeriesDir(ctx, &list[i]); err == nil {
					dirs = append(dirs, d)
				}
			}
			errs := a.Rescanner.Now(ctx, dirs)
			if len(errs) > 0 {
				var msgs []string
				for k, v := range errs {
					msgs = append(msgs, k+": "+v.Error())
				}
				return fmt.Errorf("%s", strings.Join(msgs, "; "))
			}
			r.Progress("requested rescan of %d series folders", len(dirs))
			return nil
		}})
	a.Queue.Register(jobs.Definition{Name: "ExtensionUpdateCheck", Description: "Check source extensions for updates (and install them when enabled)",
		Handler: a.extensionUpdates})
	a.Queue.Register(jobs.Definition{Name: "CompactImageCache", Description: "Resize cached thumbnails and covers to small JPEGs",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			res, err := a.ImageCache.Compact(ctx, func(done int) { r.Progress("checked %d images", done) })
			r.Progress("resized %d of %d images: %s → %s", res.Converted, res.Files, humanBytes(res.Before), humanBytes(res.After))
			a.Bus.Changed("cache", "compacted", 0)
			return err
		}})
	a.Health.AddCheck(func(ctx context.Context) []health.Check {
		g, _ := a.Settings.General(ctx)
		limit := int64(g.ImageCacheMaxMB) << 20
		if size := a.ImageCache.Size(); limit > 0 && size > limit+limit/10 {
			return []health.Check{{Source: "Image cache", Type: health.Warning, Link: "/system/status",
				Message: fmt.Sprintf("The image cache uses %s, over its %s limit; clear it or check the cache folder's permissions", humanBytes(size), humanBytes(limit))}}
		}
		return nil
	})
	if a.ImageCache.NeedsCompact() {
		// thumbnails cached before resizing existed
		_, _ = a.Queue.Push(ctx, "CompactImageCache", nil, "upgrade")
	}

	for _, t := range []jobs.Task{
		{Name: "HealthCheck", Interval: 5 * time.Minute, RunOnStart: true},
		{Name: "DiskScan", Interval: 24 * time.Hour},
		{Name: "Backup", Interval: 24 * time.Hour},
		{Name: "Housekeeping", Interval: 24 * time.Hour},
		{Name: "ExtensionUpdateCheck", Interval: 12 * time.Hour},
	} {
		if err := a.Scheduler.Add(ctx, t); err != nil {
			return err
		}
	}
	// re-check health shortly after module changes
	var pending *time.Timer
	a.Modules.OnChange(func() {
		if pending != nil {
			pending.Stop()
		}
		pending = time.AfterFunc(3*time.Second, func() {
			_, _ = a.Queue.Push(context.Background(), "HealthCheck", nil, "modules-changed")
		})
	})
	return a.wireReaders(ctx)
}

func (a *App) housekeeping(ctx context.Context, r *jobs.Run) error {
	purged, err := a.Library.PurgeRecycleBin(ctx)
	if err != nil {
		a.Log.Warn("recycle bin purge", "err", err)
	}
	_ = a.DLQueue.ClearFinished(ctx, 7*24*time.Hour)
	_, _ = a.DB.NewDelete().Model((*model.Command)(nil)).Where("queued_at < ?", time.Now().UTC().Add(-30*24*time.Hour)).
		Where("status NOT IN (?, ?)", model.CommandQueued, model.CommandStarted).Exec(ctx)
	a.Auth.PurgeSessions(ctx)
	if _, err := a.Reading.PruneEvents(ctx); err != nil {
		a.Log.Warn("read events purge", "err", err)
	}
	previews, err := a.Series.PurgePreviews(ctx, time.Now().UTC().Add(-series.PreviewTTL))
	if err != nil {
		a.Log.Warn("preview purge", "err", err)
	}
	g, _ := a.Settings.General(ctx)
	cleaned := a.ImageCache.Trim(30*24*time.Hour, int64(g.ImageCacheMaxMB)<<20)
	r.Progress("purged %d recycled files, %d unopened previews, %d cached images", purged, previews, cleaned)
	return nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func (a *App) extensionUpdates(ctx context.Context, r *jobs.Run) error {
	var items []string
	for _, m := range modules.ActiveAs[source.ExtensionManager](a.Modules, modules.KindSource) {
		list, err := m.Instance.Extensions(ctx, true)
		if err != nil {
			a.Log.Warn("extension update check", "module", m.Def.Name, "err", err)
			continue
		}
		auto := false
		if au, ok := m.Instance.(source.AutoUpdater); ok {
			auto = au.AutoUpdateExtensions()
		}
		for _, e := range list {
			if !e.Installed || !e.HasUpdate {
				continue
			}
			if auto {
				if err := m.Instance.UpdateExtension(ctx, e.Pkg); err != nil {
					items = append(items, fmt.Sprintf("%s: update failed (%v)", e.Name, err))
					continue
				}
				items = append(items, e.Name+" updated")
			} else {
				items = append(items, e.Name+" has an update")
			}
		}
	}
	r.Progress("%d extension updates", len(items))
	if len(items) > 0 {
		a.Bus.Publish(events.Event{Type: events.ExtensionUpdate, Payload: events.MessagePayload{Title: "Extension updates", Message: fmt.Sprintf("%d extensions", len(items)), Items: items}})
	}
	return nil
}
