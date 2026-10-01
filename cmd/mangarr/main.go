// Command mangarr is a Sonarr-style PVR for manga.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/app"
	"github.com/Asion001/mangarr/internal/config"
	"github.com/Asion001/mangarr/internal/envcfg"
	"github.com/Asion001/mangarr/internal/imageenc"
	"github.com/Asion001/mangarr/internal/logging"
	_ "github.com/Asion001/mangarr/internal/modules/all"
	"github.com/Asion001/mangarr/internal/upscaler"
	"github.com/Asion001/mangarr/internal/version"
	"github.com/Asion001/mangarr/internal/worker"
)

func main() {
	if mode, err := config.ModeFromEnv(); err == nil {
		switch mode {
		case config.ModeWorker:
			os.Exit(runWorker())
		case config.ModeUpscaler:
			// the old processing node: it is a worker now, and needs a key
			fmt.Fprintln(os.Stderr, "MANGARR_MODE=upscaler is deprecated: use MANGARR_MODE=worker with a key from System → Workers")
			os.Exit(runWorker())
		}
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Println(version.Version, "build", version.Build, version.Commit)
			return
		case "openapi":
			if err := dumpOpenAPI(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "healthcheck":
			os.Exit(healthcheck())
		case "bench":
			os.Exit(bench(os.Args[2:]))
		case "env":
			if len(os.Args) > 2 && os.Args[2] == "--markdown" {
				envcfg.WriteMarkdown(os.Stdout)
			} else {
				envcfg.WriteText(os.Stdout, config.Environ())
			}
			return
		}
	}
	if err := run(); err != nil {
		if errors.Is(err, errRestart) {
			reexec()
		}
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

// errRestart ends run when the app asked to restart (e.g. after moving the
// database).
var errRestart = errors.New("restart requested")

// reexec replaces the process with a fresh copy of itself. If that isn't
// possible it exits, and the container's restart policy starts it again.
func reexec() {
	exe, err := os.Executable()
	if err == nil {
		err = syscall.Exec(exe, os.Args, os.Environ())
	}
	fmt.Fprintln(os.Stderr, "restart: couldn't re-execute, exiting for the restart policy:", err)
	os.Exit(3)
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	var out io.Writer = os.Stdout
	if cfg.LogDir != "" {
		rot, err := logging.OpenRotating(cfg.LogDir, "mangarr", 5<<20, cfg.LogFiles)
		if err != nil {
			fmt.Fprintln(os.Stderr, "log files disabled:", err)
			cfg.LogDir = ""
		} else {
			defer rot.Close()
			out = io.MultiWriter(os.Stdout, rot)
			keepCrashes(cfg.LogDir)
		}
	}
	log, ring := logging.Setup(cfg.LogLevel, out)
	log.Info("starting mangarr", "version", version.Version, "build", version.Build, "commit", version.Commit, "data", cfg.DataDir, "listen", cfg.Listen)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx, cfg, log, ring)
	if err != nil {
		return err
	}
	defer a.Close()
	if err := a.Start(ctx); err != nil {
		return err
	}

	// Request contexts derive from ctx so long-lived SSE streams end on SIGTERM
	// instead of holding Shutdown until its timeout.
	// h2c: a reverse proxy in front (which is where TLS lives) can then speak
	// HTTP/2 to us and multiplex a reader's page requests over one connection.
	srv := &http.Server{Addr: cfg.Listen, Handler: h2c.NewHandler(api.New(a), &http2.Server{}), ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx }}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("http server listening", "addr", cfg.Listen)

	restart := false
	select {
	case <-ctx.Done():
	case <-a.RestartRequested():
		restart = true
		stop() // end SSE streams and background work
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !restart {
		return err
	}
	if restart {
		return errRestart
	}
	return nil
}

// keepCrashes copies what the Go runtime prints when the process dies (a
// panic, or running out of memory) into crash.txt next to the logs: it goes
// to stderr only, which the log files and the diagnostics bundle never see.
func keepCrashes(dir string) {
	f, err := os.OpenFile(filepath.Join(dir, "crash.txt"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close() // SetCrashOutput keeps its own copy
	_ = debug.SetCrashOutput(f, debug.CrashOptions{})
}

// dumpOpenAPI prints the OpenAPI document (used to generate web client types).
func dumpOpenAPI() error {
	// the schema is committed and CI regenerates it with a plain build, so it
	// must not carry whatever version this binary was stamped with
	version.Version = "dev"
	dir, err := os.MkdirTemp("", "mangarr-openapi-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	os.Setenv("MANGARR_DATA_DIR", dir)
	os.Setenv("MANGARR_DB", "sqlite://"+dir+"/openapi.db")
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log, ring := logging.Setup("error", os.Stderr)
	a, err := app.New(context.Background(), cfg, log, ring)
	if err != nil {
		return err
	}
	defer a.Close()
	h := api.New(a)
	rec := &captureWriter{header: http.Header{}}
	req, _ := http.NewRequest(http.MethodGet, "/api/openapi.json", nil)
	req.Header.Set("X-Api-Key", mustAPIKey(a))
	h.ServeHTTP(rec, req)
	if rec.status != 0 && rec.status != 200 {
		return fmt.Errorf("openapi: status %d", rec.status)
	}
	var out bytes.Buffer
	if err := json.Indent(&out, rec.body, "", "  "); err != nil {
		return err
	}
	out.WriteByte('\n')
	_, err = os.Stdout.Write(out.Bytes())
	return err
}

func mustAPIKey(a *app.App) string {
	g, err := a.Settings.General(context.Background())
	if err != nil {
		panic(err)
	}
	return g.APIKey
}

type captureWriter struct {
	header http.Header
	body   []byte
	status int
}

func (c *captureWriter) Header() http.Header { return c.header }
func (c *captureWriter) Write(b []byte) (int, error) {
	c.body = append(c.body, b...)
	return len(b), nil
}
func (c *captureWriter) WriteHeader(s int) { c.status = s }

// healthcheck is used by the Docker HEALTHCHECK (distroless has no curl).
func healthcheck() int {
	addr := os.Getenv("MANGARR_LISTEN")
	if addr == "" {
		addr = ":8787"
	}
	base := os.Getenv("MANGARR_URL_BASE")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://127.0.0.1" + addr + base + "/ping")
	if err != nil || resp.StatusCode != 200 {
		return 1
	}
	return 0
}

// bench runs `mangarr bench encode <chapter.cbz> [pages]`.
func bench(args []string) int {
	if len(args) < 2 || args[0] != "encode" {
		fmt.Fprintln(os.Stderr, "usage: mangarr bench encode <chapter.cbz> [max pages]")
		return 2
	}
	maxPages := 10
	if len(args) > 2 {
		if n, err := strconv.Atoi(args[2]); err == nil {
			maxPages = n
		}
	}
	enc := imageenc.Detect()
	formats := []string{"avif"}
	if _, ok := enc.Engine("jxl"); ok {
		formats = append(formats, "jxl")
	}
	if err := enc.Bench(context.Background(), args[1], formats, maxPages, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// runWorker runs this process as a worker (MANGARR_MODE=worker): it asks a
// server for tasks and does them. There is no listener, so "healthcheck"
// only says whether the process is up.
func runWorker() int {
	cfg, err := worker.LoadConfig(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck":
			return 0
		case "version":
			fmt.Println(version.Version, "build", version.Build, version.Commit)
			return 0
		}
	}
	log, _ := logging.Setup(os.Getenv("MANGARR_LOG_LEVEL"), os.Stdout)
	cfg.Log, cfg.Version = log, version.Version
	if limit := worker.SetMemoryLimit(os.Getenv); limit > 0 {
		log.Info("go heap limited to half the container's memory", "mb", limit>>20)
	}
	// the upscaling engine is the same one the old node ran, configured the
	// same way; a worker without the tools simply doesn't offer to upscale
	if engine, err := upscaler.LoadEngineConfig(os.Getenv); err == nil {
		cfg.Upscaler = upscaler.NewEngine(engine, log)
	}
	w, err := worker.New(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := w.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		return 1
	}
	return 0
}
