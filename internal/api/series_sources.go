package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/series"
)

func init() {
	register((*Server).registerSeriesSources)
	register((*Server).registerSwitchSources)
	register((*Server).registerSourceUsage)
}

// SourceUsage is how many of the asked-about series link one catalog.
type SourceUsage struct {
	ModuleID   int64  `json:"moduleId" bun:"module_id"`
	SourceID   string `json:"sourceId" bun:"source_id"`
	SourceName string `json:"sourceName" bun:"source_name"`
	Series     int    `json:"series" bun:"series"`
}

func (s *Server) registerSourceUsage() {
	huma.Register(s.api, huma.Operation{OperationID: "series-sources-usage", Method: http.MethodPost, Path: "/api/v1/series/sources/usage", Tags: []string{"Series"},
		Summary: "Which catalogs the given series link, and how many of them each"},
		func(ctx context.Context, in *struct {
			Body struct {
				SeriesIDs []int64 `json:"seriesIds" minItems:"1"`
			}
		}) (*struct{ Body []SourceUsage }, error) {
			out := []SourceUsage{}
			err := s.app.DB.NewSelect().Model((*model.SeriesSource)(nil)).
				ColumnExpr("module_id, source_id, MAX(source_name) AS source_name, COUNT(DISTINCT series_id) AS series").
				Where("series_id IN (?)", bun.List(in.Body.SeriesIDs)).Group("module_id", "source_id").OrderExpr("series DESC, source_name").
				Scan(ctx, &out)
			return &struct{ Body []SourceUsage }{out}, toHTTPError(err)
		})
}

// BulkSourcesOutput is a preview, or the command doing the work.
type BulkSourcesOutput struct {
	// Results is what would happen (a preview), at most PreviewLimit series.
	Results []series.BulkResult `json:"results,omitempty"`
	// Previewed is how many series the preview looked at, of Total.
	Previewed int `json:"previewed,omitempty"`
	Total     int `json:"total"`
	// Command is the queued command when this wasn't a preview.
	Command *model.Command `json:"command,omitempty"`
}

// SwitchSourcesOutput is a plan, or the command carrying it out.
type SwitchSourcesOutput struct {
	// Rows is what would happen to each catalog's links.
	Rows []series.SwitchRow `json:"rows"`
	// Command is the queued command when this wasn't a preview.
	Command *model.Command `json:"command,omitempty"`
}

func (s *Server) registerSeriesSources() {
	tags := []string{"Series"}
	huma.Register(s.api, huma.Operation{OperationID: "series-sources-bulk", Method: http.MethodPost, Path: "/api/v1/series/sources/bulk", Tags: tags,
		Summary: "Add a catalog to many series as a fallback source, or remove or switch one off across them"},
		func(ctx context.Context, in *struct {
			Body struct {
				series.BulkRequest
				// DryRun reports what would happen without changing anything.
				DryRun bool `json:"dryRun,omitempty"`
			}
		}) (*struct{ Body BulkSourcesOutput }, error) {
			req := in.Body.BulkRequest
			total, err := s.app.Series.BulkCount(ctx, req)
			if err != nil {
				return nil, seriesError(err)
			}
			out := BulkSourcesOutput{Total: total}
			if in.Body.DryRun {
				results, err := s.app.Series.BulkSources(ctx, req, true, nil)
				if err != nil {
					return nil, seriesError(err)
				}
				out.Results, out.Previewed = results, len(results)
				return &struct{ Body BulkSourcesOutput }{out}, nil
			}
			cmd, err := s.app.Queue.Push(ctx, "SeriesSources", map[string]any{
				"action": req.Action, "moduleId": req.ModuleID, "sourceId": req.SourceID, "seriesIds": req.SeriesIDs,
				"tagId": req.TagID, "rootFolderId": req.RootFolderID, "monitoredOnly": req.MonitoredOnly,
				"fromModuleId": req.FromModuleID, "fromSourceId": req.FromSourceID, "picks": req.Picks,
			}, "manual")
			if err != nil {
				return nil, toHTTPError(err)
			}
			out.Command = cmd
			return &struct{ Body BulkSourcesOutput }{out}, nil
		})
}

func (s *Server) registerSwitchSources() {
	huma.Register(s.api, huma.Operation{OperationID: "series-sources-switch", Method: http.MethodPost,
		Path: "/api/v1/series/sources/switch", Tags: []string{"Series"},
		Summary: "Move a library's source links from one source module to another"},
		func(ctx context.Context, in *struct {
			Body struct {
				series.SwitchRequest
				// DryRun reports the plan without changing anything.
				DryRun bool `json:"dryRun,omitempty"`
			}
		}) (*struct{ Body SwitchSourcesOutput }, error) {
			req := in.Body.SwitchRequest
			rows, err := s.app.Series.SwitchPlan(ctx, req)
			if err != nil {
				return nil, seriesError(err)
			}
			out := SwitchSourcesOutput{Rows: rows}
			if in.Body.DryRun {
				return &struct{ Body SwitchSourcesOutput }{out}, nil
			}
			cmd, err := s.app.Queue.Push(ctx, "SwitchSourceModule", map[string]any{
				"fromModuleId": req.FromModuleID, "toModuleId": req.ToModuleID}, "manual")
			if err != nil {
				return nil, toHTTPError(err)
			}
			out.Command = cmd
			return &struct{ Body SwitchSourcesOutput }{out}, nil
		})
}
