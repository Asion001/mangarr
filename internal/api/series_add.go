package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
	"github.com/Asion001/mangarr/internal/series"
)

func init() { register((*Server).registerSeriesAdd) }

// AddEditionsResponse is the title and the editions an add touched.
type AddEditionsResponse struct {
	WorkID   int64            `json:"workId"`
	Editions []SeriesResource `json:"editions"`
}

type LookupResult struct {
	metadataagg.Candidate
	// ExistingSeriesID is the series in the library (one you can see).
	ExistingSeriesID int64 `json:"existingSeriesId,omitempty"`
	// Request is an open request for it.
	Request *LookupRequest `json:"request,omitempty"`
}

// LookupRequest describes an open request for a lookup result.
type LookupRequest struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	// Mine: you asked for it.
	Mine bool `json:"mine"`
}

// lookupIDs are a result's external ids including its own provider's.
func lookupIDs(md metadata.SeriesMetadata) map[string]string {
	ids := map[string]string{}
	for k, v := range md.ExternalIDs {
		ids[k] = v
	}
	if md.Provider != "" && md.ID != "" {
		ids[md.Provider] = md.ID
	}
	return ids
}

// requestedByExternalID finds open requests for lookup results.
func (s *Server) requestedByExternalID(ctx context.Context) func(ids map[string]string) *LookupRequest {
	var open []model.Request
	_ = s.app.DB.NewSelect().Model(&open).Where("status IN (?)", bun.In([]string{model.RequestPending, model.RequestApproved})).Scan(ctx)
	mine := map[int64]bool{}
	if p := access.From(ctx); p != nil && p.Kind == access.KindUser && len(open) > 0 {
		var ids []int64
		_ = s.app.DB.NewSelect().Model((*model.RequestUser)(nil)).Column("request_id").Where("user_id = ?", p.UserID).Scan(ctx, &ids)
		for _, id := range ids {
			mine[id] = true
		}
	}
	return func(ids map[string]string) *LookupRequest {
		for _, r := range open {
			for k, v := range ids {
				if k != "mal" && v != "" && r.Metadata.ExternalIDs[k] == v {
					return &LookupRequest{ID: r.ID, Status: r.Status, Mine: mine[r.ID]}
				}
			}
		}
		return nil
	}
}

// existingByExternalID returns a matcher from external ids to series in the library.
func (s *Server) existingByExternalID(ctx context.Context) func(ids map[string]string) int64 {
	var existing []model.Series
	_ = s.app.DB.NewSelect().Model(&existing).Column("id", "metadata", "tags", "root_folder_id").Where("preview = ?", false).Scan(ctx)
	p := access.From(ctx)
	return func(ids map[string]string) int64 {
		for _, e := range existing {
			if p != nil && !p.Sees(&e) {
				continue // hidden series stay hidden
			}
			for k, v := range ids {
				if k != "mal" && v != "" && e.Metadata.ExternalIDs[k] == v {
					return e.ID
				}
			}
		}
		return 0
	}
}

// LookupResponse is a metadata search's answer.
type LookupResponse struct {
	Results []LookupResult `json:"results"`
	Errors  []string       `json:"errors"`
	// Providers is how many metadata modules are set up; 0 means none is,
	// so an empty result says nothing about the title.
	Providers int `json:"providers"`
	// Modules are the providers that can be searched, by priority.
	Modules []metadataagg.Module `json:"modules" nullable:"false"`
	// ModuleID is the provider the results are from.
	ModuleID int64 `json:"moduleId,omitempty"`
	// Skipped are the providers above it that found nothing.
	Skipped []string `json:"skipped" nullable:"false"`
}

