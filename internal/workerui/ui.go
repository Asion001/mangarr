package workerui

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/awake"
	"github.com/Asion001/mangarr/internal/upscaler"
	"github.com/Asion001/mangarr/internal/version"
	"github.com/Asion001/mangarr/internal/worker"
	"github.com/Asion001/mangarr/internal/workerupdate"
)

//go:embed index.html
var indexHTML []byte

// UI runs the worker for the page: it starts it from the saved settings,
// restarts it when they change and stops it on request.
type UI struct {
	store   *Store
	logs    *Logs
	log     *slog.Logger
	version string
	status  *worker.Status

	awake    awake.Keeper
	awakeErr string // why keeping awake failed, once

	mu      sync.Mutex
	gpuDir  string   // the upscalers folder gpuList was read from
	gpuList []string // the GPUs the upscalers see, numbered as -g takes them
	w       *worker.Worker
	cancel  context.CancelFunc
	done    chan struct{}
	problem string // why it isn't running, when that isn't the worker's own doing

	// Update puts the offered version's program in place of this one (nil:
	// this worker doesn't update itself). Skip is a version not to take.
	Update func(context.Context, workerupdate.Offer) error
	Skip   string
	// retry and retryAt hold off a build whose update just failed.
	retry   string
	retryAt time.Time
	// Connected is called whenever the worker has said hello.
	Connected func()
	updated   chan struct{}
	once      sync.Once
}

// New makes the UI; log should write through logs.Handler so the page sees it.
func New(store *Store, logs *Logs, log *slog.Logger, version string) *UI {
	return &UI{store: store, logs: logs, log: log, version: version, status: worker.NewStatus(), updated: make(chan struct{})}
}

// Updated is closed once a new version is in place: the program should
// restart into it.
func (u *UI) Updated() <-chan struct{} { return u.updated }

// Start starts the worker with the current settings. It reports a setting
// that keeps it from starting; the worker's own trouble (a server that
// doesn't answer, a refused key) shows in its status instead.
func (u *UI) Start() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.cancel != nil {
		return nil
	}
	cfg, err := worker.LoadConfig(u.store.Getenv)
	if err != nil {
		u.problem = err.Error()
		return err
	}
	cfg.Log, cfg.Version, cfg.Status = u.log, u.version, u.status
	cfg.Build, cfg.Commit = version.Build, version.Commit
	cfg.SelfUpdate, cfg.Skip, cfg.Connected = u.Update != nil, u.Skip, u.Connected
	cfg.Retry, cfg.RetryAt = u.retry, u.retryAt
	if engine, err := upscaler.LoadEngineConfig(u.store.Getenv); err == nil {
		cfg.Upscaler = upscaler.NewEngine(engine, u.log)
	} else {
		u.log.Warn("upscaling is off", "err", err)
	}
	w, err := worker.New(cfg)
	if err != nil {
		u.problem = err.Error()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	u.w, u.cancel, u.done, u.problem = w, cancel, done, ""
	go func() {
		defer close(done)
		err := w.Run(ctx)
		var update *worker.UpdateError
		switch {
		case errors.As(err, &update):
			err = u.update(ctx, update.Offer)
			if err == nil {
				return // restarting: the worker stays "updating" until then
			}
			u.log.Error("the update failed; carrying on with this build", "build", update.Offer.ID(), "err", err)
		case err != nil:
			u.log.Error("the worker stopped", "err", err)
		}
		u.mu.Lock()
		if u.done == done {
			u.cancel, u.done = nil, nil
		}
		u.mu.Unlock()
		cancel()
		if update != nil && ctx.Err() == nil {
			go func() { _ = u.Start() }()
		}
	}()
	return nil
}

// RetryUpdate is how long a failed update waits before it is tried again:
// the build's zip may not have been published yet.
const RetryUpdate = 15 * time.Minute

// update applies an update; a failed one waits RetryUpdate before the
// next try.
func (u *UI) update(ctx context.Context, o workerupdate.Offer) error {
	u.log.Info("updating", "to", o.ID(), "zip", o.URL)
	if err := u.Update(ctx, o); err != nil {
		u.mu.Lock()
		u.retry, u.retryAt = o.ID(), time.Now().Add(RetryUpdate)
		u.mu.Unlock()
		return err
	}
	u.log.Info("updated; restarting", "build", o.ID())
	u.once.Do(func() { close(u.updated) })
	return nil
}

// Stop stops the worker, handing back what it holds, and waits for it.
func (u *UI) Stop() {
	u.mu.Lock()
	cancel, done := u.cancel, u.done
	u.cancel, u.done = nil, nil
	u.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		u.log.Warn("the worker took too long to stop")
	}
}

func (u *UI) running() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.cancel != nil
}

// field is one setting as the page shows it.
type field struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Locked bool   `json:"locked"`
	// Set says a secret has a value (which the page is never sent).
	Set bool `json:"set,omitempty"`
}

type state struct {
	// Awake says the computer is kept from sleeping now.
	Awake    bool            `json:"awake"`
	AwakeErr string          `json:"awakeError,omitempty"`
	Version  string          `json:"version"`
	Platform string          `json:"platform"`
	Running  bool            `json:"running"`
	Problem  string          `json:"problem,omitempty"`
	Worker   worker.Snapshot `json:"worker"`
	Settings []field         `json:"settings"`
	// GPUs are the devices the upscalers see, in the order the GPU setting
	// numbers them.
	GPUs []string `json:"gpus"`
	File string   `json:"file"`
	Logs []Line   `json:"logs"`
}

