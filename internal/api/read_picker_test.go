package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/readstate"
)

func TestReadChapterPickerCentersAndSearches(t *testing.T) {
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
	series := &model.Series{Title: "Picker", SortTitle: "picker", Status: model.StatusOngoing, Monitored: true, MonitorNew: model.MonitorAll,
		RootFolderID: root.ID, Path: "Picker", ProfileID: profile.ID, Language: "en", SourcePriorityMode: "inherit", ReadingDirection: "rtl",
		Tags: []int64{}, AddedAt: now, UpdatedAt: now}
	if _, err := app.DB.NewInsert().Model(series).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var currentID, readID int64
	for number := 1; number <= 65; number++ {
		chapter := &model.Chapter{SeriesID: series.ID, NumberKey: fmt.Sprintf("%d", number), NumberSort: float64(number), Title: fmt.Sprintf("Episode %d", number),
			Monitored: true, State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
		if _, err := app.DB.NewInsert().Model(chapter).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if number == 55 {
			currentID = chapter.ID
		}
		if number == 9 {
			readID = chapter.ID
		}
	}
	readerID, err := app.Reading.ReaderID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := readstate.Save(ctx, app.DB, &model.ChapterReadState{ReaderID: readerID, ChapterID: readID, SeriesID: series.ID, Completed: true, ReadAt: &now, SyncedAt: now}); err != nil {
		t.Fatal(err)
	}

	get := func(url string) struct {
		Items []struct {
			Number string `json:"number"`
		} `json:"items"`
		Page                         int `json:"page"`
		Total                        int `json:"total"`
		First, Latest, UpNext, Match *struct {
			Number string `json:"number"`
		}
	} {
		t.Helper()
		response, err := http.Get(srv.URL + url)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %s", url, response.Status)
		}
		var body struct {
			Items []struct {
				Number string `json:"number"`
			} `json:"items"`
			Page                         int `json:"page"`
			Total                        int `json:"total"`
			First, Latest, UpNext, Match *struct {
				Number string `json:"number"`
			}
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	centered := get(fmt.Sprintf("/api/v1/read/series/%d/chapters?currentId=%d&pageSize=20", series.ID, currentID))
	if centered.Page != 3 || centered.Total != 65 || len(centered.Items) != 20 || centered.Items[0].Number != "41" {
		t.Fatalf("centered picker: %+v", centered)
	}
	found := get(fmt.Sprintf("/api/v1/read/series/%d/chapters?q=Episode+58&pageSize=20", series.ID))
	if found.Total != 1 || len(found.Items) != 1 || found.Items[0].Number != "58" {
		t.Fatalf("searched picker: %+v", found)
	}
	if found.First == nil || found.First.Number != "1" || found.Latest == nil || found.Latest.Number != "65" || found.UpNext == nil || found.UpNext.Number != "10" || found.Match != nil {
		t.Fatalf("picker shortcuts: %+v", found)
	}
	// a number goes straight to that chapter, though "6" also appears in 16, 26, 60…
	exact := get(fmt.Sprintf("/api/v1/read/series/%d/chapters?q=6&pageSize=20", series.ID))
	if exact.Match == nil || exact.Match.Number != "6" || exact.Total < 2 {
		t.Fatalf("number match: %+v", exact)
	}

	response, err := http.Get(fmt.Sprintf("%s/api/v1/series/search?q=Pick&filter=unread&rootFolderId=%d&pageSize=12", srv.URL, root.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var search struct {
		Items []struct {
			Title string `json:"title"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(response.Body).Decode(&search); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || search.Total != 1 || len(search.Items) != 1 || search.Items[0].Title != "Picker" {
		t.Fatalf("series search: status=%s body=%+v", response.Status, search)
	}
}
