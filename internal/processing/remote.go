package processing

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/progress"
	"github.com/Asion001/mangarr/internal/upscaling"
	"github.com/Asion001/mangarr/internal/worktasks"
)

// Remote hands the whole processing stage to a worker with the encode role
// (MANGARR_PROCESSING=workers), so the image work — decoding, resizing,
// upscaling, encoding — never runs inside the server. The worker either
// reads and writes the job's folder directly (shared storage) or trades the
// pages over HTTP.
type Remote struct {
	Tasks *worktasks.Ledger
	// Guard (optional) pauses encoding when a library server can't read it.
	Guard *Guard
	// Route (optional) applies the upscale routes to a chapter: the worker
	// to keep it for, when its pages go to one, and the routes the worker
	// upscales them by.
	Route func(ctx context.Context, cfg model.ProfileConfig, pages []downloads.PageFile) (worktasks.Pin, []model.UpscaleRoute)
}

// TaskPage is one page of a processing task, as the task's spec lists it.
type TaskPage struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Format string `json:"format"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// TaskSpec is what a processing task carries.
type TaskSpec struct {
	Profile model.ProfileConfig `json:"profile"`
	Pages   []TaskPage          `json:"pages"`
	// OutDir is where a worker with shared storage writes its pages and
	// ResultFile; one without sends them as a zip to the output endpoint,
	// which stores it at Output.
	OutDir string `json:"outDir"`
	Output string `json:"output"`
	// UpscaleModel is the model the worker is set to use (System →
	// Workers), filled in when the task is handed out.
	UpscaleModel string `json:"upscaleModel,omitempty"`
	// Routes are the upscale routes: pages they match are upscaled with
	// the route's model.
	Routes []model.UpscaleRoute `json:"routes,omitempty"`
}

// ResultFile is the name of the result in OutDir (or in the output zip).
const ResultFile = "result.json"

// ResultPage is one output page: an input page left as it was (Source), or
// a new file relative to OutDir.
type ResultPage struct {
	Source *int   `json:"source,omitempty"`
	File   string `json:"file,omitempty"`
	Name   string `json:"name"`
	Format string `json:"format"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Result is what a worker's processing produced.
type Result struct {
	Pages          []ResultPage `json:"pages"`
	SourcePages    []int        `json:"sourcePages"`
	Changed        bool         `json:"changed"`
	ProcessedPages int          `json:"processedPages"`
	Upscaled       bool         `json:"upscaled"`
	UpscaleModel   string       `json:"upscaleModel,omitempty"`
	Encoded        int          `json:"encoded"`
	Encoder        string       `json:"encoder,omitempty"`
	UpscaleSeconds float64      `json:"upscaleSeconds"`
	EncodeSeconds  float64      `json:"encodeSeconds"`
	Shrunk         int          `json:"shrunk"`
	Split          int          `json:"split"`
}

