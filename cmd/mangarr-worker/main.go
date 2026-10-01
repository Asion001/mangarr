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
	"syscall"

	"github.com/Asion001/mangarr/internal/upscaler"
	"github.com/Asion001/mangarr/internal/version"
	"github.com/Asion001/mangarr/internal/worker"
	"github.com/Asion001/mangarr/internal/workerui"
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
	w, err := worker.New(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := w.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
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
	url := "http://" + listen + "/"
	if _, err := workerui.Serve(ctx, listen, ui.Handler()); err != nil {
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
	if browser {
		if err := workerui.OpenBrowser(url); err != nil {
			log.Info("open the status page in a browser", "url", url)
		}
	}
	<-ctx.Done()
	log.Info("stopping")
	ui.Stop()
	return 0
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
