package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/model"
)

func init() { register((*Server).registerUpdates) }

// UpdateItem is a title added to the library or a chapter first discovered.
type UpdateItem struct {
	Kind        string    `json:"kind" enum:"series,chapter"`
	At          time.Time `json:"at"`
	SeriesID    int64     `json:"seriesId"`
	SeriesTitle string    `json:"seriesTitle"`
	Language    string    `json:"language,omitempty"`
	Languages   []string  `json:"languages,omitempty"`
	CoverURL    string    `json:"coverUrl"`
	ChapterID   int64     `json:"chapterId,omitempty"`
	Number      string    `json:"number,omitempty"`
	Title       string    `json:"title,omitempty"`
	Readable    bool      `json:"readable,omitempty"`
	Downloaded  bool      `json:"downloaded,omitempty"`
	ReadState   string    `json:"readState,omitempty" enum:",unread,in_progress,read"`
	ReadPage    int       `json:"readPage,omitempty"`
}

// UpdatePage is a stable page of the reader-facing update feed.
type UpdatePage struct {
	Items      []UpdateItem `json:"items"`
	Total      int          `json:"total"`
	Page       int          `json:"page"`
	PageSize   int          `json:"pageSize"`
	NextCursor string       `json:"nextCursor,omitempty"`
}

type updateCursor struct {
	At   time.Time `json:"at"`
	Kind int       `json:"kind"`
	ID   int64     `json:"id"`
}

type updateFeedRow struct {
	Kind        string    `bun:"kind"`
	At          time.Time `bun:"at"`
	SeriesID    int64     `bun:"series_id"`
	SeriesTitle string    `bun:"series_title"`
	ChapterID   int64     `bun:"chapter_id"`
	WorkKey     int64     `bun:"work_key"`
	SortID      int64     `bun:"sort_id"`
}

const (
	seriesUpdateRank  = 1
	chapterUpdateRank = 2
	seriesUpdateGroup = "CASE WHEN s.work_id > 0 THEN s.work_id ELSE -s.id END"
	seriesUpdateAt    = "COALESCE(w.created_at, MIN(s.added_at))"
)

func (s *Server) registerUpdates() {
	tags := []string{"Updates"}
	huma.Register(s.api, huma.Operation{OperationID: "updates-list", Method: http.MethodGet, Path: "/api/v1/updates", Tags: tags,
		Summary: "Recently discovered chapters and newly added titles visible to you; initial catalog chapters are suppressed"},
		func(ctx context.Context, in *struct {
			Days     int    `query:"days" default:"30" minimum:"1" maximum:"365"`
			Kind     string `query:"kind" enum:",all,chapter,series"`
			Page     int    `query:"page" default:"1" minimum:"1"`
			PageSize int    `query:"pageSize" default:"100" minimum:"1" maximum:"500"`
			Cursor   string `query:"cursor"`
		}) (*struct{ Body UpdatePage }, error) {
			if in.Days <= 0 || in.Days > 365 {
				in.Days = 30
			}
			if in.Page <= 0 {
				in.Page = 1
			}
			if in.PageSize <= 0 || in.PageSize > 500 {
				in.PageSize = 100
			}
			var cursor *updateCursor
			if in.Cursor != "" {
				decoded, err := decodeUpdateCursor(in.Cursor)
				if err != nil {
					return nil, toHTTPError(badRequest("invalid updates cursor"))
				}
				cursor = &decoded
			}
			page, err := s.listUpdates(ctx, access.From(ctx), in.Days, in.Kind, in.Page, in.PageSize, cursor)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &struct{ Body UpdatePage }{page}, nil
		})
}

func encodeUpdateCursor(cursor updateCursor) string {
	b, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeUpdateCursor(encoded string) (updateCursor, error) {
	var cursor updateCursor
	b, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return cursor, err
	}
	err = json.Unmarshal(b, &cursor)
	if err == nil && (cursor.At.IsZero() || cursor.ID <= 0 || (cursor.Kind != seriesUpdateRank && cursor.Kind != chapterUpdateRank)) {
		err = huma.Error400BadRequest("invalid cursor values")
	}
	return cursor, err
}

