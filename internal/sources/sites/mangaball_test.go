package sites

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

const mballTitleID = "68514a0000000000000000aa"

func newMangaBall(t *testing.T) *mangaball {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/title/search-advanced", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("keyword") != "berserk" || q.Get("page") != "1" || q.Get("adult_mode") != "all" || q.Has("sort_by") {
			t.Errorf("search %v", q)
		}
		_, _ = w.Write([]byte(`{"data":[
			{"id":"` + mballTitleID + `","slug":"berserk","name":"Berserk","image":{"cdn_mangadex":"https://img.example/md.jpg","cover":{"path":"` + mballTitleID + `\\cover.webp"}}},
			{"id":"6a0000000000000000000000","slug":"berserk-deluxe","name":"Berserk Deluxe","image":{"file":"https://img.example/d.jpg"}}],
			"pagination":{"page":1,"total_pages":3}}`))
	})
	mux.HandleFunc("/api/v1/title/detail/{key}", func(w http.ResponseWriter, r *http.Request) {
		if k := r.PathValue("key"); k != mballTitleID && k != "berserk" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":"` + mballTitleID + `","slug":"berserk","name":"Berserk",
			"description":["Guts, the Black Swordsman."],"alternateName":["Beruseruku"],"status":"ongoing",
			"tags":[{"name":"Action"},{"name":"Dark Fantasy"}],"author":[{"name":"Kentaro Miura"}]}}`))
	})
	mux.HandleFunc("/api/v1/chapter/chapter-listing-by-title-id", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || string(body) != `{"title_id":"`+mballTitleID+`"}` {
			t.Errorf("chapters %s %q %s", r.Method, r.Header.Get("Content-Type"), body)
		}
		_, _ = w.Write([]byte(`{"data":[
			{"id":"ch-en","name":"Chapter 2 The Brand","number":2,"volume":1,"lang":"en","group":{"name":"Evil Genius"},"created_at":"2025-06-01T12:30:00.357000"},
			{"id":"ch-es","name":"La marca","number":2,"volume":0,"lang":"es","group":{"name":"Otro"},"created_at":"2025-06-01T12:30:00"},
			{"id":"ch-en-1","name":"Extra","number":1.5,"volume":0,"lang":"en","created_at":"bad"}]}`))
	})
	mux.HandleFunc("/api/v1/chapter-detail", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("chapter_id") != "ch-en" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"chapter":{"title_id":"` + mballTitleID + `","pages":["https://cdn.example/1.jpg"," https://cdn.example/2.jpg",""]}}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &mangaball{c: sourcekit.NewClient(srv.Client()), base: srv.URL, lang: "en", adult: true}
}

// TestMangaBall walks a series the way the library does: find it, read its
// details, list chapters, then a chapter's pages.
func TestMangaBall(t *testing.T) {
	m := newMangaBall(t)
	ctx := context.Background()

	res, err := m.Search(ctx, "berserk", 1)
	if err != nil || len(res.Mangas) != 2 || !res.HasNext {
		t.Fatalf("search: %v %+v", err, res)
	}
	if got := res.Mangas[0]; got.URL != "berserk" || got.ID != mballTitleID || got.Title != "Berserk" ||
		got.CoverURL != mballCovers+mballTitleID+"/cover.webp" {
		t.Fatalf("result %+v", got)
	}
	if res.Mangas[1].CoverURL != "https://img.example/d.jpg" {
		t.Fatalf("fallback cover %+v", res.Mangas[1])
	}

	d, err := m.Details(ctx, sourcekit.Ref{URL: "berserk"})
	if err != nil || d.Title != "Berserk" || d.ID != mballTitleID || d.Author != "Kentaro Miura" || d.Status != sourcekit.StatusOngoing ||
		d.Description != "Guts, the Black Swordsman.\n\nAlternative Names:\n- Beruseruku" ||
		strings.Join(d.Genres, ",") != "Action,Dark Fantasy" {
		t.Fatalf("details: %v %+v", err, d)
	}

	// a library entry saved from the site's older links has only the slug
	// with the id on its end
	for _, ref := range []sourcekit.Ref{{URL: "berserk", ID: mballTitleID}, {URL: "/title-detail/berserk-" + mballTitleID + "/"}, {URL: "berserk"}} {
		chs, err := m.Chapters(ctx, ref)
		if err != nil || len(chs) != 2 {
			t.Fatalf("chapters of %+v: %v %+v", ref, err, chs)
		}
		if c := chs[0]; c.URL != "ch-en" || c.Name != "Vol. 1 Chapter 2 The Brand" || c.Number != 2 || c.Scanlator != "Evil Genius" ||
			c.UploadedAt == nil || c.UploadedAt.Hour() != 12 {
			t.Fatalf("first chapter %+v", c)
		}
		if c := chs[1]; c.Name != "Ch. 1.5 Extra" || c.Scanlator != "" || c.UploadedAt != nil {
			t.Fatalf("second chapter %+v", c)
		}
	}

	pages, err := m.Pages(ctx, sourcekit.PageRef{URL: "ch-en"})
	if err != nil || len(pages) != 2 {
		t.Fatalf("pages: %v %+v", err, pages)
	}
	if pages[0].URL != "https://cdn.example/1.jpg" || pages[1].URL != "https://cdn.example/2.jpg" || pages[0].Headers["Referer"] == "" {
		t.Fatalf("pages %+v", pages)
	}
	if _, err := m.Pages(ctx, sourcekit.PageRef{URL: "gone"}); err == nil {
		t.Fatal("a missing chapter should fail")
	}
}

// TestMangaBallLanguages: each language is a catalog with Keiyoushi's id,
// asking the site for its regional variants.
func TestMangaBallLanguages(t *testing.T) {
	n := 0
	for _, s := range sourcekit.Build(sourcekit.Deps{Client: sourcekit.NewClient(nil)}) {
		if s.Info().Name == "Manga Ball" {
			n++
		}
	}
	if n != len(mballLangs) {
		t.Fatalf("%d Manga Ball catalogs for %d languages", n, len(mballLangs))
	}
	if got := (&mangaball{lang: "ja"}).siteLangs(); strings.Join(got, ",") != "ja,jp" {
		t.Fatalf("ja asks the site for %v", got)
	}
	if got := (&mangaball{lang: "pt-BR"}).siteLangs(); strings.Join(got, ",") != "pt-br,pt-pt" {
		t.Fatalf("pt-BR asks the site for %v", got)
	}
	if mballSlug("https://mangaball.net/title-detail/berserk-68514a/") != "berserk-68514a" || mballSlug("berserk-68514a") != "berserk-68514a" {
		t.Fatal("slug")
	}
}
