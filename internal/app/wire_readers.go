package app

import (
	"context"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/cleanup"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/jobs"
	"github.com/Asion001/mangarr/internal/readsync"
	"github.com/Asion001/mangarr/internal/soundtrack"
)

// ReaderServices are created by wireReaders.
type ReaderServices struct {
	ReadSync *readsync.Syncer
	// Watcher applies progress pushed by library servers (Komga) live.
	Watcher *readsync.Watcher
	Cleaner *cleanup.Cleaner
	// Spotify plays soundtrack music through readers' Spotify accounts.
	Spotify *soundtrack.Spotify
	// Packs keeps the sound packs (ambience and effects).
	Packs *soundtrack.Packs
}

// watcherService runs the watcher with the app.
type watcherService struct{ w *readsync.Watcher }

func (s watcherService) Start(ctx context.Context) error {
	go s.w.Run(ctx, time.Minute)
	return nil
}

// wireReaders registers read-progress sync and cleanup.
func (a *App) wireReaders(ctx context.Context) error {
	a.ReadSync = readsync.New(a.DB, a.Modules, a.Bus, a.Log.With("component", "readsync"))
	a.Cleaner = cleanup.New(a.DB, a.Settings, a.Library, a.Bus, a.Log.With("component", "cleanup"))
	a.Watcher = readsync.NewWatcher(a.ReadSync)
	a.Spotify = soundtrack.New(a.DB, a.Settings)
	a.Packs = soundtrack.NewPacks(a.DB, a.Settings, a.Cfg.DataDir)
	a.Cleaner.Leftovers = func(ctx context.Context) ([]cleanup.LeftoverFile, error) {
		list, err := a.Packs.Leftovers(ctx)
		out := make([]cleanup.LeftoverFile, len(list))
		for i, l := range list {
			out[i] = cleanup.LeftoverFile{Path: l.Path, Size: l.Size}
		}
		return out, err
	}
	a.Cleaner.RemoveLeftovers = a.Packs.RemoveLeftovers
	a.Queue.Register(jobs.Definition{Name: "InstallSoundPack", Description: "Download and install a sound pack from the catalog",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var body struct {
				ID string `json:"id"`
			}
			_ = r.Body(&body)
			pack, err := a.Packs.InstallFromCatalog(ctx, body.ID, func(msg string) { r.Progress("%s", msg) })
			if err == nil {
				r.Progress("installed %s %s", pack.Name, pack.Version)
				a.Bus.Changed("soundtrack", "updated", 0)
			}
			return err
		}})
	a.Queue.Register(jobs.Definition{Name: "DetectMoods", Description: "Find moods from chapter titles and place them as automatic soundtrack cues", Priority: 50,
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var body struct {
				SeriesID int64 `json:"seriesId"`
			}
			_ = r.Body(&body)
			ids := []int64{body.SeriesID}
			if body.SeriesID == 0 {
				ids = nil
				if err := a.DB.NewSelect().TableExpr("series").Column("id").Where("preview = ?", false).Scan(ctx, &ids); err != nil {
					return err
				}
			}
			found := 0
			for i, id := range ids {
				d, err := soundtrack.DetectChapters(ctx, a.DB, a.Settings, id)
				if err != nil {
					return err
				}
				found += d.Cues
				r.Progress("%d of %d series, %d cues", i+1, len(ids), found)
			}
			a.Bus.Changed("soundtrack", "updated", body.SeriesID)
			return nil
		}})
	// new chapters bring new titles: look again a little after imports settle
	var detectMu sync.Mutex
	detectAfter := map[int64]*time.Timer{}
	a.Bus.Subscribe(func(e events.Event) {
		if e.SeriesID == 0 {
			return
		}
		if cfg, err := a.Settings.Sound(context.Background()); err != nil || !cfg.AutoMoods {
			return
		}
		detectMu.Lock()
		defer detectMu.Unlock()
		if t := detectAfter[e.SeriesID]; t != nil {
			t.Stop()
		}
		id := e.SeriesID
		detectAfter[id] = time.AfterFunc(2*time.Minute, func() {
			detectMu.Lock()
			delete(detectAfter, id)
			detectMu.Unlock()
			_, _ = a.Queue.Push(context.Background(), "DetectMoods", map[string]any{"seriesId": id}, "chapter-imported")
		})
	}, events.ChapterImported)
	a.Queue.Register(jobs.Definition{Name: "ImportSoundFolder", Description: "Add the sounds in a folder on the server as a sound pack",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var body struct {
				Path string `json:"path"`
			}
			_ = r.Body(&body)
			pack, skipped, err := a.Packs.ImportFolder(ctx, body.Path, func(done, total int) { r.Progress("%d of %d sounds", done, total) })
			if err == nil {
				r.Progress("added %s (%d music files skipped)", pack.Name, skipped)
				a.Bus.Changed("soundtrack", "updated", 0)
			}
			return err
		}})
	var cleanupMu sync.Mutex
	var cleanupAt time.Time
	a.Watcher.OnChange = func(int64) {
		// live changes may make chapters removable; clean up at most every 10 minutes
		if cs, _ := a.Settings.Cleanup(context.Background()); !cs.Enabled {
			return
		}
		cleanupMu.Lock()
		defer cleanupMu.Unlock()
		if time.Since(cleanupAt) < 10*time.Minute {
			return
		}
		cleanupAt = time.Now()
		time.AfterFunc(time.Minute, func() { _, _ = a.Queue.Push(context.Background(), "Cleanup", nil, "live-read-sync") })
	}
	a.AddService(watcherService{a.Watcher})
	a.Modules.OnChange(func() { go a.Watcher.Refresh(context.Background()) })

	a.Queue.Register(jobs.Definition{Name: "SyncReadProgress", Description: "Pull per-reader progress from Komga/Kavita",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			res, err := a.ReadSync.Sync(ctx)
			r.Progress("%d accounts, %d changes, %d failed", res.Accounts, res.Updated, res.Failed)
			if err == nil {
				if cs, _ := a.Settings.Cleanup(ctx); cs.Enabled {
					_, _ = a.Queue.Push(ctx, "Cleanup", nil, "after-sync")
				}
			}
			return err
		}})
	a.Queue.Register(jobs.Definition{Name: "Cleanup", Description: "Delete chapters every reader has finished (read-based cleanup)",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var body struct {
				Force bool `json:"force"`
			}
			_ = r.Body(&body)
			plan, deleted, err := a.Cleaner.Run(ctx, body.Force)
			if plan != nil {
				if plan.DryRun && !body.Force {
					r.Progress("dry run: %d chapters (%d bytes) would be removed", len(plan.Candidates), plan.TotalSize)
				} else {
					r.Progress("removed %d chapters", deleted)
				}
			}
			return err
		}})
	if err := a.Scheduler.Add(ctx, jobs.Task{Name: "SyncReadProgress", Interval: 30 * time.Minute}); err != nil {
		return err
	}
	return a.wireProcess(ctx)
}
