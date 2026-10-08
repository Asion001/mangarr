package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/app"
	"github.com/Asion001/mangarr/internal/catalogs"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

type cacheEnv struct {
	t   *testing.T
	url string
	app *app.App
	sc  *fakesource.Scenario
	mod int64
}

func newCacheEnv(t *testing.T, scenario string) *cacheEnv {
	srv, a := newServer(t, true)
	sc := fakesource.NewScenario(scenario)
	sc.Sources = []source.SourceInfo{
		{ID: "A", Name: "Safe", DisplayName: "Safe (EN)", Lang: "en"},
		{ID: "N", Name: "Spicy", DisplayName: "Spicy (EN)", Lang: "en", NSFW: true},
		{ID: "J", Name: "Japanese", DisplayName: "Japanese (JA)", Lang: "ja"},
	}
	for _, sid := range []string{"A", "N", "J"} {
		sc.AddManga(&fakesource.Manga{SourceID: sid, URL: "/tower", Title: "Tower of God"})
	}
	def := &model.ProviderDefinition{Kind: "source", Implementation: "fake", Name: "Fake", Enabled: true, Settings: map[string]any{"scenario": scenario}}
	if err := a.Modules.Create(context.Background(), def); err != nil {
		t.Fatal(err)
	}
	e := &cacheEnv{t: t, url: srv.URL, app: a, sc: sc, mod: def.ID}
	e.setSources(func(s *settings.Sources) { s.HideNSFW = false; s.Throttle.Preset = "fast" })
	return e
}

func (e *cacheEnv) setSources(fn func(*settings.Sources)) {
	e.t.Helper()
	var cur settings.Sources
	doJSON(e.t, http.MethodGet, e.url+"/api/v1/settings/sources", "", &cur)
	fn(&cur)
	b, _ := json.Marshal(cur)
	if code := doJSON(e.t, http.MethodPut, e.url+"/api/v1/settings/sources", string(b), nil); code != 200 {
		e.t.Fatalf("put sources settings: %d", code)
	}
}

func (e *cacheEnv) search(query string) map[string]api.SearchResultGroup {
	e.t.Helper()
	var groups []api.SearchResultGroup
	if code := doJSON(e.t, http.MethodGet, e.url+"/api/v1/sources/search?"+query, "", &groups); code != 200 {
		e.t.Fatalf("search %s: %d", query, code)
	}
	out := map[string]api.SearchResultGroup{}
	for _, g := range groups {
		out[g.SourceID] = g
	}
	return out
}

func (e *cacheEnv) searches() int {
	var n int
	e.sc.Update(func() { n = e.sc.Searches })
	return n
}

func (e *cacheEnv) key(sid string) string { return fmt.Sprintf("%d:%s", e.mod, sid) }

// Hiding NSFW catalogs must take effect for searches that were cached before.
func TestSearchCacheRespectsHiddenCatalogs(t *testing.T) {
	e := newCacheEnv(t, "cache-nsfw")
	got := e.search("q=tower&scope=all")
	if len(got) != 3 || len(got["N"].Results) != 1 {
		t.Fatalf("want 3 catalogs incl. NSFW, got %v", got)
	}
	before := e.searches()
	if again := e.search("q=tower&scope=all"); !again["A"].Cached || e.searches() != before {
		t.Fatal("identical search should be served from the cache")
	}
	e.setSources(func(s *settings.Sources) { s.HideNSFW = true })
	got = e.search("q=tower&scope=all")
	if _, ok := got["N"]; ok || len(got) != 2 {
		t.Fatalf("NSFW catalog still searched after hiding it: %v", got)
	}
	if got["A"].Cached {
		t.Fatal("the settings change must invalidate cached results")
	}
	// hidden catalogs are unreachable through browse, manga and thumbnails
	for _, p := range []string{"/browse?type=search&q=tower", "/manga?url=%2Ftower", "/thumbnail?url=%2Ftower"} {
		resp, err := http.Get(e.url + fmt.Sprintf("/api/v1/sources/%d/N", e.mod) + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s on hidden catalog: %d", p, resp.StatusCode)
		}
	}
	var list []api.SourceResource
	doJSON(t, http.MethodGet, e.url+"/api/v1/sources", "", &list)
	for _, c := range list {
		if c.ID == "N" {
			t.Fatal("hidden catalog listed in /sources")
		}
	}
}

