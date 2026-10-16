package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/naming"
	"github.com/Asion001/mangarr/internal/organize"
	"github.com/Asion001/mangarr/internal/series"
)

func init() { register((*Server).registerSeries) }

func seriesError(err error) error {
	var ve series.ValidationError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ve):
		return huma.Error400BadRequest(ve.Msg)
	case errors.Is(err, series.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, series.ErrExists):
		return huma.Error409Conflict(err.Error())
	}
	return toHTTPError(err)
}

// moveAfterUpdate queues a MoveSeries command when the root folder or folder
// changed, or when the title changed and folders follow titles.
func (s *Server) moveAfterUpdate(ctx context.Context, before, after model.Series, req series.UpdateRequest) error {
	move := organize.MoveRequest{SeriesID: after.ID, MoveFiles: req.MoveFiles == nil || *req.MoveFiles}
	if req.RootFolderID != nil && *req.RootFolderID != before.RootFolderID {
		if _, err := s.app.Library.RootFolder(ctx, *req.RootFolderID); err != nil {
			return huma.Error400BadRequest("unknown root folder")
		}
		move.RootFolderID = *req.RootFolderID
	}
	if req.Path != nil && strings.TrimSpace(*req.Path) != "" && *req.Path != before.Path {
		move.Path = *req.Path
	} else if before.Title != after.Title {
		if mm, _ := s.app.Settings.MediaManagement(ctx); mm.RenameFolderOnTitleChange {
			if name := naming.Sanitize(s.app.Library.FolderName(ctx, after.Title, after.Metadata.Year)); name != "" && name != before.Path {
				move.Path = name
			}
		}
	}
	if move.RootFolderID == 0 && move.Path == "" {
		return nil
	}
	_, err := s.app.Queue.Push(ctx, "MoveSeries", toBody(move), "series-edit")
	return toHTTPError(err)
}

