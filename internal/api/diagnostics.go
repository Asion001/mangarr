package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/envcfg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/redact"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/version"
)

func init() { register((*Server).registerDiagnostics) }

// diagnosticsSystem is system.json in the bundle.
type diagnosticsSystem struct {
	SystemStatus
	Uptime     string         `json:"uptime"`
	Goroutines int            `json:"goroutines"`
	Counts     map[string]int `json:"counts"`
	ImageCache int64          `json:"imageCacheBytes"`
	LogDir     string         `json:"logDir"`
}

// diagnostics builds the support bundle: everything is redacted.
func (s *Server) diagnostics(ctx context.Context) ([]byte, error) {
	red := s.app.Redactor(ctx)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, v any) error {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(red.String(string(b))))
		return err
	}

	counts := map[string]int{}
	for name, m := range map[string]any{"series": (*model.Series)(nil), "chapters": (*model.Chapter)(nil), "files": (*model.ChapterFile)(nil),
		"sources": (*model.SeriesSource)(nil), "readers": (*model.Reader)(nil), "readStates": (*model.ChapterReadState)(nil),
		"queue": (*model.DownloadJob)(nil), "imports": (*model.Import)(nil)} {
		counts[name], _ = s.app.DB.NewSelect().Model(m).Count(ctx)
	}
	sys := diagnosticsSystem{
		SystemStatus: SystemStatus{Version: version.Version, Build: version.Build, Commit: version.Commit, GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
			Database: string(s.app.DB.Kind), DataDir: s.app.Cfg.DataDir, StartedAt: s.app.StartedAt, URLBase: s.app.Cfg.URLBase,
			Mode: s.app.Cfg.Mode, Processing: s.app.Cfg.Processing},
		Uptime: time.Since(s.app.StartedAt).Round(time.Second).String(), Goroutines: runtime.NumGoroutine(),
		Counts: counts, ImageCache: s.app.ImageCache.Size(), LogDir: s.app.Cfg.LogDir,
	}
	checks, at := s.app.Health.Results()
	var mods []ModuleResource
	for _, l := range s.app.Modules.All("") {
		mods = append(mods, s.toModuleResource(l)) // secret fields are masked
	}
	settingsDocs := map[string]any{}
	for _, d := range settings.Docs {
		v := d.Default()
		if err := s.app.Settings.Get(ctx, d.Key, v); err == nil {
			settingsDocs[d.Name] = v
		}
	}
	env := envcfg.All(s.app.Cfg.Env)
	queue, _ := s.app.DLQueue.ListPage(ctx, downloads.ListFilter{IncludeDone: true}, 1, 200)
	commands, _ := s.app.Queue.Recent(ctx, 200)
	var profiles []model.Profile
	_ = s.app.DB.NewSelect().Model(&profiles).Scan(ctx)
	var roots []model.RootFolder
	_ = s.app.DB.NewSelect().Model(&roots).Scan(ctx)

	for name, v := range map[string]any{
		"system.json":   sys,
		"health.json":   map[string]any{"checkedAt": at, "checks": checks},
		"modules.json":  mods,
		"settings.json": settingsDocs,
		"env.json":      env,
		"profiles.json": profiles,
		"roots.json":    roots,
		"queue.json":    queue,
		"commands.json": commands,
		"cache.json":    map[string]any{"images": s.app.ImageCache.Stats(), "catalogEntries": func() int { n, _, _ := s.app.SourceCache.Stats(); return n }()},
	} {
		if err := add(name, v); err != nil {
			return nil, err
		}
	}
	if err := s.writeLogs(zw, "logs/", red); err != nil {
		return nil, err
	}
	if w, err := zw.CreateHeader(&zip.FileHeader{Name: "README.txt", Method: zip.Deflate, Modified: time.Now()}); err == nil {
		_, _ = w.Write([]byte("mangarr diagnostics " + time.Now().UTC().Format(time.RFC3339) + "\n" +
			"API keys, passwords, tokens and module secrets are replaced with " + redact.Mask + ".\n" +
			"Check the files before sharing them anyway (series titles and folder names are included).\n"))
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Server) registerDiagnostics() {
	huma.Register(s.api, huma.Operation{OperationID: "system-diagnostics", Method: http.MethodGet, Path: "/api/v1/system/diagnostics", Tags: []string{"System"},
		Summary: "A zip for bug reports: status, health, modules, settings, queue and logs with secrets removed"},
		func(ctx context.Context, _ *struct{}) (*downloadOutput, error) {
			data, err := s.diagnostics(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			name := "mangarr-diagnostics-" + time.Now().Format("20060102-150405") + ".zip"
			return &downloadOutput{ContentType: "application/zip", ContentDisposition: `attachment; filename="` + name + `"`, Body: data}, nil
		})
}
