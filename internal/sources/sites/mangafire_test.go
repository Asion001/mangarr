package sites

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// The answers below have the shapes the extension parses: lists as
// {items, meta}, one title or chapter as {data}.
const (
	mfListJSON = `{"items":[
	  {"hid":"abc12","slug":"one-piece","title":"One Piece","poster":{"small":"https://s.example/s.jpg","large":"https://s.example/l.jpg"}},
	  {"hid":"def34","title":"One Punch-Man","poster":{"medium":"https://s.example/m.jpg"}}],
	  "meta":{"page":1,"lastPage":3,"hasNext":true}}`

	mfTitleJSON = `{"data":{"hid":"abc12","slug":"one-piece","title":"One Piece","type":"manga","status":"releasing",
	  "poster":{"large":"https://s.example/l.jpg"},"synopsisHtml":"<p>Gol D. Roger was known as the <b>Pirate King</b>.</p>",
	  "authors":[{"title":"Oda Eiichiro"}],"artists":[{"title":"Oda Eiichiro"}],
	  "genres":[{"title":"Action"},{"title":"Adventure"}],"themes":[{"title":"Pirates"}],
	  "rating":9.4000000000000003,"ratingCount":198}}`

	mfChaptersPage1 = `{"items":[
	  {"id":9002,"number":1100,"name":"Chapter 1100: Thank You, Bonney","createdAt":1783633137,"type":"official"},
	  {"id":9001,"number":1099.5,"name":null,"createdAt":1783000000}],"meta":{"lastPage":2}}`
	mfChaptersPage2 = `{"items":[{"id":1,"number":1,"name":"Romance Dawn","type":"scan"}],"meta":{"lastPage":2}}`

	mfPagesJSON = `{"data":{"pages":[{"url":"https://img.example/1.jpg"},{"url":"https://img.example/2.jpg"}]}}`
)

// mfUnsign undoes mfSign, so a test can read back what a vrf signed.
func mfUnsign(t *testing.T, vrf string) string {
	t.Helper()
	data, err := base64.RawURLEncoding.DecodeString(vrf)
	if err != nil {
		t.Fatalf("vrf %q: %v", vrf, err)
	}
	for s := len(mfStages) - 1; s >= 0; s-- {
		st := mfStages[s]
		var inverse [256]byte
		for i, v := range st.table {
			inverse[v] = byte(i)
		}
		out := make([]byte, len(data))
		prev := st.iv
		for i, b := range data {
			out[i] = inverse[b] ^ st.key[i%len(st.key)] ^ prev
			prev = b
		}
		data = out
	}
	return string(data)
}

func newMangaFire(t *testing.T) *mangafire {
	t.Helper()
	// signed checks every API call carries a vrf signing exactly the
	// canonical request the extension would.
	signed := func(want string, next func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if got := mfUnsign(t, r.URL.Query().Get("vrf")); got != want {
				t.Errorf("%s signed %q, want %q", r.URL.Path, got, want)
			}
			next(w, r)
		}
	}
	write := func(body string) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/titles", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case q.Get("keyword") != "":
			signed("/titles?content_rating[0]=safe&content_rating[1]=suggestive&genres_mode=and&keyword=one piece&limit=50&order[relevance]=desc&page=1&theme_mode=and",
				write(mfListJSON))(w, r)
		case q.Get("order[views_30d]") != "":
			signed("/titles?content_rating[0]=safe&content_rating[1]=suggestive&limit=50&order[views_30d]=desc&page=2", write(mfListJSON))(w, r)
		default:
			t.Errorf("unexpected list %v", q)
		}
	})
	mux.HandleFunc("/api/titles/abc12", signed("/titles/abc12", write(mfTitleJSON)))
	mux.HandleFunc("/api/titles/gone1", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/api/titles/abc12/chapters", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		want := "/titles/abc12/chapters?language=pt-br&limit=200&order=desc&page=" + page + "&sort=number"
		body := mfChaptersPage1
		if page == "2" {
			body = mfChaptersPage2
		}
		signed(want, write(body))(w, r)
	})
	mux.HandleFunc("/api/chapters/9002", signed("/chapters/9002", write(mfPagesJSON)))
	mux.HandleFunc("/api/volumes/77", signed("/volumes/77", write(mfPagesJSON)))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &mangafire{c: sourcekit.NewClient(srv.Client()), base: srv.URL, code: "pt-BR", lang: "pt-br"}
}

