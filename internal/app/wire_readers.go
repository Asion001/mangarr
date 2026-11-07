package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/cleanup"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/jobs"
	"github.com/Asion001/mangarr/internal/pagemoods"
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
	// PageMoods finds moods in pages.
	PageMoods *pagemoods.Service
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
	a.PageMoods = pagemoods.New(a.DB, a.Settings, a.Tasks, a.Cfg.DataDir, a.Log.With("component", "pagemoods"))
	a.PageMoods.Paths = func(ctx context.Context, chapterID int64) (string, error) {
		b, err := a.Reading.PageBook(ctx, chapterID)
		if err != nil {
			return "", err
		}
		if b.Path == "" {
			return "", errors.New("the chapter isn't downloaded")
		}
		return b.Path, nil
	}
	scorePages := func(ctx context.Context, r *jobs.Run) error {
		var body struct {
			SeriesID   int64   `json:"seriesId"`
			ChapterIDs []int64 `json:"chapterIds"`
		}
		_ = r.Body(&body)
		ids := []int64{body.SeriesID}
		if body.SeriesID == 0 {
			ids = nil
			if err := a.DB.NewSelect().TableExpr("series").Column("id").Where("preview = ?", false).Scan(ctx, &ids); err != nil {
				return err
			}
		}
		scored, queued := 0, 0
		for i, id := range ids {
			res, err := a.PageMoods.Score(ctx, id, body.ChapterIDs, func(msg string) { r.Progress("series %d of %d: %s", i+1, len(ids), msg) })
			if res != nil {
				scored, queued = scored+res.Scored, queued+res.Queued
			}
			if err != nil {
				return err
			}
		}
		r.Progress("%d chapters scored here, %d handed to workers", scored, queued)
		a.Bus.Changed("soundtrack", "updated", body.SeriesID)
		return nil
	}
	a.Queue.Register(jobs.Definition{Name: "ScorePages", Description: "Find soundtrack moods in chapters' pages", Priority: 60, Handler: scorePages})
	// The chapters someone is reading now: ahead of whole series scored
	// after imports, and beside them (a command name runs one at a time).
	a.Queue.Register(jobs.Definition{Name: "ScoreReaderPages", Description: "Find soundtrack moods in the pages someone is reading", Handler: scorePages})
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
	detect := NewDebouncer[int64](2 * time.Minute)
	a.Bus.Subscribe(func(e events.Event) {
		if e.SeriesID == 0 {
			return
		}
		cfg, err := a.Settings.Sound(context.Background())
		if err != nil || (!cfg.AutoMoods && !cfg.PageMoods) {
			return
		}
		id := e.SeriesID
		detect.Do(id, func() {
			cfg, _ := a.Settings.Sound(context.Background())
			if cfg.AutoMoods {
				_, _ = a.Queue.Push(context.Background(), "DetectMoods", map[string]any{"seriesId": id}, "chapter-imported")
			}
			if cfg.PageMoods {
				_, _ = a.Queue.Push(context.Background(), "ScorePages", map[string]any{"seriesId": id}, "chapter-imported")
			}
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
