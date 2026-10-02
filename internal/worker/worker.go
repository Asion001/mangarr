// Package worker is mangarr running as a worker: it holds a key, dials the
// server, asks for tasks and does them. It listens on nothing — the server
// never connects to a worker, which is what makes a worker behind someone
// else's NAT (or on a different continent) as easy as one in the same rack.
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Asion001/mangarr/internal/imageenc"
	"github.com/Asion001/mangarr/internal/upscaler"
	"github.com/Asion001/mangarr/internal/workerupdate"
	"github.com/Asion001/mangarr/internal/worktasks"
)

// Config is what a worker is started with.
type Config struct {
	// ServerURL is the mangarr server, e.g. http://mangarr:8989.
	ServerURL string
	// Key is this worker's own key (mgw_...).
	Key string
	// Roles it is willing to do; the server narrows this to what it may.
	Roles []string
	// Version, Build and Commit name this build: they are shown in System →
	// Workers, and the server compares them with its own to offer updates.
	Version string
	Build   string
	Commit  string
	// Concurrent is how many tasks it takes at once (0: what the server says).
	Concurrent int
	// Prefetch is how many pages it fetches ahead of its uploads (0: what
	// the server says).
	Prefetch int
	// PageConcurrency is how many pages it fetches at a time when the server
	// leaves it to the worker (0: 4).
	PageConcurrency int
	// SharedStorage says this worker sees the server's data folder at the
	// same path, so it works on the files there instead of trading them
	// over HTTP.
	SharedStorage bool
	// Upscaler is the engine this machine upscales with (nil: it can't).
	Upscaler *upscaler.Server
	Log      *slog.Logger
	// HTTP talks to the server; Fetch talks to the manga sites (they are
	// separate so a proxy can be put in front of one and not the other).
	HTTP  *http.Client
	Fetch *http.Client
	// Status keeps what it is doing for a status page (nil: nothing is kept).
	Status *Status
	// AutoUpdate lets it move to the server's version when the server is
	// newer (MANGARR_WORKER_AUTO_UPDATE).
	AutoUpdate bool
	// SelfUpdate says whoever runs the worker can replace its program: Run
	// then stops for an update with an *UpdateError. Without it (a
	// container) the worker only says an update is waiting.
	SelfUpdate bool
	// Skip is a build (Offer.ID) not to move to: one that was rolled back
	// here.
	Skip string
	// Retry holds off a build whose update just failed (its zip may not be
	// published yet) until RetryAt.
	Retry   string
	RetryAt time.Time
	// Connected is called after every successful hello.
	Connected func()
}

// UpdateError is what Run returns when it stopped to move to the server's
// version: it took no new work, finished what it held and said goodbye.
type UpdateError struct{ Offer workerupdate.Offer }

func (e *UpdateError) Error() string { return "stopped to update to " + e.Offer.ID() }

// Worker is the running worker.
type Worker struct {
	cfg     Config
	log     *slog.Logger
	welcome Welcome

	// concurrent and pages are the limits the server gave last: they come
	// with every lease, so a change in System → Workers applies without a
	// restart.
	concurrent atomic.Int64
	pages      atomic.Int64

	// up is the upscaling engine on this machine (nil when it has none).
	up *upscaler.Server
	// enc re-encodes pages for the processing stage.
	enc *imageenc.Encoder

	mu   sync.Mutex
	held map[int64]bool // tasks in progress, for a clean goodbye
	// told is the newer version already logged as waiting.
	told string

	// update is the version it stops to move to, once offered.
	update atomic.Pointer[workerupdate.Offer]

	status *Status
}

// Welcome is what the server tells a worker at hello.
type Welcome struct {
	WorkerID         int64    `json:"workerId"`
	Name             string   `json:"name"`
	Roles            []string `json:"roles"`
	LeaseSeconds     int      `json:"leaseSeconds"`
	PollSeconds      int      `json:"pollSeconds"`
	Prefetch         int      `json:"prefetch"`
	Concurrent       int      `json:"concurrent"`
	PageConcurrency  int      `json:"pageConcurrency"`
	OutputChunkBytes int      `json:"outputChunkBytes"`
	ServerTime       string   `json:"serverTime"`
	// Update is the server's version when this worker should move to it.
	Update *workerupdate.Offer `json:"update,omitempty"`
}

// Task is one piece of work, as the server hands it over.
type Task struct {
	ID         int64          `json:"id"`
	JobID      int64          `json:"jobId"`
	Kind       string         `json:"kind"`
	Spec       map[string]any `json:"spec"`
	PagesTotal int            `json:"pagesTotal"`
	// Label names the chapter, for the status page (set from the lease).
	Label string `json:"-"`
}

