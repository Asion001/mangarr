package worker

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Asion001/mangarr/internal/upscaler"
)

// upscale runs one batch of pages through the upscaling engine on this
// machine: fetch the batch, run it, send the result back. The server
// decided which pages need it and at what scale, so there is nothing to
// work out here.
func (w *Worker) upscale(ctx context.Context, t Task) (result, error) {
	if w.up == nil {
		return result{}, errors.New("this worker has no upscaling engine (is MANGARR_UPSCALER_TOOLS_DIR set?)")
	}
	var params upscaler.Params
	if raw, ok := t.Spec["params"]; ok {
		data, err := json.Marshal(raw)
		if err != nil {
			return result{}, err
		}
		if err := json.Unmarshal(data, &params); err != nil {
			return result{}, fmt.Errorf("the upscaling parameters make no sense: %w", err)
		}
	}
	inPath, _ := t.Spec["input"].(string)
	outPath, _ := t.Spec["output"].(string)
	shared := w.cfg.SharedStorage && inPath != "" && outPath != ""
	var in []byte
	var err error
	w.status.stage(t.ID, "fetching pages", -1, -1)
	if shared {
		// the batch is on the volume this worker shares with the server
		if in, err = os.ReadFile(inPath); err != nil {
			w.log.Info("shared storage: the batch isn't here, fetching it over HTTP", "path", inPath)
			shared = false
		}
	}
	if !shared {
		if in, err = w.input(ctx, t.ID); err != nil {
			return result{}, err
		}
	}
	images, err := unzip(in)
	if err != nil {
		return result{}, fmt.Errorf("the batch: %w", err)
	}
	if len(images) == 0 {
		return result{}, errors.New("the batch has no pages")
	}
	beat := w.beating(ctx, t, len(images))
	defer beat()

	w.status.stage(t.ID, "upscaling", 0, len(images))
	out, gpu, err := w.up.ProcessDevice(ctx, params, images)
	if err != nil {
		return result{Pages: 0, BytesIn: int64(len(in))}, err
	}
	data, err := zipImages(out)
	if err != nil {
		return result{}, err
	}
	w.status.stage(t.ID, "sending back", len(out), -1)
	if shared {
		if err := writeAtomic(outPath, data); err != nil {
			return result{}, err
		}
	} else if err := w.output(ctx, t.ID, data); err != nil {
		return result{}, err
	}
	return result{Pages: len(out), BytesIn: int64(len(in)), BytesOut: int64(len(data)), GPU: gpu}, nil
}

// beating keeps a task's lease while something slow runs, and stops when
// the returned function is called.
func (w *Worker) beating(ctx context.Context, t Task, pages int) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		every := max(w.welcome.LeaseSeconds/3, 10)
		tick := time.NewTicker(time.Duration(every) * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				var out struct {
					Cancel bool `json:"cancel"`
				}
				err := w.call(ctx, http.MethodPost, fmt.Sprintf("/api/v1/worker/tasks/%d/heartbeat", t.ID),
					map[string]any{"pagesDone": 0, "pagesTotal": pages, "bytesIn": 0, "bytesOut": 0}, &out)
				if err != nil {
					// a lost heartbeat loses the task when the lease runs out,
					// so it is worth a line in the log
					w.log.Warn("heartbeat didn't reach the server", "task", t.ID, "err", err)
				}
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

// input downloads what a task works on.
func (w *Worker) input(ctx context.Context, taskID int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/v1/worker/tasks/%d/input", w.cfg.ServerURL, taskID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", w.cfg.Key)
	resp, err := w.cfg.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return nil, &httpError{Status: resp.StatusCode, Body: string(bytes.TrimSpace(msg))}
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<30))
}

// output sends back what it produced.
func (w *Worker) output(ctx context.Context, taskID int64, data []byte) error {
	chunkBytes := w.welcome.OutputChunkBytes
	if chunkBytes <= 0 { // a server from before chunked uploads
		chunkBytes = max(len(data), 1)
	}
	chunks := max((len(data)+chunkBytes-1)/chunkBytes, 1)
	for i := 0; i < chunks; i++ {
		start := i * chunkBytes
		end := min(start+chunkBytes, len(data))
		if err := w.outputChunk(ctx, taskID, i+1, chunks, data[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) outputChunk(ctx context.Context, taskID int64, chunk, chunks int, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/api/v1/worker/tasks/%d/output", w.cfg.ServerURL, taskID), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", w.cfg.Key)
	req.Header.Set("Content-Type", "application/zip")
	req.Header.Set("X-Mangarr-Chunk", strconv.Itoa(chunk))
	req.Header.Set("X-Mangarr-Chunks", strconv.Itoa(chunks))
	resp, err := w.cfg.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return &httpError{Status: resp.StatusCode, Body: string(bytes.TrimSpace(msg))}
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// unzip reads a batch of pages.
func unzip(data []byte) ([]upscaler.Image, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	out := make([]upscaler.Image, 0, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, upscaler.Image{Name: f.Name, Data: b})
	}
	return out, nil
}

// zipImages packs the results.
func zipImages(images []upscaler.Image) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, img := range images {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: img.Name, Method: zip.Store})
		if err != nil {
			return nil, err
		}
		if _, err := f.Write(img.Data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
