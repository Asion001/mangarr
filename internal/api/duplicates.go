package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/duplicates"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
)

func init() { register((*Server).registerDuplicates) }

// DuplicateTitle is one side of a finding, as the Duplicates page shows it.
type DuplicateTitle struct {
	ID          int64    `json:"id"`
	WorkID      int64    `json:"workId,omitempty"`
	Title       string   `json:"title"`
	Language    string   `json:"language"`
	CoverURL    string   `json:"coverUrl"`
	AddedAt     string   `json:"addedAt"`
	Chapters    int      `json:"chapters"`
	Files       int      `json:"files"`
	Sources     []string `json:"sources" nullable:"false"`
	Authors     []string `json:"authors,omitempty"`
	HasMetadata bool     `json:"hasMetadata"`
}

type DuplicateFinding struct {
	duplicates.Finding
	A DuplicateTitle  `json:"a"`
	B *DuplicateTitle `json:"b,omitempty"`
}

type DuplicatesList struct {
	Findings []DuplicateFinding `json:"findings" nullable:"false"`
	// Scanning is true while a scan is queued or running.
	Scanning bool `json:"scanning"`
}

func duplicatesError(err error) error {
	if errors.Is(err, duplicates.ErrNotFound) {
		return huma.Error404NotFound(err.Error())
	}
	return seriesError(err)
}

func (s *Server) registerDuplicates() {
	tags := []string{"Duplicates"}
	huma.Register(s.api, huma.Operation{OperationID: "duplicates-list", Method: http.MethodGet, Path: "/api/v1/duplicates", Tags: tags,
		Summary: "What the duplicate scan found: titles that look like one work, and titles without metadata"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body DuplicatesList }, error) {
			list, err := s.app.Duplicates.List(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			titles, err := s.duplicateTitles(ctx, list)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := DuplicatesList{Findings: []DuplicateFinding{}}
			for _, f := range list {
				a, ok := titles[f.SeriesA]
				if !ok {
					continue // deleted since
				}
				d := DuplicateFinding{Finding: f, A: a}
				if f.Kind == duplicates.KindPair {
					b, ok := titles[f.SeriesB]
					if !ok {
						continue
					}
					d.B = &b
				}
				out.Findings = append(out.Findings, d)
			}
			n, err := s.app.DB.NewSelect().Model((*model.Command)(nil)).
				Where("name = ? AND status IN (?)", "FindDuplicates", bun.In([]string{model.CommandQueued, model.CommandStarted})).Count(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out.Scanning = n > 0
			return &struct{ Body DuplicatesList }{out}, nil
		})
	type commandResult struct{ Body *model.Command }
	huma.Register(s.api, huma.Operation{OperationID: "duplicates-scan", Method: http.MethodPost, Path: "/api/v1/duplicates/scan", Tags: tags,
		Summary: "Scan the library for duplicates now; with seriesIds, look those titles up at the metadata providers again"},
		func(ctx context.Context, in *struct {
			Body struct {
				SeriesIDs []int64 `json:"seriesIds,omitempty"`
			}
		}) (*commandResult, error) {
			var body map[string]any
			if len(in.Body.SeriesIDs) > 0 {
				body = map[string]any{"seriesIds": in.Body.SeriesIDs}
			}
			c, err := s.app.Queue.Push(ctx, "FindDuplicates", body, "manual")
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			return &commandResult{c}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "duplicates-group", Method: http.MethodPost, Path: "/api/v1/duplicates/{id}/group", Tags: tags,
		Summary: "Make the pair one title and link the metadata found for it (or the given match)"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Match *metadataagg.Ref `json:"match,omitempty"`
			}
		}) (*struct{}, error) {
			return nil, duplicatesError(s.app.Duplicates.Group(ctx, in.ID, in.Body.Match))
		})
	huma.Register(s.api, huma.Operation{OperationID: "duplicates-merge", Method: http.MethodPost, Path: "/api/v1/duplicates/{id}/merge", Tags: tags,
		Summary: "Merge a same-language pair into keepId: sources, files it lacks and progress move over, the other title is deleted"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				KeepID int64 `json:"keepId"`
			}
		}) (*struct{}, error) {
			return nil, duplicatesError(s.app.Duplicates.Merge(ctx, in.ID, in.Body.KeepID))
		})
	huma.Register(s.api, huma.Operation{OperationID: "duplicates-dismiss", Method: http.MethodPut, Path: "/api/v1/duplicates/{id}/dismissed", Tags: tags,
		Summary: "Mark a finding as not duplicates (or bring it back)"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Dismissed bool `json:"dismissed"`
			}
		}) (*struct{}, error) {
			return nil, duplicatesError(s.app.Duplicates.SetDismissed(ctx, in.ID, in.Body.Dismissed))
		})
	huma.Register(s.api, huma.Operation{OperationID: "duplicates-link", Method: http.MethodPost, Path: "/api/v1/duplicates/link", Tags: tags,
		Summary: "Link the metadata found for these titles"},
		func(ctx context.Context, in *struct {
			Body struct {
				SeriesIDs []int64 `json:"seriesIds" minItems:"1"`
			}
		}) (*struct {
			Body struct {
				Linked int `json:"linked"`
			}
		}, error) {
			n, err := s.app.Duplicates.Link(ctx, in.Body.SeriesIDs)
			out := &struct {
				Body struct {
					Linked int `json:"linked"`
				}
			}{}
			out.Body.Linked = n
			if err != nil && n == 0 {
				return nil, duplicatesError(err)
			}
			return out, nil
		})
}

