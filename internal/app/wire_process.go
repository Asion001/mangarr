package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/config"
	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/health"
	"github.com/Asion001/mangarr/internal/imageenc"
	"github.com/Asion001/mangarr/internal/jobs"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/processing"
	"github.com/Asion001/mangarr/internal/upscaling"
)

// BacklogPriority queues background processing behind downloads.
const BacklogPriority = -100

// wireProcess connects upscaling and re-encoding to the download manager
// and registers the background processing commands.
func (a *App) wireProcess(ctx context.Context) error {
	if a.Encoder == nil {
		a.Encoder = imageenc.Detect()
	}
	a.Processing = processing.New(upscaling.New(a.Modules), a.Encoder)
	a.Processing.Guard = processing.NewGuard(a.Settings, a.Modules, a.Bus, a.Log.With("component", "processing"))
	a.Processing.Up.Routes, a.Processing.Up.Pick = a.upscaleRoutes, a.pickUpscaler
	a.Downloads.Processor = a.Processing
	// with MANGARR_PROCESSING=workers, or this server's own work switched
	// off in System → Workers, the image work runs on a worker with the
	// encode role; this process only hands the pages over and imports what
	// comes back
	a.Downloads.Processor = &processing.Switch{Local: a.Processing, UseRemote: a.processesOnWorkers,
		Remote: &processing.Remote{Tasks: a.Tasks, Guard: a.Processing.Guard, Route: a.routeChapter}}
	if a.Cfg.Processing == config.ProcessingWorkers {
		a.Log.Info("processing runs on workers (MANGARR_PROCESSING=workers)")
	}
	if a.Cfg.Mode == config.ModeServer {
		// MANGARR_MODE=server upscales nowhere in this process, even when
		// the built-in upscaler was set up by an earlier integrated run
		a.Processing.Up.Online = func(def model.ProviderDefinition) bool { return def.Implementation != "local" }
	}
	a.Health.AddCheck(a.processingHealth)
	if err := a.wireUpscalers(ctx); err != nil {
		return err
	}
	if err := a.wireOrganize(ctx); err != nil {
		return err
	}
	if err := a.wireImports(ctx); err != nil {
		return err
	}
	if err := a.wireReading(ctx); err != nil {
		return err
	}

	existing := func(ctx context.Context, r *jobs.Run) error {
		var body struct {
			SeriesID   int64   `json:"seriesId"`
			ChapterIDs []int64 `json:"chapterIds"`
			ProfileID  int64   `json:"profileId"`
			// Force re-processes files already processed with the current settings.
			Force bool `json:"force"`
		}
		if err := r.Body(&body); err != nil {
			return err
		}
		q := a.DB.NewUpdate().Model((*model.ChapterFile)(nil)).Set("process_params = ?", model.ProcessForce).
			Set("process_attempts = 0").Set("process_retry_at = NULL")
		switch {
		case body.SeriesID > 0:
			q = q.Where("series_id = ?", body.SeriesID)
		case body.ProfileID > 0:
			q = q.Where("series_id IN (SELECT id FROM series WHERE profile_id = ?)", body.ProfileID)
		default: // the whole library (Run on the Tasks page): series whose profile processes pages
			var profiles []model.Profile
			if err := a.DB.NewSelect().Model(&profiles).Scan(ctx); err != nil {
				return err
			}
			var ids []int64
			for _, p := range profiles {
				if p.Config.ProcessParams() != "" {
					ids = append(ids, p.ID)
				}
			}
			if len(ids) == 0 {
				r.Progress("no profile upscales or re-encodes pages")
				return nil
			}
			q = q.Where("series_id IN (SELECT id FROM series WHERE profile_id IN (?))", bun.In(ids))
		}
		if len(body.ChapterIDs) > 0 {
			q = q.Where("chapter_id IN (?)", bun.In(body.ChapterIDs))
		}
		if !body.Force {
			q = q.Where("process_state <> ? OR process_params = ''", model.ProcessDone)
		}
		res, err := q.Exec(ctx)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		r.Progress("marked %d chapters for processing", n)
		queued, err := a.processBacklog(ctx)
		if err == nil {
			r.Progress("marked %d chapters for processing, queued %d", n, queued)
		}
		return err
	}
	for _, name := range []string{"ProcessExisting", "UpscaleExisting"} {
		a.Queue.Register(jobs.Definition{Name: name, Description: "Process already downloaded chapters (of a series, or the whole library) with their profile's upscaling and re-encoding settings",
			Handler: existing})
	}
	a.Queue.Register(jobs.Definition{Name: "ProcessBacklog", Description: "Queue background upscaling/re-encoding of chapters that need it",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			n, err := a.processBacklog(ctx)
			if err == nil {
				r.Progress("queued %d chapters for processing", n)
			}
			return err
		}})
	if err := a.Scheduler.Add(ctx, jobs.Task{Name: "ProcessBacklog", Interval: 30 * time.Minute}); err != nil {
		return err
	}

	// a new or re-enabled upscaler may unblock waiting chapters
	var mu sync.Mutex
	var pending *time.Timer
	a.Modules.OnChange(func() {
		mu.Lock()
		defer mu.Unlock()
		if pending != nil {
			pending.Stop()
		}
		pending = time.AfterFunc(5*time.Second, func() { a.PushProcessBacklog("modules-changed") })
	})

	// a chapter imported unprocessed (background timing) is queued for
	// processing right away instead of waiting for the next sweep
	var importedMu sync.Mutex
	imported := map[int64]bool{}
	a.Bus.Subscribe(func(e events.Event) {
		if e.SeriesID == 0 {
			return
		}
		importedMu.Lock()
		defer importedMu.Unlock()
		if imported[e.SeriesID] {
			return
		}
		imported[e.SeriesID] = true
		id := e.SeriesID
		time.AfterFunc(ImportedProcessDelay, func() {
			importedMu.Lock()
			delete(imported, id)
			importedMu.Unlock()
			if n, err := a.processBacklogSeries(ctx, id); err != nil && ctx.Err() == nil {
				a.Log.Warn("could not queue processing of imported chapters", "seriesId", id, "err", err)
			} else if n > 0 {
				a.Log.Debug("queued processing of imported chapters", "seriesId", id, "chapters", n)
			}
		})
	}, events.ChapterImported, events.ChapterUpgraded)
	return nil
}