func (u *UI) state(logSeq int64) state {
	u.mu.Lock()
	w, problem := u.w, u.problem
	u.mu.Unlock()
	snap := u.status.Snapshot(0)
	if w != nil {
		snap = w.Snapshot()
	}
	u.mu.Lock()
	awakeErr := u.awakeErr
	u.mu.Unlock()
	st := state{Awake: u.awake.Holding(), AwakeErr: awakeErr, Version: u.version, Platform: runtime.GOOS + "/" + runtime.GOARCH, Running: u.running(), Problem: problem,
		Worker: snap, File: u.store.Path(), Logs: u.logs.Since(logSeq)}
	st.GPUs = u.gpus()
	for _, name := range Fields {
		f := field{Name: name, Value: u.store.Getenv(name), Locked: u.store.Locked(name)}
		if f.Value == "" {
			f.Value = builtIn(name)
		}
		if name == "MANGARR_WORKER_KEY" {
			f.Set, f.Value = f.Value != "", ""
		}
		st.Settings = append(st.Settings, f)
	}
	return st
}

// gpus lists the GPUs in the current upscalers folder. Asking the tools
// takes a moment, so a new folder is read in the background and the page
// gets the list on a later poll.
func (u *UI) gpus() []string {
	dir := u.store.Getenv("MANGARR_UPSCALER_TOOLS_DIR")
	if dir == "" {
		dir = builtIn("MANGARR_UPSCALER_TOOLS_DIR")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.gpuDir != dir {
		u.gpuDir, u.gpuList = dir, nil
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			list := upscaler.CLIRunner{ToolsDir: dir}.Devices(ctx)
			u.mu.Lock()
			if u.gpuDir == dir {
				u.gpuList = list
			}
			u.mu.Unlock()
		}()
	}
	return append([]string{}, u.gpuList...)
}

// KeepAwake holds sleep off while the worker is switched on and connected
// (and MANGARR_WORKER_KEEP_AWAKE isn't false), until ctx ends.
func (u *UI) KeepAwake(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	defer u.awake.Release()
	for {
		u.mu.Lock()
		w := u.w
		u.mu.Unlock()
		on, _ := strconv.ParseBool(u.store.Getenv(KeepAwakeVar))
		if u.store.Getenv(KeepAwakeVar) == "" {
			on = true
		}
		want := on && u.running() && w != nil && w.Snapshot().State == worker.StateReady
		switch {
		case want && !u.awake.Holding():
			if err := u.awake.Hold(); err != nil {
				u.mu.Lock()
				if u.awakeErr == "" {
					u.log.Warn("could not keep this computer awake", "err", err)
				}
				u.awakeErr = err.Error()
				u.mu.Unlock()
			} else {
				u.log.Info("keeping this computer awake while the worker is switched on")
			}
		case !want && u.awake.Holding():
			u.awake.Release()
			u.log.Info("this computer may sleep again")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// KeepAwakeVar turns keeping the computer awake off ("false").
const KeepAwakeVar = "MANGARR_WORKER_KEEP_AWAKE"

// builtIn is the worker's own default for name, shown when nothing sets it.
func builtIn(name string) string {
	if name == KeepAwakeVar {
		return "true"
	}
	for _, vars := range [][][3]string{worker.Vars, upscaler.EngineVars} {
		for _, v := range vars {
			if v[0] == name {
				return v[2]
			}
		}
	}
	return ""
}

// Handler serves the page and its API.
func (u *UI) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(indexHTML)
	})
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		seq, _ := strconv.ParseInt(r.URL.Query().Get("logs"), 10, 64)
		writeJSON(w, u.state(seq))
	})
	mux.HandleFunc("POST /api/start", func(w http.ResponseWriter, r *http.Request) {
		if err := u.Start(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, u.state(-1))
	})
	mux.HandleFunc("POST /api/stop", func(w http.ResponseWriter, r *http.Request) {
		u.Stop()
		writeJSON(w, u.state(-1))
	})
	mux.HandleFunc("POST /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		vals := map[string]string{}
		for _, name := range Fields {
			v, ok := in[name]
			if !ok || u.store.Locked(name) {
				continue
			}
			v = strings.TrimSpace(v)
			if name == "MANGARR_WORKER_KEY" && v == "" {
				continue // left blank: keep the saved key
			}
			vals[name] = v
		}
		if err := u.store.Save(vals); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		u.log.Info("settings saved", "file", u.store.Path())
		u.Stop()
		if err := u.Start(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, u.state(-1))
	})
	return guard(mux)
}

// guard keeps the page to this machine. Only a localhost name is served
// (a DNS-rebinding page can't reach it under its own name), and a change
// needs a header a cross-site form can't send.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		switch strings.Trim(host, "[]") {
		case "127.0.0.1", "localhost", "::1":
		default:
			http.Error(w, "this page is only served to this computer", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-Mangarr-Worker") != "1" {
			http.Error(w, "missing X-Mangarr-Worker header", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

// Serve listens on addr. A port already taken by another copy of this
// worker is reported as ErrRunning, so the caller can open that one.
func Serve(ctx context.Context, addr string, h http.Handler) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if isInUse(err) {
			return nil, ErrRunning
		}
		return nil, err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	go func() { _ = srv.Serve(ln) }()
	return ln, nil
}

// ErrRunning says the page's port is taken, most likely by this worker
// started a second time.
var ErrRunning = errors.New("the status page's port is in use")

func isInUse(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "address already in use") || strings.Contains(s, "only one usage of each socket address")
}

// OpenBrowser opens url in the default browser, best effort.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
