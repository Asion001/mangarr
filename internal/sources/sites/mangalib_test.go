package sites

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// The answers below are trimmed from the site's own: a manga is addressed by
// "<id>--<slug>", its description arrives as a rich-text document, chapters
// come in per-team branches, and page paths are relative to an image server
// the API names separately.
const (
	mlListJSON = `{"data":[
	  {"id":41,"name":"Kuroshitsuji","rus_name":"Тёмный дворецкий","eng_name":"Black Butler","slug_url":"41--kuroshitsuji",
	   "cover":{"default":"https://cover.example/41.jpg","thumbnail":"https://cover.example/41_thumb.jpg"},
	   "status":{"id":1,"label":"Онгоинг"}}],
	  "meta":{"has_next_page":true}}`

	mlDetailsJSON = `{"data":{"id":41,"name":"Kuroshitsuji","rus_name":"Тёмный дворецкий","eng_name":"Black Butler",
	  "slug_url":"41--kuroshitsuji","cover":{"default":"https://cover.example/41.jpg"},
	  "status":{"id":1,"label":"Онгоинг"},
	  "summary":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Граф Фантомхайв."}]}]},
	  "genres":[{"name":"Детектив"},{"name":"Фэнтези"}],
	  "authors":[{"name":"TOBOSO Yana"}],"artists":[{"name":"TOBOSO Yana"}]}}`

	mlChaptersJSON = `{"data":[
	  {"id":2290,"volume":"1","number":"1","name":"На рассвете","branches":[
	    {"id":2290,"branch_id":null,"created_at":"2016-05-31T23:01:01.000000Z","teams":[{"name":"Неизвестный"}]},
	    {"id":9001,"branch_id":7,"created_at":"2020-01-02T03:04:05.000000Z","teams":[{"name":"Vesperum"}]}]},
	  {"id":2291,"volume":"1","number":"2","name":"","branches":[
	    {"id":2291,"branch_id":null,"created_at":"2016-06-01T23:01:01.000000Z","teams":[]}]},
	  {"id":2292,"volume":"1","number":"3","name":"","branches":[
	    {"id":2292,"branch_id":null,"created_at":"2016-06-02T23:01:01.000000Z","teams":[],
	     "expired_type":2,"restricted_view":{"is_open":false,"expired_at":null,"price":null}}]}]}`

	mlPagesJSON = `{"data":{"id":2290,"pages":[
	  {"slug":2,"url":"//manga/kuroshitsuji/chapters/1-1/02.png"},
	  {"slug":1,"url":"//manga/kuroshitsuji/chapters/1-1/01.png"}]}}`

	mlConstantsJSON = `{"data":{"imageServers":[
	  {"id":"main","label":"Первый","url":"https://img.example","site_ids":[1,3]},
	  {"id":"main","label":"Первый","url":"https://other.example","site_ids":[2]},
	  {"id":"compress","label":"Сжатия","url":"https://img3.example","site_ids":[1,3]}]}}`
)

