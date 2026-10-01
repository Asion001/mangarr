package downloads

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/imagecheck"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/sourcegov"
	"github.com/Asion001/mangarr/internal/worktasks"
)

// PageSpec is one page as a worker is told to fetch it: where it is, and
// what the request needs to look like. The server resolves these, because
// working out a page's address is the module's job and needs its session.
type PageSpec struct {
	Index   int               `json:"index"`
	Name    string            `json:"name"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// offload hands a download job to the workers, by writing the task they
// pull from. It reports whether the job was handed over; false means this
// process should do it.
func (m *Manager) offload(ctx context.Context, job model.DownloadJob) (bool, error) {
	if m.Tasks == nil || job.Kind != model.JobKindDownload {
		return false, nil
	}
	dl, _ := m.settings.Downloads(ctx)
	if dl.WorkerPlacement == settingsLocal {
		return false, nil
	}
	ready, err := m.workersFor(ctx, model.RoleDownload, dl.WorkerPlacement)
	if err != nil || !ready {
		return false, err
	}
	if !m.claim(ctx, &job) {
		return true, nil // someone else has it
	}
	jc, err := m.load(ctx, &job)
	if err != nil {
		m.fail(ctx, &job, nil, err)
		return true, nil
	}
	if jc.release == nil || jc.link == nil {
		cand, upgrade, cerr := m.nextCandidate(ctx, jc)
		if cerr != nil || cand == nil {
			m.fail(ctx, &job, jc, permanent(errors.New("no downloadable release left for this chapter")))
			return true, nil
		}
		rel, link := cand.Release, cand.Source
		jc.release, jc.link, job.ReleaseID, job.IsUpgrade = &rel, &link, &rel.ID, upgrade
		_, _ = m.db.NewUpdate().Model(&job).Column("release_id", "is_upgrade").WherePK().Exec(ctx)
	}
	if m.Gov != nil {
		// the same pause between chapters of a catalog a download here would
		// take: pacing belongs to the server, whoever fetches the pages
		if err := m.Gov.Pace(ctx, sourcegov.Key{ModuleID: jc.link.ModuleID, SourceID: jc.link.SourceID}, "chapter"); err != nil {
			m.fail(ctx, &job, jc, err)
			return true, nil
		}
	}
	specs, err := m.pageSpecs(ctx, jc)
	if err != nil {
		if errors.Is(err, source.ErrUnsupported) {
			// this module's pages can only be fetched here: put the job back
			// and run it locally
			m.requeue(ctx, &job)
			return false, nil
		}
		m.fail(ctx, &job, jc, err)
		return true, nil
	}
	spec := map[string]any{
		"pages":    specs,
		"prefetch": dl.WorkerPrefetch,
		"series":   jc.series.Title,
		"chapter":  jc.chapter.NumberKey,
		"rate":     m.workerBudget(jc, dl.MaxPerSource),
	}
	task := &model.WorkerTask{JobID: job.ID, Kind: model.TaskDownload, Spec: spec, PagesTotal: len(specs)}
	if err := m.Tasks.Add(ctx, task); err != nil {
		m.requeue(ctx, &job)
		return false, err
	}
	m.setStatus(ctx, &job, model.JobDownloading, model.ChapterDownloading)
	m.progress(&job, 0, len(specs), 0)
	m.log.Info("chapter handed to the workers", "job", job.ID, "series", jc.series.Title, "chapter", jc.chapter.NumberKey, "pages", len(specs))
	return true, nil
}

// workerBudget is the share of a catalog's request budget a worker is told
// to keep to. It is divided by the chapters of that catalog that may run at
// once, never handed over whole: several workers on one catalog must add up
// to what one machine here would have done.
func (m *Manager) workerBudget(jc *jobCtx, perSource int) map[string]any {
	if m.Gov == nil {
		return nil
	}
	lim := m.Gov.Limits(sourcegov.Key{ModuleID: jc.link.ModuleID, SourceID: jc.link.SourceID})
	share := max(perSource, 1)
	out := map[string]any{"minDelayMs": lim.MinDelayMs, "jitterMs": lim.JitterMs}
	if lim.RequestsPerMinute > 0 {
		out["requestsPerMinute"] = max(lim.RequestsPerMinute/share, 1)
	}
	if lim.MaxConcurrent > 0 {
		out["maxConcurrent"] = max(lim.MaxConcurrent/share, 1)
	}
	return out
}

// settingsLocal avoids importing the settings package for one constant in
// the hot path; it is settings.PlaceLocal.
const settingsLocal = "local"

// workersFor reports whether a worker could take this kind of work.
func (m *Manager) workersFor(ctx context.Context, role, placement string) (bool, error) {
	var list []model.Worker
	if err := m.db.NewSelect().Model(&list).Where("enabled = ?", true).Scan(ctx); err != nil {
		return false, err
	}
	for _, w := range list {
		if !w.Offers(role) {
			continue
		}
		if placement == "workers" {
			return true, nil // wait for it even when it is away
		}
		if w.LastSeenAt != nil && time.Since(*w.LastSeenAt) < 2*time.Minute {
			return true, nil
		}
	}
	return false, nil
}

// requeue puts a claimed job back for the next pass.
func (m *Manager) requeue(ctx context.Context, job *model.DownloadJob) {
	now := time.Now().UTC()
	job.Status, job.UpdatedAt = model.JobQueued, now
	_, _ = m.db.NewUpdate().Model(job).Column("status", "updated_at").WherePK().Exec(ctx)
}

// pageSpecs resolves a chapter's pages into requests another machine can
// make. It returns source.ErrUnsupported when the module's pages can only
// be fetched here.
func (m *Manager) pageSpecs(ctx context.Context, jc *jobCtx) ([]PageSpec, error) {
	mod, _, err := modules.GetAs[source.Module](m.mods, jc.link.ModuleID)
	if err != nil {
		return nil, infraError{err}
	}
	fetchable, ok := mod.(source.Fetchable)
	if !ok {
		return nil, source.ErrUnsupported
	}
	ref := source.ChapterRef{
		Manga:     source.MangaRef{SourceID: jc.link.SourceID, URL: jc.link.MangaURL, EngineRef: jc.link.EngineRef, TitleHint: firstNonEmpty(jc.link.Title, jc.series.Title)},
		URL:       jc.release.ChapterURL,
		EngineRef: jc.release.EngineRef,
	}
	pctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	list, err := mod.Pages(pctx, ref)
	cancel()
	if err != nil {
		if errors.Is(err, source.ErrNotFound) {
			return nil, permanent(err)
		}
		return nil, classify(err)
	}
	if min := jc.profile.Config.MinPages; min > 0 && len(list) < min {
		return nil, permanent(fmt.Errorf("chapter has %d pages, profile requires at least %d", len(list), min))
	}
	out := make([]PageSpec, 0, len(list))
	for i, p := range list {
		req, err := fetchable.PageRequest(ctx, p)
		if err != nil || req.URL == "" {
			return nil, source.ErrUnsupported // fall back to fetching here
		}
		out = append(out, PageSpec{Index: i, Name: fmt.Sprintf("%04d", i+1), URL: req.URL, Headers: req.Headers})
	}
	return out, nil
}

// WorkDir is where a job's pages are collected, wherever they come from.
func (m *Manager) WorkDir(jobID int64) string { return m.workDir(jobID) }

// AcceptPage stores one page a worker fetched, exactly where a page
// fetched here would go, so importing needs no special case.
func (m *Manager) AcceptPage(ctx context.Context, jobID int64, index int, data []byte) (PageFile, error) {
	info, err := imagecheck.Detect(data)
	if err != nil {
		return PageFile{}, err
	}
	dir := m.workDir(jobID)
	if err := os.MkdirAll(dir, 0o775); err != nil {
		return PageFile{}, err
	}
	name := cbz.PageName(index, imagecheck.Ext(info.Format))
	path := filepath.Join(dir, name)
	tmp := path + ".part"
	if err := os.WriteFile(tmp, data, 0o664); err != nil {
		return PageFile{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return PageFile{}, err
	}
	return PageFile{Name: name, Path: path, Format: info.Format, Width: info.Width, Height: info.Height}, nil
}

// TaskDone finishes a job whose pages a worker has uploaded: from here on
// it is the same as one downloaded here (processing, then import).
func (m *Manager) TaskDone(ctx context.Context, task model.WorkerTask) error {
	var job model.DownloadJob
	if err := m.db.NewSelect().Model(&job).Where("id = ?", task.JobID).Scan(ctx); err != nil {
		return err
	}
	jc, err := m.load(ctx, &job)
	if err != nil {
		m.fail(ctx, &job, nil, err)
		return err
	}
	workDir := m.workDir(job.ID)
	pages, err := collectPages(workDir)
	if err != nil {
		m.fail(ctx, &job, jc, err)
		return err
	}
	if len(pages) == 0 {
		m.fail(ctx, &job, jc, errors.New("the worker uploaded no pages"))
		return nil
	}
	if want := task.PagesTotal; want > 0 && len(pages) != want {
		m.fail(ctx, &job, jc, fmt.Errorf("the worker uploaded %d of %d pages", len(pages), want))
		return nil
	}
	m.Live.Start(job.ID, job.Kind)
	defer m.Live.Finish(job.ID)
	defer os.RemoveAll(workDir)
	m.finish(ctx, job, jc, pages, workDir)
	return nil
}

// TaskFailed reports a worker's failure against the job, with the same
// classification a failure here would get (a retry, another release, or a
// blocklisted one).
func (m *Manager) TaskFailed(ctx context.Context, task model.WorkerTask, reason string) {
	var job model.DownloadJob
	if err := m.db.NewSelect().Model(&job).Where("id = ?", task.JobID).Scan(ctx); err != nil {
		return
	}
	jc, _ := m.load(ctx, &job)
	_ = os.RemoveAll(m.workDir(job.ID))
	m.fail(ctx, &job, jc, classify(errors.New(reason)))
}

// TaskAbandoned fails a job whose task nobody finished. It counts as an
// infrastructure failure: the release is fine, the machine that was going
// to fetch it wasn't, so it is retried and never blocklisted.
func (m *Manager) TaskAbandoned(ctx context.Context, task model.WorkerTask, reason string) {
	var job model.DownloadJob
	if err := m.db.NewSelect().Model(&job).Where("id = ?", task.JobID).Scan(ctx); err != nil {
		return
	}
	if job.Status == model.JobCompleted || job.Status == model.JobFailed {
		return
	}
	_ = os.RemoveAll(m.workDir(job.ID))
	if task.Attempt == 0 {
		// nobody ever started it: the worker that was there when the task was
		// written went away. Nothing was tried, so nothing is held against
		// this release — the job goes straight back to the queue and is
		// downloaded here (or by the next worker) at once.
		m.log.Info("a chapter came back from the workers", "job", job.ID, "reason", reason)
		m.requeue(ctx, &job)
		m.bus.Changed("queue", "updated", job.ID)
		m.queue.signal()
		return
	}
	jc, _ := m.load(ctx, &job)
	if reason == "" {
		reason = "no worker finished this chapter"
	}
	m.fail(ctx, &job, jc, infraError{errors.New(reason)})
}

// TaskProgress reports what a worker has done so far against its job, so
// the queue shows the same numbers a local download would.
func (m *Manager) TaskProgress(task model.WorkerTask, done, total int, bytesIn int64) {
	job := &model.DownloadJob{ID: task.JobID}
	m.progress(job, done, total, bytesIn)
}

// collectPages reads what was uploaded into a working directory, in order.
func collectPages(dir string) ([]PageFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []PageFile
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) == ".part" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		info, err := imagecheck.Detect(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, PageFile{Name: e.Name(), Path: path, Format: info.Format, Width: info.Width, Height: info.Height})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Ledger is the task ledger this manager hands work to (nil when workers
// are not in use).
func (m *Manager) Ledger() *worktasks.Ledger { return m.Tasks }
