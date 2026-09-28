package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/library"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/reading"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"
)

func init() { register((*Server).registerRecycle) }

type RecycledResource struct {
	model.RecycledFile
	Chapter      string             `json:"chapter"`
	Current      *model.ChapterFile `json:"current,omitempty"`
	PurgeAt      *time.Time         `json:"purgeAt,omitempty"`
	CountChanged bool               `json:"countChanged"`
}

type RecycleGroup struct {
	SeriesID    *int64 `json:"seriesId,omitempty"`
	SeriesTitle string `json:"seriesTitle"`
	Count       int    `json:"count"`
	Size        int64  `json:"size"`
}

type RecycleList struct {
	Items  []RecycledResource `json:"items"`
	Groups []RecycleGroup     `json:"groups"`
	// Reasons counts the files per reason under every filter but the reason.
	Reasons         map[string]int `json:"reasons"`
	Total           int            `json:"total"`
	Size            int64          `json:"size"`
	Page            int            `json:"page"`
	PageSize        int            `json:"pageSize"`
	RetentionDays   int            `json:"retentionDays"`
	RetentionLocked bool           `json:"retentionLocked"`
}

type RecycleResult struct {
	ID       int64  `json:"id"`
	JobID    int64  `json:"jobId,omitempty"`
	SeriesID int64  `json:"seriesId,omitempty"`
	Error    string `json:"error,omitempty"`
}

type RecycleIDs struct {
	IDs []int64 `json:"ids" minItems:"1" maxItems:"500"`
}

type RecycleRun struct {
	IDs       []int64              `json:"ids" minItems:"1" maxItems:"500"`
	StartFrom string               `json:"startFrom,omitempty" enum:",recycled,current,download"`
	Release   string               `json:"release,omitempty" enum:",same,best"`
	ProfileID int64                `json:"profileId,omitempty" minimum:"0"`
	Config    *model.ProfileConfig `json:"config,omitempty"`
}

func recycleError(err error) error {
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, os.ErrNotExist) {
		return huma.Error404NotFound("recycled file not found")
	}
	if errors.Is(err, library.ErrRecycleConflict) {
		return huma.Error409Conflict(err.Error())
	}
	return toHTTPError(err)
}

func (s *Server) recycleResources(ctx context.Context, rows []model.RecycledFile) ([]RecycledResource, error) {
	mm, err := s.app.Settings.MediaManagement(ctx)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for _, r := range rows {
		if r.ChapterID != nil {
			ids = append(ids, *r.ChapterID)
		}
	}
	files := []model.ChapterFile{}
	chapters := []model.Chapter{}
	if len(ids) > 0 {
		if err := s.app.DB.NewSelect().Model(&files).Where("chapter_id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
			return nil, err
		}
		if err := s.app.DB.NewSelect().Model(&chapters).Where("id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
			return nil, err
		}
	}
	byChapter := map[int64]*model.ChapterFile{}
	numbers := map[int64]string{}
	for i := range files {
		byChapter[files[i].ChapterID] = &files[i]
	}
	for _, ch := range chapters {
		numbers[ch.ID] = ch.NumberKey
	}
	out := make([]RecycledResource, 0, len(rows))
	for _, r := range rows {
		v := RecycledResource{RecycledFile: r}
		if mm.RecycleBinDays > 0 {
			at := r.RecycledAt.Add(time.Duration(mm.RecycleBinDays) * 24 * time.Hour)
			v.PurgeAt = &at
		}
		if r.ChapterID != nil {
			v.Current, v.Chapter = byChapter[*r.ChapterID], numbers[*r.ChapterID]
		}
		v.CountChanged = v.Current != nil && v.PageCount != v.Current.PageCount
		out = append(out, v)
	}
	return out, nil
}

