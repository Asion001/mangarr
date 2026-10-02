package downloads

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/imagecheck"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/quiet"
	"github.com/Asion001/mangarr/internal/worktasks"
)

// ProcessResult is the outcome of the processing stage.
type ProcessResult struct {
	Pages []PageFile
	// SourcePages maps each output page to its zero-based input page. It is
	// populated when a stage may change page count (currently tall splitting).
	SourcePages []int
	Changed     bool // pages differ from the input
	// ProcessedPages counts pages whose stored image changed. It deliberately
	// excludes pages inspected by a no-op run so speed statistics stay honest.
	ProcessedPages int
	Upscaled       bool
	UpscaleModel   string
	Encoded        int // pages re-encoded
	Encoder        string
	// Seconds is how long processing took (set by the manager).
	Seconds float64
	// UpscaleSeconds and EncodeSeconds time the two steps (for previews).
	UpscaleSeconds float64
	EncodeSeconds  float64
	// Shrunk counts pages downsized to the profile's maximum width.
	Shrunk int
	// Split counts tall input pages split into two or more output pages.
	Split int
}

// Processor upscales and/or re-encodes pages according to a profile. On success,
// Pages contains the complete ordered import input, even when Changed is false.
type Processor interface {
	Process(ctx context.Context, cfg model.ProfileConfig, pages []PageFile, workDir string) (ProcessResult, error)
}

// finish is the second half of a download: processing and import. Pages
// reach it either from here or from a worker that uploaded them, and
// everything after this point is the same either way.
func (m *Manager) finish(ctx context.Context, job model.DownloadJob, jc *jobCtx, pages []PageFile, workDir string) {
	log := m.log.With("job", job.ID, "chapterId", job.ChapterID)
	ctx = worktasks.WithJob(ctx, job.ID)
	m.pagesIn(ctx, job.ID)
	if job.Kind == model.JobKindDownload {
		kept, err := m.screenPages(ctx, jc, pages)
		if err != nil {
			m.fail(ctx, &job, jc, err)
			return
		}
		pages = kept
	}
	// processing (upscale / re-encode)
	cfg := jc.profile.Config
	params := cfg.ProcessParams()
	var sizeBefore int64
	for _, p := range pages {
		if st, err := os.Stat(p.Path); err == nil {
			sizeBefore += st.Size()
		}
	}
	proc := ProcessResult{Pages: pages}
	processed := false
	if job.Kind == model.JobKindReprocess && job.RecycledFileID == nil && params == "" {
		m.markProcessed(ctx, jc.file, "", 0)
		m.completeUnchanged(ctx, &job, "processing is disabled for this series' profile")
		return
	}
	processingPaused := false
	if job.Kind == model.JobKindDownload {
		sched, _ := m.settings.Schedule(ctx)
		processingPaused = quiet.Evaluate(sched, time.Now()).PauseProcessing
		if job.ConfigOverride != nil && processingPaused && params != "" {
			m.fail(ctx, &job, jc, infraError{errors.New("processing is paused by the schedule")})
			return
		}
	}
	inline := job.Kind == model.JobKindReprocess || cfg.ProcessTiming == "inline"
	if m.Processor != nil && params != "" && inline && !processingPaused {
		m.setStatus(ctx, &job, model.JobProcessing, model.ChapterProcessing)
		started := time.Now()
		res, perr := m.Processor.Process(ctx, cfg, pages, workDir)
		res.Seconds = time.Since(started).Seconds()
		if res.ProcessedPages == 0 {
			res.Seconds = 0
		}
		var tmp interface{ Temporary() bool }
		switch {
		case perr != nil && ctx.Err() != nil:
			m.fail(ctx, &job, jc, ctx.Err())
			return
		case perr != nil && job.Kind == model.JobKindReprocess && errors.As(perr, &tmp) && tmp.Temporary():
			// the file on disk is fine; retry once the engine is back
			m.markProcessRetry(ctx, jc.file, perr)
			m.fail(ctx, &job, jc, infraError{perr})
			return
		case perr != nil && job.Kind == model.JobKindReprocess:
			m.markProcessFailed(ctx, jc.file, perr)
			m.fail(ctx, &job, jc, permanent(perr))
			return
		case perr != nil && job.ConfigOverride != nil:
			m.fail(ctx, &job, jc, infraError{perr})
			return
		case perr != nil:
			log.Warn("processing failed, importing original pages", "err", perr)
			m.bus.Publish(events.Event{Type: events.HealthIssue, SeriesID: jc.series.ID, Payload: events.MessagePayload{
				Title: "Processing failed", Message: fmt.Sprintf("%s ch. %s was imported without processing (it will be retried in the background): %v", jc.series.Title, jc.chapter.NumberKey, perr)}})
		default:
			proc, processed = res, true
		}
		if processed && job.Kind == model.JobKindReprocess && job.RecycledFileID == nil && !proc.Changed {
			// nothing to upscale and re-encoding wouldn't save space
			m.markProcessed(ctx, jc.file, params, proc.Seconds)
			m.completeUnchanged(ctx, &job, "nothing to change")
			return
		}
	}
	if !processed {
		params = "" // the background backlog will process it
	}

	m.setStatus(ctx, &job, model.JobImporting, "")
	if err := m.importChapter(ctx, jc, proc, params, sizeBefore); err != nil {
		m.fail(ctx, &job, jc, err)
		return
	}
	log.Info("chapter imported", "series", jc.series.Title, "chapter", jc.chapter.NumberKey, "pages", len(proc.Pages),
		"upscaled", proc.Upscaled, "encoded", proc.Encoded)
}