func updateRank(kind string) int {
	if kind == "chapter" {
		return chapterUpdateRank
	}
	return seriesUpdateRank
}

func updateRowAfter(a, b updateFeedRow) bool {
	if !a.At.Equal(b.At) {
		return a.At.After(b.At)
	}
	if updateRank(a.Kind) != updateRank(b.Kind) {
		return updateRank(a.Kind) > updateRank(b.Kind)
	}
	return a.SortID > b.SortID
}

func (s *Server) applyUpdateVisibility(q *bun.SelectQuery, viewer *access.Principal, alias string) *bun.SelectQuery {
	if viewer == nil {
		return q.Where("1 = 0")
	}
	q = q.Where(alias+".preview = ?", false) // previews aren't in the library
	if viewer.Can(access.LibraryManage) {
		return q
	}
	if len(viewer.Scope.RootFolders) > 0 {
		q = q.Where(alias+".root_folder_id IN (?)", bun.In(viewer.Scope.RootFolders))
	}
	if len(viewer.Scope.ExcludeTags) > 0 {
		if s.app.DB.Kind == db.Postgres {
			q = q.Where("NOT EXISTS (SELECT 1 FROM jsonb_array_elements_text("+alias+".tags) AS scope_tag(value) WHERE CAST(scope_tag.value AS BIGINT) IN (?))", bun.In(viewer.Scope.ExcludeTags))
		} else {
			q = q.Where("NOT EXISTS (SELECT 1 FROM json_each("+alias+".tags) AS scope_tag WHERE CAST(scope_tag.value AS INTEGER) IN (?))", bun.In(viewer.Scope.ExcludeTags))
		}
	}
	if len(viewer.Scope.IncludeTags) > 0 {
		if s.app.DB.Kind == db.Postgres {
			q = q.Where("EXISTS (SELECT 1 FROM jsonb_array_elements_text("+alias+".tags) AS scope_tag(value) WHERE CAST(scope_tag.value AS BIGINT) IN (?))", bun.In(viewer.Scope.IncludeTags))
		} else {
			q = q.Where("EXISTS (SELECT 1 FROM json_each("+alias+".tags) AS scope_tag WHERE CAST(scope_tag.value AS INTEGER) IN (?))", bun.In(viewer.Scope.IncludeTags))
		}
	}
	return q
}

func (s *Server) chapterUpdateQuery(viewer *access.Principal, cutoff time.Time, cursor *updateCursor) *bun.SelectQuery {
	q := s.app.DB.NewSelect().TableExpr("chapters AS c").
		ColumnExpr("'chapter' AS kind, c.first_seen_at AS at, c.series_id, s.title AS series_title, c.id AS chapter_id, 0 AS work_key, c.id AS sort_id").
		Join("JOIN series AS s ON s.id = c.series_id").
		Where("c.first_seen_at >= ?", cutoff)
	if s.app.DB.Kind == db.Postgres {
		q = q.Where("c.first_seen_at > s.added_at + INTERVAL '5 minutes'")
	} else {
		q = q.Where("datetime(c.first_seen_at) > datetime(s.added_at, '+5 minutes')")
	}
	q = s.applyUpdateVisibility(q, viewer, "s")
	if cursor != nil {
		q = q.Where("(c.first_seen_at < ?) OR (c.first_seen_at = ? AND (? < ? OR (? = ? AND c.id < ?)))",
			cursor.At, cursor.At, chapterUpdateRank, cursor.Kind, chapterUpdateRank, cursor.Kind, cursor.ID)
	}
	return q
}

