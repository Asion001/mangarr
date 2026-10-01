package worker

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/modules/upscale"
	"github.com/Asion001/mangarr/internal/processing"
	"github.com/Asion001/mangarr/internal/upscaler"
	"github.com/Asion001/mangarr/internal/upscaling"
)

// process runs a chapter's whole processing stage here — resize, upscale,
// split, re-encode — so none of it runs in the server. With shared storage
// it works in the job's folder on the server's volume; otherwise it fetches
// the pages and sends back what changed.
func (w *Worker) process(ctx context.Context, t Task) (result, error) {
	spec, err := processing.ParseSpec(t.Spec)
	if err != nil {
		return result{}, err
	}
	cfg := spec.Profile
	if cfg.Upscale.Enabled && w.up == nil && needsUpscale(spec) {
		return result{}, errors.New("these pages need upscaling and this worker has no upscaling engine: give the encode role to a worker that also upscales")
	}
	beat := w.beating(ctx, t, len(spec.Pages))
	defer beat()
	w.status.stage(t.ID, "fetching pages", 0, len(spec.Pages))

	shared := w.sees(spec)
	var (
		in      []downloads.PageFile
		workDir string
		bytesIn int64
	)
	if shared {
		workDir = spec.OutDir
		for _, p := range spec.Pages {
			in = append(in, downloads.PageFile{Name: p.Name, Path: p.Path, Format: p.Format, Width: p.Width, Height: p.Height})
		}
	} else {
		tmp, err := os.MkdirTemp("", "mangarr-process-*")
		if err != nil {
			return result{}, err
		}
		defer os.RemoveAll(tmp)
		data, err := w.input(ctx, t.ID)
		if err != nil {
			return result{}, err
		}
		bytesIn = int64(len(data))
		if in, err = unpackPages(data, spec, filepath.Join(tmp, "in")); err != nil {
			return result{}, fmt.Errorf("the pages: %w", err)
		}
		workDir = filepath.Join(tmp, "out")
		if err := os.MkdirAll(workDir, 0o775); err != nil {
			return result{}, err
		}
	}

	proc := processing.New(nil, w.enc)
	if w.up != nil {
		proc.Up = upscaling.NewFixed(&engine{srv: w.up, model: spec.UpscaleModel})
	}
	w.status.stage(t.ID, "processing", -1, -1)
	res, err := proc.Process(ctx, cfg, in, workDir)
	if err != nil {
		return result{Pages: 0, BytesIn: bytesIn}, err
	}
	out, files, err := describe(res, in, workDir)
	if err != nil {
		return result{}, err
	}
	data, err := json.Marshal(out)
	if err != nil {
		return result{}, err
	}
	w.status.stage(t.ID, "sending back", len(res.Pages), -1)
	if shared {
		if err := writeAtomic(filepath.Join(spec.OutDir, processing.ResultFile), data); err != nil {
			return result{}, err
		}
		return result{Pages: len(res.Pages), BytesOut: sizeOf(files, workDir)}, nil
	}
	pack, err := zipFiles(workDir, files, data)
	if err != nil {
		return result{}, err
	}
	if err := w.output(ctx, t.ID, pack); err != nil {
		return result{}, err
	}
	return result{Pages: len(res.Pages), BytesIn: bytesIn, BytesOut: int64(len(pack))}, nil
}

// sees reports whether this worker can work on a task's files where they
// are: it says it shares the server's storage, and the pages and the
// output folder are really there.
func (w *Worker) sees(spec processing.TaskSpec) bool {
	if !w.cfg.SharedStorage || spec.OutDir == "" {
		return false
	}
	for _, p := range spec.Pages {
		if _, err := os.Stat(p.Path); err != nil {
			w.log.Info("shared storage: a page isn't here, sending the pages over HTTP", "path", p.Path)
			return false
		}
	}
	if st, err := os.Stat(spec.OutDir); err != nil || !st.IsDir() {
		return false
	}
	return true
}

// needsUpscale reports whether any page is narrower than the profile wants.
func needsUpscale(spec processing.TaskSpec) bool {
	for _, p := range spec.Pages {
		if upscaling.NeedsUpscale(downloads.PageFile{Format: p.Format, Width: p.Width, Height: p.Height}, spec.Profile.Upscale.MinWidth) {
			return true
		}
	}
	return false
}

