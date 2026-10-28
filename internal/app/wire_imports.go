package app

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/duplicates"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/imports"
	"github.com/Asion001/mangarr/internal/jobs"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/readstate"
)

// PushProgressDelay batches chapter imports before imported read state is
// written to library servers.
var PushProgressDelay = 2 * time.Minute

// wireImports registers backup imports.
func (a *App) wireImports(ctx context.Context) error {
	a.Imports = &imports.Service{DB: a.DB, Bus: a.Bus, Settings: a.Settings, Mods: a.Modules, Catalogs: a.Catalogs, Search: a.Search,
		Metadata: a.Metadata, Series: a.Series, Log: a.Log.With("component", "imports"), Dir: filepath.Join(a.Cfg.DataDir, "imports"),
		Sync: func(ctx context.Context, seriesID int64) error {
			_, err := a.Refresher.SyncSeries(ctx, seriesID, false)
			return err
		},
		Push: func(ctx context.Context, name string, body map[string]any) error {
			_, err := a.Queue.Push(ctx, name, body, "import")
			return err
		}}

	a.Duplicates = &duplicates.Service{DB: a.DB, Series: a.Series, Metadata: a.Metadata, Settings: a.Settings,
		Log: a.Log.With("component", "duplicates"), Changed: func() { a.Bus.Changed("duplicates", "updated", 0) }}
	a.Queue.Register(jobs.Definition{Name: "FindDuplicates", Description: "Find titles that are one work, and the metadata of titles that have none",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var body struct {
				SeriesIDs []int64 `json:"seriesIds"`
			}
			_ = r.Body(&body)
			res, err := a.Duplicates.Scan(ctx, body.SeriesIDs, func(s string) { r.Progress("%s", s) })
			r.Progress("looked up %d (%d found), %d possible duplicates, %d grouped, %d linked", res.LookedUp, res.Found, res.Pairs, res.Grouped, res.Linked)
			return err
		}})
	if err := a.Scheduler.Add(ctx, jobs.Task{Name: "FindDuplicates", Interval: 7 * 24 * time.Hour}); err != nil {
		return err
	}

	a.Queue.Register(jobs.Definition{Name: "MapImport", Description: "Match a backup's manga to catalogs and metadata",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var body struct {
				ImportID int64   `json:"importId"`
				EntryIDs []int64 `json:"entryIds"`
			}
			if err := r.Body(&body); err != nil || body.ImportID == 0 {
				return fmt.Errorf("importId required")
			}
			return a.Imports.Map(ctx, body.ImportID, body.EntryIDs, func(done, total int) { r.Progress("matched %d of %d", done, total) })
		}})
	a.Queue.Register(jobs.Definition{Name: "InstallImportExtensions", Description: "Install the extensions a backup import needs",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var body struct {
				ImportID int64 `json:"importId"`
			}
			if err := r.Body(&body); err != nil || body.ImportID == 0 {
				return fmt.Errorf("importId required")
			}
			ids, err := a.Imports.InstallExtensions(ctx, body.ImportID, func(s string) { r.Progress("%s", s) })
			if len(ids) > 0 {
				if merr := a.Imports.Map(ctx, body.ImportID, ids, func(done, total int) { r.Progress("matched %d of %d", done, total) }); merr != nil {
					return merr
				}
			}
			return err
		}})
	a.Queue.Register(jobs.Definition{Name: "RunImport", Description: "Add a backup's manga to the library with their read chapters",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var body struct {
				ImportID int64 `json:"importId"`
			}
			if err := r.Body(&body); err != nil || body.ImportID == 0 {
				return fmt.Errorf("importId required")
			}
			res, err := a.Imports.Run(ctx, body.ImportID, func(s string) { r.Progress("%s", s) })
			// a user's import: their follows and lists, and their request
			if err := a.UserImport.Added(ctx, body.ImportID, nil); err != nil {
				a.Log.Warn("finish a user's import", "import", body.ImportID, "err", err)
			}
			if err == nil {
				r.Progress("%d added, %d merged, %d failed", res.Added, res.Merged, res.Failed)
				if res.Added > 0 {
					// an import that missed the metadata adds a second title for one already here
					_, _ = a.Queue.Push(ctx, "FindDuplicates", nil, "import")
				}
			}
			return err
		}})

	// progress imported at a chapter's last page is read once the chapter's
	// page count is known: now for chapters on disk, later as they arrive
	if n, err := readstate.FinishLastPages(ctx, a.DB, 0, time.Now().UTC()); err != nil {
		a.Log.Warn("finish imported last pages", "err", err)
	} else if n > 0 {
		a.Log.Info("imported chapters read to their last page marked read", "chapters", n)
	}
	a.Bus.Subscribe(func(e events.Event) {
		if e.SeriesID == 0 {
			return
		}
		go func(id int64) {
			if _, err := readstate.FinishLastPages(context.Background(), a.DB, id, time.Now().UTC()); err != nil {
				a.Log.Warn("finish imported last pages", "series", id, "err", err)
			}
		}(e.SeriesID)
	}, events.ChapterImported)

	a.UserImport.Imports = a.Imports
	remap, err := a.Imports.Recover(ctx)
	if err != nil {
		return err
	}
	for _, id := range remap {
		_, _ = a.Queue.Push(ctx, "MapImport", map[string]any{"importId": id}, "startup")
	}

	// imported read state, and what reading apps read before the chapter was
	// downloaded, is written to Komga/Kavita once chapters are on disk
	var mu sync.Mutex
	pending := map[int64]bool{}
	a.Bus.Subscribe(func(e events.Event) {
		if e.SeriesID == 0 {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if pending[e.SeriesID] {
			return
		}
		pending[e.SeriesID] = true
		id := e.SeriesID
		time.AfterFunc(PushProgressDelay, func() {
			mu.Lock()
			delete(pending, id)
			mu.Unlock()
			ctx := context.Background()
			n, err := a.DB.NewSelect().Model((*model.ChapterReadState)(nil)).Where("series_id = ? AND origin IN (?, ?)", id, model.ReadOriginBackup, model.ReadOriginApp).Count(ctx)
			if err == nil && n > 0 {
				_, _ = a.Queue.Push(ctx, "RestoreProgress", map[string]any{"seriesId": id}, "imported-read-state")
			}
		})
	}, events.ChapterImported)
	return nil
}