func newMangaLib(t *testing.T) (*mangalib, *int) {
	t.Helper()
	constants := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/manga", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Site-Id") != "1" {
			t.Errorf("the API expects a site id, got %q", r.Header.Get("Site-Id"))
		}
		_, _ = w.Write([]byte(mlListJSON))
	})
	mux.HandleFunc("/api/manga/41--kuroshitsuji", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(mlDetailsJSON))
	})
	mux.HandleFunc("/api/manga/41--kuroshitsuji/chapters", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(mlChaptersJSON))
	})
	mux.HandleFunc("/api/manga/41--kuroshitsuji/chapter", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("number") != "1" || q.Get("volume") != "1" || q.Get("branch_id") != "7" {
			t.Errorf("chapter %v", q)
		}
		_, _ = w.Write([]byte(mlPagesJSON))
	})
	mux.HandleFunc("/api/constants", func(w http.ResponseWriter, r *http.Request) {
		constants++
		_, _ = w.Write([]byte(mlConstantsJSON))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &mangalib{c: sourcekit.NewClient(srv.Client()), site: "https://mangalib.example", api: srv.URL,
		title: "en", server: "main"}, &constants
}

// TestMangaLib reads a series the way the library does, and makes each team's
// translation a chapter of its own.
func TestMangaLib(t *testing.T) {
	m, constants := newMangaLib(t)
	ctx := context.Background()

	res, err := m.Search(ctx, "kuro", 1)
	if err != nil || len(res.Mangas) != 1 || !res.HasNext {
		t.Fatalf("search: %v %+v", err, res)
	}
	got := res.Mangas[0]
	if got.Title != "Black Butler" || got.URL != "/41--kuroshitsuji" || got.ID != "41" || got.CoverURL == "" {
		t.Fatalf("result %+v", got)
	}

	d, err := m.Details(ctx, sourcekit.Ref{URL: got.URL})
	if err != nil || d.Status != sourcekit.StatusOngoing || d.Author != "TOBOSO Yana" || len(d.Genres) != 2 ||
		d.Description != "Граф Фантомхайв." || d.WebURL != "https://mangalib.example/ru/manga/41--kuroshitsuji" {
		t.Fatalf("details: %v %+v", err, d)
	}

	chs, err := m.Chapters(ctx, sourcekit.Ref{URL: d.URL})
	if err != nil || len(chs) != 3 { // chapter 1 is translated twice; chapter 3 is paid
		t.Fatalf("chapters: %v %+v", err, chs)
	}
	if chs[0].Number != 1 || chs[0].Scanlator != "Неизвестный" || chs[0].URL != "/41--kuroshitsuji/chapter?number=1&volume=1" {
		t.Fatalf("chapter %+v", chs[0])
	}
	if chs[0].WebURL != "https://mangalib.example/ru/41--kuroshitsuji/read/v1/c1" {
		t.Fatalf("public chapter URL %q", chs[0].WebURL)
	}
	if chs[1].Scanlator != "Vesperum" || chs[1].URL != "/41--kuroshitsuji/chapter?branch_id=7&number=1&volume=1" {
		t.Fatalf("the other team's chapter %+v", chs[1])
	}
	if chs[1].WebURL != "https://mangalib.example/ru/41--kuroshitsuji/read/v1/c1?bid=7" {
		t.Fatalf("branch public chapter URL %q", chs[1].WebURL)
	}
	if chs[2].Name != "Глава 2" || chs[2].UploadedAt == nil {
		t.Fatalf("a chapter with no name of its own: %+v", chs[2])
	}

	// pages come back in the site's order and from this site's image server
	pages, err := m.Pages(ctx, sourcekit.PageRef{URL: chs[1].URL})
	if err != nil || len(pages) != 2 {
		t.Fatalf("pages: %v %+v", err, pages)
	}
	if pages[0].URL != "https://img.example//manga/kuroshitsuji/chapters/1-1/01.png" || pages[0].Headers["Referer"] == "" {
		t.Fatalf("page %+v", pages[0])
	}

	// the server list is asked for once, not per chapter
	if _, err := m.Pages(ctx, sourcekit.PageRef{URL: chs[1].URL}); err != nil {
		t.Fatal(err)
	}
	if *constants != 1 {
		t.Fatalf("the image servers were looked up %d times", *constants)
	}
}

// TestMangaLibSlug: a link is stored as "<id>--<slug>", which is what keeps
// it working when the site renames a manga.
func TestMangaLibSlug(t *testing.T) {
	for _, in := range []string{"https://mangalib.me/ru/manga/41--kuroshitsuji", "/41--kuroshitsuji",
		"/41--kuroshitsuji/chapter?number=1"} {
		if got := mangalibSlug(in); got != "41--kuroshitsuji" {
			t.Fatalf("mangalibSlug(%q) = %q", in, got)
		}
	}
}
