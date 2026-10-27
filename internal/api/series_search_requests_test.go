package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/model"
)

func TestSeriesSearchShowsAndFiltersRequesters(t *testing.T) {
	srv, app := newServer(t, true)
	ctx := t.Context()
	now := time.Now().UTC()
	root := &model.RootFolder{Path: t.TempDir(), Language: "en", CreatedAt: now}
	if _, err := app.DB.NewInsert().Model(root).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var profile model.Profile
	if err := app.DB.NewSelect().Model(&profile).Order("id").Limit(1).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	add := func(title string) *model.Series {
		series := &model.Series{Title: title, SortTitle: title, Status: model.StatusOngoing, Monitored: true, MonitorNew: model.MonitorAll,
			RootFolderID: root.ID, Path: title, ProfileID: profile.ID, Language: "en", SourcePriorityMode: "inherit", ReadingDirection: "rtl",
			Tags: []int64{}, AddedAt: now, UpdatedAt: now}
		if _, err := app.DB.NewInsert().Model(series).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		return series
	}
	asked, other := add("Asked"), add("Other")
	alex := &model.User{Username: "alex", DisplayName: "Alex"}
	if _, err := app.DB.NewInsert().Model(alex).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	request := &model.Request{Title: "Asked", Status: model.RequestAvailable, SeriesID: &asked.ID, CreatedAt: now, UpdatedAt: now}
	declined := &model.Request{Title: "Other", Status: model.RequestDeclined, SeriesID: &other.ID, CreatedAt: now, UpdatedAt: now}
	for _, r := range []*model.Request{request, declined} {
		if _, err := app.DB.NewInsert().Model(r).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := app.DB.NewInsert().Model(&model.RequestUser{RequestID: r.ID, UserID: alex.ID, CreatedAt: now}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	get := func(query string) (body struct {
		Items []struct {
			Title       string `json:"title"`
			RequestedBy []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"requestedBy"`
		} `json:"items"`
		Requesters []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"requesters"`
	}) {
		response, err := http.Get(srv.URL + "/api/v1/series/search?pageSize=12&" + query)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", query, response.StatusCode)
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	all := get("sort=requested")
	if len(all.Items) != 2 || all.Items[0].Title != "Asked" || len(all.Items[0].RequestedBy) != 1 || all.Items[0].RequestedBy[0].Name != "Alex" || len(all.Items[1].RequestedBy) != 0 {
		t.Fatalf("requested by: %+v", all.Items)
	}
	if len(all.Requesters) != 1 || all.Requesters[0].ID != alex.ID {
		t.Fatalf("requesters: %+v", all.Requesters)
	}
	if mine := get(fmt.Sprintf("requestedBy=%d", alex.ID)); len(mine.Items) != 1 || mine.Items[0].Title != "Asked" {
		t.Fatalf("requested by alex: %+v", mine.Items)
	}
	if none := get("requestedBy=none"); len(none.Items) != 1 || none.Items[0].Title != "Other" {
		t.Fatalf("not requested: %+v", none.Items)
	}
}
