package api

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/organize"
)

func init() { register((*Server).registerOrganize) }

// EditorRequest edits many series at once; nil fields stay unchanged.
type EditorRequest struct {
	SeriesIDs    []int64 `json:"seriesIds" minItems:"1"`
	Monitored    *bool   `json:"monitored,omitempty"`
	MonitorNew   *string `json:"monitorNew,omitempty" enum:"all,none"`
	ProfileID    *int64  `json:"profileId,omitempty"`
	RootFolderID *int64  `json:"rootFolderId,omitempty"`
	// MoveFiles applies to root folder changes (default true).
	MoveFiles *bool   `json:"moveFiles,omitempty"`
	Tags      []int64 `json:"tags,omitempty"`
	TagMode   string  `json:"tagMode,omitempty" enum:",add,remove,replace"`
}

type EditorResult struct {
	Updated int `json:"updated"`
	// Moves are queued MoveSeries commands.
	Moves int `json:"moves"`
}

func (s *Server) registerOrganize() {
	tags := []string{"Series"}
	huma.Register(s.api, huma.Operation{OperationID: "series-editor", Method: http.MethodPost, Path: "/api/v1/series/editor", Tags: tags,
		Summary: "Edit many series at once (monitoring, profile, tags, root folder)"},
		func(ctx context.Context, in *struct{ Body EditorRequest }) (*struct{ Body EditorResult }, error) {
			req := in.Body
			var list []model.Series
			if err := s.app.DB.NewSelect().Model(&list).Where("id IN (?)", bun.List(req.SeriesIDs)).Scan(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			if req.ProfileID != nil {
				if n, _ := s.app.DB.NewSelect().Model((*model.Profile)(nil)).Where("id = ?", *req.ProfileID).Count(ctx); n == 0 {
					return nil, huma.Error400BadRequest("unknown profile")
				}
			}
			if req.RootFolderID != nil {
				if _, err := s.app.Library.RootFolder(ctx, *req.RootFolderID); err != nil {
					return nil, huma.Error400BadRequest("unknown root folder")
				}
			}
			var res EditorResult
			now := time.Now().UTC()
			err := s.app.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				for i := range list {
					ser := &list[i]
					cols := []string{"updated_at"}
					if req.Monitored != nil {
						ser.Monitored = *req.Monitored
						cols = append(cols, "monitored")
					}
					if req.MonitorNew != nil {
						ser.MonitorNew = *req.MonitorNew
						cols = append(cols, "monitor_new")
					}
					if req.ProfileID != nil {
						ser.ProfileID = *req.ProfileID
						cols = append(cols, "profile_id")
					}
					if req.TagMode != "" {
						switch req.TagMode {
						case "replace":
							ser.Tags = append([]int64{}, req.Tags...)
						case "add":
							for _, t := range req.Tags {
								if !slices.Contains(ser.Tags, t) {
									ser.Tags = append(ser.Tags, t)
								}
							}
						case "remove":
							ser.Tags = slices.DeleteFunc(ser.Tags, func(t int64) bool { return slices.Contains(req.Tags, t) })
						}
						if ser.Tags == nil {
							ser.Tags = []int64{}
						}
						cols = append(cols, "tags")
					}
					ser.UpdatedAt = now
					if _, err := tx.NewUpdate().Model(ser).Column(cols...).WherePK().Exec(ctx); err != nil {
						return err
					}
					res.Updated++
				}
				return nil
			})
			if err != nil {
				return nil, toHTTPError(err)
			}
			if req.RootFolderID != nil {
				for _, ser := range list {
					if ser.RootFolderID == *req.RootFolderID {
						continue
					}
					move := organize.MoveRequest{SeriesID: ser.ID, RootFolderID: *req.RootFolderID, MoveFiles: req.MoveFiles == nil || *req.MoveFiles}
					if _, err := s.app.Queue.Push(ctx, "MoveSeries", toBody(move), "editor"); err != nil {
						return nil, toHTTPError(err)
					}
					res.Moves++
				}
			}
			if req.ProfileID != nil {
				s.app.PushProcessBacklog("series-profile")
			}
			s.app.Bus.Changed("series", "updated", 0)
			return &struct{ Body EditorResult }{res}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "series-rename-preview", Method: http.MethodPost, Path: "/api/v1/series/rename/preview", Tags: tags,
		Summary: "Files (and folders) that don't match the current naming format"},
		func(ctx context.Context, in *struct {
			Body struct {
				SeriesIDs []int64 `json:"seriesIds" minItems:"1"`
				Folders   bool    `json:"folders"`
			}
		}) (*struct{ Body []organize.SeriesRename }, error) {
			plan, err := s.app.Organize.Preview(ctx, in.Body.SeriesIDs, in.Body.Folders)
			return &struct{ Body []organize.SeriesRename }{plan}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-rename", Method: http.MethodPost, Path: "/api/v1/series/rename", Tags: tags,
		Summary: "Rename files (and folders) to the current naming format; read progress is restored afterwards"},
		func(ctx context.Context, in *struct {
			Body struct {
				SeriesIDs []int64 `json:"seriesIds" minItems:"1"`
				Folders   bool    `json:"folders"`
			}
		}) (*struct{ Body *model.Command }, error) {
			ids := make([]any, len(in.Body.SeriesIDs))
			for i, id := range in.Body.SeriesIDs {
				ids[i] = id
			}
			c, err := s.app.Queue.Push(ctx, "RenameFiles", map[string]any{"seriesIds": ids, "folders": in.Body.Folders}, "manual")
			return &struct{ Body *model.Command }{c}, toHTTPError(err)
		})

	huma.Register(s.api, huma.Operation{OperationID: "rootfolders-move", Method: http.MethodPut, Path: "/api/v1/rootfolders/{id}", Tags: []string{"Root folders"},
		Summary: "Change a root folder's location: move its series there, or only update the path (files moved by hand)"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Path      string `json:"path" minLength:"1"`
				MoveFiles bool   `json:"moveFiles"`
			}
		}) (*struct{ Body *model.Command }, error) {
			rf, err := s.app.Library.RootFolder(ctx, in.ID)
			if err != nil {
				return nil, huma.Error404NotFound("root folder not found")
			}
			if rf.ManagedBy != "" {
				return nil, huma.Error409Conflict("this root folder is set by MANGARR_ROOT_FOLDERS")
			}
			path := strings.TrimSpace(in.Body.Path)
			if n, _ := s.app.DB.NewSelect().Model((*model.RootFolder)(nil)).Where("path = ? AND id <> ?", path, in.ID).Count(ctx); n > 0 {
				return nil, huma.Error409Conflict("another root folder already uses this path")
			}
			c, err := s.app.Queue.Push(ctx, "MoveRootFolder", map[string]any{"rootFolderId": in.ID, "path": path, "moveFiles": in.Body.MoveFiles}, "manual")
			return &struct{ Body *model.Command }{c}, toHTTPError(err)
		})
}