// PageSpec is one page to fetch, with the request the site expects.
type PageSpec struct {
	Index   int               `json:"index"`
	Name    string            `json:"name"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// New makes a worker from its configuration.
func New(cfg Config) (*Worker, error) {
	if cfg.ServerURL == "" {
		return nil, errors.New("a worker needs the server's address (MANGARR_SERVER_URL)")
	}
	if cfg.Key == "" {
		return nil, errors.New("a worker needs its own key (MANGARR_WORKER_KEY)")
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 10 * time.Minute}
	}
	if cfg.Fetch == nil {
		cfg.Fetch = &http.Client{Timeout: 2 * time.Minute}
	}
	cfg.ServerURL = strings.TrimRight(cfg.ServerURL, "/")
	w := &Worker{cfg: cfg, log: cfg.Log, held: map[int64]bool{}, enc: imageenc.Detect(), status: cfg.Status}
	if slices.Contains(cfg.Roles, "upscale") && cfg.Upscaler != nil {
		w.up = cfg.Upscaler
	}
	return w, nil
}

// Run says hello and then takes tasks until the context ends.
func (w *Worker) Run(ctx context.Context) error {
	w.status.Set(StateStarting, nil)
	if err := w.hello(ctx); err != nil {
		if ctx.Err() != nil {
			w.status.Set(StateStopped, nil)
			return nil
		}
		w.status.Set(StateFailed, err)
		return err
	}
	w.log.Info("worker ready", "name", w.welcome.Name, "roles", w.welcome.Roles, "server", w.cfg.ServerURL)
	var (
		wg     sync.WaitGroup
		active atomic.Int64
		freed  = make(chan struct{}, 1)
	)
	for {
		if ctx.Err() != nil {
			wg.Wait()
			w.bye()
			w.status.Set(StateStopped, nil)
			return nil
		}
		if o := w.update.Load(); o != nil {
			// take nothing new; what it holds is finished first, so an
			// update never costs a chapter
			if w.status.State() != StateUpdating {
				w.log.Info("the server runs a later build: finishing the tasks in hand, then updating",
					"build", o.ID(), "tasks", active.Load())
				w.status.Set(StateUpdating, nil)
			}
			wg.Wait()
			if ctx.Err() != nil {
				continue
			}
			w.bye()
			return &UpdateError{Offer: *o}
		}
		if active.Load() >= int64(w.taskLimit()) {
			select {
			case <-ctx.Done():
			case <-freed:
			case <-time.After(10 * time.Second):
			}
			continue
		}
		task, err := w.lease(ctx)
		if err != nil {
			if ctx.Err() != nil {
				continue
			}
			wait := 10 * time.Second
			if switchedOff(err) {
				if w.status.State() != StateOff {
					w.log.Info("switched off in System → Workers; waiting to be switched on")
				}
				w.status.Set(StateOff, nil)
				wait = offPoll
			} else {
				w.log.Warn("could not ask for work", "err", err)
				w.status.Set(StateWaiting, err)
			}
			select {
			case <-ctx.Done():
			case <-time.After(wait):
			}
			continue
		}
		switch w.status.State() {
		case StateOff:
			w.log.Info("switched on again")
			w.status.Set(StateReady, nil)
		case StateWaiting:
			w.status.Set(StateReady, nil)
		}
		if task == nil {
			continue
		}
		active.Add(1)
		wg.Add(1)
		go func(t Task) {
			defer wg.Done()
			defer func() {
				active.Add(-1)
				select {
				case freed <- struct{}{}:
				default:
				}
			}()
			w.do(ctx, t)
		}(*task)
	}
}

// Snapshot is what the worker is doing now, for a status page.
func (w *Worker) Snapshot() Snapshot { return w.status.Snapshot(w.taskLimit()) }

// taskLimit is how many tasks it takes at once: its own setting, or what the
// server said last.
func (w *Worker) taskLimit() int {
	if w.cfg.Concurrent > 0 {
		return w.cfg.Concurrent
	}
	return max(int(w.concurrent.Load()), 1)
}

// pageLimit is how many pages a download fetches at a time: what System →
// Workers says for this worker, or its own setting when that is left at 0.
func (w *Worker) pageLimit() int {
	if n := int(w.pages.Load()); n > 0 {
		return n
	}
	if w.cfg.PageConcurrency > 0 {
		return w.cfg.PageConcurrency
	}
	return 4
}

// hello announces the worker, retrying until the server answers: a worker
// that starts before its server should wait for it, not give up.
func (w *Worker) hello(ctx context.Context) error {
	info := map[string]any{"cpus": runtime.NumCPU(), worktasks.InfoProcess: true, "sharedStorage": w.cfg.SharedStorage}
	roles := w.cfg.Roles
	var models, devices []string
	if w.up != nil {
		up := w.up.Info()
		for _, m := range up.Models {
			models = append(models, m.Name)
		}
		devices = up.Devices
		info["models"], info["devices"], info["formats"] = up.Models, up.Devices, up.Formats
		if len(up.Models) == 0 {
			// no engine on this machine: don't offer to upscale
			roles = without(roles, "upscale")
			w.up = nil
		}
	} else {
		roles = without(roles, "upscale")
	}
	body := map[string]any{
		"version": w.cfg.Version, "build": w.cfg.Build, "commit": w.cfg.Commit, "platform": runtime.GOOS + "/" + runtime.GOARCH,
		"roles": roles,
		"info":  info,
	}
	wait := time.Second
	for {
		var out Welcome
		err := w.call(ctx, http.MethodPost, "/api/v1/worker/hello", body, &out)
		if err == nil {
			if len(out.Roles) == 0 {
				return fmt.Errorf("this worker has no roles it may do here (it asked for %v)", w.cfg.Roles)
			}
			w.welcome = out
			w.concurrent.Store(int64(out.Concurrent))
			w.pages.Store(int64(out.PageConcurrency))
			w.status.welcome(out, models, devices)
			if w.cfg.Connected != nil {
				w.cfg.Connected()
			}
			w.offered(out.Update)
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if switchedOff(err) {
			// not refused, just off for now: ask again now and then
			w.log.Info("switched off in System → Workers; waiting to be switched on")
			w.status.Set(StateOff, nil)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(offPoll):
			}
			continue
		}
		var he *httpError
		if errors.As(err, &he) && (he.Status == http.StatusUnauthorized || he.Status == http.StatusForbidden) {
			return fmt.Errorf("the server refused this worker's key: %w", err)
		}
		w.log.Warn("waiting for the server", "err", err, "retry", wait)
		w.status.Set(StateWaiting, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		if wait < time.Minute {
			wait *= 2
		}
	}
}

// lease asks for one task. The call itself waits at the server for a while,
// so an idle worker makes one request every half minute or so.
func (w *Worker) lease(ctx context.Context) (*Task, error) {
	var out struct {
		Task            *Task  `json:"task"`
		Label           string `json:"label"`
		Concurrent      *int   `json:"concurrent"`
		PageConcurrency *int   `json:"pageConcurrency"`
		// Update says the server moved to a newer version.
		Update *workerupdate.Offer `json:"update"`
	}
	if err := w.call(ctx, http.MethodPost, "/api/v1/worker/lease", map[string]any{"kinds": w.welcome.Roles}, &out); err != nil {
		return nil, err
	}
	// an older server says nothing about limits: keep the ones from hello
	if out.Concurrent != nil {
		w.concurrent.Store(int64(*out.Concurrent))
	}
	if out.PageConcurrency != nil {
		w.pages.Store(int64(*out.PageConcurrency))
	}
	if out.Task != nil {
		out.Task.Label = out.Label
	}
	w.offered(out.Update)
	return out.Task, nil
}

// offered takes in what the server said about its version: an update this
// worker can apply makes it stop taking work; one it can't is logged once
// and shown on the status page.
func (w *Worker) offered(o *workerupdate.Offer) {
	if o == nil {
		w.status.SetUpdate("")
		return
	}
	id := o.ID()
	w.status.SetUpdate(id)
	if w.update.Load() != nil {
		return
	}
	held := id == w.cfg.Retry && time.Now().Before(w.cfg.RetryAt)
	if w.cfg.SelfUpdate && w.cfg.AutoUpdate && o.URL != "" && id != w.cfg.Skip && !held {
		w.update.Store(o)
		return
	}
	w.mu.Lock()
	first := w.told != id
	w.told = id
	w.mu.Unlock()
	if !first {
		return
	}
	switch {
	case !w.cfg.SelfUpdate:
		w.log.Warn("the server runs a later build; update this worker's image", "build", id, "image", o.Image)
	case id == w.cfg.Skip:
		w.log.Warn("the server runs a later build, which didn't start here before; update this worker by hand", "build", id)
	case held:
		w.log.Info("the update to the server's build failed; trying again later", "build", id, "at", w.cfg.RetryAt.Format(time.Kitchen))
	case o.URL == "":
		w.log.Warn("the server runs a later build, with no worker zip for this platform; update this worker by hand", "build", id)
	default:
		w.log.Info("the server runs a later build; automatic updates are off (MANGARR_WORKER_AUTO_UPDATE)", "build", id)
	}
}

// do performs one task and tells the server how it went.
func (w *Worker) do(ctx context.Context, t Task) {
	w.mu.Lock()
	w.held[t.ID] = true
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.held, t.ID)
		w.mu.Unlock()
	}()

	started := time.Now()
	var err error
	var res result
	w.status.begin(t)
	defer func() {
		if ctx.Err() == nil {
			w.status.end(t, res, started, err)
		}
	}()
	switch t.Kind {
	case "download":
		res, err = w.download(ctx, t)
	case "upscale":
		res, err = w.upscale(ctx, t)
	case "encode":
		res, err = w.process(ctx, t)
	default:
		err = fmt.Errorf("this worker doesn't know how to %q", t.Kind)
	}
	if err != nil {
		if ctx.Err() != nil {
			return // shutting down: the lease runs out and someone else takes it
		}
		w.log.Warn("task failed", "task", t.ID, "kind", t.Kind, "err", err)
		_ = w.call(context.WithoutCancel(ctx), http.MethodPost, fmt.Sprintf("/api/v1/worker/tasks/%d/fail", t.ID),
			map[string]any{"reason": err.Error(), "pages": res.Pages}, nil)
		return
	}
	if err = w.call(ctx, http.MethodPost, fmt.Sprintf("/api/v1/worker/tasks/%d/complete", t.ID),
		map[string]any{"pages": res.Pages, "bytesIn": res.BytesIn, "bytesOut": res.BytesOut, "gpu": res.GPU}, nil); err != nil {
		w.log.Warn("could not report a finished task", "task", t.ID, "err", err)
		err = fmt.Errorf("could not report it: %w", err)
		return
	}
	w.log.Info("task done", "task", t.ID, "kind", t.Kind, "pages", res.Pages,
		"mb", fmt.Sprintf("%.1f", float64(res.BytesIn)/(1<<20)), "seconds", fmt.Sprintf("%.1f", time.Since(started).Seconds()))
}

// result is what a task did.
type result struct {
	Pages    int
	BytesIn  int64
	BytesOut int64
	GPU      string
}

// bye hands back whatever this worker still holds, so a restart doesn't
// leave chapters waiting for a lease to run out.
func (w *Worker) bye() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	w.mu.Lock()
	ids := make([]int64, 0, len(w.held))
	for id := range w.held {
		ids = append(ids, id)
	}
	w.mu.Unlock()
	if err := w.call(ctx, http.MethodPost, "/api/v1/worker/bye", map[string]any{"taskIds": ids}, nil); err != nil {
		w.log.Debug("goodbye didn't reach the server", "err", err)
	}
}

// ---- talking to the server --------------------------------------------------

// httpError is a server answer that isn't a success.
type httpError struct {
	Status int
	Body   string
}

func (e *httpError) Error() string { return fmt.Sprintf("the server said %d: %s", e.Status, e.Body) }

// offPoll is how often a worker that is switched off asks again.
var offPoll = 30 * time.Second

// switchedOff reports whether the server says this worker is switched off
// (System → Workers), which it waits out rather than giving up.
func switchedOff(err error) bool {
	var he *httpError
	return errors.As(err, &he) && he.Status == http.StatusForbidden && strings.Contains(he.Body, "switched off")
}

// gone reports whether the server has taken the task away (409): the worker
// drops what it was doing and asks for something else.
func gone(err error) bool {
	var he *httpError
	return errors.As(err, &he) && he.Status == http.StatusConflict
}

func (w *Worker) call(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, w.cfg.ServerURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", w.cfg.Key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := w.cfg.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return &httpError{Status: resp.StatusCode, Body: strings.TrimSpace(string(msg))}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// upload sends one finished page.
func (w *Worker) upload(ctx context.Context, taskID int64, number int, data []byte) error {
	url := fmt.Sprintf("%s/api/v1/worker/tasks/%d/pages/%d", w.cfg.ServerURL, taskID, number)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", w.cfg.Key)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := w.cfg.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return &httpError{Status: resp.StatusCode, Body: strings.TrimSpace(string(msg))}
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// without is a role list with one role left out.
func without(roles []string, role string) []string {
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		if r != role {
			out = append(out, r)
		}
	}
	return out
}
