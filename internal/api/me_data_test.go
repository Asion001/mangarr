package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/app"
	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/readstate"
	"github.com/Asion001/mangarr/internal/userdata"
)

type dataTitle struct {
	title, source, url, anilist string
	chapters                    int
}

// seedData adds series with sources and chapters 1..n; it returns them by title.
func seedData(t *testing.T, a *app.App, titles ...dataTitle) map[string]*model.Series {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	rf := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
	if _, err := a.DB.NewInsert().Model(rf).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	out := map[string]*model.Series{}
	for _, d := range titles {
		ser := &model.Series{Title: d.title, SortTitle: d.title, Status: model.StatusOngoing, RootFolderID: rf.ID, Path: d.title, ProfileID: 1,
			Tags: []int64{}, Language: "en", AddedAt: now, UpdatedAt: now}
		if d.anilist != "" {
			ser.Metadata.ExternalIDs = map[string]string{"anilist": d.anilist}
		}
		if _, err := a.DB.NewInsert().Model(ser).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if d.source != "" {
			src := &model.SeriesSource{SeriesID: ser.ID, ModuleID: 1, SourceID: d.source, SourceName: "Site", Lang: "en", MangaURL: d.url, Title: d.title,
				NextCheckAt: now, CreatedAt: now}
			if _, err := a.DB.NewInsert().Model(src).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		for i := 1; i <= d.chapters; i++ {
			c := &model.Chapter{SeriesID: ser.ID, NumberKey: strconv.Itoa(i), NumberSort: float64(i), Monitored: true, State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
			if _, err := a.DB.NewInsert().Model(c).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		out[d.title] = ser
	}
	return out
}

func chapterOf(t *testing.T, a *app.App, seriesID int64, number string) int64 {
	t.Helper()
	var c model.Chapter
	if err := a.DB.NewSelect().Model(&c).Where("series_id = ? AND number_key = ?", seriesID, number).Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c.ID
}

// TestMyDataMovesToAnotherServer: a reader exports their own data on one
// server and imports it on another, where the titles have other ids and
// are found by source, AniList id and name.
func TestMyDataMovesToAnotherServer(t *testing.T) {
	ctx := context.Background()
	srvA, a := newServer(t, false)
	old := seedData(t, a,
		dataTitle{title: "Blue Period", source: "123", url: "/manga/blue", chapters: 3},
		dataTitle{title: "Vinland Saga", anilist: "30013", chapters: 3},
		dataTitle{title: "Kaguya-sama", chapters: 2},
		dataTitle{title: "Dandadan", chapters: 2},
		dataTitle{title: "Untouched", chapters: 2})
	anna, err := a.Auth.CreateUser(ctx, auth.NewUser{Username: "anna", Password: "anna-pass-1"})
	if err != nil {
		t.Fatal(err)
	}
	read := func(ser *model.Series, number string, completed bool, page int) {
		at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		st := &model.ChapterReadState{UserID: anna.ID, ChapterID: chapterOf(t, a, ser.ID, number), Completed: completed, Page: page, ReadAt: &at, SyncedAt: at}
		if err := readstate.Save(ctx, a.DB, st); err != nil {
			t.Fatal(err)
		}
	}
	read(old["Blue Period"], "1", true, 0)
	read(old["Blue Period"], "2", false, 7)
	read(old["Vinland Saga"], "1", true, 0)
	read(old["Kaguya-sama"], "1", true, 0)
	read(old["Dandadan"], "1", true, 0)
	ev := &model.ReadEvent{UserID: anna.ID, SeriesID: old["Blue Period"].ID, ChapterID: chapterOf(t, a, old["Blue Period"].ID, "1"), Chapters: 1,
		Completed: true, Origin: "web", At: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	if _, err := a.DB.NewInsert().Model(ev).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.NewInsert().Model(&model.Follow{UserID: anna.ID, SeriesID: old["Vinland Saga"].ID, CreatedAt: time.Now()}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.NewInsert().Model(&model.ReaderPrefs{UserID: anna.ID, SeriesID: 0, Data: `{"mode":"webtoon"}`, UpdatedAt: time.Now()}).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	resp, err := login(t, srvA.URL, "anna", "anna-pass-1").Get(srvA.URL + "/api/v1/me/data")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("export: %v %v", err, resp)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var data userdata.Export
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	var sum userdata.Summary
	if code := (caller{t, login(t, srvA.URL, "anna", "anna-pass-1"), srvA.URL}).do("GET", "/api/v1/me/data/summary", "", &sum); code != 200 || sum.Titles != 4 || sum.Read != 4 || sum.History != 1 || sum.Follows != 1 || !sum.Settings {
		t.Fatalf("summary %d %+v", code, sum)
	}
	if data.Format != userdata.Format || data.User != "anna" || len(data.Titles) != 4 || !strings.Contains(string(data.Reader), "webtoon") {
		t.Fatalf("export %s", raw)
	}

	srvB, b := newServer(t, false)
	here := seedData(t, b,
		dataTitle{title: "Blue Period (EN)", source: "123", url: "/manga/blue", chapters: 2},
		dataTitle{title: "Vinland", anilist: "30013", chapters: 3},
		dataTitle{title: "Kaguya Sama", chapters: 2})
	annaB, err := b.Auth.CreateUser(ctx, auth.NewUser{Username: "anna", Password: "anna-pass-2"})
	if err != nil {
		t.Fatal(err)
	}
	// already further here: kept
	if err := readstate.Save(ctx, b.DB, &model.ChapterReadState{UserID: annaB.ID, ChapterID: chapterOf(t, b, here["Blue Period (EN)"].ID, "2"), Completed: true, SyncedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	me := caller{t, login(t, srvB.URL, "anna", "anna-pass-2"), srvB.URL}
	body, _ := json.Marshal(map[string]any{"data": json.RawMessage(raw), "options": map[string]any{"progress": true, "history": true, "follows": true, "settings": true}})
	var matches []userdata.DataTitleMatch
	if code := me.do("POST", "/api/v1/me/data/preview", string(body), &matches); code != 200 || len(matches) != 4 {
		t.Fatalf("preview %d %+v", code, matches)
	}
	want := map[string]string{"Blue Period": "source", "Vinland Saga": "id", "Kaguya-sama": "title", "Dandadan": ""}
	for _, m := range matches {
		if m.By != want[m.Title] {
			t.Fatalf("%s matched by %q (%+v)", m.Title, m.By, matches)
		}
	}
	var res userdata.DataImportResult
	if code := me.do("POST", "/api/v1/me/data/import", string(body), &res); code != 200 {
		t.Fatalf("import %d", code)
	}
	if res.Titles != 3 || res.Progress != 3 || res.Kept != 1 || res.History != 1 || res.Follows != 1 || res.Settings != 1 {
		t.Fatalf("import %+v", res)
	}
	var states []model.ChapterReadState
	if err := b.DB.NewSelect().Model(&states).Where("user_id = ?", annaB.ID).Scan(ctx); err != nil || len(states) != 4 {
		t.Fatalf("states %+v %v", states, err)
	}
	if n, _ := b.DB.NewSelect().Model((*model.Follow)(nil)).Where("user_id = ? AND series_id = ?", annaB.ID, here["Vinland"].ID).Count(ctx); n != 1 {
		t.Fatal("follow not imported")
	}
	if code := me.do("POST", "/api/v1/me/data/import", string(body), &res); code != 200 || res.History != 0 || res.Follows != 0 || res.Progress != 0 {
		t.Fatalf("second import %d %+v", code, res)
	}
	if code := me.do("POST", "/api/v1/me/data/preview", `{"data":{"format":"something else"}}`, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("a foreign file: %d", code)
	}
}
