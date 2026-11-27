package worker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/imagecheck"
	"github.com/Asion001/mangarr/internal/processing"
)

// A direct download (the server's library is on object storage) keeps the
// pages here, tells the server only their sizes, and then — as the server
// says — packages the chapter and uploads it straight to the bucket, or
// sends the pages to the server as before.

// direct reports whether a download task may be packaged here.
func direct(t Task) bool {
	v, _ := t.Spec[downloads.SpecDirect].(bool)
	return v
}

// keptPage is a fetched page waiting in the task's folder.
type keptPage struct {
	downloads.DirectPage
	Path string
}

// keepPage stores a fetched page in dir.
func keepPage(dir string, index int, data []byte) (keptPage, error) {
	info, err := imagecheck.Detect(data)
	if err != nil {
		return keptPage{}, fmt.Errorf("page %d: %w", index+1, err)
	}
	path := filepath.Join(dir, cbz.PageName(index, imagecheck.Ext(info.Format)))
	if err := os.WriteFile(path, data, 0o664); err != nil {
		return keptPage{}, err
	}
	return keptPage{DirectPage: downloads.DirectPage{Index: index, Format: info.Format, Width: info.Width, Height: info.Height, Size: int64(len(data))}, Path: path}, nil
}

// uploadClient sends archives to the bucket: no overall timeout, since a
// big chapter on a slow line takes what it takes.
var uploadClient = &http.Client{Transport: http.DefaultTransport}