// toBody converts a request struct to a command body.
func toBody(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// visibleSeries loads a series the caller may see (404 otherwise, so hidden
// series don't reveal they exist).
func (s *Server) visibleSeries(ctx context.Context, id int64) (*model.Series, error) {
	ser, err := s.app.Series.Get(ctx, id)
	if err != nil {
		return nil, seriesError(err)
	}
	if !access.From(ctx).Sees(ser) {
		return nil, huma.Error404NotFound("series not found")
	}
	return ser, nil
}

func (s *Server) registerSeries() {
	tags := []string{"Series"}
	huma.Register(s.api, huma.Operation{OperationID: "series-list", Method: http.MethodGet, Path: "/api/v1/series", Tags: tags},
		func(ctx context.Context, in *struct {
			AcceptLanguage string `header:"Accept-Language"`
		}) (*struct{ Body []SeriesResource }, error) {
			var list []model.Series
			if err := s.app.DB.NewSelect().Model(&list).Where("preview = ?", false).Order("sort_title").Scan(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			out, err := s.groupedSeriesResources(ctx, list, uiLanguage(in.AcceptLanguage))
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &struct{ Body []SeriesResource }{out}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "series-get", Method: http.MethodGet, Path: "/api/v1/series/{id}", Tags: tags},
		func(ctx context.Context, in *IDPath) (*struct{ Body SeriesResource }, error) {
			ser, err := s.visibleSeries(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if ser.Preview {
				_ = s.app.Series.TouchPreview(ctx, ser.ID) // opened: keep it
			}
			stats, err := s.seriesStats(ctx, in.ID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			r := s.seriesResource(ctx, *ser, stats, true)
			r.Following = s.follows(ctx)[ser.ID]
			r.WorkTitle, r.Editions = s.workContext(ctx, ser.ID)
			return &struct{ Body SeriesResource }{r}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "series-update", Method: http.MethodPut, Path: "/api/v1/series/{id}", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body series.UpdateRequest
		}) (*struct{ Body SeriesResource }, error) {
			var before model.Series
			if err := s.app.DB.NewSelect().Model(&before).Where("id = ?", in.ID).Scan(ctx); err != nil {
				return nil, huma.Error404NotFound("series not found")
			}
			ser, err := s.app.Series.Update(ctx, in.ID, in.Body)
			if err != nil {
				return nil, seriesError(err)
			}
			if in.Body.ProfileID != nil {
				s.app.PushProcessBacklog("series-profile") // the new profile may process differently
			}
			if err := s.moveAfterUpdate(ctx, before, *ser, in.Body); err != nil {
				return nil, err
			}
			stats, _ := s.seriesStats(ctx, in.ID)
			return &struct{ Body SeriesResource }{s.seriesResource(ctx, *ser, stats, true)}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "series-delete", Method: http.MethodDelete, Path: "/api/v1/series/{id}", Tags: tags},
		func(ctx context.Context, in *struct {
			ID          int64 `path:"id"`
			DeleteFiles bool  `query:"deleteFiles"`
		}) (*struct{}, error) {
			return nil, seriesError(s.app.Series.Delete(ctx, in.ID, in.DeleteFiles))
		})

	type commandResult struct{ Body *model.Command }
	push := func(ctx context.Context, name string, body map[string]any) (*commandResult, error) {
		c, err := s.app.Queue.Push(ctx, name, body, "manual")
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		return &commandResult{c}, nil
	}
	huma.Register(s.api, huma.Operation{OperationID: "series-refresh", Method: http.MethodPost, Path: "/api/v1/series/{id}/refresh", Tags: tags},
		func(ctx context.Context, in *IDPath) (*commandResult, error) {
			return push(ctx, "RefreshSeries", map[string]any{"seriesId": in.ID})
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-search", Method: http.MethodPost, Path: "/api/v1/series/{id}/search", Tags: tags,
		Summary: "Grab missing chapters. With chapterIds, only those (explicit search ignores monitoring)."},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body *struct {
				ChapterIDs []int64 `json:"chapterIds,omitempty"`
			}
		}) (*commandResult, error) {
			body := map[string]any{"seriesId": in.ID}
			if in.Body != nil && len(in.Body.ChapterIDs) > 0 {
				body["chapterIds"], body["explicit"] = in.Body.ChapterIDs, true
			}
			return push(ctx, "SearchMissing", body)
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-metadata-refresh", Method: http.MethodPost, Path: "/api/v1/series/{id}/metadata/refresh", Tags: tags},
		func(ctx context.Context, in *IDPath) (*commandResult, error) {
			return push(ctx, "RefreshMetadata", map[string]any{"seriesId": in.ID})
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-metadata-link", Method: http.MethodPut, Path: "/api/v1/series/{id}/metadata", Tags: tags,
		Summary: "Replace the metadata match of a series"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body metadataagg.Ref
		}) (*struct{ Body SeriesResource }, error) {
			ser, err := s.app.Series.LinkMetadata(ctx, in.ID, in.Body)
			if err != nil {
				return nil, seriesError(err)
			}
			return &struct{ Body SeriesResource }{s.seriesResource(ctx, *ser, nil, true)}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "series-cover", Method: http.MethodGet, Path: "/api/v1/series/{id}/cover", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   int64  `path:"id"`
			V    string `query:"v"`
			Size string `query:"size" enum:",full" doc:"full = the library's cover.jpg as is (default: a resized copy)"`
		}) (*imageOutput, error) {
			ser, err := s.visibleSeries(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if in.Size == "" {
				// a signed link a CDN may cache; the browser keeps the redirect
				version := in.V
				if version == "" {
					version = strconv.FormatInt(ser.UpdatedAt.Unix(), 10)
				}
				if u := s.signedCoverURL(ctx, ser.ID, version); u != "" {
					return &imageOutput{Status: http.StatusFound, Location: s.app.Cfg.URLBase + "/" + u, CacheControl: coverCache(in.V)}, nil
				}
			}
			if in.Size == "full" {
				if p := s.app.Library.CoverPath(ctx, ser); p != "" {
					if data, err := os.ReadFile(p); err == nil {
						return imageReply(ctx, data, http.DetectContentType(data), coverCache(in.V)), nil
					}
				}
			}
			data, ct, err := s.app.Reading.Cover(ctx, ser)
			if err != nil {
				return nil, huma.Error404NotFound("no cover")
			}
			return imageReply(ctx, data, ct, coverCache(in.V)), nil
		})
}
