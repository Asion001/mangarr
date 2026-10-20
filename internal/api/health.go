package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/app"
	"github.com/Asion001/mangarr/internal/backup"
	"github.com/Asion001/mangarr/internal/fsutil"
	"github.com/Asion001/mangarr/internal/health"
)

func init() { register((*Server).registerHealth) }

type HealthResponse struct {
	Checks    []health.Check `json:"checks"`
	CheckedAt time.Time      `json:"checkedAt"`
}

func (s *Server) registerHealth() {
	tags := []string{"System"}
	huma.Register(s.api, huma.Operation{OperationID: "health-get", Method: http.MethodGet, Path: "/api/v1/health", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body HealthResponse }, error) {
			checks, at := s.app.Health.Results()
			if at.IsZero() {
				checks = s.app.Health.Run(ctx)
				at = time.Now().UTC()
			}
			if checks == nil {
				checks = []health.Check{}
			}
			return &struct{ Body HealthResponse }{HealthResponse{checks, at}}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "health-run", Method: http.MethodPost, Path: "/api/v1/health/check", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body HealthResponse }, error) {
			checks := s.app.Health.Run(ctx)
			if checks == nil {
				checks = []health.Check{}
			}
			return &struct{ Body HealthResponse }{HealthResponse{checks, time.Now().UTC()}}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "backups-list", Method: http.MethodGet, Path: "/api/v1/system/backups", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []backup.Backup }, error) {
			list, err := s.app.Backups.List()
			return &struct{ Body []backup.Backup }{list}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "backups-create", Method: http.MethodPost, Path: "/api/v1/system/backups", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body *backup.Backup }, error) {
			b, err := s.app.Backups.Create(ctx, "manual")
			return &struct{ Body *backup.Backup }{b}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "backups-download", Method: http.MethodGet, Path: "/api/v1/system/backups/{name}", Tags: tags},
		func(ctx context.Context, in *struct {
			Name string `path:"name"`
		}) (*huma.StreamResponse, error) {
			p, err := s.app.Backups.Path(in.Name)
			if err != nil {
				return nil, huma.Error404NotFound("backup not found")
			}
			return &huma.StreamResponse{Body: func(hctx huma.Context) {
				f, err := os.Open(p)
				if err != nil {
					hctx.SetStatus(http.StatusNotFound)
					return
				}
				defer f.Close()
				hctx.SetHeader("Content-Type", "application/zip")
				hctx.SetHeader("Content-Disposition", `attachment; filename="`+in.Name+`"`)
				_, _ = io.Copy(hctx.BodyWriter(), f)
			}}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "backups-restore", Method: http.MethodPost, Path: "/api/v1/system/backups/{name}/restore", Tags: tags,
		Summary: "Replace all data with a backup's and restart (progress under /api/v1/system/database)"},
		func(ctx context.Context, in *struct {
			Name string `path:"name"`
			Body *RestoreRequest
		}) (*struct{}, error) {
			p, err := s.app.Backups.Path(in.Name)
			if err != nil {
				return nil, huma.Error404NotFound("backup not found")
			}
			v, err := s.app.Backups.Verified(ctx, in.Name)
			if err != nil {
				return nil, huma.Error400BadRequest("backup verification failed: " + err.Error())
			}
			opts := app.RestoreOptions{Folders: map[int64]string{}}
			for _, f := range v.Folders {
				opts.Folders[f.ID] = f.Path
			}
			if in.Body != nil {
				for _, f := range in.Body.Folders {
					if _, ok := opts.Folders[f.ID]; !ok {
						return nil, huma.Error400BadRequest(fmt.Sprintf("the backup has no library folder %d", f.ID))
					}
					opts.Folders[f.ID] = f.Path
				}
				opts.Library = in.Body.Library
			}
			if err := s.app.RestoreBackup(p, in.Name, opts); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			return nil, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "backups-export", Method: http.MethodGet, Path: "/api/v1/system/export", Tags: tags,
		Summary: "Download everything needed to move mangarr to another server: a backup and, with library=true, the library files"},
		func(ctx context.Context, in *struct {
			Library bool `query:"library"`
		}) (*huma.StreamResponse, error) {
			return &huma.StreamResponse{Body: func(hctx huma.Context) {
				name := backup.ExportName()
				hctx.SetHeader("Content-Type", "application/zip")
				hctx.SetHeader("Content-Disposition", `attachment; filename="`+name+`"`)
				if err := s.app.Backups.Export(hctx.Context(), hctx.BodyWriter(), in.Library); err != nil {
					// the download is cut short, so the browser shows it failed
					s.app.Log.Error("export failed", "err", err)
					panic(http.ErrAbortHandler)
				}
			}}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "backups-check-folder", Method: http.MethodPost, Path: "/api/v1/system/backups/check-folder", Tags: tags,
		Summary: "Look at a folder a backup's library folder could be restored to"},
		func(ctx context.Context, in *struct {
			Body struct {
				Path string `json:"path"`
			}
		}) (*struct{ Body FolderCheck }, error) {
			return &struct{ Body FolderCheck }{checkFolder(in.Body.Path)}, nil
		})
	uploadOp := huma.Operation{OperationID: "backups-upload", Method: http.MethodPost, Path: "/api/v1/system/backups/upload", Tags: tags,
		Summary: "Add and verify a backup zip from another install (restore it from the list)", MaxBodyBytes: -1,
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/zip": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}},
		Responses: map[string]*huma.Response{
			"200":     {Description: "Verified backup", Content: map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{Ref: "#/components/schemas/BackupBackup"}}}},
			"400":     {Description: "Invalid backup archive", Content: map[string]*huma.MediaType{"application/problem+json": {Schema: &huma.Schema{Ref: "#/components/schemas/ErrorModel"}}}},
			"default": {Description: "Error", Content: map[string]*huma.MediaType{"application/problem+json": {Schema: &huma.Schema{Ref: "#/components/schemas/ErrorModel"}}}},
		}}
	s.api.OpenAPI().AddOperation(&uploadOp)
	s.api.Adapter().Handle(&uploadOp, s.api.Middlewares().Handler(func(ctx huma.Context) {
		b, err := s.app.Backups.SaveFrom(ctx.Context(), ctx.BodyReader())
		if err != nil {
			writeBackupAPIError(ctx, http.StatusBadRequest, err)
			return
		}
		writeBackupAPIJSON(ctx, http.StatusOK, b)
	}))
	huma.Register(s.api, huma.Operation{OperationID: "backups-verify", Method: http.MethodPost, Path: "/api/v1/system/backups/{name}/verify", Tags: tags,
		Summary: "Verify a backup before restore"},
		func(ctx context.Context, in *struct {
			Name string `path:"name"`
		}) (*struct{ Body *backup.Verification }, error) {
			v, err := s.app.Backups.Verify(ctx, in.Name)
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			return &struct{ Body *backup.Verification }{v}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "backups-delete", Method: http.MethodDelete, Path: "/api/v1/system/backups/{name}", Tags: tags},
		func(ctx context.Context, in *struct {
			Name string `path:"name"`
		}) (*struct{}, error) {
			if err := s.app.Backups.Delete(in.Name); err != nil {
				return nil, huma.Error404NotFound(err.Error())
			}
			return nil, nil
		})
}

