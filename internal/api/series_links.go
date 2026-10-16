package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/series"
)

func init() { register((*Server).registerSeriesLinks) }

func (s *Server) registerSeriesLinks() {
	tags := []string{"Series"}
	huma.Register(s.api, huma.Operation{OperationID: "series-source-link", Method: http.MethodPost, Path: "/api/v1/series/{id}/sources", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body series.SourceLink
		}) (*struct{ Body *model.SeriesSource }, error) {
			ss, err := s.app.Series.LinkSource(ctx, in.ID, in.Body)
			if err != nil {
				if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "duplicate") {
					return nil, huma.Error409Conflict("source already linked")
				}
				return nil, seriesError(err)
			}
			return &struct{ Body *model.SeriesSource }{ss}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-source-update", Method: http.MethodPut, Path: "/api/v1/series/{id}/sources/{linkId}", Tags: tags},
		func(ctx context.Context, in *struct {
			ID     int64 `path:"id"`
			LinkID int64 `path:"linkId"`
			Body   series.SourceUpdate
		}) (*struct{ Body *model.SeriesSource }, error) {
			ss, err := s.app.Series.UpdateSource(ctx, in.ID, in.LinkID, in.Body)
			return &struct{ Body *model.SeriesSource }{ss}, seriesError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-source-replace", Method: http.MethodPost, Path: "/api/v1/series/{id}/sources/{linkId}/replace", Tags: tags,
		Summary: "Swap a source link for another manga at the same priority (change a wrong match)"},
		func(ctx context.Context, in *struct {
			ID     int64 `path:"id"`
			LinkID int64 `path:"linkId"`
			Body   series.SourceLink
		}) (*struct{ Body *model.SeriesSource }, error) {
			ss, err := s.app.Series.ReplaceSource(ctx, in.ID, in.LinkID, in.Body)
			if err != nil {
				if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "duplicate") {
					return nil, huma.Error409Conflict("source already linked")
				}
				return nil, seriesError(err)
			}
			return &struct{ Body *model.SeriesSource }{ss}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-source-order", Method: http.MethodPut, Path: "/api/v1/series/{id}/sources/order", Tags: tags,
		Summary: "Set the series' own source order in one step (switches it to a custom order)"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				LinkIDs []int64 `json:"linkIds" minItems:"1"`
			}
		}) (*struct{}, error) {
			return nil, seriesError(s.app.Series.ReorderSources(ctx, in.ID, in.Body.LinkIDs))
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-source-unlink", Method: http.MethodDelete, Path: "/api/v1/series/{id}/sources/{linkId}", Tags: tags},
		func(ctx context.Context, in *struct {
			ID     int64 `path:"id"`
			LinkID int64 `path:"linkId"`
		}) (*struct{}, error) {
			return nil, seriesError(s.app.Series.UnlinkSource(ctx, in.ID, in.LinkID))
		})
}