// TestMangaFire walks a whole library flow through the signed API: search,
// details, chapters (two pages of them) and a chapter's pages.
func TestMangaFire(t *testing.T) {
	f := newMangaFire(t)
	ctx := context.Background()
	if err := f.SetOption("content_rating", []any{"suggestive", "safe"}); err != nil {
		t.Fatal(err)
	}

	res, err := f.Search(ctx, " one piece ", 1)
	if err != nil || len(res.Mangas) != 2 || !res.HasNext {
		t.Fatalf("search: %v %+v", err, res)
	}
	if m := res.Mangas[0]; m.URL != "/title/abc12-one-piece" || m.ID != "abc12" || m.CoverURL != "https://s.example/l.jpg" {
		t.Fatalf("result %+v", m)
	}
	if m := res.Mangas[1]; m.URL != "/title/def34" || m.CoverURL != "https://s.example/m.jpg" {
		t.Fatalf("result without a slug %+v", m)
	}
	if _, err := f.Popular(ctx, 2); err != nil {
		t.Fatalf("popular: %v", err)
	}

	d, err := f.Details(ctx, sourcekit.Ref{URL: "/title/abc12-one-piece"})
	if err != nil || d.Title != "One Piece" || d.Author != "Oda Eiichiro" || d.Status != sourcekit.StatusOngoing ||
		d.Description != "Gol D. Roger was known as the Pirate King." || len(d.Genres) != 4 || d.Genres[0] != "Manga" {
		t.Fatalf("details: %v %+v", err, d)
	}
	if d.Rating == nil || *d.Rating != (sourcekit.Rating{Score: 9.4, Max: 10, Votes: 198}) {
		t.Errorf("rating %+v", d.Rating)
	}
	if _, err := f.Details(ctx, sourcekit.Ref{URL: "/title/gone1-x"}); !errors.Is(err, sourcekit.ErrNotFound) {
		t.Fatalf("a missing title: %v", err)
	}

	chs, err := f.Chapters(ctx, sourcekit.Ref{URL: "/title/abc12-one-piece"})
	if err != nil || len(chs) != 3 {
		t.Fatalf("chapters: %v %+v", err, chs)
	}
	// a name with its own number keeps it; one without gets "Ch. n"
	if c := chs[0]; c.URL != "/title/abc12-one-piece/9002-chapter-1100-pt-br" || c.Name != "Chapter 1100: Thank You, Bonney" ||
		c.Number != 1100 || c.Scanlator != "official" || c.UploadedAt == nil {
		t.Fatalf("first chapter %+v", c)
	}
	if c := chs[1]; c.Name != "Ch. 1099.5" || c.Number != 1099.5 || c.Scanlator != "Unknown" ||
		c.URL != "/title/abc12-one-piece/9001-chapter-1099.5-pt-br" {
		t.Fatalf("second chapter %+v", c)
	}
	if c := chs[2]; c.Name != "Ch. 1 - Romance Dawn" || c.Number != 1 || c.UploadedAt != nil {
		t.Fatalf("last chapter %+v", c)
	}

	pages, err := f.Pages(ctx, sourcekit.PageRef{URL: chs[0].URL})
	if err != nil || len(pages) != 2 || pages[1].URL != "https://img.example/2.jpg" || pages[0].Headers["Referer"] == "" {
		t.Fatalf("pages: %v %+v", err, pages)
	}
	// a volume, as the extension's "prefer volumes" lists them
	if pages, err := f.Pages(ctx, sourcekit.PageRef{URL: "/title/abc12-one-piece/volume/77"}); err != nil || len(pages) != 2 {
		t.Fatalf("volume pages: %v %+v", err, pages)
	}
}

// TestMangaFireSign checks the signer against vectors worked out
// independently from the extension's VrfSigner.
func TestMangaFireSign(t *testing.T) {
	for in, want := range map[string]string{
		"/titles/abc12": "8sK3xtqdFdsz1msMFg",
		"/titles?genres_mode=and&keyword=one piece&limit=50&order[relevance]=desc&page=1&theme_mode=and": "8sK3xtqdFZcCRT11BJBKh-X6a2zK_pw6HuwZwVG9WZ6TPzqkoxZeAiqn-AVATUUdqQ5odoLuumuH4xip5BCMj38GULkuEl_HcQ9dOZxjGMX9YQmagBTlsx1PuJ2Xaw",
	} {
		if got := mfSign(in); got != want {
			t.Errorf("mfSign(%q) = %q, want %q", in, got, want)
		}
	}
	// the request itself keeps the "[]" names, in sorted order, vrf last
	f := &mangafire{base: "https://mangafire.to"}
	got := f.mfURL("/api/titles", []mfParam{{"page", "1"}, {"content_rating[]", "safe"}, {"keyword", "a b"}})
	want := "https://mangafire.to/api/titles?content_rating%5B%5D=safe&keyword=a%20b&page=1&vrf=" +
		mfSign("/titles?content_rating[0]=safe&keyword=a b&page=1")
	if got != want {
		t.Fatalf("url\n got %s\nwant %s", got, want)
	}
}

// TestMangaFireIDs: every url shape the extension ever stored finds its title.
func TestMangaFireIDs(t *testing.T) {
	for in, want := range map[string]string{
		"/title/abc12-one-piece":                         "abc12",
		"/title/abc12-dr.-stone":                         "abc12",
		"/title/abc12":                                   "abc12",
		"https://mangafire.to/manga/one-piece.abc12":     "abc12",
		"/title/abc12-one-piece/9002-chapter-1100-pt-br": "abc12",
	} {
		if got := mfHID(in, ""); got != want {
			t.Errorf("mfHID(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestMangaFireLanguages: a catalog per language, each with the id Mihon
// gives it and the site's own language code.
func TestMangaFireLanguages(t *testing.T) {
	langs := map[string]string{}
	for _, s := range sourcekit.Build(sourcekit.Deps{Client: sourcekit.NewClient(nil)}) {
		if i := s.Info(); i.Name == "MangaFire" {
			if i.ID != sourcekit.KeiyoushiID("MangaFire", i.Lang, 1) {
				t.Errorf("%s catalog id %s", i.Lang, i.ID)
			}
			langs[i.Lang] = s.(*mangafire).lang
		}
	}
	if len(langs) != len(mfLangs) || langs["es-419"] != "es-la" || langs["pt-BR"] != "pt-br" || langs["ja"] != "ja" {
		t.Fatalf("languages %v", langs)
	}
}
