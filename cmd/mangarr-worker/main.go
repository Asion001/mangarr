// Command mangarr-worker runs a worker, the same as `mangarr` with
// MANGARR_MODE=worker, in a build without the server: a Windows exe next to
// the ncnn upscalers on a gaming PC, say. It asks a mangarr server for work
// and needs no inbound access of its own.
//
// Started as is, it serves a status page on http://127.0.0.1:8790 and opens
// it in the browser: the server address and key are entered there, and it
// shows what the worker is doing. With -headless it runs from the
// environment alone, as the container does.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"time"

	"github.com/Asion001/mangarr/internal/upscaler"
	"github.com/Asion001/mangarr/internal/version"
	"github.com/Asion001/mangarr/internal/worker"
	"github.com/Asion001/mangarr/internal/workerui"
	"github.com/Asion001/mangarr/internal/workerupdate"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck": // a worker listens on nothing: being up is being well
			return
		case "version":
			fmt.Println(version.Version, "build", version.Build, version.Commit)
			return
		}
	}
	headless := flag.Bool("headless", false, "run from the environment only, without the status page")
	listen := flag.String("listen", envOr("MANGARR_WORKER_UI_LISTEN", "127.0.0.1:8790"), "where the status page listens")
	noBrowser := flag.Bool("no-browser", false, "don't open the status page in the browser")
	flag.Parse()
	settle()
	useBundledEncoders()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *headless {
		os.Exit(runHeadless(ctx))
	}
	os.Exit(runWithUI(ctx, *listen, !*noBrowser))
}

// runHeadless is the worker as it always ran: configured by environment
// variables, logging to stdout.
func runHeadless(ctx context.Context) int {
	getenv := withDefaults(os.Getenv)
	cfg, err := worker.LoadConfig(getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg.Log, cfg.Version = log, version.Version
	if engine, err := upscaler.LoadEngineConfig(getenv); err == nil {
		cfg.Upscaler = upscaler.NewEngine(engine, log)
	}
	exe, canUpdate := updatable()
	cfg.SelfUpdate, cfg.Skip = canUpdate, workerupdate.Skipped(exe)
	cfg.Connected = func() { workerupdate.Confirm(exe) }
	for {
		w, err := worker.New(cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		err = w.Run(ctx)
		var update *worker.UpdateError
		if !errors.As(err, &update) {
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return 0
		}
		log.Info("updating", "from", version.Version, "to", update.Offer.Version, "zip", update.Offer.URL)
		if err := workerupdate.Apply(ctx, nil, update.Offer, exe, version.Version); err != nil {
			log.Error("the update failed; carrying on with this version", "version", update.Offer.Version, "err", err)
			cfg.Skip = update.Offer.Version
			continue
		}
		log.Info("updated; restarting", "version", update.Offer.Version)
		restart(exe, log)
		return 1
	}
}

// runWithUI serves the status page and runs the worker from its settings.
func runWithUI(ctx context.Context, listen string, browser bool) int {
	logs := &workerui.Logs{}
	log := slog.New(logs.Handler(slog.NewTextHandler(os.Stdout, nil)))
	store, err := workerui.Open(workerui.DefaultPath(), os.Getenv, defaults())
	if err != nil {
		fmt.Fprintln(os.Stderr, "the settings file:", err)
		return 1
	}
	ui := workerui.New(store, logs, log, version.Version)
	exe, canUpdate := updatable()
	if canUpdate {
		ui.Update = func(ctx context.Context, o workerupdate.Offer) error {
			return workerupdate.Apply(ctx, nil, o, exe, version.Version)
		}
		ui.Skip = workerupdate.Skipped(exe)
	}
	ui.Connected = func() { workerupdate.Confirm(exe) }
	url := "http://" + listen + "/"
	ln, err := workerui.Serve(ctx, listen, ui.Handler())
	if err != nil {
		if errors.Is(err, workerui.ErrRunning) {
			// most likely this worker, started again: show the one running
			fmt.Println("the worker is already running; opening", url)
			_ = workerui.OpenBrowser(url)
			return 0
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	log.Info("status page", "url", url, "settings", store.Path())
	if err := ui.Start(); err != nil {
		log.Warn("not started yet: enter the server address and key on the status page", "err", err)
	}
	go ui.KeepAwake(ctx)
	if browser {
		if err := workerui.OpenBrowser(url); err != nil {
			log.Info("open the status page in a browser", "url", url)
		}
	}
	select {
	case <-ctx.Done():
		log.Info("stopping")
		ui.Stop()
		return 0
	case <-ui.Updated():
		// the page's port is freed for the new program; the open page
		// reconnects to it on its own
		_ = ln.Close()
		restart(exe, log, "-no-browser")
		return 1
	}
}

// updatable is this program's path and whether it may replace itself: not
// inside a container, whose image is what gets updated.
func updatable() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	for _, f := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(f); err == nil {
			return exe, false
		}
	}
	return exe, true
}

// settle checks on an update that just happened: after a few starts that
// never reached the server, the program before it is put back.
func settle() {
	exe, ok := updatable()
	if !ok {
		return
	}
	back, m, err := workerupdate.Settle(exe)
	if err != nil {
		fmt.Fprintln(os.Stderr, "checking the last update:", err)
		return
	}
	if back {
		fmt.Fprintf(os.Stderr, "%s didn't reach the server after %d starts; going back to %s\n", m.To, m.Starts-1, m.From)
		restart(exe, slog.Default())
	}
}

// restart starts exe again with this program's arguments (plus extra ones
// not already there). It returns only when that fails.
func restart(exe string, log *slog.Logger, extra ...string) {
	args := os.Args[1:]
	for _, a := range extra {
		if !slices.Contains(args, a) {
			args = append(args, a)
		}
	}
	time.Sleep(200 * time.Millisecond) // let the log reach its readers
	if err := workerupdate.Restart(exe, args); err != nil {
		log.Error("could not restart; start the worker again by hand", "err", err)
	}
}

// defaults are the settings this build starts with: the upscalers in the
// folder next to the program, as the release zip lays them out.
func defaults() map[string]string {
	d := map[string]string{}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Join(filepath.Dir(exe), "upscalers")
		if upscaler.ToolsAvailable(dir) || runtime.GOOS == "windows" {
			d["MANGARR_UPSCALER_TOOLS_DIR"] = dir
		}
	}
	return d
}

// useBundledEncoders puts the encoders folder next to the program (avifenc,
// cwebp and cjxl in the release zip) first on PATH, so pages are encoded by
// the native tools instead of the much slower built-in encoder.
func useBundledEncoders() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	dir := filepath.Join(filepath.Dir(exe), "encoders")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return
	}
	_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func withDefaults(getenv func(string) string) func(string) string {
	d := defaults()
	return func(name string) string {
		if v := getenv(name); v != "" {
			return v
		}
		return d[name]
	}
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
