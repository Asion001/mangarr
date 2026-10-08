package app_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

// TestSignedImageLinks: with CDN images on, a downloaded chapter's pages and
// covers get signed links that work without a login and may be cached by
// anyone; a changed link, a reset key or the setting turned off ends them.
func TestSignedImageLinks(t *testing.T) {
	sc := fakesource.NewScenario("imagelinks")
	sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
	sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/m", Title: "Signed", Status: source.StatusOngoing, Chapters: []fakesource.Chapter{
		{URL: "/c1", Name: "Chapter 1", Number: 1, Uploaded: time.Now()}}})
	e := newTestApp(t, dbtest.DSNs(t)["sqlite"])
	mod := e.addFakeModule(t, "imagelinks")
	ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Signed", RootFolderID: e.RFID, Monitor: model.MonitorNone,
		Sources: []series.SourceLink{{ModuleID: mod, SourceID: "A", URL: "/m", SourceName: "Source A", Lang: "en"}}})
	if err != nil {
		t.Fatal(err)
	}
	e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
	var chs []model.Chapter
	_ = e.App.DB.NewSelect().Model(&chs).Where("series_id = ?", ser.ID).Order("number_sort").Scan(e.Ctx)
	e.runCommand(t, "SearchMissing", map[string]any{"seriesId": ser.ID, "chapterIds": []int64{chs[0].ID}, "explicit": true})
	waitFor(t, 20*time.Second, "chapter downloaded", func() bool { return len(e.chapterFiles(t, ser.ID)) == 1 })

	srv := httptest.NewServer(api.New(e.App))
	defer srv.Close()
	g, _ := e.App.Settings.General(e.Ctx)
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string, authed bool, headers ...string) (int, http.Header) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if authed {
			req.Header.Set("X-Api-Key", g.APIKey)
		}
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, resp.Header
	}
	chapter := func() api.ReadChapter {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/read/chapters/"+sid(chs[0].ID), nil)
		req.Header.Set("X-Api-Key", g.APIKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var ch api.ReadChapter
		if err := json.NewDecoder(resp.Body).Decode(&ch); err != nil {
			t.Fatal(err)
		}
		return ch
	}
	setCDN := func(on bool) {
		t.Helper()
		r, _ := e.App.Settings.Reading(e.Ctx)
		r.CDNImages = on
		if err := e.App.Settings.Set(e.Ctx, settings.KeyReading, r); err != nil {
			t.Fatal(err)
		}
	}

	if ch := chapter(); ch.PageBase != "" {
		t.Fatalf("signed links are off by default: %q", ch.PageBase)
	}
	if code, h := get("/api/v1/series/"+sid(ser.ID)+"/cover?v=1", true); code == http.StatusFound {
		t.Fatalf("cover redirected with CDN images off: %v", h)
	}

	setCDN(true)
	base := chapter().PageBase
	if !strings.HasPrefix(base, "api/v1/img/p/") {
		t.Fatalf("page base: %q", base)
	}
	code, h := get("/"+base+"/1", false)
	if code != 200 || h.Get("Cache-Control") != "public, max-age=31536000, immutable" || !strings.HasPrefix(h.Get("Content-Type"), "image/") || h.Get("ETag") == "" {
		t.Fatalf("signed page without a login: %d %v", code, h)
	}
	if code, _ := get("/"+base+"/1", false, "If-None-Match", h.Get("ETag")); code != 304 {
		t.Fatalf("signed page the cache already has: %d", code)
	}
	if code, _ := get("/"+base+"/1?w=320", false); code != 200 {
		t.Fatalf("signed page at a width: %d", code)
	}
	if code, _ := get("/"+base+"/99", false); code != 404 {
		t.Fatalf("missing page: %d", code)
	}
	if code, _ := get("/"+base[:len(base)-2]+"xx/1", false); code != 404 {
		t.Fatalf("a changed signature must not work: %d", code)
	}

	code, h = get("/api/v1/series/"+sid(ser.ID)+"/cover?v=1", true)
	loc := h.Get("Location")
	if code != http.StatusFound || !strings.HasPrefix(loc, "/api/v1/img/c/"+sid(ser.ID)+"/1/") {
		t.Fatalf("cover redirect: %d %q", code, loc)
	}
	if code, _ := get("/api/v1/series/"+sid(ser.ID)+"/cover?v=2", false); code != 401 {
		t.Fatalf("the cover endpoint itself still needs a login: %d", code)
	}
	if code, _ := get(loc, false); code == 401 || code == 403 {
		t.Fatalf("signed cover needs no login: %d", code)
	}
	if code, _ := get(strings.Replace(loc, "/1/", "/2/", 1), false); code != 404 {
		t.Fatalf("a cover link for another version must not work: %d", code)
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/settings/reading/image-links/reset", nil)
	req.Header.Set("X-Api-Key", g.APIKey)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode >= 300 {
		t.Fatalf("reset: %v %v", err, resp)
	}
	if code, _ := get("/"+base+"/1", false); code != 404 {
		t.Fatalf("links from before the reset must stop working: %d", code)
	}
	base = chapter().PageBase
	if code, _ := get("/"+base+"/1", false); code != 200 {
		t.Fatalf("new link after the reset: %d", code)
	}
	setCDN(false)
	if code, _ := get("/"+base+"/1", false); code != 404 {
		t.Fatalf("links must stop working when the setting is off: %d", code)
	}
}