// markProcessed records that a file was processed with params (no rewrite).
func (m *Manager) markProcessed(ctx context.Context, f *model.ChapterFile, params string, seconds float64) {
	if f == nil {
		return
	}
	now := time.Now().UTC()
	pages := 0
	if seconds > 0 {
		pages = f.PageCount
	}
	_, _ = m.db.NewUpdate().Model((*model.ChapterFile)(nil)).Set("process_params = ?", params).Set("process_state = ?", model.ProcessDone).
		Set("process_seconds = ?", seconds).Set("process_pages = ?", pages).
		Set("process_error = ''").Set("process_attempts = 0").Set("process_retry_at = NULL").Set("processed_at = ?", now).
		Where("id = ?", f.ID).Exec(ctx)
	m.bus.Changed("chapter", "updated", f.ChapterID)
}

// markProcessRetry postpones processing after a temporary failure.
func (m *Manager) markProcessRetry(ctx context.Context, f *model.ChapterFile, err error) {
	if f == nil {
		return
	}
	at := time.Now().UTC().Add(30 * time.Minute)
	_, _ = m.db.NewUpdate().Model((*model.ChapterFile)(nil)).Set("process_error = ?", err.Error()).Set("process_retry_at = ?", at).
		Where("id = ?", f.ID).Exec(ctx)
}

// markProcessFailed counts a failed processing attempt; the backlog retries
// with growing delays and gives up after MaxProcessAttempts.
func (m *Manager) markProcessFailed(ctx context.Context, f *model.ChapterFile, err error) {
	if f == nil {
		return
	}
	delay := time.Duration(1<<min(f.ProcessAttempts, 5)) * time.Hour
	at := time.Now().UTC().Add(delay)
	_, _ = m.db.NewUpdate().Model((*model.ChapterFile)(nil)).Set("process_state = ?", model.ProcessFailed).Set("process_error = ?", err.Error()).
		Set("process_attempts = process_attempts + 1").Set("process_retry_at = ?", at).Where("id = ?", f.ID).Exec(ctx)
	m.bus.Changed("chapter", "updated", f.ChapterID)
}

// MaxProcessAttempts is how often the backlog retries a failing file.
const MaxProcessAttempts = 5

// completeUnchanged finishes a reprocess job that had nothing to do, leaving
// the chapter file untouched.
func (m *Manager) completeUnchanged(ctx context.Context, job *model.DownloadJob, reason string) {
	job.Status, job.Progress, job.Error, job.UpdatedAt = model.JobCompleted, 100, "", time.Now().UTC()
	_, _ = m.db.NewUpdate().Model(job).Column("status", "progress", "error", "updated_at").WherePK().Where("status <> ?", model.JobPaused).Exec(ctx)
	m.log.Info("reprocess skipped", "job", job.ID, "chapter", job.ChapterID, "reason", reason)
	m.bus.Changed("queue", "updated", job.ID)
}

// extractExisting unpacks an imported CBZ for re-processing.
func (m *Manager) extractExisting(jc *jobCtx, workDir string) ([]PageFile, error) {

	var pages []cbz.Page
	var err error
	jc.inputFile = jc.file
	if jc.job.RecycledFileID != nil {
		err = m.lib.WithRecycled(context.Background(), *jc.job.RecycledFileID, func(r *model.RecycledFile, path string) error {
			if r.Kind != "file" || r.ChapterID == nil || *r.ChapterID != jc.chapter.ID {
				return errors.New("recycled input does not belong to chapter")
			}
			jc.inputFile = r.FileSnapshot
			pages, _, err = cbz.Read(path)
			return err
		})
	} else {
		if jc.file == nil {
			return nil, permanent(errors.New("chapter has no file to re-process"))
		}
		dir, e := m.lib.SeriesDir(context.Background(), &jc.series)
		if e != nil {
			return nil, infraError{e}
		}
		pages, _, err = cbz.Read(filepath.Join(dir, jc.file.RelativePath))
	}
	if err != nil {
		return nil, permanent(fmt.Errorf("read existing file: %w", err))
	}
	out := make([]PageFile, 0, len(pages))
	for i, p := range pages {
		info, err := imagecheck.Detect(p.Data)
		if err != nil {
			return nil, permanent(fmt.Errorf("existing page %s: %w", p.Name, err))
		}
		name := cbz.PageName(i, imagecheck.Ext(info.Format))
		path := filepath.Join(workDir, name)
		if err := os.WriteFile(path, p.Data, 0o664); err != nil {
			return nil, infraError{err}
		}
		out = append(out, PageFile{Name: name, Path: path, Format: info.Format, Width: info.Width, Height: info.Height})
	}
	return out, nil
}
