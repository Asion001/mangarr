package api

import (
	"context"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"

	"github.com/Asion001/mangarr/internal/diskcache"
	"github.com/Asion001/mangarr/internal/envcfg"
	"github.com/Asion001/mangarr/internal/jobs"
	"github.com/Asion001/mangarr/internal/logging"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/version"
)

type SystemStatus struct {
	Version   string    `json:"version"`
	Build     string    `json:"build"`
	Commit    string    `json:"commit"`
	GoVersion string    `json:"goVersion"`
	OS        string    `json:"os"`
	Arch      string    `json:"arch"`
	Database  string    `json:"database"`
	DataDir   string    `json:"dataDir"`
	StartedAt time.Time `json:"startedAt"`
	URLBase   string    `json:"urlBase"`
	// Mode is MANGARR_MODE (integrated, server) and Processing is
	// MANGARR_PROCESSING (local, workers): what this server does itself.
	Mode       string `json:"mode"`
	Processing string `json:"processing"`
}

type CacheStatus struct {
	// Entries/Bytes of the in-memory catalog cache (search results, details).
	Entries  int                     `json:"entries"`
	Bytes    int64                   `json:"bytes"`
	MaxBytes int64                   `json:"maxBytes"`
	Images   []diskcache.BucketStats `json:"images"`
	// ImageBytes is the image cache's total and ImageMaxBytes its cap (0 = none).
	ImageBytes    int64 `json:"imageBytes"`
	ImageMaxBytes int64 `json:"imageMaxBytes"`
	// NeedsCompact is true while images from an older version wait to be resized.
	NeedsCompact bool `json:"needsCompact"`
}

func (s *Server) imageCap(ctx context.Context) int64 {
	g, _ := s.app.Settings.General(ctx)
	return int64(g.ImageCacheMaxMB) << 20
}

type TaskInfo struct {
	Paused             bool           `json:"paused"`
	Schedule           *jobs.Schedule `json:"schedule,omitempty"`
	DefaultSchedule    *jobs.Schedule `json:"defaultSchedule,omitempty"`
	Custom             bool           `json:"custom"`
	MinIntervalMinutes int            `json:"minIntervalMinutes"`
	NextRuns           []time.Time    `json:"nextRuns"`
	Timezone           string         `json:"timezone"`
	Running            *TaskRunning   `json:"running,omitempty"`
	LastRun            *TaskLastRun   `json:"lastRun,omitempty"`
	Name               string         `json:"name"`
	Description        string         `json:"description"`
	IntervalMinutes    int            `json:"intervalMinutes"`
	LastExecution      *time.Time     `json:"lastExecution,omitempty"`
	NextExecution      *time.Time     `json:"nextExecution"`
	Scheduled          bool           `json:"scheduled"`
}

type CommandInput struct {
	Name string         `json:"name"`
	Body map[string]any `json:"body,omitempty"`
}

// managerCommands are the commands library managers may run (the library's
// own work; the rest is administration).
var managerCommands = map[string]bool{
	"RefreshSeries": true, "RefreshSources": true, "SearchMissing": true, "RefreshMetadata": true,
	"ProcessExisting": true, "DiskScan": true, "LibraryRescan": true, "SeriesSources": true, "SwitchSourceModule": true,
}

