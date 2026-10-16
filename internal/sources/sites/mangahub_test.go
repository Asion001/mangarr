package sites

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// The answers below have the shapes the extension reads. The API only
// answers with a key the site handed out as the mhub_access cookie, and a
// chapter's pages come as a JSON string of their own.
const (
	mhubSearchJSON = `{"data":{"search":{"rows":[
	  {"title":"Solo Leveling","slug":"solo-leveling","image":"mcovers/solo.jpg"},
	  {"title":"Other","slug":"other","image":""}]}}}`

	mhubMangaJSON = `{"data":{"manga":{"title":"Solo Leveling","slug":"solo-leveling","status":"completed",
	  "image":"mcovers/solo.jpg","author":"Chugong","artist":"DUBU","genres":"Action, Fantasy",
	  "description":"A weak hunter.","alternativeTitle":"Na Honjaman Level Up; Only I Level Up",
	  "chapters":[{"number":1,"title":"","date":"2020-01-01T00:00:00.000Z"},
	              {"number":2,"title":"The  Second\nOne","date":"2020-01-08T00:00:00.000Z"},
	              {"number":2.5,"title":"Chapter 2.5: Extra","date":"2020-01-09T00:00:00.000Z"}]}}}`

	mhubChapterJSON = `{"data":{"chapter":{"pages":"{\"p\":\"solo-leveling/2/\",\"i\":[\"1.jpg\",\"2.jpg\"]}","mangaID":123,"number":2}}}`
)

// mhubServer plays the site (handing out keys) and the API (checking them).
type mhubServer struct {
	t *testing.T
	// key is the one the site hands out, next the one it switches to when
	// asked to reload; refused are keys the API turns down.
	mu       sync.Mutex
	key      string
	next     string
	refused  map[string]bool
	handouts []string
	queries  []string
}

