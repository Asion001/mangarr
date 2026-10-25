package api

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"
)

func init() { register((*Server).registerReadPicker) }

type ReadChapterItem struct {
	ID         int64   `json:"id"`
	Number     string  `json:"number"`
	NumberSort float64 `json:"numberSort"`
	Volume     string  `json:"volume,omitempty"`
	Title      string  `json:"title,omitempty"`
	Available  bool    `json:"available"`
	Downloaded bool    `json:"downloaded"`
	Completed  bool    `json:"completed"`
	Page       int     `json:"page,omitempty"`
}

// ReadChapterRef points at one chapter the picker offers a shortcut to.
type ReadChapterRef struct {
	ID        int64  `json:"id"`
	Number    string `json:"number"`
	Available bool   `json:"available"`
}

type ReadChapterPickerResponse struct {
	Items    []ReadChapterItem `json:"items"`
	Page     int               `json:"page"`
	PageSize int               `json:"pageSize"`
	Total    int               `json:"total"`
	// First, Latest and UpNext are the shortcuts above the list, whatever the search.
	First  *ReadChapterRef `json:"first,omitempty"`
	Latest *ReadChapterRef `json:"latest,omitempty"`
	// UpNext is the first chapter after the highest one this reader finished.
	UpNext *ReadChapterRef `json:"upNext,omitempty"`
	// Match is the chapter whose number is the search, to go straight to.
	Match *ReadChapterRef `json:"match,omitempty"`
}

func chapterRef(chapter model.Chapter) *ReadChapterRef {
	return &ReadChapterRef{ID: chapter.ID, Number: chapter.NumberKey, Available: chapter.FileID != nil}
}

func (s *Server) registerReadPicker() {
	huma.Register(s.api, huma.Operation{OperationID: "read-chapter-picker", Method: http.MethodGet,
		Path: "/api/v1/read/series/{id}/chapters", Tags: []string{"Reading"}, Summary: "Lightweight chapter list for the web reader picker"},
		func(ctx context.Context, in *struct {
			ID        int64  `path:"id"`
			Query     string `query:"q" maxLength:"100"`
			CurrentID int64  `query:"currentId,omitempty"`
			Page      int    `query:"page" minimum:"0" default:"0"`
			PageSize  int    `query:"pageSize" minimum:"20" maximum:"100" default:"50"`
		}) (*struct{ Body ReadChapterPickerResponse }, error) {
			if _, err := s.visibleSeries(ctx, in.ID); err != nil {
				return nil, err
			}
			userID, err := s.userOf(ctx)
			if err != nil {
				return nil, readError(err)
			}
			var chapters []model.Chapter
			if err := s.app.DB.NewSelect().Model(&chapters).Where("series_id = ?", in.ID).Order("number_sort", "id").Scan(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			var first, latest, upNext, match *ReadChapterRef
			if len(chapters) > 0 {
				first, latest = chapterRef(chapters[0]), chapterRef(chapters[len(chapters)-1])
			}
			maxRead := -1.0
			if err := s.app.DB.NewSelect().TableExpr("chapter_read_states AS rs").Join("JOIN chapters AS c ON c.id = rs.chapter_id").
				ColumnExpr("COALESCE(MAX(c.number_sort), -1)").Where("rs.series_id = ? AND rs.completed AND rs.user_id = ?", in.ID, userID).
				Scan(ctx, &maxRead); err != nil {
				return nil, toHTTPError(err)
			}
			if maxRead >= 0 {
				for _, chapter := range chapters {
					if chapter.NumberSort > maxRead {
						upNext = chapterRef(chapter)
						break
					}
				}
			}
			needle := strings.ToLower(strings.TrimSpace(in.Query))
			if number, err := strconv.ParseFloat(needle, 64); err == nil {
				for _, chapter := range chapters {
					if strings.ToLower(chapter.NumberKey) == needle || chapter.NumberSort == number {
						match = chapterRef(chapter)
						break
					}
				}
			}
			if needle != "" {
				filtered := chapters[:0]
				for _, chapter := range chapters {
					if strings.Contains(strings.ToLower(chapter.NumberKey), needle) || strings.Contains(strings.ToLower(chapter.Title), needle) || strings.Contains(strings.ToLower(chapter.Volume), needle) {
						filtered = append(filtered, chapter)
					}
				}
				chapters = filtered
			}
			total := len(chapters)
			pageSize := min(max(in.PageSize, 20), 100)
			page := in.Page
			if page == 0 {
				page = 1
				if in.CurrentID > 0 && needle == "" {
					for index, chapter := range chapters {
						if chapter.ID == in.CurrentID {
							page = index/pageSize + 1
							break
						}
					}
				}
			}
			start := min((page-1)*pageSize, total)
			end := min(start+pageSize, total)
			pageChapters := chapters[start:end]
			ids := make([]int64, len(pageChapters))
			for i := range pageChapters {
				ids[i] = pageChapters[i].ID
			}
			refs := []*ReadChapterRef{}
			for _, ref := range []*ReadChapterRef{first, latest, upNext, match} {
				if ref != nil {
					refs = append(refs, ref)
				}
			}
			releaseIDs := slices.Clone(ids)
			for _, ref := range refs {
				releaseIDs = append(releaseIDs, ref.ID)
			}
			releases := map[int64]bool{}
			states := map[int64]model.ChapterReadState{}
			if len(releaseIDs) > 0 {
				var releaseRows []struct {
					ChapterID int64 `bun:"chapter_id"`
				}
				if err := s.app.DB.NewSelect().Model((*model.ChapterRelease)(nil)).Column("chapter_id").Where("chapter_id IN (?)", bun.In(releaseIDs)).Where("removed = ?", false).Group("chapter_id").Scan(ctx, &releaseRows); err != nil {
					return nil, toHTTPError(err)
				}
				for _, row := range releaseRows {
					releases[row.ChapterID] = true
				}
				for _, ref := range refs {
					ref.Available = ref.Available || releases[ref.ID]
				}
			}
			if len(ids) > 0 {
				var stateRows []model.ChapterReadState
				if err := s.app.DB.NewSelect().Model(&stateRows).Where("user_id = ? AND chapter_id IN (?)", userID, bun.In(ids)).Scan(ctx); err != nil {
					return nil, toHTTPError(err)
				}
				for _, state := range stateRows {
					states[state.ChapterID] = state
				}
			}
			items := make([]ReadChapterItem, 0, len(pageChapters))
			for _, chapter := range pageChapters {
				state := states[chapter.ID]
				items = append(items, ReadChapterItem{ID: chapter.ID, Number: chapter.NumberKey, NumberSort: chapter.NumberSort, Volume: chapter.Volume,
					Title: chapter.Title, Downloaded: chapter.FileID != nil, Available: chapter.FileID != nil || releases[chapter.ID], Completed: state.Completed, Page: state.Page})
			}
			sort.SliceStable(items, func(i, j int) bool { return items[i].NumberSort < items[j].NumberSort })
			return &struct{ Body ReadChapterPickerResponse }{ReadChapterPickerResponse{Items: items, Page: page, PageSize: pageSize, Total: total,
				First: first, Latest: latest, UpNext: upNext, Match: match}}, nil
		})
}
