package downloads

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/comicinfo"
	"github.com/Asion001/mangarr/internal/libfs"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/quiet"
)

// Direct downloads: with the library on object storage, a worker that
// fetched a chapter's pages builds the CBZ itself and uploads it straight
// to the bucket, so the pages never pass through the server. The server
// still makes every decision: it screens the pages from their sizes alone,
// says whether and how to process them, writes the ComicInfo, and moves the
// uploaded archive into place with a copy inside the bucket.

// SpecDirect in a download task's spec tells the worker it may package the
// chapter itself (see PlanDirect).
const SpecDirect = "direct"

// DirectPage is one fetched page as a worker describes it.
type DirectPage struct {
	Index  int    `json:"index"`
	Format string `json:"format"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Size   int64  `json:"size"`
}

// Direct plan modes.
const (
	// DirectPackage: process (when Process is set), package and upload.
	DirectPackage = "package"
	// DirectUpload: send the pages to the server as before.
	DirectUpload = "upload"
	// DirectStop: the server ended the task (the job failed or is gone);
	// the worker reports nothing more.
	DirectStop = "stop"
)

// DirectPlan is the server's answer to a worker's pages.
type DirectPlan struct {
	Mode   string `json:"mode" enum:"package,upload,stop"`
	Reason string `json:"reason,omitempty"`
	// Keep are the pages (by index) that go into the archive, in order.
	Keep []int `json:"keep,omitempty"`
	// Process is the profile to process the kept pages with (nil: none).
	Process *model.ProfileConfig `json:"process,omitempty"`
	// ComicInfo goes into the archive, with PageCount set by the worker.
	ComicInfo *comicinfo.ComicInfo `json:"comicInfo,omitempty"`
	// UploadURL takes the archive in one PUT.
	UploadURL string `json:"uploadUrl,omitempty"`
}

// DirectOutPage is one page of a packaged archive.
type DirectOutPage struct {
	Name   string `json:"name"`
	Format string `json:"format"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// DirectResult is what a worker packaged and uploaded.
type DirectResult struct {
	Size   int64           `json:"size"`
	SHA256 string          `json:"sha256"`
	Pages  []DirectOutPage `json:"pages" maxItems:"10000"`
	// SizeBefore is the pages' size as fetched.
	SizeBefore int64 `json:"sizeBefore"`
	// Processed is set when Process ran; the fields after it describe it.
	Processed      bool    `json:"processed"`
	Changed        bool    `json:"changed,omitempty"`
	ProcessedPages int     `json:"processedPages,omitempty"`
	Encoded        int     `json:"encoded,omitempty"`
	Encoder        string  `json:"encoder,omitempty"`
	Shrunk         int     `json:"shrunk,omitempty"`
	Split          int     `json:"split,omitempty"`
	SourcePages    []int   `json:"sourcePages,omitempty" maxItems:"10000"`
	Seconds        float64 `json:"seconds,omitempty"`
}

// DirectOK reports whether a download should be packaged by the worker:
// the library is on object storage.
func DirectOK() bool { return libfs.Remote() != nil }

// stagingPath is where a worker uploads a job's archive: a hidden folder in
// the series' root folder, so it sits in the same bucket as its target.
func (m *Manager) stagingPath(ctx context.Context, jc *jobCtx, taskID int64) (string, error) {
	rf, err := m.lib.RootFolder(ctx, jc.series.RootFolderID)
	if err != nil {
		return "", err
	}
	return filepath.Join(rf.Path, StagingDir, fmt.Sprintf("job-%d-task-%d.cbz", jc.job.ID, taskID)), nil
}

// StagingDir is the folder in a root folder that holds uploads waiting to
// be moved into place.
const StagingDir = ".mangarr-staging"

// directTTL is how long a worker has to process and upload a chapter.
const directTTL = 6 * time.Hour

// PlanDirect screens the pages a worker fetched and says what to do with
// them. canProcess is whether that worker may process pages (it has the
// encode role).
func (m *Manager) PlanDirect(ctx context.Context, task model.WorkerTask, pages []DirectPage, canProcess bool) (DirectPlan, error) {
	var job model.DownloadJob
	if err := m.db.NewSelect().Model(&job).Where("id = ?", task.JobID).Scan(ctx); err != nil {
		return DirectPlan{Mode: DirectStop, Reason: "the job is gone"}, nil
	}
	jc, err := m.load(ctx, &job)
	if err != nil {
		m.fail(ctx, &job, nil, err)
		return DirectPlan{Mode: DirectStop, Reason: err.Error()}, nil
	}
	if want := task.PagesTotal; want > 0 && len(pages) != want {
		err := fmt.Errorf("the worker fetched %d of %d pages", len(pages), want)
		m.fail(ctx, &job, jc, err)
		return DirectPlan{Mode: DirectStop, Reason: err.Error()}, nil
	}
	files := make([]PageFile, len(pages))
	for i, p := range pages {
		files[i] = PageFile{Name: fmt.Sprint(p.Index), Format: p.Format, Width: p.Width, Height: p.Height}
	}
	m.pagesIn(ctx, job.ID)
	kept, err := m.screenPages(ctx, jc, files)
	if err != nil {
		m.fail(ctx, &job, jc, err)
		return DirectPlan{Mode: DirectStop, Reason: err.Error()}, nil
	}
	plan := DirectPlan{Mode: DirectPackage}
	for _, p := range kept {
		var idx int
		_, _ = fmt.Sscan(p.Name, &idx)
		plan.Keep = append(plan.Keep, idx)
	}
	cfg := jc.profile.Config
	params := cfg.ProcessParams()
	sched, _ := m.settings.Schedule(ctx)
	paused := quiet.Evaluate(sched, time.Now()).PauseProcessing
	if job.ConfigOverride != nil && paused && params != "" {
		err := infraError{errors.New("processing is paused by the schedule")}
		m.fail(ctx, &job, jc, err)
		return DirectPlan{Mode: DirectStop, Reason: err.Error()}, nil
	}
	if params != "" && cfg.ProcessTiming == "inline" && !paused && m.Processor != nil {
		if !canProcess || m.DirectProcess == nil || !m.DirectProcess(ctx, cfg, kept) {
			// processing has to happen elsewhere (here, or on a worker
			// with an upscaler): the pages go to the server as before
			return DirectPlan{Mode: DirectUpload, Reason: "these pages are processed elsewhere"}, nil
		}
		plan.Process = &cfg
		m.setStatus(ctx, &job, model.JobProcessing, model.ChapterProcessing)
	}
	staging, err := m.stagingPath(ctx, jc, task.ID)
	if err != nil {
		return DirectPlan{}, err
	}
	url, ok := libfs.UploadURL(staging, directTTL)
	if !ok {
		return DirectPlan{Mode: DirectUpload, Reason: "the library is on this server's disk"}, nil
	}
	mm, _ := m.settings.MediaManagement(ctx)
	ci := m.comicInfo(ctx, jc, 0, mm.WriteVolume)
	plan.ComicInfo, plan.UploadURL = &ci, url
	return plan, nil
}

// FinishDirect imports the archive a worker uploaded for task: it checks
// what arrived, then moves it into place like an archive written here.
func (m *Manager) FinishDirect(ctx context.Context, task model.WorkerTask, res DirectResult) error {
	var job model.DownloadJob
	if err := m.db.NewSelect().Model(&job).Where("id = ?", task.JobID).Scan(ctx); err != nil {
		return err
	}
	jc, err := m.load(ctx, &job)
	if err != nil {
		m.fail(ctx, &job, nil, err)
		return err
	}
	staging, err := m.stagingPath(ctx, jc, task.ID)
	if err != nil {
		m.fail(ctx, &job, jc, infraError{err})
		return err
	}
	defer func() { _ = libfs.Remove(staging) }() // gone after the move; left only on failure
	libfs.Forget(staging)
	st, err := libfs.Stat(staging)
	switch {
	case err != nil:
		err = infraError{fmt.Errorf("the worker's upload isn't there: %w", err)}
	case st.Size() != res.Size:
		err = infraError{fmt.Errorf("the worker uploaded %d bytes, %d arrived", res.Size, st.Size())}
	case len(res.Pages) == 0:
		err = permanent(errors.New("the worker packaged no pages"))
	}
	if err != nil {
		m.fail(ctx, &job, jc, err)
		return err
	}
	m.Live.Start(job.ID, job.Kind)
	defer m.Live.Finish(job.ID)
	proc := ProcessResult{Changed: res.Changed, ProcessedPages: res.ProcessedPages, Encoded: res.Encoded, Encoder: res.Encoder,
		Shrunk: res.Shrunk, Split: res.Split, SourcePages: res.SourcePages, Seconds: res.Seconds}
	for _, p := range res.Pages {
		proc.Pages = append(proc.Pages, PageFile{Name: p.Name, Format: p.Format, Width: p.Width, Height: p.Height})
	}
	params := ""
	if res.Processed {
		params = jc.profile.Config.ProcessParams()
	}
	m.setStatus(ctx, &job, model.JobImporting, "")
	err = m.importWith(ctx, jc, proc, params, res.SizeBefore, func(target string, _ []byte, _ os.FileMode) (cbz.Result, error) {
		// the worker put the ComicInfo in; the archive only moves
		if err := libfs.Rename(staging, target); err != nil {
			return cbz.Result{}, err
		}
		return cbz.Result{Size: res.Size, SHA256: res.SHA256}, nil
	})
	if err != nil {
		m.fail(ctx, &job, jc, err)
		return err
	}
	m.log.Info("chapter imported from a worker's upload", "series", jc.series.Title, "chapter", jc.chapter.NumberKey,
		"pages", len(proc.Pages), "encoded", proc.Encoded, "job", job.ID)
	return nil
}

// SweepStaging deletes uploads older than a day that no job moved into
// place (a worker that died mid-way, a job that failed).
func (m *Manager) SweepStaging(ctx context.Context) (int, error) {
	var roots []model.RootFolder
	if err := m.db.NewSelect().Model(&roots).Scan(ctx); err != nil {
		return 0, err
	}
	n := 0
	for _, rf := range roots {
		dir := filepath.Join(rf.Path, StagingDir)
		entries, err := libfs.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			info, err := e.Info()
			if err != nil || e.IsDir() || time.Since(info.ModTime()) < 24*time.Hour {
				continue
			}
			if libfs.Remove(filepath.Join(dir, e.Name())) == nil {
				n++
			}
		}
	}
	return n, nil
}
