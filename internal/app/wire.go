package app

import (
	"context"
	"fmt"
	"time"

	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/jobs"
	"github.com/Asion001/mangarr/internal/library"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/refresh"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/worktasks"
)

// Services created by wire.
type Services struct {
	Library   *library.Library
	Metadata  *metadataagg.Aggregator
	DLQueue   *downloads.Queue
	Searcher  *downloads.Searcher
	Refresher *refresh.Refresher
	Series    *series.Service
	Downloads *downloads.Manager
}

// wire constructs domain services and registers commands/tasks.
func (a *App) wire(ctx context.Context) error {
	log := a.Log
	a.Library = library.New(a.DB, a.Settings, a.HTTP, a.Cfg.DataDir, log.With("component", "library"))
	if _, err := a.Library.RescanRecycleBin(ctx); err != nil {
		log.Warn("index recycle bin", "err", err)
	}
	if err := a.Library.AssignFolderLanguages(ctx); err != nil {
		return fmt.Errorf("root folder languages: %w", err)
	}
	a.Metadata = metadataagg.New(a.Modules, log.With("component", "metadata"))
	a.DLQueue = downloads.NewQueue(a.DB, a.Bus)
	a.Searcher = downloads.NewSearcher(a.DB, a.DLQueue, log.With("component", "search"))
	a.Refresher = refresh.New(a.DB, a.Bus, a.Modules, a.Settings, a.Searcher, a.Library, log.With("component", "refresh"))
	a.Refresher.Gov = a.Catalogs.Gov
	a.Refresher.Cache, a.Refresher.Gen = a.SourceCache, a.Catalogs.Generation
	a.Tasks = worktasks.New(a.DB, log.With("component", "worktasks"))
	a.Tasks.DataDir = a.Cfg.DataDir
	worktasks.SetDefault(a.Tasks) // the "mangarr workers" upscaler hands batches to it
	a.Tasks.Changed = func(jobID int64) { a.DLQueue.Wake() }
	a.Tasks.Abandoned = func(t model.WorkerTask) {
		if t.Kind == model.TaskDownload {
			a.Downloads.TaskAbandoned(context.WithoutCancel(ctx), t, t.Error)
		}
	}
	a.AddService(a.Tasks)
	a.Downloads = downloads.NewManager(a.DB, a.Bus, a.Modules, a.Settings, a.Library, a.DLQueue, a.Searcher, log.With("component", "downloads"), a.Cfg.DataDir)
	a.Downloads.Gov = a.Catalogs.Gov
	a.Downloads.Tasks = a.Tasks
	a.AddService(a.Downloads)
	a.Series = series.New(a.DB, a.Bus, a.Library, a.Metadata, a.Modules, a.Queue, log.With("component", "series"))
	if err := a.Series.ReconcileWorks(ctx); err != nil {
		return fmt.Errorf("group language editions: %w", err)
	}
	if _, err := a.Series.NormalizeAllGenres(ctx); err != nil {
		log.Warn("rename genres to their English names", "err", err)
	}
	if _, err := a.Series.ShareAllMetadata(ctx); err != nil {
		log.Warn("share cover and info between language editions", "err", err)
	}
	a.Series.UseSearch(a.Search) // adding a source in bulk finds each series at the catalog

	a.Queue.Register(jobs.Definition{Name: "RefreshSources", Description: "Check linked sources that are due for new chapters",
		Handler: a.Refresher.RefreshDue})
	a.Queue.Register(jobs.Definition{Name: "SeriesSources", Description: "Add, remove or switch off one catalog across many series",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var req series.BulkRequest
			if err := r.Body(&req); err != nil {
				return err
			}
			results, err := a.Series.BulkSources(ctx, req, false, func(done, total int) {
				r.Progress("%d of %d series", done, total)
			})
			if err != nil {
				return err
			}
			r.Progress("%s", series.BulkSummary(results))
			return nil
		}})
	a.Queue.Register(jobs.Definition{Name: "SwitchSourceModule", Description: "Move source links from one source module to another",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var req series.SwitchRequest
			if err := r.Body(&req); err != nil {
				return err
			}
			rows, err := a.Series.SwitchPlan(ctx, req)
			if err != nil {
				return err
			}
			r.Progress("%s", series.SwitchSummary(rows))
			moved, err := a.Series.SwitchSources(ctx, req, func(done, total int) {
				r.Progress("%d of %d links", done, total)
			})
			if err != nil {
				return err
			}
			r.Progress("%d links moved", moved)
			return nil
		}})
	a.Queue.Register(jobs.Definition{Name: "RefreshSeries", Description: "Refresh all sources of one series (seriesId) or several (seriesIds)",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var body struct {
				SeriesID  int64   `json:"seriesId"`
				SeriesIDs []int64 `json:"seriesIds"`
			}
			if err := r.Body(&body); err != nil {
				return err
			}
			ids := body.SeriesIDs
			if body.SeriesID != 0 {
				ids = append([]int64{body.SeriesID}, ids...)
			}
			if len(ids) == 0 {
				return fmt.Errorf("seriesId required")
			}
			// one series failing (its sources are down, it was deleted since)
			// doesn't stop the rest of a bulk refresh
			newChapters, grabbed, failed := 0, 0, 0
			var lastErr error
			for i, id := range ids {
				res, err := a.Refresher.SyncSeries(ctx, id, false)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if err != nil {
					failed, lastErr = failed+1, err
					a.Log.Warn("series refresh", "series", id, "err", err)
				}
				newChapters, grabbed = newChapters+res.NewChapters, grabbed+res.Grabbed
				if len(ids) > 1 {
					r.Progress("%d of %d series", i+1, len(ids))
				}
			}
			r.Progress("%d new chapters, %d grabbed", newChapters, grabbed)
			return bulkError(failed, len(ids), "refresh", lastErr)
		}})
	a.Queue.Register(jobs.Definition{Name: "SearchMissing", Description: "Grab missing monitored chapters (optionally for one series, several series or chapters)",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var body struct {
				SeriesID   int64   `json:"seriesId"`
				SeriesIDs  []int64 `json:"seriesIds"`
				ChapterIDs []int64 `json:"chapterIds"`
				Explicit   bool    `json:"explicit"`
			}
			if err := r.Body(&body); err != nil {
				return err
			}
			ids := []int64{body.SeriesID}
			if len(body.SeriesIDs) > 0 {
				ids = body.SeriesIDs
			} else if body.SeriesID == 0 {
				if err := a.DB.NewSelect().Model((*model.Series)(nil)).Column("id").Where("monitored = ?", true).Scan(ctx, &ids); err != nil {
					return err
				}
			}
			total, failed := 0, 0
			var lastErr error
			for _, id := range ids {
				n, err := a.Searcher.Evaluate(ctx, id, body.ChapterIDs, body.Explicit)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if err != nil {
					failed, lastErr = failed+1, err
					a.Log.Warn("search missing", "series", id, "err", err)
				}
				total += n
			}
			r.Progress("%d chapters queued", total)
			return bulkError(failed, len(ids), "search", lastErr)
		}})
	a.Queue.Register(jobs.Definition{Name: "RefreshMetadata", Description: "Refresh series metadata from metadata modules",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var body struct {
				SeriesID int64 `json:"seriesId"`
			}
			_ = r.Body(&body)
			var ids []int64
			if body.SeriesID > 0 {
				ids = []int64{body.SeriesID}
			} else if err := a.DB.NewSelect().Model((*model.Series)(nil)).Column("id").Where("preview = ?", false).Scan(ctx, &ids); err != nil {
				return err
			}
			changed := 0
			for i, id := range ids {
				ok, err := a.Series.RefreshMetadata(ctx, id)
				if err != nil {
					a.Log.Warn("metadata refresh", "series", id, "err", err)
					continue
				}
				if ok {
					changed++
					if s, err := a.Series.Get(ctx, id); err == nil {
						a.Refresher.WriteSidecars(ctx, s)
					}
				}
				r.Progress("%d/%d series, %d updated", i+1, len(ids), changed)
			}
			return nil
		}})

	if err := a.Scheduler.Add(ctx, jobs.Task{Name: "RefreshSources", Interval: 10 * time.Minute, RunOnStart: true}); err != nil {
		return err
	}
	if err := a.Scheduler.Add(ctx, jobs.Task{Name: "RefreshMetadata", Interval: 24 * time.Hour}); err != nil {
		return err
	}
	return a.wireMore(ctx)
}

// bulkError is a job's result after it worked through several series: the
// error itself for one series, else how many of them failed.
func bulkError(failed, total int, what string, last error) error {
	switch {
	case failed == 0:
		return nil
	case total == 1:
		return last
	default:
		return fmt.Errorf("%d of %d series failed to %s, the last: %w", failed, total, what, last)
	}
}
