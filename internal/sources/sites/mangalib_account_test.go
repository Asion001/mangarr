package sites

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// TestMangaLibAccount reads a profile's public lists: titles from this site
// only, the chapter reached by its id and the page in it.
func TestMangaLibAccount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Site-Id") != "1" || r.Header.Get("Authorization") != "" {
			t.Errorf("headers %v", r.Header)
		}
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/api/user/77":
			_, _ = w.Write([]byte(`{"data":{"id":77,"username":"reader"}}`))
		case r.URL.Path == "/api/user/404":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"data":{"toast":{"message":"Not Found"}}}`))
		case r.URL.Path == "/api/bookmarks/folder/77":
			_, _ = w.Write([]byte(`{"data":[
			  {"id":1,"name":"Читаю","public":true,"is_abandoned":false,"count":2,"site_ids":[0,1,2,3,4]},
			  {"id":3,"name":"Брошено","public":true,"is_abandoned":true,"count":0,"site_ids":[0,1,2,3,4]},
			  {"id":9,"name":"Секрет","public":false,"is_abandoned":false,"count":5,"site_ids":[1]},
			  {"id":21,"name":"Смотрю","public":true,"is_abandoned":false,"count":4,"site_ids":[5]}]}`))
		case r.URL.Path == "/api/bookmarks" && q.Get("status") == "1" && q.Get("user_id") == "77" && q.Get("page") == "1":
			_, _ = w.Write([]byte(`{"data":[
			  {"item_id":4413034,"progress":"21","meta":{"item_number":2},"media":{"id":2084,"name":"Kumo desu ga, Nani ka?","rus_name":"Паучок","eng_name":"So I'm a Spider","slug_url":"2084--kumo","model":"manga","site":1,"status":{"id":1,"label":"Онгоинг"},"cover":{"default":"https://img.example/k.jpg"}}},
			  {"item_id":0,"progress":"0","media":{"id":5,"name":"Novel","slug_url":"5--novel","model":"ranobe","site":3}}],
			  "meta":{"next_page_url":"http://api.example/api/bookmarks?page=2"}}`))
		case r.URL.Path == "/api/bookmarks" && q.Get("status") == "1" && q.Get("page") == "2":
			_, _ = w.Write([]byte(`{"data":[
			  {"item_id":0,"progress":"0","media":{"id":6,"name":"Planned","slug_url":"6--planned","model":"manga","site":1,"status":{"id":2,"label":"Завершён"}}}],
			  "meta":{"next_page_url":false}}`))
		case r.URL.Path == "/api/manga/2084--kumo/chapters":
			_, _ = w.Write([]byte(`{"data":[{"id":1,"number":"80","branches":[{"id":1}]},{"id":4413035,"index":2,"number":"80.1","branches":[{"id":4413035}]}]}`))
		default:
			t.Errorf("unexpected %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	m := &mangalib{c: sourcekit.NewClient(srv.Client()), site: srv.URL, api: srv.URL, title: "en", server: "main"}
	acc, err := m.ReadAccount(context.Background(), "https://mangalib.me/ru/user/77?section=bookmarks")
	if err != nil {
		t.Fatal(err)
	}
	if acc.User != "reader" || len(acc.Lists) != 2 || acc.Lists[0].Status != sourcekit.ListReading || acc.Lists[1].Status != sourcekit.ListDropped {
		t.Fatalf("account = %+v", acc)
	}
	if len(acc.Entries) != 2 {
		t.Fatalf("entries = %+v", acc.Entries)
	}
	k := acc.Entries[0]
	if k.URL != "/2084--kumo" || k.Title != "So I'm a Spider" || k.ChapterRef != "4413034" || k.Page != 21 || k.Chapter != 80.1 || k.Lists[0] != "1" {
		t.Fatalf("first = %+v", k)
	}
	if p := acc.Entries[1]; p.ChapterRef != "" || p.Page != 0 || p.Status != sourcekit.StatusCompleted {
		t.Fatalf("planned = %+v", p)
	}
	for _, bad := range []string{"https://mangalib.me/ru/manga/x", "404"} {
		if _, err := m.ReadAccount(context.Background(), bad); !errors.Is(err, sourcekit.ErrAccount) {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}