// ImportedProcessDelay gathers chapters of a series imported close together
// into one look for processing work.
var ImportedProcessDelay = 2 * time.Second

// processesOnWorkers reports whether pages are processed by the workers
// rather than in this process: MANGARR_PROCESSING=workers, or this
// server's own work switched off in System → Workers.
func (a *App) processesOnWorkers(ctx context.Context) bool {
	if a.Cfg.Processing == config.ProcessingWorkers {
		return true
	}
	dl, err := a.Settings.Downloads(ctx)
	return err == nil && dl.LocalOff()
}

// PushProcessBacklog asks the backlog to look for work (e.g. after profile changes).
func (a *App) PushProcessBacklog(trigger string) {
	_, _ = a.Queue.Push(context.Background(), "ProcessBacklog", nil, trigger)
}

// processBacklog queues processing jobs for files whose profile settings
// changed since they were processed (or that were never processed).
func (a *App) processBacklog(ctx context.Context) (int, error) {
	return a.processBacklogSeries(ctx, 0)
}

// processBacklogSeries is processBacklog for one series (0: all of them).
func (a *App) processBacklogSeries(ctx context.Context, seriesID int64) (int, error) {
	var profiles []model.Profile
	pq := a.DB.NewSelect().Model(&profiles)
	if seriesID > 0 {
		pq = pq.Where("id IN (SELECT profile_id FROM series WHERE id = ?)", seriesID)
	}
	if err := pq.Scan(ctx); err != nil {
		return 0, err
	}
	queued := 0
	now := time.Now().UTC()
	for _, p := range profiles {
		params := p.Config.ProcessParams()
		if params == "" {
			continue
		}
		var files []model.ChapterFile
		q := a.DB.NewSelect().Model(&files).
			Where("process_params <> ?", params).
			Where("series_id IN (SELECT id FROM series WHERE profile_id = ?)", p.ID).
			Where("(process_retry_at IS NULL OR process_retry_at <= ?)", now).
			Where("process_attempts < ?", downloads.MaxProcessAttempts).
			Where("NOT EXISTS (SELECT 1 FROM download_jobs j WHERE j.chapter_id = chapter_file.chapter_id AND j.status IN (?))", bun.In(downloads.ActiveStatuses()))
		if seriesID > 0 {
			q = q.Where("series_id = ?", seriesID)
		}
		if !p.Config.ProcessExisting && p.Config.ProcessChangedAt != nil {
			// only chapters imported since processing was set up, unless asked
			q = q.Where("(imported_at >= ? OR process_params = ?)", p.Config.ProcessChangedAt.UTC(), model.ProcessForce)
		}
		if err := q.OrderExpr("imported_at DESC").Scan(ctx); err != nil {
			return queued, err
		}
		created, err := a.DLQueue.EnqueueReprocessFiles(ctx, files, BacklogPriority)
		queued += created
		if err != nil {
			return queued, err
		}
	}
	return queued, nil
}

