package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/model"
)

// TestSeriesRecommendations: AniList's related and recommended titles come
// first, marked when they're in the library, then library titles sharing two
// genres; the title's other editions aren't recommended, a group's content
// limits hide what its members can't see, and people who can't add or
// request only get library titles.
func TestSeriesRecommendations(t *testing.T) {
	srv, a := newServer(t, false)
	ctx := context.Background()
	now := time.Now().UTC()
	rf := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
	if _, err := a.DB.NewInsert().Model(rf).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"Media":{"id":1,
"relations":{"edges":[{"relationType":"SEQUEL","node":{"id":2,"type":"MANGA","title":{"english":"Base Two"},"genres":["Drama"]}}]},
"recommendations":{"nodes":[
 {"rating":50,"mediaRecommendation":{"id":5,"type":"MANGA","title":{"english":"Plain on AniList"}}},
 {"rating":40,"mediaRecommendation":{"id":7,"type":"MANGA","isAdult":true,"title":{"english":"Grown"}}},
 {"rating":30,"mediaRecommendation":{"id":8,"type":"MANGA","title":{"english":"Splatter"},"genres":["Gore"]}},
 {"rating":20,"mediaRecommendation":{"id":9,"type":"MANGA","title":{"english":"Clean"},"genres":["Comedy"]}}
]}}}}`)
	}))
	defer upstream.Close()
	def := &model.ProviderDefinition{Kind: "metadata", Implementation: "anilist", Name: "AniList", Enabled: true,
		Settings: map[string]any{"endpoint": upstream.URL, "titleLanguage": "english"}}
	if err := a.Modules.Create(ctx, def); err != nil {
		t.Fatal(err)
	}
	anilist := map[string]string{"Base": "1", "Plain": "5"}
	mk := func(title string, workID int64, genres ...string) *model.Series {
		s := &model.Series{Title: title, SortTitle: title, RootFolderID: rf.ID, Path: title, ProfileID: 1, Tags: []int64{}, WorkID: workID,
			Metadata: model.SeriesMetadata{Genres: genres}, AddedAt: now, UpdatedAt: now}
		if id := anilist[title]; id != "" {
			s.Metadata.ExternalIDs = map[string]string{"anilist": id}
		}
		if _, err := a.DB.NewInsert().Model(s).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		ch := &model.Chapter{SeriesID: s.ID, NumberKey: "1", NumberSort: 1, State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
		if _, err := a.DB.NewInsert().Model(ch).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		return s
	}
	work := &model.Work{Title: "Base", SortTitle: "base", CreatedAt: now, UpdatedAt: now}
	if _, err := a.DB.NewInsert().Model(work).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	base := mk("Base", work.ID, "Action", "Drama")
	mk("Base (other language)", work.ID, "Action", "Drama")
	mk("Plain", 0, "Drama", "Action", "Comedy")
	mk("Bloody", 0, "Action", "Drama", "Gore")
	mk("Lonely", 0, "Action")
	if _, err := a.Auth.CreateUser(ctx, auth.NewUser{Username: "boss", Password: "boss-pass-1"}); err != nil {
		t.Fatal(err)
	}
	kids := &model.Group{Name: "Kids", Permissions: []string{"requests.create"}, IncludeTags: []int64{}, ExcludeTags: []int64{}, RootFolders: []int64{},
		MaxRating: "teen", BlockedGenres: []string{"gore"}, CreatedAt: now}
	if _, err := a.DB.NewInsert().Model(kids).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Auth.CreateUser(ctx, auth.NewUser{Username: "kid", Password: "kid-pass-1", GroupID: kids.ID}); err != nil {
		t.Fatal(err)
	}
	readers := &model.Group{Name: "Readers", Permissions: []string{}, IncludeTags: []int64{}, ExcludeTags: []int64{}, RootFolders: []int64{}, CreatedAt: now}
	if _, err := a.DB.NewInsert().Model(readers).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Auth.CreateUser(ctx, auth.NewUser{Username: "reader", Password: "reader-pass-1", GroupID: readers.ID}); err != nil {
		t.Fatal(err)
	}
	type rec struct {
		Title            string   `json:"title"`
		Relation         string   `json:"relation"`
		Votes            int      `json:"votes"`
		ExistingSeriesID int64    `json:"existingSeriesId"`
		SharedGenres     []string `json:"sharedGenres"`
		SeriesCoverURL   string   `json:"seriesCoverUrl"`
	}
	var got struct {
		Related []rec    `json:"related"`
		Similar []rec    `json:"similar"`
		Errors  []string `json:"errors"`
	}
	path := "/api/v1/series/" + strconv.FormatInt(base.ID, 10) + "/recommendations"
	titles := func(rs []rec) []string {
		out := []string{}
		for _, r := range rs {
			out = append(out, r.Title)
		}
		return out
	}
	expect := func(who caller, related, similar []string) {
		t.Helper()
		if code := who.do("GET", path, "", &got); code != 200 {
			t.Fatalf("status %d", code)
		}
		if !reflect.DeepEqual(titles(got.Related), related) || !reflect.DeepEqual(titles(got.Similar), similar) || len(got.Errors) != 0 {
			t.Fatalf("got related %v similar %v errors %v, want %v and %v", titles(got.Related), titles(got.Similar), got.Errors, related, similar)
		}
	}
	boss := caller{t, login(t, srv.URL, "boss", "boss-pass-1"), srv.URL}
	expect(boss, []string{"Base Two"}, []string{"Plain on AniList", "Grown", "Splatter", "Clean", "Bloody"})
	if r := got.Related[0]; r.Relation != "sequel" || r.ExistingSeriesID != 0 {
		t.Fatalf("related: %+v", r)
	}
	if p := got.Similar[0]; p.ExistingSeriesID == 0 || p.SeriesCoverURL == "" || p.Votes != 50 {
		t.Fatalf("recommended title in the library: %+v", p)
	}
	if b := got.Similar[4]; b.ExistingSeriesID == 0 || len(b.SharedGenres) != 2 {
		t.Fatalf("library title: %+v", b)
	}
	expect(caller{t, login(t, srv.URL, "kid", "kid-pass-1"), srv.URL}, []string{"Base Two"}, []string{"Plain on AniList", "Clean"})
	expect(caller{t, login(t, srv.URL, "reader", "reader-pass-1"), srv.URL}, []string{}, []string{"Plain on AniList", "Bloody"})
	if n := calls.Load(); n != 1 {
		t.Fatalf("AniList asked %d times; the answer should be cached", n)
	}
}
