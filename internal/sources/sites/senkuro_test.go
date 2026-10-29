package sites

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// The answers below are trimmed from the site's own: everything is one POST
// to /graphql, told apart by the operation name, and everything is addressed
// by node id.
const (
	snSearchJSON = `{"data":{"mangaTachiyomiSearch":{"mangas":[
	  {"id":"MANGA:1","slug":"berserk","originalName":{"lang":"EN","content":"Berserk"},
	   "titles":[{"lang":"EN","content":"Berserk"},{"lang":"RU","content":"Берсерк"}],
	   "cover":{"original":{"url":"https://img.example/berserk.jpeg"}}}]}}}`

	snDetailsJSON = `{"data":{"mangaTachiyomiInfo":{"id":"MANGA:1","slug":"berserk",
	  "originalName":{"lang":"EN","content":"Berserk"},
	  "titles":[{"lang":"EN","content":"Berserk"},{"lang":"RU","content":"Берсерк"}],
	  "localizations":[{"lang":"RU","description":"Его зовут Гатс."}],
	  "status":"ONGOING","score":9.479999542236328,"labels":[{"slug":"dark-fantasy","titles":[{"lang":"RU","content":"Тёмное фэнтези"}]}],
	  "cover":{"original":{"url":"https://img.example/berserk.jpeg"}},
	  "mainStaff":[{"roles":["STORY_AND_ART"],"person":{"name":"Kentarou Miura"}},
	               {"roles":["ART"],"person":{"name":"Studio Gaga"}}]}}}`

	snChaptersJSON = `{"data":{"mangaTachiyomiChapters":{"chapters":[
	  {"id":"CHAPTER:2","slug":"200","name":"Неведение безначально","teamIds":["TEAM:1"],"number":"399","volume":"43",
	   "createdAt":"2025-09-11T17:56:47.379969","updatedAt":"2025-09-11T17:56:47.379969"},
	  {"id":"CHAPTER:1","slug":"100","name":"","teamIds":["TEAM:1"],"number":"398","volume":"43",
	   "createdAt":"2025-06-27T06:34:45.286525","updatedAt":null}],
	  "teams":[{"id":"TEAM:1","name":"Команда"}]}}}`

	snPagesJSON = `{"data":{"mangaTachiyomiChapterPages":{"pages":[
	  {"url":"https://img.example/system/tachiyomi-op.jpeg"},
	  {"url":"https://img.example/manga-chapters/1/1/page-1.jpeg"},
	  {"url":"https://img.example/manga-chapters/1/1/page-2.jpeg"}]}}}`
)

func newSenkuro(t *testing.T) (*senkuro, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("App-Id") == "" {
			t.Error("the API expects a client id")
		}
		body, _ := io.ReadAll(r.Body)
		var in struct {
			Operation string `json:"operationName"`
		}
		_ = json.Unmarshal(body, &in)
		calls++
		switch in.Operation {
		case "searchTachiyomiManga":
			_, _ = w.Write([]byte(snSearchJSON))
		case "fetchTachiyomiManga":
			_, _ = w.Write([]byte(snDetailsJSON))
		case "fetchTachiyomiChapters":
			_, _ = w.Write([]byte(snChaptersJSON))
		case "fetchTachiyomiChapterPages":
			_, _ = w.Write([]byte(snPagesJSON))
		default:
			t.Errorf("unexpected operation %q", in.Operation)
		}
	}))
	t.Cleanup(srv.Close)
	return &senkuro{c: sourcekit.NewClient(srv.Client()), site: srv.URL, api: srv.URL + "/graphql", title: "ru"}, &calls
}

// TestSenkuro reads a series the way the library does, in the language the
// site is in.
func TestSenkuro(t *testing.T) {
	s, _ := newSenkuro(t)
	ctx := context.Background()

	res, err := s.Search(ctx, "berserk", 1)
	if err != nil || len(res.Mangas) != 1 {
		t.Fatalf("search: %v %+v", err, res)
	}
	got := res.Mangas[0]
	if got.Title != "Берсерк" || got.URL != "/manga/berserk" || got.ID != "MANGA:1" || got.CoverURL == "" {
		t.Fatalf("result %+v", got)
	}

	d, err := s.Details(ctx, sourcekit.Ref{URL: got.URL, ID: got.ID})
	if err != nil || d.Status != sourcekit.StatusOngoing || d.Description != "Его зовут Гатс." ||
		d.Author != "Kentarou Miura" || d.Artist != "Kentarou Miura, Studio Gaga" || len(d.Genres) != 1 {
		t.Fatalf("details: %v %+v", err, d)
	}
	if d.Rating == nil || *d.Rating != (sourcekit.Rating{Score: 9.48, Max: 10}) {
		t.Errorf("rating %+v", d.Rating)
	}

	chs, err := s.Chapters(ctx, sourcekit.Ref{URL: d.URL, ID: d.ID})
	if err != nil || len(chs) != 2 {
		t.Fatalf("chapters: %v %+v", err, chs)
	}
	if chs[0].Number != 399 || chs[0].ID != "CHAPTER:2" || chs[0].URL != "/manga/berserk/chapters/200" ||
		chs[0].Scanlator != "Команда" || chs[0].UploadedAt == nil {
		t.Fatalf("chapter %+v", chs[0])
	}
	if chs[1].Name != "Глава 398" {
		t.Fatalf("a chapter with no name of its own: %+v", chs[1])
	}

	pages, err := s.Pages(ctx, sourcekit.PageRef{Manga: sourcekit.Ref{URL: d.URL, ID: d.ID}, URL: chs[0].URL, ID: chs[0].ID})
	if err != nil || len(pages) != 2 {
		// the site puts its own banner in front of every chapter
		t.Fatalf("pages: %v %+v", err, pages)
	}
	if pages[0].URL != "https://img.example/manga-chapters/1/1/page-1.jpeg" || pages[0].Headers["Referer"] == "" {
		t.Fatalf("page %+v", pages[0])
	}
}

// TestSenkuroFindsIDsFromAURL: the API refuses a slug where it wants a node
// id, so a link stored without one is looked up instead of failing.
func TestSenkuroFindsIDsFromAURL(t *testing.T) {
	s, calls := newSenkuro(t)
	ctx := context.Background()
	chs, err := s.Chapters(ctx, sourcekit.Ref{URL: "/manga/berserk"})
	if err != nil || len(chs) != 2 {
		t.Fatalf("chapters from a url alone: %v %+v", err, chs)
	}
	if *calls != 2 { // one search to find the id, then the chapters
		t.Fatalf("%d requests", *calls)
	}
	if _, err := s.Chapters(ctx, sourcekit.Ref{URL: "/not-a-manga"}); err == nil {
		t.Fatal("a url that names no manga should be refused")
	}
}