func (s *mhubServer) handler() http.Handler {
	mux := http.NewServeMux()
	page := func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.handouts = append(s.handouts, r.URL.RequestURI())
		if r.URL.Query().Get("reloadKey") == "1" && s.next != "" {
			s.key = s.next
		}
		if r.Header.Get("Referer") == "" || r.Header.Get("Sec-Fetch-Mode") != "navigate" {
			s.t.Errorf("a key is asked for like a browser opening the page: %v", r.Header)
		}
		http.SetCookie(w, &http.Cookie{Name: "mhub_access", Value: s.key, Path: "/"})
		w.WriteHeader(http.StatusNotFound) // the page's status doesn't matter
	}
	mux.HandleFunc("/api/chapter-crypto", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"keys":{"` + mhubTestKeyID + `":"` + base64.RawURLEncoding.EncodeToString(mhubTestKey) + `"},"key":"wrong","keyId":"other"}`))
	})
	mux.HandleFunc("/chapter/", page)
	mux.HandleFunc("/manga/", page)
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		key := r.Header.Get("x-mhub-access")
		if r.Method != http.MethodPost || r.Header.Get("Accept") != "application/json" {
			s.t.Errorf("graphql %s accept %q", r.Method, r.Header.Get("Accept"))
		}
		if key == "" || s.refused[key] {
			_, _ = w.Write([]byte(`{"errors":[{"message":"Invalid API key provided"}],"data":null}`))
			return
		}
		var body struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.queries = append(s.queries, body.Query)
		switch {
		case strings.Contains(body.Query, "search(x: m01"):
			_, _ = w.Write([]byte(mhubSearchJSON))
		case strings.Contains(body.Query, `manga(x: m01, slug: "solo-leveling")`):
			_, _ = w.Write([]byte(mhubMangaJSON))
		case strings.Contains(body.Query, `chapter(x: m01, slug: "solo-leveling", number: 2.0)`):
			_, _ = w.Write([]byte(mhubChapterJSON))
		case strings.Contains(body.Query, `chapter(x: m01, slug: "solo-leveling", number: 1.0)`):
			out, _ := json.Marshal(map[string]any{"data": map[string]any{"chapter": map[string]any{
				"pages": mhubEncrypt(s.t, `["solo-leveling/1/1.jpg","https://other.example/2.jpg"]`), "mangaID": 123, "number": 1}}})
			_, _ = w.Write(out)
		default:
			s.t.Errorf("unexpected query %s", body.Query)
			_, _ = w.Write([]byte(`{"data":null}`))
		}
	})
	return mux
}

var (
	mhubTestKey   = []byte("0123456789abcdef0123456789abcdef")
	mhubTestKeyID = "e3e9478a9d64c71c"
)

// mhubEncrypt seals a page list the way the site does:
// "enc:v1:<key id>:<iv>:<tag>:<data>", AES-GCM, base64url.
func mhubEncrypt(t *testing.T, plain string) string {
	block, err := aes.NewCipher(mhubTestKey)
	if err != nil {
		t.Fatal(err)
	}
	gcm, _ := cipher.NewGCM(block)
	iv := []byte("123456789012")
	sealed := gcm.Seal(nil, iv, []byte(plain), nil)
	data, tag := sealed[:len(sealed)-16], sealed[len(sealed)-16:]
	enc := base64.RawURLEncoding.EncodeToString
	return "enc:v1:" + mhubTestKeyID + ":" + enc(iv) + ":" + enc(tag) + ":" + enc(data)
}

// TestMangaHubEncryptedPages: the site can send a chapter's page list
// encrypted, with the key on its own endpoint.
func TestMangaHubEncryptedPages(t *testing.T) {
	m, _ := newMangaHub(t)
	pages, err := m.Pages(context.Background(), sourcekit.PageRef{URL: "/solo-leveling/chapter-1.0"})
	if err != nil || len(pages) != 2 {
		t.Fatalf("pages: %v %+v", err, pages)
	}
	if pages[0].URL != "https://imgx.example/solo-leveling/1/1.jpg" || pages[1].URL != "https://other.example/2.jpg" {
		t.Fatalf("pages %+v", pages)
	}
}

func newMangaHub(t *testing.T) (*mhubSite, *mhubServer) {
	t.Helper()
	s := &mhubServer{t: t, key: "key-1", refused: map[string]bool{}}
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)
	return &mhubSite{c: sourcekit.NewClient(srv.Client()), id: "test", name: "MangaHub", base: srv.URL, api: srv.URL,
		cdn: "https://imgx.example", thumbs: "https://thumb.example", source: "m01", refresh: &mhubRefresh{}}, s
}

// TestMangaHub reads a series the way the library does: find it, read its
// details, list chapters, then a chapter's pages. The first call has no key
// yet, so the site is asked for one.
func TestMangaHub(t *testing.T) {
	m, srv := newMangaHub(t)
	ctx := context.Background()

	res, err := m.Search(ctx, `solo "leveling"`, 2)
	if err != nil || len(res.Mangas) != 2 || res.HasNext {
		t.Fatalf("search: %v %+v", err, res)
	}
	if len(srv.handouts) != 1 || !strings.HasPrefix(srv.handouts[0], "/chapter/martial-peak/chapter-") {
		t.Fatalf("key requests %v", srv.handouts)
	}
	if q := srv.queries[0]; !strings.Contains(q, `q: "solo \"leveling\""`) || !strings.Contains(q, "offset: 30") ||
		!strings.Contains(q, "mod: POPULAR") {
		t.Fatalf("search query %s", q)
	}
	got := res.Mangas[0]
	if got.Title != "Solo Leveling" || got.URL != "/manga/solo-leveling" || got.CoverURL != "https://thumb.example/mcovers/solo.jpg" ||
		res.Mangas[1].CoverURL != "" {
		t.Fatalf("results %+v", res.Mangas)
	}

	d, err := m.Details(ctx, sourcekit.Ref{URL: got.URL})
	if err != nil || d.Title != "Solo Leveling" || d.Author != "Chugong" || d.Artist != "DUBU" ||
		d.Status != sourcekit.StatusCompleted || strings.Join(d.Genres, "|") != "Action|Fantasy" {
		t.Fatalf("details: %v %+v", err, d)
	}
	if d.Description != "A weak hunter.\n\nAlternative Names:\n- Na Honjaman Level Up\n- Only I Level Up" {
		t.Fatalf("description %q", d.Description)
	}

	chs, err := m.Chapters(ctx, sourcekit.Ref{URL: got.URL})
	if err != nil || len(chs) != 3 {
		t.Fatalf("chapters: %v %+v", err, chs)
	}
	// newest first; the name is built as the extension builds it, and the
	// link keeps Kotlin's "2.0"
	if chs[0].Name != "Chapter 2.5: Extra" || chs[1].Name != "Chapter 2 - The Second One" || chs[2].Name != "Chapter 1" {
		t.Fatalf("names %q %q %q", chs[0].Name, chs[1].Name, chs[2].Name)
	}
	if chs[1].URL != "/solo-leveling/chapter-2.0" || chs[1].Number != 2 || chs[1].UploadedAt == nil ||
		chs[1].WebURL != m.base+"/chapter/solo-leveling/chapter-2.0" {
		t.Fatalf("chapter %+v", chs[1])
	}

	pages, err := m.Pages(ctx, sourcekit.PageRef{URL: chs[1].URL})
	if err != nil || len(pages) != 2 {
		t.Fatalf("pages: %v %+v", err, pages)
	}
	if pages[0].URL != "https://imgx.example/solo-leveling/2/1.jpg" || pages[1].Index != 1 {
		t.Fatalf("pages %+v", pages)
	}
	// reading a chapter leaves the cookie a browser would have
	base, _ := url.Parse(m.base)
	var recent bool
	for _, c := range m.c.HTTP.Jar.Cookies(base) {
		recent = recent || c.Name == "recently"
	}
	if !recent || len(srv.handouts) != 1 {
		t.Fatalf("recently cookie %v, key requests %v", recent, srv.handouts)
	}
}

// TestMangaHubRefusedKey: when the API refuses the key, a new one is fetched
// from the page being read, asking the site to reload it if it hands out the
// same one, and the query is tried again.
func TestMangaHubRefusedKey(t *testing.T) {
	m, srv := newMangaHub(t)
	base, _ := url.Parse(m.base)
	m.c.HTTP.Jar.SetCookies(base, []*http.Cookie{{Name: "mhub_access", Value: "stale", Path: "/"}})
	srv.refused["stale"] = true
	srv.key, srv.next = "stale", "fresh" // a plain page load hands out the same key again

	d, err := m.Details(context.Background(), sourcekit.Ref{URL: "/manga/solo-leveling"})
	if err != nil || d.Title != "Solo Leveling" {
		t.Fatalf("details: %v %+v", err, d)
	}
	if strings.Join(srv.handouts, " ") != "/manga/solo-leveling /manga/solo-leveling?reloadKey=1" {
		t.Fatalf("key requests %v", srv.handouts)
	}
	if m.key() != "fresh" {
		t.Fatalf("key %q", m.key())
	}
}

// TestMangaHubLinks: chapter links carry the slug and Kotlin's float.
func TestMangaHubLinks(t *testing.T) {
	for in, want := range map[float64]string{1: "1.0", 12.5: "12.5", 100.1: "100.1"} {
		if got := mhubFloat(in); got != want {
			t.Errorf("mhubFloat(%v) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"/solo-leveling/chapter-12.5", "https://mangahub.io/chapter/solo-leveling/chapter-12.5"} {
		if slug, n, ok := mhubChapter(in); !ok || slug != "solo-leveling" || n != 12.5 {
			t.Errorf("mhubChapter(%q) = %q %v %v", in, slug, n, ok)
		}
	}
	if slug := mhubSlug(sourcekit.Ref{URL: "https://mangahub.io/manga/solo-leveling"}); slug != "solo-leveling" {
		t.Errorf("mhubSlug = %q", slug)
	}
}
