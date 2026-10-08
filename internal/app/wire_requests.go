package app

import (
	"context"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/requests"
)

// wireRequests sets up series requests and personal notifications: new
// chapters go to followers who can see the series.
func (a *App) wireRequests() {
	a.Requests = &requests.Service{DB: a.DB, Bus: a.Bus, Mods: a.Modules, Series: a.Series, Search: a.Search,
		Log: a.Log.With("component", "requests"), Tell: a.Notifications.SendToUsers,
		QueueSearch: func(ctx context.Context, seriesID int64) error {
			_, err := a.Queue.Push(ctx, "SearchMissing", map[string]any{"seriesId": seriesID}, "request")
			return err
		}}
	a.AddService(a.Requests)
	a.Notifications.Followers = a.Followers
}

// Followers lists the users following a series who can still see it.
func (a *App) Followers(ctx context.Context, seriesID int64) []int64 {
	var ser model.Series
	if err := a.DB.NewSelect().Model(&ser).Column("id", "tags", "root_folder_id", "metadata").Where("id = ?", seriesID).Scan(ctx); err != nil {
		return nil
	}
	var users []int64
	if err := a.DB.NewSelect().Model((*model.Follow)(nil)).Column("user_id").Where("series_id = ?", seriesID).Scan(ctx, &users); err != nil {
		return nil
	}
	out := []int64{}
	for _, u := range users {
		if p, err := a.Auth.UserPrincipal(ctx, u); err == nil && p != nil && p.Sees(&ser) {
			out = append(out, u)
		}
	}
	return out
}