func (s *Server) registerSystem() {
	tags := []string{"System"}
	huma.Register(s.api, huma.Operation{OperationID: "system-status", Method: http.MethodGet, Path: "/api/v1/system/status", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body SystemStatus }, error) {
			return &struct{ Body SystemStatus }{SystemStatus{
				Version: version.Version, Build: version.Build, Commit: version.Commit, GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
				Database: string(s.app.DB.Kind), DataDir: s.app.Cfg.DataDir, StartedAt: s.app.StartedAt, URLBase: s.app.Cfg.URLBase,
				Mode: s.app.Cfg.Mode, Processing: s.app.Cfg.Processing,
			}}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "system-env", Method: http.MethodGet, Path: "/api/v1/system/env", Tags: tags,
		Summary: "Supported environment variables and which are set"},
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Vars    []envcfg.Var `json:"vars"`
				Unknown []string     `json:"unknown"`
			}
		}, error) {
			out := &struct {
				Body struct {
					Vars    []envcfg.Var `json:"vars"`
					Unknown []string     `json:"unknown"`
				}
			}{}
			out.Body.Vars = envcfg.All(s.app.Cfg.Env)
			out.Body.Unknown = envcfg.Unknown(s.app.Cfg.Env)
			if out.Body.Unknown == nil {
				out.Body.Unknown = []string{}
			}
			return out, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "system-cache", Method: http.MethodGet, Path: "/api/v1/system/cache", Tags: tags,
		Summary: "Sizes of the in-memory catalog cache and the on-disk image cache"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body CacheStatus }, error) {
			n, b, max := s.app.SourceCache.Stats()
			return &struct{ Body CacheStatus }{CacheStatus{Entries: n, Bytes: b, MaxBytes: max,
				Images: s.app.ImageCache.Stats(), ImageBytes: s.app.ImageCache.Size(), ImageMaxBytes: s.imageCap(ctx), NeedsCompact: s.app.ImageCache.NeedsCompact()}}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "system-cache-clear", Method: http.MethodPost, Path: "/api/v1/system/cache/clear", Tags: tags,
		Summary: "Clear caches: catalogs (search/details) and image buckets"},
		func(ctx context.Context, in *struct {
			Body struct {
				Catalogs bool     `json:"catalogs"`
				Images   []string `json:"images,omitempty" doc:"Image buckets to clear (thumbs, assets, covers, pages); empty = none"`
			}
		}) (*struct{ Body CacheStatus }, error) {
			if in.Body.Catalogs {
				s.app.SourceCache.Clear()
				s.app.Catalogs.Invalidate(0)
			}
			for _, b := range in.Body.Images {
				if !slices.Contains(diskcache.Buckets, b) {
					return nil, huma.Error400BadRequest("unknown image bucket " + b)
				}
			}
			if len(in.Body.Images) > 0 {
				if err := s.app.ImageCache.Clear(in.Body.Images); err != nil {
					return nil, toHTTPError(err)
				}
			}
			s.app.Bus.Changed("cache", "cleared", 0)
			n, b, max := s.app.SourceCache.Stats()
			return &struct{ Body CacheStatus }{CacheStatus{Entries: n, Bytes: b, MaxBytes: max,
				Images: s.app.ImageCache.Stats(), ImageBytes: s.app.ImageCache.Size(), ImageMaxBytes: s.imageCap(ctx), NeedsCompact: s.app.ImageCache.NeedsCompact()}}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "system-logs", Method: http.MethodGet, Path: "/api/v1/system/logs", Tags: tags},
		func(ctx context.Context, in *struct {
			Level string `query:"level" default:"info" enum:"debug,info,warn,error"`
			Limit int    `query:"limit" default:"500" minimum:"1" maximum:"2000"`
		}) (*struct{ Body []logging.Entry }, error) {
			entries := s.app.LogRing.Entries(logging.ParseLevel(in.Level), in.Limit)
			red := s.app.Redactor(ctx) // safe to screenshot or copy
			for i := range entries {
				entries[i].Message = red.String(entries[i].Message)
				attrs := make(map[string]string, len(entries[i].Attrs))
				for k, v := range entries[i].Attrs {
					// with the key, so "apikey=…" patterns match
					if r := red.String(k + "=" + v); strings.HasPrefix(r, k+"=") {
						attrs[k] = r[len(k)+1:]
					} else {
						attrs[k] = red.String(v)
					}
				}
				entries[i].Attrs = attrs
			}
			return &struct{ Body []logging.Entry }{entries}, nil
		})

	s.registerTasks()

	huma.Register(s.api, huma.Operation{OperationID: "commands-list", Method: http.MethodGet, Path: "/api/v1/commands", Tags: tags},
		func(ctx context.Context, in *struct {
			Limit int    `query:"limit" default:"50" minimum:"1" maximum:"500"`
			Name  string `query:"name"`
		}) (*struct{ Body []model.Command }, error) {
			out, err := s.app.Queue.Recent(ctx, in.Limit, in.Name)
			if out == nil {
				out = []model.Command{}
			}
			// overlay live messages of running commands
			live := map[int64]*model.Command{}
			for _, c := range s.app.Queue.Active() {
				live[c.ID] = c
			}
			for i := range out {
				if c, ok := live[out[i].ID]; ok {
					out[i] = *c
				}
			}
			return &struct{ Body []model.Command }{out}, toHTTPError(err)
		})

	huma.Register(s.api, huma.Operation{OperationID: "commands-push", Method: http.MethodPost, Path: "/api/v1/commands", Tags: tags,
		Summary: "Queue a command (e.g. RefreshSources, RefreshSeries {seriesId}, SearchMissing, Cleanup)"},
		func(ctx context.Context, in *struct{ Body CommandInput }) (*struct{ Body *model.Command }, error) {
			if !access.From(ctx).IsAdmin() && !managerCommands[in.Body.Name] {
				return nil, huma.Error403Forbidden("only administrators can run " + in.Body.Name)
			}
			trigger := "manual"
			if user := access.From(ctx).Username; user != "" {
				trigger += " · " + user
			}
			c, err := s.app.Queue.Push(ctx, in.Body.Name, in.Body.Body, trigger)
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			return &struct{ Body *model.Command }{c}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "commands-get", Method: http.MethodGet, Path: "/api/v1/commands/{id}", Tags: tags},
		func(ctx context.Context, in *IDPath) (*struct{ Body *model.Command }, error) {
			c, err := s.app.Queue.Get(ctx, in.ID)
			if err != nil {
				return nil, huma.Error404NotFound("command not found")
			}
			return &struct{ Body *model.Command }{c}, nil
		})
}
