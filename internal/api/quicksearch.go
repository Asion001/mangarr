package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/sourcesearch"
)

type (
	QuickSearchInput  = sourcesearch.QuickSearchInput
	QuickSearchResult = sourcesearch.QuickSearchResult
	DefaultsInput     = sourcesearch.DefaultsInput
	DefaultsResult    = sourcesearch.DefaultsResult
)

func (s *Server) registerQuickSearch() {
	huma.Register(s.api, huma.Operation{OperationID: "sources-quick-search", Method: http.MethodPost, Path: "/api/v1/sources/quick-search", Tags: []string{"Sources"},
		Summary: "Search catalogs one by one in priority order and stop at the first confident title match"},
		func(ctx context.Context, in *struct{ Body QuickSearchInput }) (*struct{ Body *QuickSearchResult }, error) {
			in.Body.Query = strings.TrimSpace(in.Body.Query)
			res, err := s.app.Search.Quick(ctx, in.Body, sourcesearch.QuickOptions{})
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &struct{ Body *QuickSearchResult }{res}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "sources-defaults-search", Method: http.MethodPost, Path: "/api/v1/sources/defaults-search", Tags: []string{"Sources"},
		Summary: "Search every source in each language's default list (the search languages unless langs is given)"},
		func(ctx context.Context, in *struct{ Body DefaultsInput }) (*struct{ Body *DefaultsResult }, error) {
			in.Body.Query = strings.TrimSpace(in.Body.Query)
			res, err := s.app.Search.Defaults(ctx, in.Body)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &struct{ Body *DefaultsResult }{res}, nil
		})
}

func init() { register((*Server).registerQuickSearch) }
