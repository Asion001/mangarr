package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/requests"
)

func init() { register((*Server).registerRequests) }

type RequestView = requests.Request

func requestError(err error) error {
	var av requests.AvailableError
	var bad requests.ValidationError
	switch {
	case errors.As(err, &bad):
		return huma.Error400BadRequest(bad.Msg)
	case errors.As(err, &av):
		return huma.Error409Conflict("already in the library")
	case errors.Is(err, requests.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	}
	return toHTTPError(err)
}

func (s *Server) registerRequests() {
	tags := []string{"Requests"}

	huma.Register(s.api, huma.Operation{OperationID: "requests-list", Method: http.MethodGet, Path: "/api/v1/requests", Tags: tags,
		Summary: "Requests: yours, or everyone's (all=true, for managers)"},
		func(ctx context.Context, in *struct {
			Status string `query:"status" enum:"pending,approved,available,declined,"`
			All    bool   `query:"all"`
		}) (*struct{ Body []RequestView }, error) {
			list, err := s.app.Requests.List(ctx, access.From(ctx), requests.Filter{Status: in.Status, All: in.All})
			if err != nil {
				return nil, requestError(err)
			}
			return &struct{ Body []RequestView }{list}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "requests-count", Method: http.MethodGet, Path: "/api/v1/requests/count", Tags: tags,
		Summary: "How many requests wait for a manager"},
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Pending int `json:"pending"`
			}
		}, error) {
			n, err := s.app.Requests.Pending(ctx)
			out := &struct {
				Body struct {
					Pending int `json:"pending"`
				}
			}{}
			out.Body.Pending = n
			return out, toHTTPError(err)
		})

	huma.Register(s.api, huma.Operation{OperationID: "requests-get", Method: http.MethodGet, Path: "/api/v1/requests/{id}", Tags: tags},
		func(ctx context.Context, in *IDPath) (*struct{ Body RequestView }, error) {
			v, err := s.app.Requests.Get(ctx, access.From(ctx), in.ID)
			if err != nil {
				return nil, requestError(err)
			}
			return &struct{ Body RequestView }{*v}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "requests-create", Method: http.MethodPost, Path: "/api/v1/requests", Tags: tags,
		Summary: "Ask for a series found with the series lookup, or another language of a series (joins an open request for it)"},
		func(ctx context.Context, in *struct {
			Body struct {
				ModuleID int64  `json:"moduleId,omitempty"`
				ID       string `json:"id,omitempty" doc:"Metadata id from the series lookup"`
				SeriesID int64  `json:"seriesId,omitempty" doc:"A series in the library, to ask for another language of it"`
				Language string `json:"language,omitempty" maxLength:"20" doc:"The edition asked for, one of GET /api/v1/languages (empty: the default languages)"`
				Monitor  bool   `json:"monitor,omitempty" doc:"With seriesId: download its chapters that aren't monitored"`
				Note     string `json:"note,omitempty" maxLength:"500"`
			}
		}) (*struct {
			Body struct {
				Request RequestView `json:"request"`
				// Joined: someone had asked already; you were added.
				Joined bool `json:"joined"`
			}
		}, error) {
			if in.Body.SeriesID == 0 && in.Body.ID == "" {
				return nil, huma.Error400BadRequest("id or seriesId is required")
			}
			v, joined, err := s.app.Requests.Create(ctx, access.From(ctx), requests.CreateInput{ModuleID: in.Body.ModuleID, MetaID: in.Body.ID,
				SeriesID: in.Body.SeriesID, Language: in.Body.Language, Monitor: in.Body.Monitor, Note: in.Body.Note})
			if err != nil {
				return nil, requestError(err)
			}
			out := &struct {
				Body struct {
					Request RequestView `json:"request"`
					Joined  bool        `json:"joined"`
				}
			}{}
			out.Body.Request, out.Body.Joined = *v, joined
			return out, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "requests-withdraw", Method: http.MethodDelete, Path: "/api/v1/requests/{id}/mine", Tags: tags,
		Summary: "Take back your request (it goes away when nobody else asked)"},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			return nil, requestError(s.app.Requests.Withdraw(ctx, in.ID, access.From(ctx).UserID))
		})

	huma.Register(s.api, huma.Operation{OperationID: "requests-decline", Method: http.MethodPost, Path: "/api/v1/requests/{id}/decline", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Reason string `json:"reason,omitempty" maxLength:"500"`
			}
		}) (*struct{}, error) {
			return nil, requestError(s.app.Requests.Decline(ctx, in.ID, in.Body.Reason, access.From(ctx)))
		})

	huma.Register(s.api, huma.Operation{OperationID: "requests-link", Method: http.MethodPost, Path: "/api/v1/requests/{id}/link", Tags: tags,
		Summary: "Mark a request fulfilled by a series already in the library"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				SeriesID int64 `json:"seriesId"`
			}
		}) (*struct{}, error) {
			return nil, requestError(s.app.Requests.Link(ctx, in.ID, in.Body.SeriesID, access.From(ctx)))
		})

	huma.Register(s.api, huma.Operation{OperationID: "requests-monitor", Method: http.MethodPost, Path: "/api/v1/requests/{id}/monitor", Tags: tags,
		Summary: "Fulfil a download request: monitor the title's chapters, search the missing ones"},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			return nil, requestError(s.app.Requests.Monitor(ctx, in.ID, access.From(ctx)))
		})

	huma.Register(s.api, huma.Operation{OperationID: "requests-delete", Method: http.MethodDelete, Path: "/api/v1/requests/{id}", Tags: tags},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			return nil, requestError(s.app.Requests.Delete(ctx, in.ID))
		})
}
