package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/app"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/series"
)

// TestSeriesAPICharacterization records what the series endpoints answer for
// a small library with two editions of one title, a title of its own, and
// chapters with releases, so splitting the handlers can't change a response
// unnoticed. Run with -update after an intended change.
func TestSeriesAPICharacterization(t *testing.T) {
	srv, a := newServer(t, true)
	ctx := context.Background()
	enDir, ruDir := t.TempDir(), t.TempDir()
	var en, ru model.RootFolder
	if code := doJSON(t, http.MethodPost, srv.URL+"/api/v1/rootfolders", `{"path":`+jsonString(enDir)+`,"language":"en"}`, &en); code != 200 {
		t.Fatalf("root folder: %d", code)
	}
	if code := doJSON(t, http.MethodPost, srv.URL+"/api/v1/rootfolders", `{"path":`+jsonString(ruDir)+`,"language":"ru"}`, &ru); code != 200 {
		t.Fatalf("root folder: %d", code)
	}
	add := func(req series.AddRequest) *model.Series {
		t.Helper()
		req.Monitor, req.NoRefresh = model.MonitorAll, true
		s, err := a.Series.Add(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	abyssEN := add(series.AddRequest{Title: "Abyss", RootFolderID: en.ID, Language: "en",
		Sources: []series.SourceLink{{ModuleID: 1, SourceID: "A", URL: "/abyss", SourceName: "Source A", Lang: "en"}, {ModuleID: 1, SourceID: "B", URL: "/abyss-b", SourceName: "Source B", Lang: "en"}}})
	abyssRU := add(series.AddRequest{Title: "Бездна", RootFolderID: ru.ID, Language: "ru", WorkID: workOf(t, a, abyssEN.ID),
		Sources: []series.SourceLink{{ModuleID: 1, SourceID: "R", URL: "/bezdna", SourceName: "Source R", Lang: "ru"}}})
	solo := add(series.AddRequest{Title: "Solo", RootFolderID: en.ID, Language: "en",
		Sources: []series.SourceLink{{ModuleID: 1, SourceID: "A", URL: "/solo", SourceName: "Source A", Lang: "en"}}})

	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var links []model.SeriesSource
	if err := a.DB.NewSelect().Model(&links).Where("series_id = ?", abyssEN.ID).Order("priority").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	for i, state := range []string{"downloaded", "missing", "missing"} {
		ch := &model.Chapter{SeriesID: abyssEN.ID, NumberKey: itoa(int64(i + 1)), NumberSort: float64(i + 1), Title: "Chapter " + itoa(int64(i+1)),
			Monitored: i != 2, State: state, ReleaseDate: &at, FirstSeenAt: at, UpdatedAt: at}
		if _, err := a.DB.NewInsert().Model(ch).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		for j, link := range links[:1+i%2] {
			rel := &model.ChapterRelease{SeriesID: abyssEN.ID, ChapterID: &ch.ID, SeriesSourceID: link.ID, ChapterURL: "/abyss/" + itoa(int64(i+1)) + "/" + link.SourceID,
				Name: "Ch. " + itoa(int64(i+1)), Scanlator: []string{"Team One", "Team Two"}[j], RawNumber: float64(i + 1), UploadDate: &at, CreatedAt: at}
			if _, err := a.DB.NewInsert().Model(rel).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}

	var out bytes.Buffer
	call := func(name, method, path, body string, header ...string) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var pretty bytes.Buffer
		if json.Indent(&pretty, raw, "", "  ") != nil {
			pretty.Reset()
			pretty.Write(raw)
		}
		out.WriteString("### " + name + "\n" + method + " " + path + " -> " + resp.Status + "\n" + pretty.String() + "\n\n")
	}
	id := func(s *model.Series) string { return itoa(s.ID) }

	call("library, English UI", http.MethodGet, "/api/v1/series", "", "Accept-Language", "en")
	call("library, Russian UI", http.MethodGet, "/api/v1/series", "", "Accept-Language", "ru")
	call("English edition", http.MethodGet, "/api/v1/series/"+id(abyssEN), "")
	call("Russian edition", http.MethodGet, "/api/v1/series/"+id(abyssRU), "")
	call("missing series", http.MethodGet, "/api/v1/series/999", "")
	call("chapters", http.MethodGet, "/api/v1/series/"+id(abyssEN)+"/chapters", "")
	call("decision", http.MethodGet, "/api/v1/series/"+id(abyssEN)+"/chapters/2/decision", "")
	call("unmonitor a chapter", http.MethodPut, "/api/v1/chapters/monitor", `{"chapterIds":[1],"monitored":false}`)
	call("chapters after unmonitoring", http.MethodGet, "/api/v1/series/"+id(abyssEN)+"/chapters", "")
	call("group Solo with Abyss", http.MethodPut, "/api/v1/series/"+id(solo)+"/work", `{"workId":`+itoa(workOf(t, a, abyssEN.ID))+`}`)
	call("library after grouping", http.MethodGet, "/api/v1/series", "", "Accept-Language", "en")
	call("separate Solo again", http.MethodPut, "/api/v1/series/"+id(solo)+"/work", `{"workId":0}`)
	call("lookup without metadata modules", http.MethodGet, "/api/v1/series/lookup?term=abyss", "")
	call("link a source", http.MethodPost, "/api/v1/series/"+id(solo)+"/sources", `{"moduleId":1,"sourceId":"B","url":"/solo-b","sourceName":"Source B","lang":"en"}`)
	call("unlink a missing source", http.MethodDelete, "/api/v1/series/"+id(solo)+"/sources/999", "")
	call("delete Solo", http.MethodDelete, "/api/v1/series/"+id(solo), "")
	call("library after deleting", http.MethodGet, "/api/v1/series", "", "Accept-Language", "en")

	got := normalizeCharacterization(out.String(), enDir, ruDir)
	golden := filepath.Join("testdata", "series_characterization.golden")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got != string(want) {
		gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gl) || i < len(wl); i++ {
			var g, w string
			if i < len(gl) {
				g = gl[i]
			}
			if i < len(wl) {
				w = wl[i]
			}
			if g != w {
				t.Fatalf("series API answers changed at line %d (run with -update if intended)\ngot:  %s\nwant: %s", i+1, g, w)
			}
		}
	}
}

func workOf(t *testing.T, a *app.App, seriesID int64) int64 {
	t.Helper()
	var id int64
	if err := a.DB.NewSelect().Table("series").Column("work_id").Where("id = ?", seriesID).Scan(context.Background(), &id); err != nil {
		t.Fatal(err)
	}
	return id
}

var (
	volatileTime = regexp.MustCompile(`"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)"`)
	volatileDate = regexp.MustCompile(`(?m)^Date: .*$`)
	volatileVer  = regexp.MustCompile(`\?v=\d+`)
)

// normalizeCharacterization drops what changes from run to run: the temp
// folders and the clock (the seeded 2026-09-01 dates stay).
func normalizeCharacterization(s, enDir, ruDir string) string {
	s = strings.ReplaceAll(s, enDir, "<en>")
	s = strings.ReplaceAll(s, ruDir, "<ru>")
	s = volatileTime.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasPrefix(m, `"2026-09-01T00:00:00`) {
			return m
		}
		return `"<now>"`
	})
	s = volatileVer.ReplaceAllString(s, "?v=<now>")
	return volatileDate.ReplaceAllString(s, "")
}