func (s *Server) seriesUpdateQuery(viewer *access.Principal, cutoff time.Time, cursor *updateCursor) *bun.SelectQuery {
	q := s.app.DB.NewSelect().TableExpr("series AS s").
		ColumnExpr("'series' AS kind").
		ColumnExpr(seriesUpdateAt + " AS at").
		ColumnExpr("MIN(s.id) AS series_id").
		ColumnExpr("COALESCE(w.title, MIN(s.title)) AS series_title").
		ColumnExpr("0 AS chapter_id").
		ColumnExpr(seriesUpdateGroup + " AS work_key").
		ColumnExpr("MIN(s.id) AS sort_id").
		Join("LEFT JOIN works AS w ON w.id = s.work_id")
	q = s.applyUpdateVisibility(q, viewer, "s").
		GroupExpr(seriesUpdateGroup).
		GroupExpr("w.title").
		GroupExpr("w.created_at").
		Having(seriesUpdateAt+" >= ?", cutoff)
	if cursor != nil {
		q = q.Having("("+seriesUpdateAt+" < ?) OR ("+seriesUpdateAt+" = ? AND (? < ? OR (? = ? AND MIN(s.id) < ?)))",
			cursor.At, cursor.At, seriesUpdateRank, cursor.Kind, seriesUpdateRank, cursor.Kind, cursor.ID)
	}
	return q
}

func (s *Server) countSeriesUpdates(ctx context.Context, viewer *access.Principal, cutoff time.Time) (int, error) {
	grouped := s.seriesUpdateQuery(viewer, cutoff, nil)
	return s.app.DB.NewSelect().TableExpr("(?) AS grouped_updates", grouped).Count(ctx)
}

