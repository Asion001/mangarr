package sites

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// fakeMangaDex serves the pieces of the API the site uses.
func fakeMangaDex(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/manga", func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query().Get("title"); q != "" && q != "berserk" {
			_, _ = w.Write([]byte(`{"data":[],"limit":24,"total":0}`))
			return
		}
		_, _ = w.Write([]byte(`{"limit":24,"total":26,"data":[
			{"id":"801513ba-a712-498c-8f57-cae55b38cc92","attributes":{"title":{"en":"Berserk"},"status":"ongoing",
			 "description":{"en":"Guts, a former mercenary."},
			 "tags":[{"attributes":{"name":{"en":"Action"}}},{"attributes":{"name":{"en":"Horror"}}}]},
			 "relationships":[{"id":"c","type":"cover_art","attributes":{"fileName":"cover.jpg"}},
			                  {"id":"a","type":"author","attributes":{"name":"Kentaro Miura"}}]}]}`))
	})
	mux.HandleFunc("/manga/801513ba-a712-498c-8f57-cae55b38cc92", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"id":"801513ba-a712-498c-8f57-cae55b38cc92","attributes":{"title":{"en":"Berserk"},
			"status":"hiatus","description":{"en":"Guts, a former mercenary."},
			"tags":[{"attributes":{"name":{"en":"Action"}}}]},
			"relationships":[{"id":"c","type":"cover_art","attributes":{"fileName":"cover.jpg"}},
			                 {"id":"a","type":"author","attributes":{"name":"Kentaro Miura"}},
			                 {"id":"b","type":"artist","attributes":{"name":"Studio Gaga"}}]}}`))
	})
	mux.HandleFunc("/statistics/manga/801513ba-a712-498c-8f57-cae55b38cc92", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"statistics":{"801513ba-a712-498c-8f57-cae55b38cc92":{"rating":{"bayesian":9.5651,"distribution":{"9":3,"10":7}}}}}`))
	})
	mux.HandleFunc("/manga/801513ba-a712-498c-8f57-cae55b38cc92/feed", func(w http.ResponseWriter, r *http.Request) {
		if lang := r.URL.Query().Get("translatedLanguage[]"); lang != "en" {
			t.Errorf("feed asked for %q", lang)
		}
		_, _ = w.Write([]byte(`{"limit":500,"offset":0,"total":2,"data":[
			{"id":"ch-2","attributes":{"volume":"41","chapter":"365","title":"Beyond","translatedLanguage":"en","publishAt":"2026-01-02T00:00:00Z","pages":20},
			 "relationships":[{"id":"g","type":"scanlation_group","attributes":{"name":"Evil Genius"}}]},
			{"id":"ch-1","attributes":{"volume":"41","chapter":"364","title":"","translatedLanguage":"en","publishAt":"2026-01-01T00:00:00Z","pages":18},
			 "relationships":[]},
			{"id":"ch-x","attributes":{"chapter":"999","externalUrl":"https://elsewhere.example/ch","translatedLanguage":"en"},"relationships":[]}]}`))
	})
	mux.HandleFunc("/at-home/server/ch-2", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"baseUrl":"https://cdn.example.org","chapter":{"hash":"abc","data":["1.png","2.png","3.png"]}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newMangaDex(t *testing.T) (*mangadex, *httptest.Server) {
	srv := fakeMangaDex(t)
	return &mangadex{c: sourcekit.NewClient(srv.Client()), api: srv.URL, site: "https://mangadex.org",
		cdn: "https://uploads.mangadex.org", code: "en", lang: "en", ratings: []string{"safe"}}, srv
}

// TestMangaDex walks a whole library flow: find a series, read its details,
// list its chapters and get a chapter's pages.
func TestMangaDex(t *testing.T) {
	m, _ := newMangaDex(t)
	ctx := context.Background()

	res, err := m.Search(ctx, "berserk", 1)
	if err != nil || len(res.Mangas) != 1 {
		t.Fatalf("search: %v %+v", err, res)
	}
	got := res.Mangas[0]
	if got.Title != "Berserk" || got.URL != "/manga/801513ba-a712-498c-8f57-cae55b38cc92" ||
		got.CoverURL != "https://uploads.mangadex.org/covers/801513ba-a712-498c-8f57-cae55b38cc92/cover.jpg.512.jpg" {
		t.Fatalf("result %+v", got)
	}
	if !res.HasNext {
		t.Fatal("there are more results than one page")
	}

	d, err := m.Details(ctx, sourcekit.Ref{URL: got.URL})
	if err != nil || d.Author != "Kentaro Miura" || d.Artist != "Studio Gaga" || d.Status != sourcekit.StatusHiatus ||
		len(d.Genres) != 1 || d.WebURL == "" {
		t.Fatalf("details: %v %+v", err, d)
	}
	if d.Rating == nil || *d.Rating != (sourcekit.Rating{Score: 9.57, Max: 10, Votes: 10}) {
		t.Errorf("rating %+v", d.Rating)
	}

	chs, err := m.Chapters(ctx, sourcekit.Ref{URL: got.URL})
	if err != nil || len(chs) != 2 {
		t.Fatalf("chapters: %v %+v", err, chs)
	}
	if chs[0].Number != 365 || chs[0].Scanlator != "Evil Genius" || chs[0].Name != "Vol. 41 Ch. 365 – Beyond" {
		t.Fatalf("first chapter %+v", chs[0])
	}
	if chs[1].Name != "Vol. 41 Ch. 364" || chs[1].UploadedAt == nil {
		t.Fatalf("second chapter %+v", chs[1])
	}

	pages, err := m.Pages(ctx, sourcekit.PageRef{URL: chs[0].URL})
	if err != nil || len(pages) != 3 {
		t.Fatalf("pages: %v %+v", err, pages)
	}
	if pages[0].URL != "https://cdn.example.org/data/abc/1.png" || pages[0].Headers["Referer"] == "" {
		t.Fatalf("page %+v", pages[0])
	}
}

// TestMangaDexNotFound: a manga the site no longer has is reported as gone,
// not as a random failure, so the core can tell the difference.
func TestMangaDexNotFound(t *testing.T) {
	m, _ := newMangaDex(t)
	_, err := m.Details(context.Background(), sourcekit.Ref{URL: "/manga/00000000-0000-0000-0000-000000000000"})
	if err == nil || !errorsAs(err, new(*sourcekit.StatusError)) && err.Error() == "" {
		t.Fatalf("details of a missing manga: %v", err)
	}
	if _, err := m.Pages(context.Background(), sourcekit.PageRef{URL: "/not-a-chapter"}); err == nil {
		t.Fatal("a url that isn't a chapter should be refused")
	}
}

// TestMangaDexOptions: the site's own settings are applied.
func TestMangaDexOptions(t *testing.T) {
	m, _ := newMangaDex(t)
	if err := m.SetOption("adult", true); err != nil || !m.hasRating("pornographic") {
		t.Fatalf("ratings: %v %v", err, m.ratings)
	}
	if err := m.SetOption("nope", 1); err == nil {
		t.Fatal("an unknown option should be refused")
	}
}

// TestMangaDexLanguages: every language is its own catalog, with the id
// Mihon gives that language's MangaDex source and the API's language code.
func TestMangaDexLanguages(t *testing.T) {
	ids := map[string]sourcekit.Info{}
	for _, s := range sourcekit.Build(sourcekit.Deps{Client: sourcekit.NewClient(nil)}) {
		if i := s.Info(); i.Name == "MangaDex" {
			ids[i.ID] = i
		}
	}
	if len(ids) != len(mangadexLangs) {
		t.Fatalf("%d MangaDex catalogs for %d languages", len(ids), len(mangadexLangs))
	}
	// the English one keeps the id libraries already link to
	if en, ok := ids[mangadexID]; !ok || en.Lang != "en" {
		t.Fatalf("English catalog: %+v", en)
	}
	if i := ids[sourcekit.KeiyoushiID("MangaDex", "pt-BR", 1)]; i.Lang != "pt-BR" {
		t.Fatalf("Brazilian Portuguese catalog: %+v", i)
	}
	if got := dexLang("es-419"); got != "es-la" {
		t.Fatalf("es-419 asks the API for %q", got)
	}
}