// finishDirect hands a direct download's pages on, as the server decides.
func (w *Worker) finishDirect(ctx context.Context, t Task, dir string, pages []keptPage, res result) (result, error) {
	slices.SortFunc(pages, func(a, b keptPage) int { return a.Index - b.Index })
	stop := w.keepLease(ctx, t, len(pages))
	defer stop()

	described := make([]downloads.DirectPage, len(pages))
	var sizeBefore int64
	for i, p := range pages {
		described[i] = p.DirectPage
		sizeBefore += p.Size
	}
	var plan downloads.DirectPlan
	body := withRequests(map[string]any{"pages": described, "canProcess": slices.Contains(w.welcome.Roles, "encode")}, res.Requests)
	res.Requests = nil
	if err := w.call(ctx, http.MethodPost, fmt.Sprintf("/api/v1/worker/tasks/%d/pages", t.ID), body, &plan); err != nil {
		return res, fmt.Errorf("describing the pages: %w", err)
	}
	switch plan.Mode {
	case downloads.DirectStop:
		w.log.Info("the server ended a download", "task", t.ID, "reason", plan.Reason)
		res.Reported = true
		return res, nil
	case downloads.DirectUpload:
		w.status.stage(t.ID, "uploading", 0, len(pages))
		res.BytesOut = 0
		for i, p := range pages {
			data, err := os.ReadFile(p.Path)
			if err != nil {
				return res, err
			}
			if err := w.upload(ctx, t.ID, p.Index+1, data); err != nil {
				return res, fmt.Errorf("uploading page %d: %w", p.Index+1, err)
			}
			res.BytesOut += int64(len(data))
			w.status.stage(t.ID, "", i+1, -1)
		}
		return res, nil
	case downloads.DirectPackage:
	default:
		return res, fmt.Errorf("the server answered %q, which this worker doesn't know", plan.Mode)
	}

	byIndex := make(map[int]keptPage, len(pages))
	for _, p := range pages {
		byIndex[p.Index] = p
	}
	in := make([]downloads.PageFile, 0, len(plan.Keep))
	for _, idx := range plan.Keep {
		p, ok := byIndex[idx]
		if !ok {
			return res, fmt.Errorf("the server kept page %d, which isn't here", idx+1)
		}
		in = append(in, downloads.PageFile{Name: filepath.Base(p.Path), Path: p.Path, Format: p.Format, Width: p.Width, Height: p.Height})
	}
	out := downloads.DirectResult{SizeBefore: sizeBefore}
	final := in
	if plan.Process != nil {
		w.status.stage(t.ID, "processing", 0, len(in))
		workDir := filepath.Join(dir, "processed")
		if err := os.MkdirAll(workDir, 0o775); err != nil {
			return res, err
		}
		started := time.Now()
		pr, err := processing.New(nil, w.enc).Process(ctx, *plan.Process, in, workDir)
		switch {
		case err != nil && ctx.Err() != nil:
			return res, ctx.Err()
		case err != nil:
			// as on the server: the chapter comes in unprocessed, and the
			// background processing tries again later
			w.log.Warn("processing failed, packaging the original pages", "task", t.ID, "err", err)
		default:
			final = pr.Pages
			out.Processed, out.Changed, out.ProcessedPages = true, pr.Changed, pr.ProcessedPages
			out.Encoded, out.Encoder, out.Shrunk, out.Split, out.SourcePages = pr.Encoded, pr.Encoder, pr.Shrunk, pr.Split, pr.SourcePages
			out.Seconds = time.Since(started).Seconds()
			if pr.ProcessedPages == 0 {
				out.Seconds = 0
			}
		}
	}
	if plan.ComicInfo == nil {
		return res, errors.New("the server sent no ComicInfo")
	}
	ci := *plan.ComicInfo
	ci.PageCount = len(final)
	xml, err := ci.Marshal()
	if err != nil {
		return res, err
	}
	w.status.stage(t.ID, "packaging", 0, -1)
	archive := filepath.Join(dir, "chapter.cbz")
	cbzPages := make([]cbz.Page, len(final))
	for i, p := range final {
		cbzPages[i] = cbz.Page{Name: p.Name, Path: p.Path}
		out.Pages = append(out.Pages, downloads.DirectOutPage{Name: p.Name, Format: p.Format, Width: p.Width, Height: p.Height})
	}
	written, err := cbz.Write(archive, cbzPages, xml, 0o664, time.Now())
	if err != nil {
		return res, fmt.Errorf("packaging: %w", err)
	}
	out.Size, out.SHA256 = written.Size, written.SHA256

	w.status.stage(t.ID, "uploading", 0, -1)
	if err := putFile(ctx, plan.UploadURL, archive, written.Size); err != nil {
		return res, fmt.Errorf("uploading the chapter to the bucket: %w", err)
	}
	res.BytesOut = written.Size
	report := struct {
		downloads.DirectResult
		BytesIn int64 `json:"bytesIn"`
	}{out, res.BytesIn}
	if err := w.call(ctx, http.MethodPost, fmt.Sprintf("/api/v1/worker/tasks/%d/packaged", t.ID), report, nil); err != nil {
		return res, fmt.Errorf("the server didn't import the chapter: %w", err)
	}
	res.Pages, res.Reported = len(final), true
	return res, nil
}

// putFile uploads a file to a signed link in one PUT.
func putFile(ctx context.Context, url, path string, size int64) error {
	var last error
	for attempt := range 3 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt*5) * time.Second):
			}
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, f)
		if err != nil {
			f.Close()
			return err
		}
		req.ContentLength = size
		req.Header.Set("Content-Type", "application/vnd.comicbook+zip")
		resp, err := uploadClient.Do(req)
		f.Close()
		if err != nil {
			last = err
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		last = fmt.Errorf("the bucket answered %s", resp.Status)
		if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return last // a refused link won't work the next time either
		}
	}
	return last
}

// keepLease sends heartbeats while a direct download is processed and
// uploaded, which can take longer than a lease.
func (w *Worker) keepLease(ctx context.Context, t Task, pages int) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		every := time.Duration(max(w.welcome.LeaseSeconds/3, 10)) * time.Second
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				body := map[string]any{"pagesDone": pages, "pagesTotal": pages}
				if err := w.call(ctx, http.MethodPost, fmt.Sprintf("/api/v1/worker/tasks/%d/heartbeat", t.ID), body, nil); err != nil && ctx.Err() == nil {
					w.log.Warn("heartbeat didn't reach the server", "task", t.ID, "err", err)
				}
			}
		}
	}()
	return func() { cancel(); <-done }
}
