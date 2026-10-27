package api

import (
	"context"
	"sort"
	"time"

	"github.com/Asion001/mangarr/internal/model"
)

// addSourceCounts sets each title's number of sources, over its editions.
func (s *Server) addSourceCounts(ctx context.Context, items []SeriesResource) error {
	var rows []struct {
		SeriesID int64 `bun:"series_id"`
		Count    int   `bun:"count"`
	}
	if err := s.app.DB.NewSelect().Model((*model.SeriesSource)(nil)).
		ColumnExpr("series_id").ColumnExpr("COUNT(*) AS count").Group("series_id").Scan(ctx, &rows); err != nil {
		return err
	}
	counts := make(map[int64]int, len(rows))
	for _, row := range rows {
		counts[row.SeriesID] = row.Count
	}
	for i := range items {
		items[i].SourceCount = 0
		for _, edition := range editionIDs(items[i]) {
			items[i].SourceCount += counts[edition]
		}
	}
	return nil
}

// editionIDs are a title's series ids: its editions, or itself alone.
func editionIDs(item SeriesResource) []int64 {
	if len(item.Editions) == 0 {
		return []int64{item.ID}
	}
	ids := make([]int64, len(item.Editions))
	for i, edition := range item.Editions {
		ids[i] = edition.ID
	}
	return ids
}

// addRequesters sets who asked for each title (by any of its editions or
// its work, declined requests left out) and returns everyone who did, by
// name.
func (s *Server) addRequesters(ctx context.Context, items []SeriesResource) ([]SeriesRequester, error) {
	var rows []struct {
		SeriesID    *int64    `bun:"series_id"`
		WorkID      *int64    `bun:"work_id"`
		UserID      int64     `bun:"user_id"`
		Username    string    `bun:"username"`
		DisplayName string    `bun:"display_name"`
		CreatedAt   time.Time `bun:"created_at"`
	}
	if err := s.app.DB.NewSelect().TableExpr("request_users AS ru").
		Join("JOIN requests AS r ON r.id = ru.request_id").
		Join("JOIN users AS u ON u.id = ru.user_id").
		ColumnExpr("r.series_id, r.work_id, ru.user_id, u.username, u.display_name, ru.created_at").
		Where("r.status <> ?", model.RequestDeclined).
		Where("r.series_id IS NOT NULL OR r.work_id IS NOT NULL").
		OrderExpr("ru.created_at, ru.user_id").
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	type asked struct {
		who SeriesRequester
		at  time.Time
	}
	bySeries, byWork := map[int64][]asked{}, map[int64][]asked{}
	everyone := map[int64]SeriesRequester{}
	for _, row := range rows {
		name := row.DisplayName
		if name == "" {
			name = row.Username
		}
		a := asked{SeriesRequester{ID: row.UserID, Name: name}, row.CreatedAt}
		if row.SeriesID != nil {
			bySeries[*row.SeriesID] = append(bySeries[*row.SeriesID], a)
		} else {
			byWork[*row.WorkID] = append(byWork[*row.WorkID], a)
		}
	}
	for i := range items {
		item := &items[i]
		var all []asked
		for _, id := range editionIDs(*item) {
			all = append(all, bySeries[id]...)
		}
		if item.WorkID != 0 {
			all = append(all, byWork[item.WorkID]...)
		}
		item.RequestedBy, item.RequestedAt = nil, nil
		seen := map[int64]bool{}
		sort.SliceStable(all, func(a, b int) bool { return all[a].at.Before(all[b].at) })
		for _, a := range all {
			if item.RequestedAt == nil {
				at := a.at
				item.RequestedAt = &at
			}
			if !seen[a.who.ID] {
				seen[a.who.ID] = true
				item.RequestedBy = append(item.RequestedBy, a.who)
				everyone[a.who.ID] = a.who
			}
		}
	}
	list := make([]SeriesRequester, 0, len(everyone))
	for _, who := range everyone {
		list = append(list, who)
	}
	sort.Slice(list, func(a, b int) bool { return list[a].Name < list[b].Name })
	return list, nil
}
