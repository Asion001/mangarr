package api

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/decision"
	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/model"
)

func init() { register((*Server).registerSeriesChapters) }

type ReleaseView struct {
	model.ChapterRelease
	SourceName  string `json:"sourceName"`
	Priority    int    `json:"priority"`
	Blocklisted bool   `json:"blocklisted"`
}

type ReadStateView struct {
	ReaderID  int64      `json:"readerId"`
	Reader    string     `json:"reader"`
	Completed bool       `json:"completed"`
	Page      int        `json:"page"`
	ReadAt    *time.Time `json:"readAt,omitempty"`
}

type ChapterResource struct {
	model.Chapter
	File     *model.ChapterFile `json:"file,omitempty"`
	Releases []ReleaseView      `json:"releases"`
	ReadBy   []ReadStateView    `json:"readBy"`
	Job      *model.DownloadJob `json:"job,omitempty"`
}

func (s *Server) chapterResources(ctx context.Context, seriesID int64) ([]ChapterResource, error) {
	db := s.app.DB
	var chapters []model.Chapter
	if err := db.NewSelect().Model(&chapters).Where("series_id = ?", seriesID).Order("number_sort DESC").Scan(ctx); err != nil {
		return nil, err
	}
	var files []model.ChapterFile
	if err := db.NewSelect().Model(&files).Where("series_id = ?", seriesID).Scan(ctx); err != nil {
		return nil, err
	}
	var sources []model.SeriesSource
	if err := db.NewSelect().Model(&sources).Where("series_id = ?", seriesID).Scan(ctx); err != nil {
		return nil, err
	}
	var rels []model.ChapterRelease
	if err := db.NewSelect().Model(&rels).Where("series_id = ?", seriesID).Order("id").Scan(ctx); err != nil {
		return nil, err
	}
	var bl []model.Blocklist
	_ = db.NewSelect().Model(&bl).Where("series_id = ?", seriesID).Scan(ctx)
	var jobs []model.DownloadJob
	_ = db.NewSelect().Model(&jobs).Where("series_id = ?", seriesID).Order("id").Scan(ctx)
	type rs struct {
		model.ChapterReadState
		Name string `bun:"name"`
	}
	var reads []rs
	_ = db.NewSelect().TableExpr("chapter_read_states AS r").ColumnExpr("r.*, rd.name").
		Join("JOIN readers AS rd ON rd.id = r.reader_id").Where("r.series_id = ?", seriesID).Scan(ctx, &reads)

	fileBy := map[int64]*model.ChapterFile{}
	for i := range files {
		fileBy[files[i].ChapterID] = &files[i]
	}
	srcBy := map[int64]model.SeriesSource{}
	for _, ss := range sources {
		srcBy[ss.ID] = ss
	}
	blocked := map[string]bool{}
	for _, b := range bl {
		blocked[strconv.FormatInt(b.SeriesSourceID, 10)+"|"+b.ChapterURL] = true
	}
	relBy := map[int64][]ReleaseView{}
	for _, r := range rels {
		if r.ChapterID == nil {
			continue
		}
		ss := srcBy[r.SeriesSourceID]
		relBy[*r.ChapterID] = append(relBy[*r.ChapterID], ReleaseView{ChapterRelease: r, SourceName: ss.SourceName, Priority: ss.Priority,
			Blocklisted: blocked[strconv.FormatInt(r.SeriesSourceID, 10)+"|"+r.ChapterURL]})
	}
	jobBy := map[int64]*model.DownloadJob{}
	activeStatuses := downloads.ActiveStatuses()
	for i := range jobs {
		previous := jobBy[jobs[i].ChapterID]
		if previous == nil || !slices.Contains(activeStatuses, previous.Status) {
			jobBy[jobs[i].ChapterID] = &jobs[i] // active job wins; otherwise the latest
		}
	}
	readBy := map[int64][]ReadStateView{}
	own, onlyOwn := ownReaderOnly(ctx)
	labels := s.progressReaderLabels(ctx)
	for _, r := range reads {
		if onlyOwn && r.ReaderID != own {
			continue
		}
		readBy[r.ChapterID] = append(readBy[r.ChapterID], ReadStateView{ReaderID: r.ReaderID,
			Reader: progressReaderLabel(ctx, labels, r.ReaderID, r.Name), Completed: r.Completed, Page: r.Page, ReadAt: r.ReadAt})
	}
	out := make([]ChapterResource, 0, len(chapters))
	for _, ch := range chapters {
		cr := ChapterResource{Chapter: ch, File: fileBy[ch.ID], Releases: relBy[ch.ID], ReadBy: readBy[ch.ID], Job: jobBy[ch.ID]}
		if cr.Releases == nil {
			cr.Releases = []ReleaseView{}
		}
		if cr.ReadBy == nil {
			cr.ReadBy = []ReadStateView{}
		}
		out = append(out, cr)
	}
	return out, nil
}

func (s *Server) registerSeriesChapters() {
	tags := []string{"Series"}
	huma.Register(s.api, huma.Operation{OperationID: "series-chapters", Method: http.MethodGet, Path: "/api/v1/series/{id}/chapters", Tags: tags},
		func(ctx context.Context, in *IDPath) (*struct{ Body []ChapterResource }, error) {
			if _, err := s.visibleSeries(ctx, in.ID); err != nil {
				return nil, err
			}
			out, err := s.chapterResources(ctx, in.ID)
			return &struct{ Body []ChapterResource }{out}, toHTTPError(err)
		})

	huma.Register(s.api, huma.Operation{OperationID: "chapters-monitor", Method: http.MethodPut, Path: "/api/v1/chapters/monitor", Tags: tags},
		func(ctx context.Context, in *struct {
			Body struct {
				ChapterIDs []int64 `json:"chapterIds" minItems:"1"`
				Monitored  bool    `json:"monitored"`
			}
		}) (*struct{}, error) {
			_, err := s.app.DB.NewUpdate().Model((*model.Chapter)(nil)).Set("monitored = ?", in.Body.Monitored).
				Set("updated_at = ?", time.Now().UTC()).Where("id IN (?)", bun.In(in.Body.ChapterIDs)).Exec(ctx)
			s.app.Bus.Changed("chapter", "updated", 0)
			return nil, toHTTPError(err)
		})

	huma.Register(s.api, huma.Operation{OperationID: "chapter-decision", Method: http.MethodGet, Path: "/api/v1/series/{id}/chapters/{chapterId}/decision", Tags: tags,
		Summary: "Explain why a chapter would or would not be downloaded"},
		func(ctx context.Context, in *struct {
			ID        int64 `path:"id"`
			ChapterID int64 `path:"chapterId"`
		}) (*struct {
			Body struct {
				Decision *decision.Decision `json:"decision"`
				Approved *ReleaseView       `json:"approved,omitempty"`
			}
		}, error) {
			d, best, err := s.app.Searcher.Explain(ctx, in.ID, in.ChapterID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &struct {
				Body struct {
					Decision *decision.Decision `json:"decision"`
					Approved *ReleaseView       `json:"approved,omitempty"`
				}
			}{}
			if d == nil {
				d = &decision.Decision{}
			}
			if d.Rejections == nil {
				d.Rejections = []decision.Rejection{}
			}
			out.Body.Decision = d
			if best != nil {
				out.Body.Approved = &ReleaseView{ChapterRelease: best.Release, SourceName: best.Source.SourceName, Priority: best.Source.Priority}
			}
			return out, nil
		})
}