func (s *Server) registerSeriesAdd() {
	tags := []string{"Series"}
	huma.Register(s.api, huma.Operation{OperationID: "series-lookup", Method: http.MethodGet, Path: "/api/v1/series/lookup", Tags: tags,
		Summary: "Search the metadata providers one at a time by priority (or one of them) for a series"},
		func(ctx context.Context, in *struct {
			Query string `query:"q" minLength:"1"`
			Lang  string `query:"lang" required:"false"`
			// ModuleID searches just that provider; without it the providers
			// are asked by priority and the first that has results answers.
			ModuleID int64 `query:"moduleId" required:"false"`
			// without a lang, results are titled in the interface language
			AcceptLanguage string `header:"Accept-Language"`
		}) (*struct{ Body LookupResponse }, error) {
			lang := in.Lang
			if lang == "" {
				lang = uiLanguage(in.AcceptLanguage)
			}
			cands, shown, skipped, errs := s.app.Metadata.SearchFirst(ctx, in.Query, lang, 10, in.ModuleID)
			out := &struct{ Body LookupResponse }{}
			out.Body.Results, out.Body.Errors, out.Body.Skipped = []LookupResult{}, []string{}, skipped
			if out.Body.Skipped == nil {
				out.Body.Skipped = []string{}
			}
			out.Body.Modules = s.app.Metadata.Modules()
			out.Body.Providers = len(out.Body.Modules)
			out.Body.ModuleID = shown
			existing := s.existingByExternalID(ctx)
			requested := s.requestedByExternalID(ctx)
			sc := access.From(ctx).ContentScope()
			for _, c := range cands {
				if sc != nil && !sc.AllowsContent("", c.Adult, append(append([]string{}, c.Genres...), c.Tags...)) {
					continue // past the group's content limits
				}
				out.Body.Results = append(out.Body.Results, LookupResult{Candidate: c, ExistingSeriesID: existing(lookupIDs(c.SeriesMetadata)),
					Request: requested(lookupIDs(c.SeriesMetadata))})
			}
			for _, e := range errs {
				out.Body.Errors = append(out.Body.Errors, e.Error())
			}
			return out, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "series-lookup-get", Method: http.MethodGet, Path: "/api/v1/series/lookup/{moduleId}/{id}", Tags: tags,
		Summary: "Get one metadata result by module and provider id"},
		func(ctx context.Context, in *struct {
			ModuleID int64  `path:"moduleId"`
			ID       string `path:"id"`
			Lang     string `query:"lang" required:"false"`
		}) (*struct{ Body LookupResult }, error) {
			mod, def, err := modules.GetAs[metadata.Module](s.app.Modules, in.ModuleID)
			if err != nil {
				return nil, huma.Error404NotFound(err.Error())
			}
			var md *metadata.SeriesMetadata
			if localized, ok := mod.(metadata.LanguageGetter); ok && in.Lang != "" {
				md, err = localized.GetLanguage(ctx, in.ID, in.Lang)
			} else {
				md, err = mod.Get(ctx, in.ID)
			}
			if err != nil {
				return nil, huma.Error404NotFound(err.Error())
			}
			if sc := access.From(ctx).ContentScope(); sc != nil && !sc.AllowsContent("", md.Adult, append(append([]string{}, md.Genres...), md.Tags...)) {
				return nil, huma.Error404NotFound("not found")
			}
			if md.Adult && s.app.Metadata.AdultHidden(ctx) {
				return nil, huma.Error404NotFound("adult titles are hidden (Settings → Sources)")
			}
			c := metadataagg.Candidate{SeriesMetadata: *md, ModuleID: def.ID, ModuleName: def.Name}
			ids := lookupIDs(*md)
			return &struct{ Body LookupResult }{LookupResult{Candidate: c, ExistingSeriesID: s.existingByExternalID(ctx)(ids), Request: s.requestedByExternalID(ctx)(ids)}}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "series-add", Method: http.MethodPost, Path: "/api/v1/series", Tags: tags},
		func(ctx context.Context, in *struct{ Body series.AddRequest }) (*struct{ Body SeriesResource }, error) {
			ser, err := s.app.Series.Add(ctx, in.Body)
			var exists series.ExistsError
			if err != nil && in.Body.RequestID > 0 && errors.As(err, &exists) {
				// Retrying an add after the series row was committed but request
				// fulfilment failed must finish the link, not create a duplicate.
				ser, err = s.app.Series.Get(ctx, exists.SeriesID)
			}
			if err != nil {
				return nil, seriesError(err)
			}
			if in.Body.RequestID > 0 {
				if err := s.app.Requests.Link(ctx, in.Body.RequestID, ser.ID, access.From(ctx)); err != nil {
					return nil, huma.Error500InternalServerError(fmt.Sprintf(
						"series %d was added, but the request is still pending: %v; retry Add or link it from Requests", ser.ID, err))
				}
			}
			return &struct{ Body SeriesResource }{s.seriesResource(ctx, *ser, nil, true)}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "series-add-editions", Method: http.MethodPost, Path: "/api/v1/series/editions", Tags: tags,
		Summary: "Add a title in one or more languages", Description: "Sources are split by language; each language becomes an edition in its own root folder."},
		func(ctx context.Context, in *struct{ Body series.AddEditionsRequest }) (*struct{ Body AddEditionsResponse }, error) {
			res, err := s.app.Series.AddEditions(ctx, in.Body)
			if err != nil && (res == nil || len(res.Editions) == 0) {
				return nil, seriesError(err)
			}
			if err != nil {
				return nil, huma.Error500InternalServerError(fmt.Sprintf("some editions were added, but not all: %v", err))
			}
			if in.Body.RequestID > 0 {
				if err := s.app.Requests.Link(ctx, in.Body.RequestID, res.Editions[0].ID, access.From(ctx)); err != nil {
					return nil, huma.Error500InternalServerError(fmt.Sprintf(
						"series %d was added, but the request is still pending: %v; retry Add or link it from Requests", res.Editions[0].ID, err))
				}
			}
			out := AddEditionsResponse{WorkID: res.WorkID, Editions: make([]SeriesResource, 0, len(res.Editions))}
			for _, ser := range res.Editions {
				out.Editions = append(out.Editions, s.seriesResource(ctx, *ser, nil, true))
			}
			return &struct{ Body AddEditionsResponse }{out}, nil
		})
}