// RestoreRequest says where a backup's library folders go on this server.
type RestoreRequest struct {
	Folders []RestoreFolder `json:"folders,omitempty" doc:"Folders whose path differs here; the rest keep theirs"`
	Library bool            `json:"library,omitempty" doc:"Unpack the archive's library files into the folders first"`
}

type RestoreFolder struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
}

// FolderCheck is what is at a path a library folder could go to.
type FolderCheck struct {
	Path     string `json:"path"`
	Absolute bool   `json:"absolute"`
	Exists   bool   `json:"exists"`
	// Folders counts the folders in it (series folders, once restored).
	Folders int `json:"folders"`
	// Free is the free space where its files would go (0 if unknown).
	Free int64 `json:"free"`
}

func checkFolder(p string) FolderCheck {
	out := FolderCheck{Path: p, Absolute: filepath.IsAbs(p)}
	if !out.Absolute {
		return out
	}
	p = filepath.Clean(p)
	if entries, err := os.ReadDir(p); err == nil {
		out.Exists = true
		for _, e := range entries {
			if e.IsDir() {
				out.Folders++
			}
		}
	}
	// the nearest folder that exists, for one that will be created
	for dir := p; ; dir = filepath.Dir(dir) {
		if free, err := fsutil.FreeSpace(dir); err == nil {
			out.Free = int64(free)
			break
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	return out
}

func writeBackupAPIJSON(ctx huma.Context, status int, value any) {
	ctx.SetStatus(status)
	ctx.SetHeader("Content-Type", "application/json")
	_ = json.NewEncoder(ctx.BodyWriter()).Encode(value)
}

func writeBackupAPIError(ctx huma.Context, status int, err error) {
	ctx.SetStatus(status)
	ctx.SetHeader("Content-Type", "application/problem+json")
	_ = json.NewEncoder(ctx.BodyWriter()).Encode(map[string]any{"detail": err.Error(), "status": status, "title": http.StatusText(status)})
}