func TestDisabledCatalogsAndScopes(t *testing.T) {
	e := newCacheEnv(t, "cache-scope")
	if code := doJSON(t, http.MethodPut, e.url+"/api/v1/catalogs", fmt.Sprintf(`{%q:{"enabled":false},%q:{"priority":5}}`, e.key("A"), e.key("J")), nil); code != 200 {
		t.Fatalf("update catalogs: %d", code)
	}
	active := e.search("q=tower")
	if _, ok := active["A"]; ok {
		t.Fatal("disabled catalog searched in active scope")
	}
	if all := e.search("q=tower&scope=all"); len(all["A"].Results) != 1 {
		t.Fatal("disabled catalog missing from scope=all")
	}
	if one := e.search("q=tower&source=" + url.QueryEscape(e.key("A"))); len(one) != 1 {
		t.Fatal("explicitly picked catalogs are searched even when disabled")
	}
	// default languages narrow the active scope; priority orders results
	e.setSources(func(s *settings.Sources) { s.DefaultLanguages = []string{"ja"} })
	if active := e.search("q=tower"); len(active) != 1 || active["J"].SourceID != "J" {
		t.Fatalf("default languages: %v", active)
	}
	var cl api.CatalogList
	doJSON(t, http.MethodGet, e.url+"/api/v1/catalogs", "", &cl)
	if len(cl.Items) != 3 || cl.Generation == 0 {
		t.Fatalf("catalog list: %+v", cl)
	}
}

func TestLanguageDefaultsSelectSourcesInConfiguredOrder(t *testing.T) {
	e := newCacheEnv(t, "language-defaults")
	e.setSources(func(s *settings.Sources) {
		s.LanguageDefaults = []settings.LanguageDefault{{Language: "ru", Sources: []string{e.key("J"), e.key("A")}}}
	})
	selected, errs := e.app.Catalogs.Select(context.Background(), catalogs.Filter{Scope: catalogs.ScopeActive, Lang: "ru"})
	if len(errs) != 0 {
		t.Fatalf("select errors: %v", errs)
	}
	if len(selected) != 2 || selected[0].ID != "J" || selected[1].ID != "A" {
		t.Fatalf("language source order was not preserved: %+v", selected)
	}
	// switching a catalog off takes it out of its language's default too
	if code := doJSON(t, http.MethodPut, e.url+"/api/v1/catalogs", fmt.Sprintf(`{%q:{"enabled":false}}`, e.key("J")), nil); code != 200 {
		t.Fatalf("disable catalog: %d", code)
	}
	selected, _ = e.app.Catalogs.Select(context.Background(), catalogs.Filter{Scope: catalogs.ScopeActive, Lang: "ru"})
	if len(selected) != 1 || selected[0].ID != "A" {
		t.Fatalf("a disabled catalog stayed in the language default: %+v", selected)
	}
}

func TestCatalogListRefreshesOnModuleReload(t *testing.T) {
	e := newCacheEnv(t, "cache-reload")
	var list []api.SourceResource
	doJSON(t, http.MethodGet, e.url+"/api/v1/sources", "", &list)
	n := len(list)
	e.sc.Update(func() { e.sc.Sources = append(e.sc.Sources, source.SourceInfo{ID: "B", Name: "New", Lang: "en"}) })
	l, _ := e.app.Modules.Get(e.mod)
	def := l.Def
	if err := e.app.Modules.Update(context.Background(), &def); err != nil { // reload
		t.Fatal(err)
	}
	list = nil
	doJSON(t, http.MethodGet, e.url+"/api/v1/sources", "", &list)
	if len(list) != n+1 {
		t.Fatalf("catalog list not refreshed after module reload: %d -> %d", n, len(list))
	}
}

func TestConcurrentSearchesShareOneRequest(t *testing.T) {
	e := newCacheEnv(t, "cache-flight")
	e.sc.Update(func() { e.sc.SearchDelay = 150 * time.Millisecond })
	before := e.searches()
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.search("q=tower&source=" + url.QueryEscape(e.key("A")))
		}()
	}
	wg.Wait()
	if n := e.searches() - before; n != 1 {
		t.Fatalf("want 1 upstream search, got %d", n)
	}
	// clearing the cache forces a new request
	doJSON(t, http.MethodPost, e.url+"/api/v1/system/cache/clear", `{"catalogs":true}`, nil)
	e.search("q=tower&source=" + url.QueryEscape(e.key("A")))
	if n := e.searches() - before; n != 2 {
		t.Fatalf("want a new upstream search after clearing, got %d", n)
	}
}

