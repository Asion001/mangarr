// Package history records and lists series/chapter events.
package history

import (
	"context"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
)

// Record inserts a history row (errors are returned but callers usually log them).
func Record(ctx context.Context, db bun.IDB, seriesID int64, chapterID *int64, event, sourceTitle string, data map[string]string) error {
	if data == nil {
		data = map[string]string{}
	}
	h := &model.History{SeriesID: seriesID, ChapterID: chapterID, EventType: event, SourceTitle: sourceTitle, Data: data, CreatedAt: time.Now().UTC()}
	_, err := db.NewInsert().Model(h).Exec(ctx)
	return err
}

type Query struct {
	SeriesID  int64
	ChapterID int64
	EventType string
	Sort      string
	Page      int
	PageSize  int
}

type Page struct {
	Items    []model.History `json:"items"`
	Total    int             `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"pageSize"`
}

func List(ctx context.Context, db bun.IDB, q Query) (*Page, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize <= 0 || q.PageSize > 500 {
		q.PageSize = 50
	}
	var items []model.History
	sel := db.NewSelect().Model(&items).ColumnExpr("history.*")
	if q.SeriesID > 0 {
		sel = sel.Where("series_id = ?", q.SeriesID)
	}
	if q.ChapterID > 0 {
		sel = sel.Where("chapter_id = ?", q.ChapterID)
	}
	if q.EventType != "" {
		sel = sel.Where("event_type = ?", q.EventType)
	}
	switch q.Sort {
	case "oldest":
		sel = sel.OrderExpr("history.id ASC")
	case "series":
		sel = sel.Join("JOIN series AS history_series ON history_series.id = history.series_id").
			OrderExpr("LOWER(history_series.title) ASC, history.id DESC")
	case "event":
		sel = sel.OrderExpr("history.event_type ASC, history.id DESC")
	default:
		sel = sel.OrderExpr("history.id DESC")
	}
	total, err := sel.Limit(int64(q.PageSize)).Offset(int64((q.Page - 1) * q.PageSize)).ScanAndCount(ctx)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []model.History{}
	}
	return &Page{Items: items, Total: int(total), Page: q.Page, PageSize: q.PageSize}, nil
}