// unpackPages writes the pages of an input zip to dir, in the task's order.
func unpackPages(data []byte, spec processing.TaskSpec, dir string) ([]downloads.PageFile, error) {
	if err := os.MkdirAll(dir, 0o775); err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	byName := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		byName[f.Name] = f
	}
	out := make([]downloads.PageFile, len(spec.Pages))
	for i, p := range spec.Pages {
		name := processing.InputName(i, p)
		f, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("page %d is missing", i+1)
		}
		path := filepath.Join(dir, name)
		if err := extractTo(f, path); err != nil {
			return nil, err
		}
		out[i] = downloads.PageFile{Name: p.Name, Path: path, Format: p.Format, Width: p.Width, Height: p.Height}
	}
	return out, nil
}

func extractTo(f *zip.File, path string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// describe turns a processing result into what the server reads back: each
// page is either an input page left alone or a new file in workDir.
func describe(res downloads.ProcessResult, in []downloads.PageFile, workDir string) (processing.Result, []string, error) {
	out := processing.Result{SourcePages: res.SourcePages, Changed: res.Changed, ProcessedPages: res.ProcessedPages,
		Upscaled: res.Upscaled, UpscaleModel: res.UpscaleModel, Encoded: res.Encoded, Encoder: res.Encoder,
		UpscaleSeconds: res.UpscaleSeconds, EncodeSeconds: res.EncodeSeconds, Shrunk: res.Shrunk, Split: res.Split}
	if len(out.SourcePages) != len(res.Pages) {
		out.SourcePages = make([]int, len(res.Pages))
		for i := range out.SourcePages {
			out.SourcePages[i] = min(i, len(in)-1)
		}
	}
	byPath := make(map[string]int, len(in))
	for i, p := range in {
		byPath[p.Path] = i
	}
	var files []string
	for _, p := range res.Pages {
		rp := processing.ResultPage{Name: p.Name, Format: p.Format, Width: p.Width, Height: p.Height}
		if i, ok := byPath[p.Path]; ok {
			rp.Source = &i
		} else {
			rel, err := filepath.Rel(workDir, p.Path)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return out, nil, fmt.Errorf("page %s ended up outside the work folder", p.Name)
			}
			rp.File = filepath.ToSlash(rel)
			files = append(files, rp.File)
		}
		out.Pages = append(out.Pages, rp)
	}
	return out, files, nil
}

// zipFiles packs the new pages and the result for the server.
func zipFiles(dir string, files []string, result []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, r io.Reader) error {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			return err
		}
		_, err = io.Copy(f, r)
		return err
	}
	for _, name := range files {
		f, err := os.Open(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return nil, err
		}
		err = add(name, f)
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	if err := add(processing.ResultFile, bytes.NewReader(result)); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func sizeOf(files []string, dir string) int64 {
	var n int64
	for _, name := range files {
		if st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err == nil {
			n += st.Size()
		}
	}
	return n
}

// writeAtomic writes a file so a reader never sees half of it.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".part"
	if err := os.WriteFile(tmp, data, 0o664); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// engine is this machine's upscaler as the processing stage expects one.
type engine struct {
	srv *upscaler.Server
	// model replaces the profile's model when this machine has it (the
	// worker's model in System → Workers).
	model string
}

func (e *engine) Test(ctx context.Context) error { return nil }

func (e *engine) Info(ctx context.Context) (*upscale.Info, error) {
	in := e.srv.Info()
	out := &upscale.Info{Version: in.Version, Devices: in.Devices}
	for _, m := range in.Models {
		out.Models = append(out.Models, upscale.Model{Name: m.Name, Description: m.Description, Scales: m.Scales, NoiseLevels: m.NoiseLevels})
	}
	return out, nil
}

func (e *engine) Upscale(ctx context.Context, images []upscale.Image, p upscale.Params) ([]upscale.Image, error) {
	if e.model != "" && e.model != p.Model {
		for _, m := range e.srv.Info().Models {
			if m.Name == e.model {
				p.Model, p.Scale = m.Name, upscale.FitScale(p.Scale, m.Scales)
				break
			}
		}
	}
	in := make([]upscaler.Image, len(images))
	for i, img := range images {
		in[i] = upscaler.Image{Name: img.Name, Data: img.Data}
	}
	res, err := e.srv.Process(ctx, upscaler.Params{Model: p.Model, Scale: p.Scale, Noise: p.Noise, Format: p.Format, Quality: p.Quality, MaxWidth: p.MaxWidth}, in)
	if err != nil {
		return nil, err
	}
	upscale.ReportUsed(ctx, p.Model)
	out := make([]upscale.Image, len(res))
	for i, img := range res {
		out[i] = upscale.Image{Name: img.Name, Data: img.Data}
	}
	return out, nil
}
