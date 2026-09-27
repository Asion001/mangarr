package sites

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

func newComX(t *testing.T) (*comx, *int) {
	t.Helper()
	browsePosts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body string
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			body = string(b)
		}
		switch r.URL.Path {
		case "/search/Берсерк/":
			io.WriteString(w, comxListHTML(true))
		case "/comix-read/":
			browsePosts++
			if r.Method != http.MethodPost || !strings.Contains(body, "dlenewssortby=rating") {
				t.Errorf("browse request = %s %q", r.Method, body)
			}
			io.WriteString(w, comxListHTML(false))
		case "/2789-berserk-read-online.html":
			io.WriteString(w, `<!doctype html><header class="page__header"><h1>Берсерк</h1></header>
<div class="page__poster"><img data-src="/covers/berserk.jpg"></div>
<ul class="page__list"><li><div>Автор</div><a>Кэнтаро Миура</a></li><li><div>Художник</div><a>Кэнтаро Миура</a></li><li><div>Статус</div>Продолжается</li></ul>
<div class="page__tags"><a>Драма</a><a>Фэнтези</a></div><div class="page__text">История Гатса.</div>
<script>window.__DATA__ = {"news_id":2789,"chapters":[{"id":10,"title":"Глава 1","number":1,"date":"2.9.2026"},{"id":11,"title":"Глава 2","number":2,"date":"03.09.2026"}]};</script>`)
		case "/reader/2789/10":
			io.WriteString(w, `<script>window.__DATA__ = {"host":"img.com-x.life","host_ru":"rus.com-x.life","images":["2789/10/001.jpg","2789/10/002.jpg"]};</script>`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &comx{c: sourcekit.NewClient(srv.Client()), base: srv.URL}, &browsePosts
}

func comxListHTML(next bool) string {
	last := `<span>2</span>`
	if next {
		last = `<a href="/search/x/page/2/">2</a>`
	}
	return `<!doctype html><div id="dle-content"><article class="readed"><div class="readed__title"><a href="/2789-berserk-read-online.html">Berserk / Берсерк</a></div><img data-src="/covers/berserk.jpg"></article></div><div class="pagination__pages"><span>1</span>` + last + `</div>`
}

func TestComX(t *testing.T) {
	c, browsePosts := newComX(t)
	ctx := context.Background()

	res, err := c.Search(ctx, "Берсерк", 1)
	if err != nil || len(res.Mangas) != 1 || res.Mangas[0].Title != "Берсерк" || res.Mangas[0].ID != "2789" || !res.HasNext {
		t.Fatalf("search = %+v, %v", res, err)
	}
	if res.Mangas[0].CoverURL != c.base+"/covers/berserk.jpg" {
		t.Fatalf("cover = %q", res.Mangas[0].CoverURL)
	}
	if _, err := c.Popular(ctx, 1); err != nil || *browsePosts != 1 {
		t.Fatalf("popular posts = %d, err = %v", *browsePosts, err)
	}

	d, err := c.Details(ctx, sourcekit.Ref{URL: res.Mangas[0].URL})
	if err != nil || d.Title != "Берсерк" || d.Author != "Кэнтаро Миура" || d.Status != sourcekit.StatusOngoing || len(d.Genres) != 2 {
		t.Fatalf("details = %+v, %v", d, err)
	}
	chapters, err := c.Chapters(ctx, sourcekit.Ref{URL: res.Mangas[0].URL})
	if err != nil || len(chapters) != 2 || chapters[0].Number != 1 || chapters[0].UploadedAt == nil {
		t.Fatalf("chapters = %+v, %v", chapters, err)
	}
	pages, err := c.Pages(ctx, sourcekit.PageRef{Manga: sourcekit.Ref{URL: res.Mangas[0].URL}, URL: chapters[0].URL})
	if err != nil || len(pages) != 2 || pages[0].URL != "https://img.com-x.life/comix/2789/10/001.jpg" {
		t.Fatalf("pages = %+v, %v", pages, err)
	}
	if got := pages[0].Headers["Referer"]; got != c.base+chapters[0].URL {
		t.Fatalf("page referer = %q", got)
	}
}

func TestComXRegistration(t *testing.T) {
	if comxID != "1114173092141608635" {
		t.Fatalf("Com-X Keiyoushi id = %s", comxID)
	}
	if _, err := url.Parse(comxSite); err != nil {
		t.Fatal(err)
	}
}
