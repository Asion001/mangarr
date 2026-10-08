package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func init() { register((*Server).registerLanguages) }

// OfferedLanguages are the languages a title can be added or requested in.
type OfferedLanguages struct {
	Defaults  []string `json:"defaults" doc:"Searched by default, in order"`
	Languages []string `json:"languages" doc:"Every language set up: the defaults, root folder languages and language defaults"`
}

func (s *Server) registerLanguages() {
	huma.Register(s.api, huma.Operation{OperationID: "languages-list", Method: http.MethodGet, Path: "/api/v1/languages", Tags: []string{"Series"},
		Summary: "Languages titles can be added or requested in"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body OfferedLanguages }, error) {
			defaults, all, err := s.app.Series.Offered(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &struct{ Body OfferedLanguages }{OfferedLanguages{Defaults: defaults, Languages: all}}, nil
		})
}
