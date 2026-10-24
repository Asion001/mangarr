package sites

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

const (
	snViewerJSON = `{"data":{"viewer":{"user":{"id":"USER:1","slug":"reader","name":"Reader"},
	  "userBookmarkCollections":[
	    {"id":"COL:1","name":"Смотрю","entityType":"ANIME","status":"WATCHING","system":true},
	    {"id":"COL:7","name":"Читаю","entityType":"MANGA","status":"WATCHING","system":true,"preference":null},
	    {"id":"COL:9","name":"Прочитано","entityType":"MANGA","status":"COMPLETED","system":true,"preference":{"name":"Done"}},
	    {"id":"COL:12","name":"Брошено","entityType":"MANGA","status":"DROPPED","system":true},
	    {"id":"COL:40","name":"Перечитать","entityType":"MANGA","status":"WATCHING","system":false}]}}}`

	snBookmarksPage1 = `{"data":{"userBookmarks":{"edges":[
	  {"node":{"entityId":"MANGA:1","collectionId":"COL:7","page":7,"number":"112",
	    "entity":{"id":"MANGA:1","slug":"berserk","originalName":{"lang":"EN","content":"Berserk"},
	      "titles":[{"lang":"RU","content":"Берсерк"}],"mangaStatus":"ONGOING","cover":{"original":{"url":"https://img.example/b.jpeg"}}}}},
	  {"node":{"entityId":"ANIME:1","collectionId":"COL:1","page":null,"number":"3","entity":{"id":"ANIME:1"}}}],
	  "pageInfo":{"hasNextPage":true,"endCursor":"c1"}}}}`

	snBookmarksPage2 = `{"data":{"userBookmarks":{"edges":[
	  {"node":{"entityId":"MANGA:2","collectionId":"COL:9","page":null,"number":"231",
	    "entity":{"id":"MANGA:2","slug":"vagabond","originalName":{"lang":"JA","content":"Vagabond"},
	      "titles":[],"mangaStatus":"FINISHED","cover":{"original":{"url":""}}}}},
	  {"node":{"entityId":"MANGA:3","collectionId":"COL:40","page":null,"number":null,
	    "entity":{"id":"MANGA:3","slug":"planned","originalName":{"lang":"EN","content":"Planned"},"titles":[],"mangaStatus":"ONGOING"}}}],
	  "pageInfo":{"hasNextPage":false,"endCursor":"c2"}}}}`
)

func senkuroAccountServer(t *testing.T, token string) *senkuro {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			_, _ = w.Write([]byte(`{"errors":[{"message":"You're not authorized","extensions":{"code":"NOT_AUTHORIZED"}}],"data":null}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		var in struct {
			Operation string         `json:"operationName"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &in)
		switch {
		case in.Operation == "fetchViewer":
			_, _ = w.Write([]byte(snViewerJSON))
		case in.Operation == "fetchUserBookmarks" && in.Variables["userId"] == "USER:1" && in.Variables["after"] == nil:
			_, _ = w.Write([]byte(snBookmarksPage1))
		case in.Operation == "fetchUserBookmarks" && in.Variables["after"] == "c1":
			_, _ = w.Write([]byte(snBookmarksPage2))
		default:
			t.Errorf("unexpected request %s %v", in.Operation, in.Variables)
		}
	}))
	t.Cleanup(srv.Close)
	return &senkuro{c: sourcekit.NewClient(srv.Client()), site: srv.URL, api: srv.URL + "/graphql", title: "ru"}
}

// TestSenkuroAccount reads the manga lists and how far each title was read.
func TestSenkuroAccount(t *testing.T) {
	s := senkuroAccountServer(t, "tok")
	acc, err := s.ReadAccount(context.Background(), " Bearer tok ")
	if err != nil {
		t.Fatal(err)
	}
	if acc.User != "Reader" {
		t.Errorf("user = %q", acc.User)
	}
	wantLists := []sourcekit.AccountList{{ID: "COL:7", Name: "Читаю", Status: sourcekit.ListReading},
		{ID: "COL:9", Name: "Done", Status: sourcekit.ListCompleted}, {ID: "COL:12", Name: "Брошено", Status: sourcekit.ListDropped},
		{ID: "COL:40", Name: "Перечитать"}}
	if len(acc.Lists) != len(wantLists) {
		t.Fatalf("lists = %+v", acc.Lists)
	}
	for i, l := range wantLists {
		if acc.Lists[i] != l {
			t.Errorf("list %d = %+v, want %+v", i, acc.Lists[i], l)
		}
	}
	if len(acc.Entries) != 3 {
		t.Fatalf("entries = %+v", acc.Entries)
	}
	b := acc.Entries[0]
	if b.URL != "/manga/berserk" || b.ID != "MANGA:1" || b.Title != "Берсерк" || b.Chapter != 112 || b.Page != 7 ||
		b.Status != sourcekit.StatusOngoing || len(b.Lists) != 1 || b.Lists[0] != "COL:7" {
		t.Errorf("first entry = %+v", b)
	}
	if v := acc.Entries[1]; v.Chapter != 231 || v.Page != 0 || v.Status != sourcekit.StatusCompleted {
		t.Errorf("finished entry = %+v", v)
	}
	if p := acc.Entries[2]; p.Chapter != -1 || p.Lists[0] != "COL:40" {
		t.Errorf("not started entry = %+v", p)
	}
}

func TestSenkuroAccountBadToken(t *testing.T) {
	s := senkuroAccountServer(t, "tok")
	for _, tok := range []string{"", "nope"} {
		if _, err := s.ReadAccount(context.Background(), tok); !errors.Is(err, sourcekit.ErrUnauthorized) {
			t.Errorf("token %q: err = %v", tok, err)
		}
	}
}
