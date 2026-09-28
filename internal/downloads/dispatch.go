package downloads

import (
	"context"
	"strconv"
	"time"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/quiet"
	"github.com/Asion001/mangarr/internal/sourcegov"
)

func (m *Manager) loop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		m.dispatch(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.queue.wake:
		}
	}
}

type queuedJob struct {
	model.DownloadJob
	ModuleID int64  `bun:"module_id"`
	SourceID string `bun:"source_id"`
}

// srcKey identifies the catalog of a job for per-source limits.
func srcKey(moduleID int64, sourceID string) string {
	if sourceID == "" {
		return ""
	}
	return strconv.FormatInt(moduleID, 10) + ":" + sourceID
}

func (m *Manager) dispatch(ctx context.Context) {
	dl, _ := m.settings.Downloads(ctx)
	if dl.MaxConcurrent <= 0 {
		dl.MaxConcurrent = 1
	}
	if dl.MaxPerSource <= 0 {
		dl.MaxPerSource = 1
	}
	if dl.MaxConcurrentProcessing <= 0 {
		dl.MaxConcurrentProcessing = 1
	}
	now := time.Now()
	if qs, _ := m.settings.QueueState(ctx); qs.Active(now) {
		return // the whole queue is paused
	}
	sched, _ := m.settings.Schedule(ctx)
	quietNow := quiet.Evaluate(sched, now)
	var jobs []queuedJob
	err := m.db.NewSelect().TableExpr("download_jobs AS j").ColumnExpr("j.*, COALESCE(ss.module_id, 0) AS module_id, COALESCE(ss.source_id, '') AS source_id").
		Join("LEFT JOIN chapter_releases AS r ON r.id = j.release_id").
		Join("LEFT JOIN series_sources AS ss ON ss.id = r.series_source_id").
		Where("j.status = ? AND j.not_before <= ?", model.JobQueued, time.Now().UTC()).
		OrderExpr("j.rank, j.id").Limit(100).Scan(ctx, &jobs)
	if err != nil {
		if ctx.Err() == nil {
			m.log.Error("download queue", "err", err)
		}
		return
	}
	// jobs a worker holds are not ours to run
	onWorkers, err := m.openJobs(ctx)
	if err != nil {
		m.log.Warn("could not read the worker tasks", "err", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.held {
		return
	}
	busy := len(m.running) > 0
	for _, j := range jobs {
		if _, ok := m.running[j.ID]; ok {
			continue
		}
		if onWorkers[j.ID] {
			continue
		}
		if (j.Kind == model.JobKindDownload && quietNow.PauseDownloads) || (j.Kind == model.JobKindReprocess && quietNow.PauseProcessing) {
			continue // quiet hours
		}
		if j.ConfigOverride != nil && j.ConfigOverride.ProcessParams() != "" && quietNow.PauseProcessing {
			continue
		}
		if j.Kind == model.JobKindDownload && m.runningOfKind(model.JobKindDownload) >= dl.MaxConcurrent {
			continue
		}
		if j.Kind == model.JobKindReprocess && m.runningOfKind(model.JobKindReprocess) >= dl.MaxConcurrentProcessing {
			continue
		}
		src := srcKey(j.ModuleID, j.SourceID)
		if j.Kind == model.JobKindReprocess {
			src = "" // reworks the file on disk: never talks to the source
		}
		if src != "" && m.runningSrc[src] >= dl.MaxPerSource {
			continue
		}
		if m.Gov != nil && src != "" {
			if until, _ := m.Gov.Cooldown(sourcegov.Key{ModuleID: j.ModuleID, SourceID: j.SourceID}); !until.IsZero() {
				continue // throttled catalog: wait for the cooldown
			}
		}
		jctx, cancel := context.WithCancel(ctx)
		m.running[j.ID] = cancel
		m.runningKind[j.ID] = j.Kind
		m.runningSrc[src]++
		busy = true
		go func(job model.DownloadJob, src string) {
			defer func() {
				cancel()
				m.mu.Lock()
				delete(m.running, job.ID)
				delete(m.runningKind, job.ID)
				m.runningSrc[src]--
				m.mu.Unlock()
				m.progressMu.Lock()
				delete(m.lastPersist, job.ID)
				m.progressMu.Unlock()
				m.queue.signal()
			}()
			// a worker with the right role takes downloads off this machine;
			// anything it can't have runs here
			handed, err := m.offload(jctx, job)
			// this server keeps to its own task limit; while it is full, a
			// worker that frees up can still take the chapter
			for !handed && !m.takeLocal(jctx, job.Kind) {
				select {
				case <-jctx.Done():
					return
				case <-time.After(localWait):
				}
				handed, err = m.offload(jctx, job)
			}
			if err != nil {
				m.log.Warn("could not hand a chapter to the workers", "job", job.ID, "err", err)
			}
			if !handed {
				defer m.releaseLocal()
				m.run(jctx, job)
			}
		}(j.DownloadJob, src)
	}
	// Queue drained: run module housekeeping (e.g. clear engine page caches).
	if !busy && m.wasBusy && time.Since(m.lastMaint) > 10*time.Minute {
		m.lastMaint = time.Now()
		go m.maintain(context.WithoutCancel(ctx))
	}
	m.wasBusy = busy
}

func (m *Manager) maintain(ctx context.Context) {
	for _, mt := range modules.ActiveAs[source.Maintainer](m.mods, modules.KindSource) {
		if err := mt.Instance.Maintain(ctx); err != nil {
			m.log.Debug("source maintenance", "module", mt.Def.Name, "err", err)
		}
	}
}

func (m *Manager) runningOfKind(kind string) int {
	n := 0
	for _, k := range m.runningKind {
		if k == kind {
			n++
		}
	}
	return n
}

// localWait is how often a chapter waiting for one of this server's own
// slots looks again (and offers itself to the workers again).
const localWait = 2 * time.Second

// takeLocal takes one of this server's own task slots when one is free.
// With this server's own work switched off a download never gets one — it
// waits for a worker — but reprocessing a file still runs, since its image
// work is handed to the workers too and only the file handling stays here.
func (m *Manager) takeLocal(ctx context.Context, kind string) bool {
	dl, _ := m.settings.Downloads(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if dl.LocalOff() && kind == model.JobKindDownload {
		return false
	}
	if dl.MaxLocalTasks > 0 && m.local >= dl.MaxLocalTasks {
		return false
	}
	m.local++
	return true
}

// releaseLocal gives a slot back.
func (m *Manager) releaseLocal() {
	m.mu.Lock()
	m.local--
	m.mu.Unlock()
	m.queue.signal()
}