// processingHealth reports paused re-encoding and missing or slow encoders.
func (a *App) processingHealth(ctx context.Context) []health.Check {
	var out []health.Check
	if blocked, reason := a.Processing.Guard.Blocked(); blocked {
		out = append(out, health.Check{Source: "Processing", Type: health.Error, Link: "/settings/profiles",
			Message: "Re-encoding is paused: " + reason})
	}
	if dl, err := a.Settings.Downloads(ctx); err == nil && dl.LocalOff() {
		if ok, err := a.Tasks.CanDo(ctx, model.TaskDownload); err == nil && !ok {
			out = append(out, health.Check{Source: "Workers", Type: health.Warning, Link: "/system/workers",
				Message: "This server's own work is switched off and no worker with the download role is online: downloads wait until one is"})
		}
	}
	var profiles []model.Profile
	_ = a.DB.NewSelect().Model(&profiles).Scan(ctx)
	if a.processesOnWorkers(ctx) {
		// the encoders that matter are the workers'; what matters here is
		// that one is around
		for _, p := range profiles {
			if p.Config.ProcessParams() == "" {
				continue
			}
			if ok, err := a.Tasks.CanDo(ctx, model.TaskEncode); err == nil && !ok {
				out = append(out, health.Check{Source: "Processing", Type: health.Warning, Link: "/system/workers",
					Message: "Processing runs on workers, but no worker with the encode role is online: chapters are imported unprocessed and processed once one is back"})
			}
			break
		}
		return out
	}
	seen := map[string]bool{}
	for _, p := range profiles {
		f := p.Config.Encode.Format
		if f == "" || f == "keep" || seen[f] {
			continue
		}
		seen[f] = true
		eng, ok := a.Encoder.Engine(f)
		switch {
		case !ok:
			out = append(out, health.Check{Source: "Processing", Type: health.Warning, Link: "/settings/profiles",
				Message: fmt.Sprintf("Profile %q re-encodes to %s but no %s encoder is installed (use the full image)", p.Name, f, f)})
		case eng.Slow():
			out = append(out, health.Check{Source: "Processing", Type: health.Notice, Link: "/settings/profiles",
				Message: fmt.Sprintf("Re-encoding to %s uses the slow built-in encoder; the full image includes avifenc, which is several times faster", f)})
		}
	}
	return out
}
