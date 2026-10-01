package downloads

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/history"
	"github.com/Asion001/mangarr/internal/library"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/sourcegov"
	"github.com/Asion001/mangarr/internal/worktasks"
)

// Manager owns job lifecycle and shared state. Pipeline stages are methods on
// the same manager so local jobs and worker completions use the same services.
type Manager struct {
	db       *db.DB
	bus      *events.Bus
	mods     *modules.Manager
	settings *settings.Store
	lib      *library.Library
	queue    *Queue
	searcher *Searcher
	log      *slog.Logger
	dataDir  string

	Processor Processor
	// Live has the progress of running jobs.
	Live *Live
	// Gov (optional) paces chapters per catalog and defers throttled catalogs.
	Gov *sourcegov.Governor
	// Tasks (optional) is the ledger of work handed to workers. Jobs with a
	// task still open belong to a worker, not to this process.
	Tasks *worktasks.Ledger

	mu          sync.Mutex
	running     map[int64]context.CancelFunc
	runningKind map[int64]string
	runningSrc  map[string]int
	// local counts the jobs this server is working on itself (not handed
	// to a worker), for Downloads.MaxLocalTasks.
	local       int
	lastMaint   time.Time
	wasBusy     bool
	held        bool
	progressMu  sync.Mutex
	lastPersist map[int64]time.Time
}

func NewManager(d *db.DB, bus *events.Bus, mods *modules.Manager, st *settings.Store, lib *library.Library, q *Queue, s *Searcher, log *slog.Logger, dataDir string) *Manager {
	return &Manager{db: d, bus: bus, mods: mods, settings: st, lib: lib, queue: q, searcher: s, log: log, dataDir: dataDir,
		running: map[int64]context.CancelFunc{}, runningKind: map[int64]string{}, runningSrc: map[string]int{}, lastPersist: map[int64]time.Time{},
		Live: NewLive(bus)}
}

// Start recovers interrupted jobs and starts the scheduling loop. Jobs a
// worker still downloads are left alone: the restart was ours, not theirs,
// and their pages are still arriving in staging. Jobs a worker is
// processing go back to the queue, and running again picks up that
// worker's task (see run).
func (m *Manager) Start(ctx context.Context) error {
	live, err := m.openJobs(ctx)
	if err != nil {
		return err
	}
	if err := m.failCrashedProcessing(ctx, live); err != nil {
		return err
	}
	if live, err = m.openJobs(ctx, model.TaskDownload); err != nil {
		return err
	}
	now := time.Now().UTC()
	q := m.db.NewUpdate().Model((*model.DownloadJob)(nil)).
		Set("status = ?", model.JobQueued).Set("not_before = ?", now).Set("updated_at = ?", now).
		Where("status IN (?)", bun.In([]string{model.JobDownloading, model.JobProcessing, model.JobImporting}))
	if len(live) > 0 {
		q = q.Where("id NOT IN (?)", bun.In(keys(live)))
	}
	if _, err := q.Exec(ctx); err != nil {
		return err
	}
	keep, err := m.openJobs(ctx)
	if err != nil {
		return err
	}
	m.pruneStaging(ctx, keep)
	go m.loop(ctx)
	return nil
}

// openJobs is the jobs a worker is still busy with, with tasks of these
// kinds (any kind when none are given).
func (m *Manager) openJobs(ctx context.Context, kinds ...string) (map[int64]bool, error) {
	if m.Tasks == nil {
		return nil, nil
	}
	return m.Tasks.OpenJobs(ctx, kinds...)
}

func keys(m map[int64]bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ErrStoppedWhileProcessing is recorded on a chapter whose processing was
// running when mangarr died.
var ErrStoppedWhileProcessing = errors.New("mangarr stopped while processing this chapter (most likely it ran out of memory); it is retried later")

// failCrashedProcessing gives up, for now, on the reprocess jobs that were
// processing when the process died. A clean stop hands its jobs back
// (Release), so these were running when it was killed, and processing is
// what kills a small server: re-running the chapter straight away would
// kill it again, forever, and hold the processing slot while doing so. Each
// such stop counts as a failed processing attempt of the file, and the
// backlog retries it later with the usual growing delays.
func (m *Manager) failCrashedProcessing(ctx context.Context, live map[int64]bool) error {
	var jobs []model.DownloadJob
	q := m.db.NewSelect().Model(&jobs).Where("kind = ? AND status = ?", model.JobKindReprocess, model.JobProcessing)
	if len(live) > 0 {
		q = q.Where("id NOT IN (?)", bun.In(keys(live)))
	}
	if err := q.Scan(ctx); err != nil {
		return err
	}
	for i := range jobs {
		job := &jobs[i]
		m.log.Warn("mangarr stopped while this chapter was processing; it is retried later", "job", job.ID, "chapterId", job.ChapterID)
		var ch model.Chapter
		if m.db.NewSelect().Model(&ch).Where("id = ?", job.ChapterID).Scan(ctx) == nil && ch.FileID != nil {
			var f model.ChapterFile
			if m.db.NewSelect().Model(&f).Where("id = ?", *ch.FileID).Scan(ctx) == nil {
				m.markProcessFailed(ctx, &f, ErrStoppedWhileProcessing)
			}
		}
		job.Status, job.Attempt, job.Error, job.UpdatedAt = model.JobFailed, job.Attempt+1, ErrStoppedWhileProcessing.Error(), time.Now().UTC()
		if _, err := m.db.NewUpdate().Model(job).Column("status", "attempt", "error", "updated_at").WherePK().Exec(ctx); err != nil {
			return err
		}
		chID := job.ChapterID
		_ = history.Record(ctx, m.db, job.SeriesID, &chID, model.HistoryFailed, "", map[string]string{"error": ErrStoppedWhileProcessing.Error()})
	}
	return nil
}

// Release hands the jobs running here back to the queue. It is called when
// the server stops on purpose, so the next start can tell them from jobs
// that were running when the process was killed.
func (m *Manager) Release() {
	m.mu.Lock()
	ids := make([]int64, 0, len(m.running))
	for id := range m.running {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	if len(ids) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := time.Now().UTC()
	if _, err := m.db.NewUpdate().Model((*model.DownloadJob)(nil)).
		Set("status = ?", model.JobQueued).Set("not_before = ?", now).Set("updated_at = ?", now).
		Where("id IN (?) AND status IN (?)", bun.In(ids), bun.In([]string{model.JobDownloading, model.JobProcessing, model.JobImporting})).
		Exec(ctx); err != nil {
		m.log.Warn("could not hand running jobs back to the queue", "err", err)
	}
}

// pruneStaging drops the working directories of jobs nobody is doing any
// more, and keeps the ones a worker is still uploading into.
func (m *Manager) pruneStaging(ctx context.Context, live map[int64]bool) {
	staging := filepath.Join(m.dataDir, "staging")
	if len(live) == 0 {
		_ = os.RemoveAll(staging)
		return
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		return
	}
	for _, e := range entries {
		id, err := strconv.ParseInt(strings.TrimPrefix(e.Name(), "job-"), 10, 64)
		if err != nil || !live[id] {
			_ = os.RemoveAll(filepath.Join(staging, e.Name()))
		}
	}
}