// Process sends the pages to a worker and waits for them to come back.
func (r *Remote) Process(ctx context.Context, cfg model.ProfileConfig, pages []downloads.PageFile, workDir string) (downloads.ProcessResult, error) {
	res := downloads.ProcessResult{Pages: pages}
	if cfg.Encode.Format != "" && cfg.Encode.Format != "keep" && r.Guard != nil {
		if blocked, reason := r.Guard.Blocked(); blocked {
			return res, Unavailable{fmt.Errorf("re-encoding is paused: %s", reason)}
		}
	}
	if r.Tasks == nil {
		return res, Unavailable{errors.New("processing runs on workers, but there is no task ledger")}
	}
	if t := worktasks.Adopted(ctx); t != nil {
		// the server restarted while a worker had these pages: wait for that
		// worker rather than start over
		if spec, err := ParseSpec(t.Spec); err == nil && samePages(spec, pages) {
			return r.await(ctx, t.ID, spec, pages)
		}
		_ = r.Tasks.CancelTask(context.WithoutCancel(ctx), t.ID)
	}
	needsUpscale := false
	if cfg.Upscale.Enabled {
		for _, p := range pages {
			if upscaling.NeedsUpscale(p, cfg.Upscale.MinWidth) {
				needsUpscale = true
				break
			}
		}
	}
	var pin worktasks.Pin
	var routes []model.UpscaleRoute
	if needsUpscale && r.Route != nil {
		pin, routes = r.Route(ctx, cfg, pages)
	}
	probe := &model.WorkerTask{Kind: model.TaskEncode, Spec: map[string]any{worktasks.SpecNeedsUpscale: needsUpscale}}
	pin.Apply(probe.Spec)
	if ok, err := r.Tasks.CanTake(ctx, probe); err != nil {
		return res, err
	} else if !ok && pin.Strict {
		return res, Unavailable{errors.New("an upscale rule keeps these pages for a worker that is offline or can't process them")}
	} else if !ok && needsUpscale {
		return res, Unavailable{errors.New("processing runs on workers (MANGARR_PROCESSING=workers), these pages need upscaling, and no worker with the encode role and an upscaler is online")}
	} else if !ok {
		return res, Unavailable{errors.New("processing runs on workers (MANGARR_PROCESSING=workers) and no worker with the encode role is online")}
	}
	jobID := worktasks.JobFrom(ctx)
	if jobID == 0 {
		return res, errors.New("processing on a worker only happens as part of a download job")
	}
	outDir := filepath.Join(workDir, fmt.Sprintf("processed-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(outDir, 0o775); err != nil {
		return res, err
	}
	spec := TaskSpec{Profile: cfg, OutDir: outDir, Output: outDir + ".zip", Routes: routes}
	for _, p := range pages {
		spec.Pages = append(spec.Pages, TaskPage{Name: p.Name, Path: p.Path, Format: p.Format, Width: p.Width, Height: p.Height})
	}
	raw, err := specMap(spec)
	if err != nil {
		return res, err
	}
	raw[worktasks.SpecNeedsUpscale] = needsUpscale
	pin.Apply(raw)
	task := &model.WorkerTask{JobID: jobID, Kind: model.TaskEncode, Spec: raw, PagesTotal: len(pages)}
	if err := r.Tasks.Add(ctx, task); err != nil {
		os.Remove(spec.Output)
		return res, err
	}
	return r.await(ctx, task.ID, spec, pages)
}

// await waits for a worker to finish a processing task and reads what it
// made. Stopping here leaves the task with its worker: a server that stops
// picks it up again when it starts, and a cancelled job cancels its tasks
// itself.
func (r *Remote) await(ctx context.Context, taskID int64, spec TaskSpec, pages []downloads.PageFile) (downloads.ProcessResult, error) {
	res := downloads.ProcessResult{Pages: pages}
	done := r.Tasks.Await(taskID)
	defer r.Tasks.Forget(taskID)
	progress.Report(ctx, progress.Event{Stage: progress.StageEncode, Total: len(pages)})

	// a fast worker on shared storage may be done before Await was called
	var t model.WorkerTask
	if err := r.Tasks.DB().NewSelect().Model(&t).Column("state").Where("id = ?", taskID).Scan(ctx); err == nil && !t.Open() {
		if t.State != model.TaskDone {
			os.Remove(spec.Output)
			return res, r.failure(ctx, taskID, worktasks.ErrGivenUp)
		}
	} else {
		select {
		case err := <-done:
			if err != nil {
				os.Remove(spec.Output)
				return res, r.failure(ctx, taskID, err)
			}
		case <-ctx.Done():
			return res, ctx.Err()
		}
	}
	defer os.Remove(spec.Output)
	out, err := collect(spec, pages)
	if err != nil {
		return res, fmt.Errorf("the worker's result: %w", err)
	}
	progress.Report(ctx, progress.Event{Stage: progress.StageEncode, Done: len(pages), Total: len(pages)})
	return out, nil
}

// samePages reports whether a task works on exactly these pages.
func samePages(spec TaskSpec, pages []downloads.PageFile) bool {
	if len(spec.Pages) != len(pages) {
		return false
	}
	for i, p := range pages {
		if spec.Pages[i].Path != p.Path {
			return false
		}
	}
	return true
}

// failure turns a task that ended badly into the error the download manager
// expects: the worker's own reason is a real failure, a task nobody finished
// means "try again later".
func (r *Remote) failure(ctx context.Context, taskID int64, err error) error {
	var t model.WorkerTask
	if e := r.Tasks.DB().NewSelect().Model(&t).Where("id = ?", taskID).Scan(ctx); e == nil && t.State == model.TaskFailed {
		return fmt.Errorf("the worker could not process the pages: %s", t.Error)
	}
	return Unavailable{fmt.Errorf("no worker processed the pages: %w", err)}
}

// InputName is page i's name in the zip a worker downloads.
func InputName(i int, p TaskPage) string {
	return fmt.Sprintf("%05d%s", i, strings.ToLower(filepath.Ext(p.Path)))
}

// specMap turns a spec into the map a task stores.
func specMap(spec TaskSpec) (map[string]any, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	return out, json.Unmarshal(data, &out)
}

// ParseSpec reads a processing task's spec.
func ParseSpec(raw map[string]any) (TaskSpec, error) {
	var spec TaskSpec
	data, err := json.Marshal(raw)
	if err != nil {
		return spec, err
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		return spec, err
	}
	if len(spec.Pages) == 0 {
		return spec, errors.New("the task has no pages")
	}
	return spec, nil
}

// collect reads what the worker left: the result file in OutDir (shared
// storage), or the zip it uploaded, unpacked there first.
func collect(spec TaskSpec, pages []downloads.PageFile) (downloads.ProcessResult, error) {
	resultPath := filepath.Join(spec.OutDir, ResultFile)
	if _, err := os.Stat(resultPath); err != nil {
		if err := unpack(spec.Output, spec.OutDir); err != nil {
			return downloads.ProcessResult{}, err
		}
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		return downloads.ProcessResult{}, err
	}
	var r Result
	if err := json.Unmarshal(data, &r); err != nil {
		return downloads.ProcessResult{}, err
	}
	out := downloads.ProcessResult{SourcePages: r.SourcePages, Changed: r.Changed, ProcessedPages: r.ProcessedPages,
		Upscaled: r.Upscaled, UpscaleModel: r.UpscaleModel, Encoded: r.Encoded, Encoder: r.Encoder,
		UpscaleSeconds: r.UpscaleSeconds, EncodeSeconds: r.EncodeSeconds, Shrunk: r.Shrunk, Split: r.Split}
	if len(r.Pages) == 0 {
		return out, errors.New("no pages came back")
	}
	if len(out.SourcePages) != len(r.Pages) {
		return out, fmt.Errorf("%d pages came back with %d source indexes", len(r.Pages), len(out.SourcePages))
	}
	for i, p := range r.Pages {
		if s := out.SourcePages[i]; s < 0 || s >= len(pages) {
			return out, fmt.Errorf("page %d maps to input page %d of %d", i, s, len(pages))
		}
		switch {
		case p.Source != nil:
			if *p.Source < 0 || *p.Source >= len(pages) {
				return out, fmt.Errorf("page %d is input page %d of %d", i, *p.Source, len(pages))
			}
			out.Pages = append(out.Pages, pages[*p.Source])
		default:
			path, err := inside(spec.OutDir, p.File)
			if err != nil {
				return out, err
			}
			if _, err := os.Stat(path); err != nil {
				return out, fmt.Errorf("page %d: %w", i, err)
			}
			out.Pages = append(out.Pages, downloads.PageFile{Name: p.Name, Path: path, Format: p.Format, Width: p.Width, Height: p.Height})
		}
	}
	return out, nil
}

// inside resolves a worker-given relative name within dir, refusing
// anything that would leave it.
func inside(dir, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if name == "" || filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("the worker named a file outside its folder: %q", name)
	}
	return filepath.Join(dir, clean), nil
}

