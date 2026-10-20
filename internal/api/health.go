package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/backup"
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
		}) (*struct{}, error) {
			p, err := s.app.Backups.Path(in.Name)
			if err != nil {
				return nil, huma.Error404NotFound("backup not found")
			}
			if _, err := s.app.Backups.Verify(ctx, in.Name); err != nil {
				return nil, huma.Error400BadRequest("backup verification failed: " + err.Error())
			}
			if err := s.app.RestoreBackup(p, in.Name); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			return nil, nil
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
