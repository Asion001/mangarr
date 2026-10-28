package app

import (
	"context"
	"fmt"
	"time"

	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/jobs"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/requests"
	"github.com/Asion001/mangarr/internal/userdata"
	"github.com/Asion001/mangarr/internal/userimport"
)

// PlacePause spaces the chapter lists asked of a site while placing a
// user's imported progress (sites like MangaLib allow about one a second).
var PlacePause = 1500 * time.Millisecond

// wireRequests sets up series requests and personal notifications: new
// chapters go to followers who can see the series.
func (a *App) wireRequests() {
	a.Requests = &requests.Service{DB: a.DB, Bus: a.Bus, Mods: a.Modules, Series: a.Series, Search: a.Search,
		Log: a.Log.With("component", "requests"), Tell: a.Notifications.SendToUsers,
		DropImport: func(ctx context.Context, id int64) error { return a.Imports.Delete(ctx, id) },
		QueueSearch: func(ctx context.Context, seriesID int64) error {
			_, err := a.Queue.Push(ctx, "SearchMissing", map[string]any{"seriesId": seriesID}, "request")
			return err
		}}
	a.AddService(a.Requests)
	a.Notifications.Followers = a.Followers

	a.UserImport = &userimport.Service{DB: a.DB, Mods: a.Modules, Metadata: a.Metadata, Requests: a.Requests,
		Log: a.Log.With("component", "userimport")}
	a.Queue.Register(jobs.Definition{Name: "RequestImportedTitles", Description: "Send the titles of a user's import that aren't in the library to the admins as one import",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var body struct {
				UserID int64                  `json:"userId"`
				Titles []userdata.ExportTitle `json:"titles"`
				Origin *userdata.ExportOrigin `json:"origin"`
			}
			if err := r.Body(&body); err != nil || body.UserID == 0 {
				return fmt.Errorf("userId required")
			}
			p, err := a.Auth.UserPrincipal(ctx, body.UserID)
			if err != nil || p == nil {
				return fmt.Errorf("user %d can't make requests", body.UserID)
			}
			imp, err := a.UserImport.RequestLibrary(ctx, p, body.Titles, body.Origin)
			if err != nil {
				return err
			}
			r.Progress("%d titles sent for review", len(body.Titles))
			_, err = a.Queue.Push(ctx, "MapImport", map[string]any{"importId": imp.ID}, "request")
			return err
		}})
	a.Queue.Register(jobs.Definition{Name: "PlaceImportedProgress", Description: "Look up at the site the chapters a user's import reached and write that progress",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var body struct {
				UserID int64                  `json:"userId"`
				Items  []userimport.Placement `json:"items"`
			}
			if err := r.Body(&body); err != nil || body.UserID == 0 {
				return fmt.Errorf("userId required")
			}
			n, err := a.UserImport.Place(ctx, body.UserID, body.Items, PlacePause)
			r.Progress("%d of %d titles placed", n, len(body.Items))
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
