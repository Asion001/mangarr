package api

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/userdata"
)

func init() { register((*Server).registerMyData) }

// maxDataImport bounds an uploaded account export (years of history).
const maxDataImport = 256 << 20

var unsafeFileName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (s *Server) myAccount(ctx context.Context) (userdata.Account, error) {
	readerID, err := s.readerOf(ctx)
	if err != nil {
		return userdata.Account{}, err
	}
	acc := userdata.Account{ReaderID: readerID}
	if p := access.From(ctx); p != nil && p.Kind == access.KindUser {
		acc.UserID = p.UserID
		var u model.User
		if err := s.app.DB.NewSelect().Model(&u).Column("username").Where("id = ?", p.UserID).Scan(ctx); err == nil {
			acc.Username = u.Username
		}
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
			res, err := (&userdata.Service{DB: s.app.DB}).Import(ctx, acc, &in.Body.Data, in.Body.Options)
			if err != nil {
				return nil, toHTTPError(err)
			}
			s.app.Bus.Changed("reading", "imported", 0)
			return &struct{ Body *userdata.DataImportResult }{res}, nil
		})
}
