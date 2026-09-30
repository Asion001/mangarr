package reading

import (
	"context"
	"sort"
	"strings"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
)

// ReadingStats is one reader's activity across the library visible to the
// current viewer. Reading time is available for web-reader sessions captured
// after the reading-time feature was installed; completed chapters include
// imported and synchronized progress.
type ReadingStats struct {
	TotalActiveSeconds int64                  `json:"totalActiveSeconds"`
	CompletedChapters  int                    `json:"completedChapters"`
	ActiveMonth        *ReadingStatsMonth     `json:"activeMonth,omitempty"`
	TopSeries          *ReadingStatsSeries    `json:"topSeries,omitempty"`
	Languages          []ReadingStatsLanguage `json:"languages"`
	Genres             []ReadingStatsGenre    `json:"genres"`
}

type ReadingStatsMonth struct {
	Month         string `json:"month" doc:"UTC calendar month in YYYY-MM format"`
	ActiveSeconds int64  `json:"activeSeconds"`
}

type ReadingStatsSeries struct {
	SeriesID          int64  `json:"seriesId"`
	Title             string `json:"title"`
	ActiveSeconds     int64  `json:"activeSeconds"`
	CompletedChapters int    `json:"completedChapters"`
}

type ReadingStatsLanguage struct {
	Language          string `json:"language"`
	ActiveSeconds     int64  `json:"activeSeconds"`
	CompletedChapters int    `json:"completedChapters"`
}

type ReadingStatsGenre struct {
	Genre             string `json:"genre"`
	ActiveSeconds     int64  `json:"activeSeconds"`
	CompletedChapters int    `json:"completedChapters"`
}

// Stats aggregates active time and current completed progress for one reader.
func (s *Service) Stats(ctx context.Context, readerID int64) (ReadingStats, error) {
	out := ReadingStats{Languages: []ReadingStatsLanguage{}, Genres: []ReadingStatsGenre{}}
	visible, err := s.AllSeries(ctx, readerID, 0)
	if err != nil {
		return out, err
	}
	if len(visible) == 0 {
		return out, nil
	}
	series := make(map[int64]model.Series, len(visible))
	ids := make([]int64, 0, len(visible))
	for _, item := range visible {
		series[item.Series.ID] = item.Series
		ids = append(ids, item.Series.ID)
	}

	var sessions []model.ReadingSession
	if err := s.DB.NewSelect().Model(&sessions).
		Column("series_id", "active_seconds", "updated_at").
		Where("reader_id = ?", readerID).Where("series_id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
		return out, err
	}
	type totals struct {
		seconds   int64
		completed int
	}
	bySeries := map[int64]*totals{}
	byMonth := map[string]int64{}
	get := func(id int64) *totals {
		if bySeries[id] == nil {
			bySeries[id] = &totals{}
		}
		return bySeries[id]
	}
	for _, session := range sessions {
		seconds := int64(session.ActiveSeconds)
		out.TotalActiveSeconds += seconds
		get(session.SeriesID).seconds += seconds
		byMonth[session.UpdatedAt.UTC().Format("2006-01")] += seconds
	}

	var completed []struct {
		SeriesID int64 `bun:"series_id"`
		Count    int   `bun:"count"`
	}
	if err := s.DB.NewSelect().TableExpr("chapter_read_states").
		ColumnExpr("series_id, COUNT(*) AS count").
		Where("reader_id = ? AND completed = ?", readerID, true).
		Where("series_id IN (?)", bun.In(ids)).GroupExpr("series_id").Scan(ctx, &completed); err != nil {
		return out, err
	}
	for _, item := range completed {
		get(item.SeriesID).completed += item.Count
	}
	// a chapter finished in a title with several language editions is one
	// chapter (the editions share the state)
	if err := s.DB.NewSelect().TableExpr("chapter_read_states").ColumnExpr("COUNT(DISTINCT id)").
		Where("reader_id = ? AND completed = ?", readerID, true).
		Where("series_id IN (?)", bun.In(ids)).Scan(ctx, &out.CompletedChapters); err != nil {
		return out, err
	}

	for month, seconds := range byMonth {
		if out.ActiveMonth == nil || seconds > out.ActiveMonth.ActiveSeconds || seconds == out.ActiveMonth.ActiveSeconds && month > out.ActiveMonth.Month {
			out.ActiveMonth = &ReadingStatsMonth{Month: month, ActiveSeconds: seconds}
		}
	}
	for id, total := range bySeries {
		ser := series[id]
		candidate := &ReadingStatsSeries{SeriesID: id, Title: ser.Title, ActiveSeconds: total.seconds, CompletedChapters: total.completed}
		if out.TopSeries == nil || candidate.CompletedChapters > out.TopSeries.CompletedChapters ||
			candidate.CompletedChapters == out.TopSeries.CompletedChapters && candidate.ActiveSeconds > out.TopSeries.ActiveSeconds ||
			candidate.CompletedChapters == out.TopSeries.CompletedChapters && candidate.ActiveSeconds == out.TopSeries.ActiveSeconds && candidate.Title < out.TopSeries.Title {
			out.TopSeries = candidate
		}
	}

	byLanguage := map[string]*ReadingStatsLanguage{}
	for id, total := range bySeries {
		language := strings.TrimSpace(series[id].Language)
		if language == "" {
			language = "und"
		}
		if byLanguage[language] == nil {
			byLanguage[language] = &ReadingStatsLanguage{Language: language}
		}
		byLanguage[language].ActiveSeconds += total.seconds
		byLanguage[language].CompletedChapters += total.completed
	}
	for _, item := range byLanguage {
		out.Languages = append(out.Languages, *item)
	}
	sort.Slice(out.Languages, func(i, j int) bool {
		a, b := out.Languages[i], out.Languages[j]
		if a.ActiveSeconds != b.ActiveSeconds {
			return a.ActiveSeconds > b.ActiveSeconds
		}
		if a.CompletedChapters != b.CompletedChapters {
			return a.CompletedChapters > b.CompletedChapters
		}
		return a.Language < b.Language
	})
	byGenre := map[string]*ReadingStatsGenre{}
	for id, total := range bySeries {
		seen := map[string]bool{}
		for _, raw := range series[id].Metadata.Genres {
			genre := strings.TrimSpace(raw)
			key := strings.ToLower(genre)
			if genre == "" || seen[key] {
				continue
			}
			seen[key] = true
			if byGenre[key] == nil {
				byGenre[key] = &ReadingStatsGenre{Genre: genre}
			}
			byGenre[key].ActiveSeconds += total.seconds
			byGenre[key].CompletedChapters += total.completed
		}
	}
	for _, item := range byGenre {
		out.Genres = append(out.Genres, *item)
	}
	sort.Slice(out.Genres, func(i, j int) bool {
		a, b := out.Genres[i], out.Genres[j]
		if a.CompletedChapters != b.CompletedChapters {
			return a.CompletedChapters > b.CompletedChapters
		}
		if a.ActiveSeconds != b.ActiveSeconds {
			return a.ActiveSeconds > b.ActiveSeconds
		}
		return a.Genre < b.Genre
	})
	return out, nil
}
