package app

import (
	"context"
	"fmt"

	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/jobs"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/requests"
	"github.com/Asion001/mangarr/internal/userdata"
	"github.com/Asion001/mangarr/internal/userimport"
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

	a.UserImport = &userimport.Service{DB: a.DB, Mods: a.Modules, Metadata: a.Metadata, Requests: a.Requests,
		Log: a.Log.With("component", "userimport")}
	a.Queue.Register(jobs.Definition{Name: "RequestImportedTitles", Description: "Ask for the titles of a user's import that aren't in the library",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var body struct {
				UserID int64                  `json:"userId"`
				Titles []userdata.ExportTitle `json:"titles"`
			}
			if err := r.Body(&body); err != nil || body.UserID == 0 {
				return fmt.Errorf("userId required")
			}
			p, err := a.Auth.UserPrincipal(ctx, body.UserID)
			if err != nil || p == nil {
				return fmt.Errorf("user %d can't make requests", body.UserID)
			}
			res, err := a.UserImport.Request(ctx, p, p.ReaderID, body.Titles)
			r.Progress("%d requested, %d already here, %d not found", res.Requested, res.Here, len(res.Unmatched))
			return err
		}})
	// kept progress is written once a request's title is added and listed
	a.Bus.Subscribe(func(events.Event) {
		go func() {
			if err := a.UserImport.ApplyReady(context.Background()); err != nil {
				a.Log.Warn("write requested progress", "err", err)
			}
		}()
	}, events.SeriesAdded, events.ChapterImported, events.RequestUpdated)
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
