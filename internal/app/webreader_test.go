package app_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

// TestWebReader: the web reader's API reads a downloaded chapter from its
// file and the next one streamed, saves progress and settings.
func TestWebReader(t *testing.T) {
	sc := fakesource.NewScenario("webreader")
	sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
	sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/m", Title: "Web", Status: source.StatusOngoing, Chapters: []fakesource.Chapter{
		{URL: "/c1", Name: "Chapter 1", Number: 1, Uploaded: time.Now()}, {URL: "/c2", Name: "Chapter 2", Number: 2, Uploaded: time.Now(), Pages: 5}}})
	e := newTestApp(t, dbtest.DSNs(t)["sqlite"])
	mod := e.addFakeModule(t, "webreader")
	ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Web", RootFolderID: e.RFID, Monitor: model.MonitorNone,
		Sources: []series.SourceLink{{ModuleID: mod, SourceID: "A", URL: "/m", SourceName: "Source A", Lang: "en"}}})
	if err != nil {
		t.Fatal(err)
	}
	e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
	var chs []model.Chapter
	_ = e.App.DB.NewSelect().Model(&chs).Where("series_id = ?", ser.ID).Order("number_sort").Scan(e.Ctx)
	e.runCommand(t, "SearchMissing", map[string]any{"seriesId": ser.ID, "chapterIds": []int64{chs[0].ID}, "explicit": true})
	waitFor(t, 20*time.Second, "chapter 1 downloaded", func() bool { return len(e.chapterFiles(t, ser.ID)) == 1 })

	srv := httptest.NewServer(api.New(e.App))
	defer srv.Close()
	g, _ := e.App.Settings.General(e.Ctx)
	call := func(method, path, body string, out any) (int, http.Header) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("X-Api-Key", g.APIKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Mozilla/5.0 (iPad; CPU OS 18_0 like Mac OS X) Safari/605.1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if out != nil && resp.StatusCode < 300 {
			if err := json.Unmarshal(b, out); err != nil {
				t.Fatalf("%s: %v %s", path, err, b)
			}
		}
		return resp.StatusCode, resp.Header
	}
	callWith := func(method, path string, headers map[string]string) (int, http.Header) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		req.Header.Set("X-Api-Key", g.APIKey)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, resp.Header
	}
	c1, c2 := sid(chs[0].ID), sid(chs[1].ID)

	var ch api.ReadChapter
	if code, _ := call("GET", "/api/v1/read/chapters/"+c1, "", &ch); code != 200 {
		t.Fatalf("chapter: %d", code)
	}
	if !ch.Downloaded || len(ch.Pages) != 3 || ch.Next == nil || ch.Next.ID != chs[1].ID || ch.Prev != nil || ch.SeriesTitle != "Web" || !ch.CanDownload {
		t.Fatalf("chapter %+v", ch)
	}
	code, h := call("GET", "/api/v1/read/chapters/"+c1+"/pages/1", "", nil)
	if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "image/") {
		t.Fatalf("page: %d %s", code, h.Get("Content-Type"))
	}
	// the browser gets what it needs to cache the page and skip it next time
	etag := h.Get("ETag")
	if etag == "" || h.Get("Content-Length") == "" || h.Get("Cache-Control") == "" {
		t.Fatalf("page headers: %v", h)
	}
	if code, _ := callWith("GET", "/api/v1/read/chapters/"+c1+"/pages/1", map[string]string{"If-None-Match": etag}); code != 304 {
		t.Fatalf("page the client already has: %d", code)
	}
	if code, _ := callWith("GET", "/api/v1/read/chapters/"+c1+"/pages/1", map[string]string{"If-None-Match": `"something-else"`}); code != 200 {
		t.Fatalf("page with a stale etag: %d", code)
	}
	var bd map[string]int
	if code, _ := call("GET", "/api/v1/read/chapters/"+c1+"/pages/1/bounds", "", &bd); code != 200 || bd["width"] == 0 || bd["w"] == 0 {
		t.Fatalf("bounds: %d %v", code, bd)
	}
	// the reader asks for every page's box in one request
	var boxes []struct {
		Number int `json:"number"`
		W      int `json:"w"`
	}
	if code, _ := call("GET", "/api/v1/read/chapters/"+c1+"/bounds", "", &boxes); code != 200 || len(boxes) != 3 || boxes[0].Number != 1 || boxes[2].W == 0 {
		t.Fatalf("chapter bounds: %d %+v", code, boxes)
	}
	if code, h := call("GET", "/api/v1/read/chapters/"+c1+"/file", "", nil); code != 200 || !strings.Contains(h.Get("Content-Disposition"), ".cbz") {
		t.Fatalf("file: %d %v", code, h)
	}
	// the next one isn't downloaded: streamed
	if code, _ := call("GET", "/api/v1/read/chapters/"+c2, "", &ch); code != 200 || ch.Downloaded || len(ch.Pages) != 5 || ch.Prev == nil {
		t.Fatalf("streamed chapter: %d %+v", code, ch)
	}
	if code, _ := call("GET", "/api/v1/read/chapters/"+c2+"/pages/5", "", nil); code != 200 {
		t.Fatalf("streamed page: %d", code)
	}
	if code, _ := call("GET", "/api/v1/read/chapters/"+c2+"/pages/6", "", nil); code != 404 {
		t.Fatalf("page out of range: %d", code)
	}

	// Reading-time heartbeats are cumulative and idempotent: retries and an
	// older out-of-order heartbeat cannot inflate the session.
	for _, seconds := range []int{30, 30, 20, 45} {
		body := `{"sessionId":"reader-session-0001","activeSeconds":` + strconv.Itoa(seconds) + `}`
		if code, _ := call("POST", "/api/v1/read/chapters/"+c1+"/time", body, nil); code != http.StatusNoContent {
			t.Fatalf("reading time %d: %d", seconds, code)
		}
	}
	var session model.ReadingSession
	if err := e.App.DB.NewSelect().Model(&session).Where("id = ?", "reader-session-0001").Scan(e.Ctx); err != nil {
		t.Fatal(err)
	}
	if session.ChapterID != chs[0].ID || session.SeriesID != ser.ID || session.ActiveSeconds != 45 {
		t.Fatalf("reading session %+v", session)
	}
	if code, _ := call("POST", "/api/v1/read/chapters/"+c1+"/time", `{"sessionId":"reader-session-0002","activeSeconds":43201}`, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("oversized reading time: %d", code)
	}

	// progress: page 2, then the last page finishes it
	if code, _ := call("PUT", "/api/v1/read/chapters/"+c1+"/progress", `{"page":2}`, nil); code != 204 {
		t.Fatalf("progress: %d", code)
	}
	call("GET", "/api/v1/read/chapters/"+c1, "", &ch)
	if ch.Progress.Page != 2 || ch.Progress.Completed {
		t.Fatalf("progress %+v", ch.Progress)
	}
	call("PUT", "/api/v1/read/chapters/"+c1+"/progress", `{"page":3}`, nil)
	call("GET", "/api/v1/read/chapters/"+c1, "", &ch)
	if !ch.Progress.Completed {
		t.Fatalf("last page didn't finish it: %+v", ch.Progress)
	}
	var ev model.ReadEvent
	_ = e.App.DB.NewSelect().Model(&ev).OrderExpr("id DESC").Limit(1).Scan(e.Ctx)
	if ev.Client != "Web reader" || ev.Device != "Safari on iPad" {
		t.Fatalf("event %+v", ev)
	}

	// Manual controls can change the current chapter and every chapter before it.
	if code, _ := call("PUT", "/api/v1/read/chapters/"+c2+"/mark", `{"read":true,"scope":"chapter"}`, nil); code != 200 {
		t.Fatalf("mark current read: %d", code)
	}
	if code, _ := call("PUT", "/api/v1/read/chapters/"+c2+"/mark", `{"read":false,"scope":"previous"}`, nil); code != 200 {
		t.Fatalf("mark previous unread: %d", code)
	}
	call("GET", "/api/v1/read/chapters/"+c1, "", &ch)
	if ch.Progress.Page != 0 || ch.Progress.Completed {
		t.Fatalf("previous chapter stayed read: %+v", ch.Progress)
	}
	if code, _ := call("PUT", "/api/v1/read/chapters/"+c2+"/mark", `{"read":true,"scope":"previous"}`, nil); code != 200 {
		t.Fatalf("mark previous read: %d", code)
	}
	call("GET", "/api/v1/read/chapters/"+c1, "", &ch)
	if !ch.Progress.Completed {
		t.Fatalf("previous chapter was not marked read: %+v", ch.Progress)
	}
	if code, _ := call("PUT", "/api/v1/read/chapters/"+c2+"/mark", `{"read":false,"scope":"chapter"}`, nil); code != 200 {
		t.Fatalf("mark current unread: %d", code)
	}
	call("GET", "/api/v1/read/chapters/"+c2, "", &ch)
	if ch.Progress.Completed {
		t.Fatalf("current chapter stayed read: %+v", ch.Progress)
	}

	// Progress and time read offline arrive late, dated when they happened.
	readAt := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	body := `{"page":0,"completed":true,"at":"` + readAt.Format(time.RFC3339) + `"}`
	if code, _ := call("PUT", "/api/v1/read/chapters/"+c2+"/progress", body, nil); code != 204 {
		t.Fatalf("late progress: %d", code)
	}
	var st model.ChapterReadState
	if err := e.App.DB.NewSelect().Model(&st).Where("chapter_id = ?", chs[1].ID).Scan(e.Ctx); err != nil {
		t.Fatal(err)
	}
	if !st.Completed || st.ReadAt == nil || !st.ReadAt.Equal(readAt) {
		t.Fatalf("late progress read at %v, want %v", st.ReadAt, readAt)
	}
	ev = model.ReadEvent{}
	_ = e.App.DB.NewSelect().Model(&ev).Where("chapter_id = ?", chs[1].ID).OrderExpr("id DESC").Limit(1).Scan(e.Ctx)
	if !ev.At.Equal(readAt) {
		t.Fatalf("history dated %v, want %v", ev.At, readAt)
	}
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if code, _ := call("PUT", "/api/v1/read/chapters/"+c2+"/progress", `{"page":1,"at":"`+future+`"}`, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("progress from the future: %d", code)
	}
	body = `{"sessionId":"reader-session-0003","activeSeconds":60,"at":"` + readAt.Format(time.RFC3339) + `"}`
	if code, _ := call("POST", "/api/v1/read/chapters/"+c2+"/time", body, nil); code != http.StatusNoContent {
		t.Fatalf("late reading time: %d", code)
	}
	session = model.ReadingSession{}
	if err := e.App.DB.NewSelect().Model(&session).Where("id = ?", "reader-session-0003").Scan(e.Ctx); err != nil {
		t.Fatal(err)
	}
	if !session.UpdatedAt.Equal(readAt) || !session.StartedAt.Equal(readAt) {
		t.Fatalf("late session %+v, want %v", session, readAt)
	}
}