// unpack extracts a worker's result zip into dir.
func unpack(zipPath, dir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		path, err := inside(dir, f.Name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o775); err != nil {
			return err
		}
		if err := extract(f, path); err != nil {
			return err
		}
	}
	return nil
}

func extract(f *zip.File, path string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(rc, 1<<30)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

var _ downloads.Processor = (*Remote)(nil)

// Switch processes pages here or on the workers, decided per chapter so a
// change in System → Workers applies without a restart.
type Switch struct {
	Local, Remote downloads.Processor
	// UseRemote says whether the workers do it now.
	UseRemote func(ctx context.Context) bool
}

func (s *Switch) Process(ctx context.Context, cfg model.ProfileConfig, pages []downloads.PageFile, workDir string) (downloads.ProcessResult, error) {
	if s.UseRemote != nil && s.UseRemote(ctx) {
		return s.Remote.Process(ctx, cfg, pages, workDir)
	}
	return s.Local.Process(ctx, cfg, pages, workDir)
}

// OnWorkers says whether the workers process chapters now.
func (s *Switch) OnWorkers(ctx context.Context) bool { return s.UseRemote != nil && s.UseRemote(ctx) }

var (
	_ downloads.Processor = (*Switch)(nil)
	_ downloads.OnWorkers = (*Switch)(nil)
)