func TestThumbnailServedStaleWhenSourceFails(t *testing.T) {
	e := newCacheEnv(t, "cache-thumb")
	thumb := fmt.Sprintf("%s/api/v1/sources/%d/A/thumbnail?url=%%2Ftower", e.url, e.mod)
	resp, err := http.Get(thumb)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("thumbnail: %v %v", err, resp.StatusCode)
	}
	resp.Body.Close()
	// age the cached file past its TTL and break the source
	root := filepath.Join(e.app.Cfg.DataDir, "cache", "thumbs")
	old := time.Now().Add(-30 * 24 * time.Hour)
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			_ = os.Chtimes(p, old, old)
		}
		return nil
	})
	e.sc.Update(func() { e.sc.ThumbErr = errors.New("site down") })
	resp, err = http.Get(thumb)
	if err != nil || resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "private, max-age=300" {
		t.Fatalf("stale thumbnail: %v %v %q", err, resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	resp.Body.Close()
	var st api.CacheStatus
	doJSON(t, http.MethodGet, e.url+"/api/v1/system/cache", "", &st)
	if st.Images[0].Name != "thumbs" || st.Images[0].Files != 1 {
		t.Fatalf("cache status: %+v", st)
	}
}

func TestThrottledCatalogCoolsDown(t *testing.T) {
	e := newCacheEnv(t, "cache-throttle")
	e.sc.Update(func() { e.sc.SearchErr = map[string]error{"A": errors.New("HTTP error 429")} })
	got := e.search("q=tower&source=" + url.QueryEscape(e.key("A")))
	if got["A"].Error == "" {
		t.Fatal("expected an error group")
	}
	before := e.searches()
	got = e.search("q=towers&source=" + url.QueryEscape(e.key("A")))
	if e.searches() != before || got["A"].Error == "" {
		t.Fatal("a cooling-down catalog must not be queried")
	}
	var cl api.CatalogList
	doJSON(t, http.MethodGet, e.url+"/api/v1/catalogs", "", &cl)
	for _, c := range cl.Items {
		if c.ID == "A" && c.CooldownUntil == nil {
			t.Fatal("cooldown not reported")
		}
	}
	// clearing the cooldown makes the catalog usable again
	e.sc.Update(func() { e.sc.SearchErr = nil })
	doJSON(t, http.MethodPut, e.url+"/api/v1/catalogs", fmt.Sprintf(`{%q:{"clearCooldown":true}}`, e.key("A")), nil)
	if got := e.search("q=towers&source=" + url.QueryEscape(e.key("A"))); got["A"].Error != "" {
		t.Fatalf("still failing after clearing the cooldown: %v", got["A"].Error)
	}
}

func TestQuickSearchStopsAtFirstConfidentMatch(t *testing.T) {
	e := newCacheEnv(t, "quick")
	e.sc.Update(func() {
		e.sc.Mangas["J|/tower"].Chapters = []fakesource.Chapter{{URL: "/c1", Name: "Ch. 1", Number: 1}, {URL: "/c2", Name: "Ch. 2", Number: 2}}
		e.sc.Mangas["A|/tower"].Title = "Tower of God (Official)"
	})
	// J is searched first
	doJSON(t, http.MethodPut, e.url+"/api/v1/catalogs", fmt.Sprintf(`{%q:{"priority":10},%q:{"priority":20},%q:{"priority":30}}`, e.key("J"), e.key("A"), e.key("N")), nil)
	before := e.searches()
	var res api.QuickSearchResult
	if code := doJSON(t, http.MethodPost, e.url+"/api/v1/sources/quick-search", `{"query":"tower","titles":["Tower of God","Sinui Tap"]}`, &res); code != 200 {
		t.Fatalf("quick search: %d", code)
	}
	if res.Match == nil || res.Match.SourceID != "J" || res.Match.Score < 0.99 {
		t.Fatalf("match: %+v", res.Match)
	}
	if res.Match.Chapters == nil || res.Match.Chapters.Count != 2 || res.Match.Chapters.LatestNumber != 2 {
		t.Fatalf("chapter summary: %+v", res.Match.Chapters)
	}
	if n := e.searches() - before; n != 1 || len(res.Remaining) != 2 || len(res.Searched) != 1 {
		t.Fatalf("should stop after one catalog: searches=%d searched=%+v remaining=%v", n, res.Searched, res.Remaining)
	}
	// no confident match: every catalog is searched (results cached for the grid)
	res = api.QuickSearchResult{}
	doJSON(t, http.MethodPost, e.url+"/api/v1/sources/quick-search", `{"query":"tower","titles":["Tower of Babel"]}`, &res)
	if res.Match != nil || len(res.Searched) != 3 || len(res.Remaining) != 0 || len(res.Top) == 0 {
		t.Fatalf("no-match search: %+v", res)
	}
	if !res.Searched[0].Cached {
		t.Fatal("catalogs searched earlier should come from the cache")
	}
	// a confident match without chapters (licensed/removed) doesn't stop the search
	e.sc.Update(func() {
		e.sc.Mangas["J|/tower"].Chapters = nil
		e.sc.Mangas["A|/tower"].Chapters = []fakesource.Chapter{{URL: "/a1", Name: "Ch. 1", Number: 1}}
	})
	doJSON(t, http.MethodPost, e.url+"/api/v1/system/cache/clear", `{"catalogs":true}`, nil)
	res = api.QuickSearchResult{}
	doJSON(t, http.MethodPost, e.url+"/api/v1/sources/quick-search", `{"query":"tower","titles":["Tower of God"]}`, &res)
	if res.Match == nil || res.Match.SourceID != "A" || res.Match.Chapters.Count != 1 {
		t.Fatalf("want the match with chapters at A, got %+v", res.Match)
	}
}

func TestQuickSearchSkipsExcludedCatalogs(t *testing.T) {
	e := newCacheEnv(t, "quick-exclude")
	e.sc.Update(func() {
		e.sc.Mangas["J|/tower"].Chapters = []fakesource.Chapter{{URL: "/c1", Name: "Ch. 1", Number: 1}}
		e.sc.Mangas["A|/tower"].Chapters = []fakesource.Chapter{{URL: "/a1", Name: "Ch. 1", Number: 1}}
	})
	doJSON(t, http.MethodPut, e.url+"/api/v1/catalogs", fmt.Sprintf(`{%q:{"priority":10},%q:{"priority":20},%q:{"priority":30}}`, e.key("J"), e.key("A"), e.key("N")), nil)
	// J would match first; a series already linked to J asks for another source
	var res api.QuickSearchResult
	body := fmt.Sprintf(`{"query":"tower","titles":["Tower of God"],"exclude":[%q]}`, e.key("J"))
	if code := doJSON(t, http.MethodPost, e.url+"/api/v1/sources/quick-search", body, &res); code != 200 {
		t.Fatalf("quick search: %d", code)
	}
	if res.Match == nil || res.Match.SourceID != "A" {
		t.Fatalf("want the match at A, got %+v", res.Match)
	}
	for _, s := range res.Searched {
		if s.Key == e.key("J") {
			t.Fatal("an excluded catalog must not be searched")
		}
	}
}

// TestDefaultsSearchUsesEverySourceInTheList: a language default's sources
// are all searched (not just until the first match), empty matches are set
// aside, and a language without a list falls back to the quick search.
func TestDefaultsSearchUsesEverySourceInTheList(t *testing.T) {
	e := newCacheEnv(t, "defaults-search")
	e.sc.Update(func() {
		e.sc.Mangas["J|/tower"].Chapters = []fakesource.Chapter{{URL: "/c1", Name: "Ch. 1", Number: 1}}
		e.sc.Mangas["N|/tower"].Chapters = []fakesource.Chapter{{URL: "/n1", Name: "Ch. 1", Number: 1}}
		e.sc.Mangas["A|/tower"].Chapters = nil // licensed and removed
	})
	e.setSources(func(s *settings.Sources) {
		s.DefaultLanguages = []string{"en", "ja"}
		s.LanguageDefaults = []settings.LanguageDefault{{Language: "en", Sources: []string{e.key("A"), e.key("N")}}}
	})
	var res api.DefaultsResult
	if code := doJSON(t, http.MethodPost, e.url+"/api/v1/sources/defaults-search", `{"query":"tower","titles":["Tower of God"]}`, &res); code != 200 {
		t.Fatalf("defaults search: %d", code)
	}
	if len(res.Editions) != 2 || res.Editions[0].Lang != "en" || res.Editions[1].Lang != "ja" {
		t.Fatalf("editions: %+v", res.Editions)
	}
	en := res.Editions[0]
	if !en.FromDefaults || len(en.Sources) != 2 || en.Sources[0].Match != nil || en.Sources[0].Empty == nil || en.Sources[1].Match == nil || en.Sources[1].Match.SourceID != "N" {
		t.Fatalf("english edition: %+v", en)
	}
	ja := res.Editions[1]
	if ja.FromDefaults || len(ja.Sources) == 0 || ja.Sources[0].Match == nil || ja.Sources[0].Match.SourceID != "J" {
		t.Fatalf("japanese edition: %+v", ja)
	}
	// no language given and none set: the caller has to ask
	e.setSources(func(s *settings.Sources) { s.DefaultLanguages = []string{} })
	res = api.DefaultsResult{}
	doJSON(t, http.MethodPost, e.url+"/api/v1/sources/defaults-search", `{"query":"tower"}`, &res)
	if !res.NoLanguages || len(res.Editions) != 0 {
		t.Fatalf("without languages: %+v", res)
	}
}