func (s *Server) registerRecycle() {
	tags := []string{"Recycle bin"}
	huma.Register(s.api, huma.Operation{OperationID: "recycle-list", Method: http.MethodGet, Path: "/api/v1/recycle-bin", Tags: tags},
		func(ctx context.Context, in *struct {
			Series   int64  `query:"series" minimum:"0"`
			Reason   string `query:"reason" enum:",upgraded,reprocessed,cleaned,deleted,series_deleted,restored_over,unknown"`
			Q        string `query:"q" maxLength:"300"`
			Page     int    `query:"page" default:"1" minimum:"1"`
			PageSize int    `query:"pageSize" default:"50" minimum:"1" maximum:"200"`
		}) (*struct{ Body RecycleList }, error) {
			base := func(byReason bool) *bun.SelectQuery {
				q := s.app.DB.NewSelect().Model((*model.RecycledFile)(nil))
				if in.Series > 0 {
					q = q.Where("series_id = ?", in.Series)
				}
				if in.Reason != "" && byReason {
					q = q.Where("reason = ?", in.Reason)
				}
				if in.Q != "" {
					term := "%" + strings.ToLower(strings.TrimSpace(in.Q)) + "%"
					q = q.Where("(LOWER(series_title) LIKE ? OR LOWER(original_relative_path) LIKE ?)", term, term)
				}
				return q
			}
			out := RecycleList{Items: []RecycledResource{}, Groups: []RecycleGroup{}, Page: in.Page, PageSize: in.PageSize}
			mm, err := s.app.Settings.MediaManagement(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out.RetentionDays = mm.RecycleBinDays
			out.RetentionLocked = s.app.Cfg.Env["MANGARR_MEDIA_RECYCLE_BIN_DAYS"] != ""
			if err := base(true).ColumnExpr("series_id, series_title, COUNT(*) AS count, COALESCE(SUM(size), 0) AS size").Group("series_id", "series_title").Order("series_title").Scan(ctx, &out.Groups); err != nil {
				return nil, toHTTPError(err)
			}
			var reasons []struct {
				Reason string `bun:"reason"`
				Count  int    `bun:"count"`
			}
			if err := base(false).ColumnExpr("reason, COUNT(*) AS count").Group("reason").Scan(ctx, &reasons); err != nil {
				return nil, toHTTPError(err)
			}
			out.Reasons = map[string]int{}
			for _, r := range reasons {
				out.Reasons[r.Reason] = r.Count
			}
			for _, g := range out.Groups {
				out.Total += g.Count
				out.Size += g.Size
			}
			var rows []model.RecycledFile
			if err := base(true).Order("series_title", "recycled_at DESC", "id DESC").Limit(in.PageSize).Offset((in.Page-1)*in.PageSize).Scan(ctx, &rows); err != nil {
				return nil, toHTTPError(err)
			}
			out.Items, err = s.recycleResources(ctx, rows)
			return &struct{ Body RecycleList }{out}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "recycle-get", Method: http.MethodGet, Path: "/api/v1/recycle-bin/{id}", Tags: tags},
		func(ctx context.Context, in *IDPath) (*struct{ Body RecycledResource }, error) {
			var row model.RecycledFile
			if err := s.app.DB.NewSelect().Model(&row).Where("id = ?", in.ID).Scan(ctx); err != nil {
				return nil, recycleError(err)
			}
			rows, err := s.recycleResources(ctx, []model.RecycledFile{row})
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &struct{ Body RecycledResource }{rows[0]}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "recycle-rescan", Method: http.MethodPost, Path: "/api/v1/recycle-bin/rescan", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Indexed int `json:"indexed"`
			}
		}, error) {
			n, err := s.app.Library.RescanRecycleBin(ctx)
			out := &struct {
				Body struct {
					Indexed int `json:"indexed"`
				}
			}{}
			out.Body.Indexed = n
			s.app.Bus.Changed("recycle-bin", "sync", 0)
			return out, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "recycle-restore", Method: http.MethodPost, Path: "/api/v1/recycle-bin/restore", Tags: tags},
		func(ctx context.Context, in *struct{ Body RecycleIDs }) (*struct{ Body []RecycleResult }, error) {
			out := []RecycleResult{}
			for _, id := range uniqueRecycleIDs(in.Body.IDs) {
				sid, err := s.app.Library.RestoreRecycled(ctx, id)
				result := RecycleResult{ID: id, SeriesID: sid}
				if err != nil {
					result.Error = err.Error()
				} else {
					_, _ = s.app.Queue.Push(ctx, "DiskScan", map[string]any{"seriesId": sid}, "recycle-restore")
					s.app.Bus.Changed("series", "updated", sid)
					s.app.Bus.Changed("chapter", "sync", 0)
					if series, err := s.app.Series.Get(ctx, sid); err == nil {
						if dir, err := s.app.Library.SeriesDir(ctx, series); err == nil {
							s.app.Bus.Publish(events.Event{Type: downloads.EventFileWritten, SeriesID: sid, Payload: filepath.Join(dir, "restored.cbz")})
						}
					}
				}
				out = append(out, result)
			}
			s.app.Bus.Changed("recycle-bin", "sync", 0)
			return &struct{ Body []RecycleResult }{out}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "recycle-reprocess", Method: http.MethodPost, Path: "/api/v1/recycle-bin/reprocess", Tags: tags},
		func(ctx context.Context, in *struct{ Body RecycleRun }) (*struct{ Body []RecycleResult }, error) {
			out := []RecycleResult{}
			for _, id := range uniqueRecycleIDs(in.Body.IDs) {
				result := RecycleResult{ID: id}
				err := s.app.Library.WithRecycled(ctx, id, func(r *model.RecycledFile, _ string) error {
					job, err := s.queueRecycled(ctx, r, in.Body)
					if job != nil {
						result.JobID = job.ID
					}
					return err
				})
				if err != nil {
					result.Error = err.Error()
				}
				out = append(out, result)
			}
			return &struct{ Body []RecycleResult }{out}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "recycle-delete", Method: http.MethodDelete, Path: "/api/v1/recycle-bin", Tags: tags},
		func(ctx context.Context, in *struct {
			All  bool `query:"all"`
			Body *struct {
				IDs []int64 `json:"ids,omitempty" maxItems:"500"`
			}
		}) (*struct{ Body []RecycleResult }, error) {
			var ids []int64
			if in.Body != nil {
				ids = in.Body.IDs
			}
			if in.All {
				if len(ids) > 0 {
					return nil, huma.Error400BadRequest("choose ids or all")
				}
				if err := s.app.DB.NewSelect().Model((*model.RecycledFile)(nil)).Column("id").Order("id").Scan(ctx, &ids); err != nil {
					return nil, toHTTPError(err)
				}
			} else if len(ids) == 0 {
				return nil, huma.Error400BadRequest("ids are required")
			}
			out := []RecycleResult{}
			for _, id := range uniqueRecycleIDs(ids) {
				result := RecycleResult{ID: id}
				if err := s.app.Library.DeleteRecycled(ctx, id); err != nil {
					result.Error = err.Error()
				}
				out = append(out, result)
			}
			s.app.Bus.Changed("recycle-bin", "sync", 0)
			return &struct{ Body []RecycleResult }{out}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "chapter-versions", Method: http.MethodGet, Path: "/api/v1/chapters/{id}/versions", Tags: tags},
		func(ctx context.Context, in *IDPath) (*struct {
			Body struct {
				Current  *model.ChapterFile `json:"current,omitempty"`
				Recycled []RecycledResource `json:"recycled"`
			}
		}, error) {
			exists, err := s.app.DB.NewSelect().Model((*model.Chapter)(nil)).Where("id = ?", in.ID).Exists(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			if !exists {
				return nil, huma.Error404NotFound("chapter not found")
			}
			out := &struct {
				Body struct {
					Current  *model.ChapterFile `json:"current,omitempty"`
					Recycled []RecycledResource `json:"recycled"`
				}
			}{}
			var current model.ChapterFile
			err = s.app.DB.NewSelect().Model(&current).Where("chapter_id = ?", in.ID).Scan(ctx)
			if err == nil {
				out.Body.Current = &current
			} else if !errors.Is(err, sql.ErrNoRows) {
				return nil, toHTTPError(err)
			}
			rows := []model.RecycledFile{}
			if err := s.app.DB.NewSelect().Model(&rows).Where("chapter_id = ?", in.ID).Order("recycled_at DESC", "id DESC").Scan(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			out.Body.Recycled, err = s.recycleResources(ctx, rows)
			return out, toHTTPError(err)
		})
	s.registerRecycleReader(tags)
}

func uniqueRecycleIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := []int64{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func (s *Server) queueRecycled(ctx context.Context, r *model.RecycledFile, in RecycleRun) (*model.DownloadJob, error) {
	if r.Kind != "file" || r.SeriesID == nil || r.ChapterID == nil {
		return nil, fmt.Errorf("%w: a linked chapter file is required", library.ErrRecycleConflict)
	}
	var series model.Series
	if err := s.app.DB.NewSelect().Model(&series).Where("id = ?", *r.SeriesID).Scan(ctx); err != nil {
		return nil, err
	}
	pid := series.ProfileID
	if in.ProfileID > 0 {
		pid = in.ProfileID
	}
	var profile model.Profile
	if err := s.app.DB.NewSelect().Model(&profile).Where("id = ?", pid).Scan(ctx); err != nil {
		return nil, err
	}
	cfg := profile.Config
	if in.Config != nil {
		cfg = *in.Config
	}
	cfg.ProcessTiming = "inline"
	options := downloads.JobOptions{Config: &cfg, ProfileName: profile.Name}
	kind := model.JobKindReprocess
	var release *int64
	if r.FileSnapshot != nil {
		release = r.FileSnapshot.ReleaseID
	}
	switch in.StartFrom {
	case "", "recycled":
		options.RecycledFileID = &r.ID
	case "current":
		var file model.ChapterFile
		if err := s.app.DB.NewSelect().Model(&file).Where("chapter_id = ?", *r.ChapterID).Scan(ctx); err != nil {
			return nil, err
		}
		release = file.ReleaseID
	case "download":
		kind, options.ForceDownload = model.JobKindDownload, true
		if in.Release == "best" {
			release = nil
		} else {
			if release == nil {
				return nil, fmt.Errorf("%w: original release is unknown", library.ErrRecycleConflict)
			}
			exists, err := s.app.DB.NewSelect().Model((*model.ChapterRelease)(nil)).Where("id = ? AND chapter_id = ? AND removed = ?", *release, *r.ChapterID, false).Exists(ctx)
			if err != nil {
				return nil, err
			}
			if !exists {
				return nil, fmt.Errorf("%w: original release is no longer available", library.ErrRecycleConflict)
			}
			options.PinRelease = true
		}
	}
	job, created, err := s.app.DLQueue.EnqueueConfigured(ctx, series.ID, *r.ChapterID, release, kind, true, 0, options)
	if err == nil && !created {
		return nil, fmt.Errorf("%w: chapter already has an active job", library.ErrRecycleConflict)
	}
	return job, err
}

func (s *Server) registerRecycleReader(tags []string) {
	huma.Register(s.api, huma.Operation{OperationID: "recycle-read", Method: http.MethodGet, Path: "/api/v1/recycle-bin/{id}/read", Tags: tags, Summary: "Read a recycled archive without changing chapter progress"},
		func(ctx context.Context, in *IDPath) (*struct{ Body ReadChapter }, error) {
			out := ReadChapter{ID: in.ID, Downloaded: true, Pages: []ReadPage{}}
			err := s.app.Library.WithRecycled(ctx, in.ID, func(r *model.RecycledFile, path string) error {
				if r.Kind != "file" {
					return huma.Error400BadRequest("only chapter archives can be read")
				}
				out.SeriesTitle = r.SeriesTitle
				if r.SeriesID != nil {
					out.SeriesID = *r.SeriesID
					var series model.Series
					if err := s.app.DB.NewSelect().Model(&series).Where("id = ?", *r.SeriesID).Scan(ctx); err != nil {
						return err
					}
					out.ReadingDirection = series.ReadingDirection
				}
				if r.ChapterID != nil {
					var ch model.Chapter
					if err := s.app.DB.NewSelect().Model(&ch).Where("id = ?", *r.ChapterID).Scan(ctx); err != nil {
						return err
					}
					out.Number, out.Title, out.Volume = ch.NumberKey, ch.Title, ch.Volume
				}
				entries, err := cbz.Entries(path)
				if err != nil {
					return err
				}
				for i, e := range entries {
					out.Pages = append(out.Pages, ReadPage{Number: i + 1, MediaType: recycleMediaType(e.Name), Size: e.Size})
				}
				return nil
			})
			return &struct{ Body ReadChapter }{out}, recycleError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "recycle-read-page", Method: http.MethodGet, Path: "/api/v1/recycle-bin/{id}/pages/{page}", Tags: tags},
		func(ctx context.Context, in *struct {
			ID          int64  `path:"id"`
			Page        int    `path:"page" minimum:"1"`
			IfNoneMatch string `header:"If-None-Match"`
		}) (*huma.StreamResponse, error) {
			var page *reading.PageContent
			err := s.app.Library.WithRecycled(ctx, in.ID, func(r *model.RecycledFile, path string) error {
				if r.Kind != "file" {
					return huma.Error400BadRequest("only chapter archives can be read")
				}
				entries, err := cbz.Entries(path)
				if err != nil {
					return err
				}
				if in.Page > len(entries) {
					return os.ErrNotExist
				}
				entry := entries[in.Page-1]
				body, size, err := cbz.OpenEntry(path, entry.Path)
				if err != nil {
					return err
				}
				page = &reading.PageContent{Body: body, Size: size, ContentType: recycleMediaType(entry.Name), ETag: fmt.Sprintf("\"%s-%d\"", r.SHA256, in.Page)}
				return nil
			})
			if err != nil {
				return nil, recycleError(err)
			}
			return streamImage(page, in.IfNoneMatch, "private, no-cache"), nil
		})
}

func recycleMediaType(name string) string {
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}