// duplicateTitles describes the findings' titles.
func (s *Server) duplicateTitles(ctx context.Context, list []duplicates.Finding) (map[int64]DuplicateTitle, error) {
	ids := []int64{}
	for _, f := range list {
		ids = append(ids, f.SeriesA)
		if f.SeriesB != 0 {
			ids = append(ids, f.SeriesB)
		}
	}
	out := map[int64]DuplicateTitle{}
	if len(ids) == 0 {
		return out, nil
	}
	var series []model.Series
	if err := s.app.DB.NewSelect().Model(&series).Where("id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
		return nil, err
	}
	var sources []model.SeriesSource
	if err := s.app.DB.NewSelect().Model(&sources).Column("series_id", "source_name").Where("series_id IN (?)", bun.In(ids)).Order("priority", "id").Scan(ctx); err != nil {
		return nil, err
	}
	var counts []struct {
		SeriesID int64 `bun:"series_id"`
		Chapters int   `bun:"chapters"`
		Files    int   `bun:"files"`
	}
	if err := s.app.DB.NewSelect().TableExpr("chapters").ColumnExpr("series_id").ColumnExpr("COUNT(*) AS chapters").
		ColumnExpr("SUM(CASE WHEN file_id IS NOT NULL THEN 1 ELSE 0 END) AS files").
		Where("series_id IN (?)", bun.In(ids)).GroupExpr("series_id").Scan(ctx, &counts); err != nil {
		return nil, err
	}
	for _, ser := range series {
		has := false
		for _, v := range ser.Metadata.ExternalIDs {
			has = has || v != ""
		}
		out[ser.ID] = DuplicateTitle{ID: ser.ID, WorkID: ser.WorkID, Title: ser.Title, Language: ser.Language, CoverURL: seriesCoverURL(ser),
			AddedAt: ser.AddedAt.UTC().Format("2006-01-02T15:04:05Z"), Sources: []string{}, Authors: ser.Metadata.Authors, HasMetadata: has}
	}
	for _, src := range sources {
		if t, ok := out[src.SeriesID]; ok && src.SourceName != "" {
			t.Sources = append(t.Sources, src.SourceName)
			out[src.SeriesID] = t
		}
	}
	for _, c := range counts {
		if t, ok := out[c.SeriesID]; ok {
			t.Chapters, t.Files = c.Chapters, c.Files
			out[c.SeriesID] = t
		}
	}
	return out, nil
}
