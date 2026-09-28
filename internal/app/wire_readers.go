package app

import (
	"context"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/cleanup"
	"github.com/Asion001/mangarr/internal/jobs"
	"github.com/Asion001/mangarr/internal/readsync"
)

// ReaderServices are created by wireReaders.
type ReaderServices struct {
	ReadSync *readsync.Syncer
	// Watcher applies progress pushed by library servers (Komga) live.
	Watcher *readsync.Watcher
	Cleaner *cleanup.Cleaner
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
