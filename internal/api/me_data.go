package api

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/backupimport"
	"github.com/Asion001/mangarr/internal/imports"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/userdata"
	"github.com/Asion001/mangarr/internal/userimport"
)

func init() { register((*Server).registerMyData) }

// maxDataImport bounds an uploaded account export (years of history).
const maxDataImport = 256 << 20

var unsafeFileName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (s *Server) myAccount(ctx context.Context) (userdata.Account, error) {
	userID, err := s.userOf(ctx)
	if err != nil {
		return userdata.Account{}, err
	}
	acc := userdata.Account{UserID: userID}
	var u model.User
	if err := s.app.DB.NewSelect().Model(&u).Column("username").Where("id = ?", userID).Scan(ctx); err == nil {
		acc.Username = u.Username
	}
	return acc, nil
}

type myDataImportInput struct {
	Body struct {
		Data    userdata.Export            `json:"data"`
		Options userdata.DataImportOptions `json:"options,omitempty"`
	}
}

func (s *Server) registerMyData() {
	tags := []string{"Account"}
	huma.Register(s.api, huma.Operation{OperationID: "me-data-export", Method: http.MethodGet, Path: "/api/v1/me/data", Tags: tags,
		Summary: "Download your own data (progress, reading history and time, follows, reader and interface settings) to import on another server"},
		func(ctx context.Context, _ *struct{}) (*mihonBackupOutput, error) {
			acc, err := s.myAccount(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			data, err := (&userdata.Service{DB: s.app.DB}).Export(ctx, acc)
			if err != nil {
				return nil, toHTTPError(err)
			}
			body, err := json.MarshalIndent(data, "", " ")
			if err != nil {
				return nil, toHTTPError(err)
			}
			name := "mangarr-data"
			if acc.Username != "" {
				name += "-" + unsafeFileName.ReplaceAllString(acc.Username, "_")
			}
			name += "-" + time.Now().Format("2006-01-02") + ".json"
			return &mihonBackupOutput{ContentType: "application/json", ContentDisposition: `attachment; filename="` + name + `"`, CacheControl: "no-store", Body: body}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-data-summary", Method: http.MethodGet, Path: "/api/v1/me/data/summary", Tags: tags,
		Summary: "Count what an export of your data would hold"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body userdata.Summary }, error) {
			acc, err := s.myAccount(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			data, err := (&userdata.Service{DB: s.app.DB}).Export(ctx, acc)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &struct{ Body userdata.Summary }{userdata.Summarize(data)}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-data-preview", Method: http.MethodPost, Path: "/api/v1/me/data/preview", Tags: tags,
		Summary: "Match an exported account's titles to this server's library without importing anything", MaxBodyBytes: maxDataImport},
		func(ctx context.Context, in *myDataImportInput) (*struct{ Body []userdata.DataTitleMatch }, error) {
			m, err := (&userdata.Service{DB: s.app.DB}).Preview(ctx, &in.Body.Data, in.Body.Options.Picked)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			return &struct{ Body []userdata.DataTitleMatch }{m}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-data-import", Method: http.MethodPost, Path: "/api/v1/me/data/import", Tags: tags,
		Summary: "Import an exported account's data into your own; progress already further here is kept", MaxBodyBytes: maxDataImport},
		func(ctx context.Context, in *myDataImportInput) (*struct{ Body *userdata.DataImportResult }, error) {
			acc, err := s.myAccount(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			if err := userdata.Check(&in.Body.Data); err != nil {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			svc := &userdata.Service{DB: s.app.DB}
			var ask []userdata.ExportTitle
			if len(in.Body.Options.Request) > 0 {
				p := access.From(ctx)
				if p == nil || p.Kind != access.KindUser || !p.Can(access.RequestsCreate) {
					return nil, huma.Error403Forbidden("you can't request titles on this server")
				}
				// only titles that really aren't here are asked for
				matches, err := svc.Preview(ctx, &in.Body.Data, in.Body.Options.Picked)
				if err != nil {
					return nil, huma.Error422UnprocessableEntity(err.Error())
				}
				for _, i := range in.Body.Options.Request {
					if i >= 0 && i < len(matches) && matches[i].SeriesID == 0 {
						ask = append(ask, in.Body.Data.Titles[i])
					}
				}
			}
			res, err := svc.Import(ctx, acc, &in.Body.Data, in.Body.Options)
			if err != nil {
				return nil, toHTTPError(err)
			}
			if len(res.Unresolved) > 0 {
				items := make([]userimport.Placement, 0, len(res.Unresolved))
				matches, _ := svc.Preview(ctx, &in.Body.Data, in.Body.Options.Picked)
				for _, i := range res.Unresolved {
					if i < len(matches) && matches[i].SeriesID > 0 {
						items = append(items, userimport.Placement{SeriesID: matches[i].SeriesID, Title: in.Body.Data.Titles[i]})
					}
				}
				if len(items) > 0 {
					if _, err := s.app.Queue.Push(ctx, "PlaceImportedProgress", map[string]any{"userId": acc.UserID, "items": items}, "import"); err != nil {
						return nil, toHTTPError(err)
					}
					res.Pending = len(items)
				}
			}
			if len(ask) > 0 {
				if _, err := s.app.Queue.Push(ctx, "RequestImportedTitles", map[string]any{"userId": acc.UserID, "titles": ask}, "import"); err != nil {
					return nil, toHTTPError(err)
				}
				res.Requested = len(ask)
			}
			s.app.Bus.Changed("reading", "imported", 0)
			return &struct{ Body *userdata.DataImportResult }{res}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "me-data-accounts", Method: http.MethodGet, Path: "/api/v1/me/data/accounts", Tags: tags,
		Summary: "Sites whose accounts you can bring your lists and progress from"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []imports.AccountSource }, error) {
			list, err := s.app.Imports.AccountSources(ctx)
			return &struct{ Body []imports.AccountSource }{list}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-data-from-backup", Method: http.MethodPost, Path: "/api/v1/me/data/from-backup", Tags: tags,
		Summary:      "Read another app's backup (Mihon, Tachiyomi, Suwayomi, Aidoku) as your data, to preview and import like an export",
		MaxBodyBytes: 128 << 20},
		func(ctx context.Context, in *struct {
			RawBody []byte `contentType:"application/octet-stream"`
		}) (*struct{ Body *userdata.Export }, error) {
			b, err := backupimport.Parse(in.RawBody)
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			acc, _ := s.myAccount(ctx)
			return &struct{ Body *userdata.Export }{userdata.FromBackup(b, acc.Username, nil)}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-data-from-account", Method: http.MethodPost, Path: "/api/v1/me/data/from-account", Tags: tags,
		Summary: "Read a manga site account's lists and progress as your data, to preview and import like an export. The token is used for this read only and isn't kept."},
		func(ctx context.Context, in *accountReadInput) (*struct{ Body *userdata.Export }, error) {
			b, lists, err := s.readAccount(ctx, in)
			if err != nil {
				return nil, err
			}
			acc, _ := s.myAccount(ctx)
			return &struct{ Body *userdata.Export }{userdata.FromBackup(b, acc.Username, lists)}, nil
		})
}
