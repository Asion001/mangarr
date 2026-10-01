package api

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Asion001/mangarr/internal/catalogs"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/sourcepriority"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"
)

func init() { register((*Server).registerSourcePriorities) }

// priorityLanguage is a lowercase BCP 47-style tag ("en", "pt-br").
var priorityLanguage = regexp.MustCompile(`^[a-z]{2,3}(-[a-z0-9]{2,8})*$`)

type PriorityPreview struct {
	SeriesID     int64                `json:"seriesId"`
	Title        string               `json:"title"`
	PreviousMode string               `json:"previousMode"`
	Sources      []model.SeriesSource `json:"sources"`
}

func (s *Server) registerSourcePriorities() {
	huma.Register(s.api, huma.Operation{OperationID: "source-priorities-list", Method: http.MethodGet, Path: "/api/v1/source-priorities", Tags: []string{"Sources"}}, func(ctx context.Context, _ *struct{}) (*struct{ Body []model.SourcePriorityList }, error) {
		rows := []model.SourcePriorityList{}
		err := s.app.DB.NewSelect().Model(&rows).Order("scope").Scan(ctx)
		return &struct{ Body []model.SourcePriorityList }{rows}, toHTTPError(err)
	})
	huma.Register(s.api, huma.Operation{OperationID: "source-priorities-save", Method: http.MethodPut, Path: "/api/v1/source-priorities", Tags: []string{"Sources"}}, func(ctx context.Context, in *struct {
		Body struct {
			Scope   string   `json:"scope" maxLength:"80"`
			Sources []string `json:"sources" maxItems:"500"`
		}
	}) (*struct{ Body model.SourcePriorityList }, error) {
		scope := in.Body.Scope
		kind, value, ok := strings.Cut(scope, ":")
		if !ok {
			return nil, huma.Error400BadRequest("invalid priority scope")
		}
		switch kind {
		case "language":
			value = sourcepriority.Language(value)
			if !priorityLanguage.MatchString(value) {
				return nil, huma.Error400BadRequest("invalid language")
			}
			scope = sourcepriority.LanguageScope(value)
		case "library":
			id, err := strconv.ParseInt(value, 10, 64)
			if err != nil || id <= 0 {
				return nil, huma.Error400BadRequest("invalid library")
			}
			exists, err := s.app.DB.NewSelect().Model((*model.RootFolder)(nil)).Where("id = ?", id).Exists(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			if !exists {
				return nil, huma.Error404NotFound("library not found")
			}
			scope = sourcepriority.LibraryScope(id)
		default:
			return nil, huma.Error400BadRequest("invalid priority scope")
		}
		seen := map[string]bool{}
		for _, key := range in.Body.Sources {
			id, _, ok := catalogs.ParseKey(key)
			if !ok || id <= 0 || seen[key] {
				return nil, huma.Error400BadRequest("source keys must be unique moduleId:sourceId values")
			}
			seen[key] = true
		}
		if kind == "language" {
			// a language's order is its language default's source list
			src, err := s.app.Settings.Sources(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			i := slices.IndexFunc(src.LanguageDefaults, func(d settings.LanguageDefault) bool { return sourcepriority.Language(d.Language) == value })
			if i < 0 {
				src.LanguageDefaults = append(src.LanguageDefaults, settings.LanguageDefault{Language: value})
				i = len(src.LanguageDefaults) - 1
			}
			src.LanguageDefaults[i].Sources = append([]string{}, in.Body.Sources...)
			if err := s.app.Settings.Set(ctx, settings.KeySources, src); err != nil {
				return nil, toHTTPError(err)
			}
			s.app.Catalogs.Bump()
			return &struct{ Body model.SourcePriorityList }{model.SourcePriorityList{Scope: scope, Sources: src.LanguageDefaults[i].Sources}}, nil
		}
		row := model.SourcePriorityList{Scope: scope, Sources: in.Body.Sources}
		if row.Sources == nil {
			row.Sources = []string{}
		}
		_, err := s.app.DB.NewInsert().Model(&row).On("CONFLICT (scope) DO UPDATE").Set("sources = EXCLUDED.sources").Exec(ctx)
		if err != nil {
			return nil, toHTTPError(err)
		}
		s.app.Catalogs.Bump()
		return &struct{ Body model.SourcePriorityList }{row}, nil
	})
	huma.Register(s.api, huma.Operation{OperationID: "source-priorities-inherit", Method: http.MethodPost, Path: "/api/v1/source-priorities/inherit", Tags: []string{"Sources"}}, func(ctx context.Context, in *struct {
		Body struct {
			SeriesIDs []int64 `json:"seriesIds" minItems:"1" maxItems:"200"`
			DryRun    bool    `json:"dryRun"`
		}
	}) (*struct{ Body []PriorityPreview }, error) {
		var series []model.Series
		if err := s.app.DB.NewSelect().Model(&series).Where("id IN (?)", bun.In(in.Body.SeriesIDs)).Order("id").Scan(ctx); err != nil {
			return nil, toHTTPError(err)
		}
		rows := []PriorityPreview{}
		for _, ser := range series {
			var links []model.SeriesSource
			if err := s.app.DB.NewSelect().Model(&links).Where("series_id = ?", ser.ID).Scan(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			row := PriorityPreview{SeriesID: ser.ID, Title: ser.Title, PreviousMode: ser.SourcePriorityMode}
			ser.SourcePriorityMode = "inherit"
			if err := sourcepriority.Apply(ctx, s.app.DB, ser, links); err != nil {
				return nil, toHTTPError(err)
			}
			row.Sources = links
			if row.Sources == nil {
				row.Sources = []model.SeriesSource{}
			}
			rows = append(rows, row)
		}
		if !in.Body.DryRun {
			if _, err := s.app.DB.NewUpdate().Model((*model.Series)(nil)).Set("source_priority_mode = 'inherit'").Where("id IN (?)", bun.In(in.Body.SeriesIDs)).Exec(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			s.app.Catalogs.Bump()
			s.app.Bus.Changed("series", "updated", 0)
		}
		return &struct{ Body []PriorityPreview }{rows}, nil
	})
}