func (s *Server) listUpdates(ctx context.Context, viewer *access.Principal, days int, kind string, page, pageSize int, cursor *updateCursor) (UpdatePage, error) {
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	start := 0
	queryLimit := pageSize + 1
	if cursor == nil && page > 1 {
		start = (page - 1) * pageSize
		queryLimit = start + pageSize + 1
	}
	rows := make([]updateFeedRow, 0, queryLimit*2)
	total := 0
	if kind != "series" {
		var chapterRows []updateFeedRow
		q := s.chapterUpdateQuery(viewer, cutoff, cursor).OrderExpr("c.first_seen_at DESC, c.id DESC")
		chapterTotal, err := s.chapterUpdateQuery(viewer, cutoff, nil).Count(ctx)
		if err != nil {
			return UpdatePage{}, err
		}
		if err := q.Limit(queryLimit).Scan(ctx, &chapterRows); err != nil {
			return UpdatePage{}, err
		}
		rows = append(rows, chapterRows...)
		total += chapterTotal
	}
	if kind != "chapter" {
		var seriesRows []updateFeedRow
		seriesTotal, err := s.countSeriesUpdates(ctx, viewer, cutoff)
		if err != nil {
			return UpdatePage{}, err
		}
		if err := s.seriesUpdateQuery(viewer, cutoff, cursor).OrderExpr(seriesUpdateAt+" DESC, MIN(s.id) DESC").Limit(queryLimit).Scan(ctx, &seriesRows); err != nil {
			return UpdatePage{}, err
		}
		rows = append(rows, seriesRows...)
		total += seriesTotal
	}
	sort.Slice(rows, func(i, j int) bool { return updateRowAfter(rows[i], rows[j]) })
	end := min(start+pageSize, len(rows))
	if start > len(rows) {
		start = len(rows)
	}
	pageRows := rows[start:end]

	chapterIDs := make([]int64, 0, len(pageRows))
	seriesIDs := make([]int64, 0, len(pageRows))
	workIDs := make([]int64, 0, len(pageRows))
	for _, row := range pageRows {
		seriesIDs = append(seriesIDs, row.SeriesID)
		if row.ChapterID > 0 {
			chapterIDs = append(chapterIDs, row.ChapterID)
		}
		if row.WorkKey > 0 {
			workIDs = append(workIDs, row.WorkKey)
		}
	}
	chapters := map[int64]model.Chapter{}
	if len(chapterIDs) > 0 {
		var found []model.Chapter
		if err := s.app.DB.NewSelect().Model(&found).Where("id IN (?)", bun.In(chapterIDs)).Scan(ctx); err != nil {
			return UpdatePage{}, err
		}
		for _, chapter := range found {
			chapters[chapter.ID] = chapter
		}
	}
	seriesByID := map[int64]model.Series{}
	if len(seriesIDs) > 0 {
		var found []model.Series
		if err := s.app.DB.NewSelect().Model(&found).Where("id IN (?)", bun.In(seriesIDs)).Scan(ctx); err != nil {
			return UpdatePage{}, err
		}
		for _, series := range found {
			seriesByID[series.ID] = series
		}
	}
	languages := map[int64][]string{}
	if len(workIDs) > 0 {
		var editions []model.Series
		q := s.app.DB.NewSelect().Model(&editions).ModelTableExpr("series AS s").ColumnExpr("s.*").Where("s.work_id IN (?)", bun.In(workIDs))
		q = s.applyUpdateVisibility(q, viewer, "s")
		if err := q.Scan(ctx); err != nil {
			return UpdatePage{}, err
		}
		for _, edition := range editions {
			if edition.Language != "" {
				languages[edition.WorkID] = append(languages[edition.WorkID], edition.Language)
			}
		}
		for key := range languages {
			sort.Strings(languages[key])
		}
	}
	available := map[int64]bool{}
	readState := map[int64]model.ChapterReadState{}
	if len(chapterIDs) > 0 {
		type releaseRow struct {
			ChapterID int64 `bun:"chapter_id"`
		}
		var releaseRows []releaseRow
		if err := s.app.DB.NewSelect().TableExpr("chapter_releases").ColumnExpr("DISTINCT chapter_id").
			Where("chapter_id IN (?) AND removed = ?", bun.In(chapterIDs), false).Scan(ctx, &releaseRows); err != nil {
			return UpdatePage{}, err
		}
		for _, row := range releaseRows {
			available[row.ChapterID] = true
		}
		if viewer != nil && viewer.ReaderID > 0 {
			var states []model.ChapterReadState
			if err := s.app.DB.NewSelect().Model(&states).Where("reader_id = ?", viewer.ReaderID).
				Where("chapter_id IN (?)", bun.In(chapterIDs)).Scan(ctx); err != nil {
				return UpdatePage{}, err
			}
			for _, state := range states {
				readState[state.ChapterID] = state
			}
		}
	}
	items := make([]UpdateItem, 0, len(pageRows))
	for _, row := range pageRows {
		series := seriesByID[row.SeriesID]
		if row.Kind == "series" {
			langs := languages[row.WorkKey]
			if row.WorkKey < 0 && series.Language != "" {
				langs = []string{series.Language}
			}
			items = append(items, UpdateItem{Kind: "series", At: row.At, SeriesID: row.SeriesID, SeriesTitle: row.SeriesTitle,
				Languages: langs, CoverURL: seriesCoverURL(series)})
			continue
		}
		chapter := chapters[row.ChapterID]
		state, readPage := "unread", 0
		if progress, ok := readState[chapter.ID]; ok {
			readPage = progress.Page
			if progress.Completed {
				state = "read"
			} else if progress.Page > 0 {
				state = "in_progress"
			}
		}
		items = append(items, UpdateItem{Kind: "chapter", At: row.At, SeriesID: series.ID, SeriesTitle: row.SeriesTitle,
			Language: series.Language, CoverURL: seriesCoverURL(series), ChapterID: chapter.ID, Number: chapter.NumberKey,
			Title: chapter.Title, Readable: chapter.FileID != nil || available[chapter.ID], Downloaded: chapter.FileID != nil,
			ReadState: state, ReadPage: readPage})
	}
	result := UpdatePage{Items: items, Total: total, Page: page, PageSize: pageSize}
	if end < len(rows) && len(pageRows) > 0 {
		last := pageRows[len(pageRows)-1]
		result.NextCursor = encodeUpdateCursor(updateCursor{At: last.At, Kind: updateRank(last.Kind), ID: last.SortID})
	}
	return result, nil
}
