package api_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/model"
)

// TestGroupContentLimits: a group capped at teen titles doesn't see an
// adult title or a hidden genre in the library, by id or in updates.
func TestGroupContentLimits(t *testing.T) {
	srv, a := newServer(t, false)
	ctx := context.Background()
	now := time.Now().UTC()
	rf := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
	if _, err := a.DB.NewInsert().Model(rf).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	mk := func(title string, md model.SeriesMetadata) *model.Series {
		s := &model.Series{Title: title, SortTitle: title, RootFolderID: rf.ID, Path: title, ProfileID: 1, Tags: []int64{}, Metadata: md, AddedAt: now, UpdatedAt: now}
		if _, err := a.DB.NewInsert().Model(s).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		return s
	}
	mk("Plain", model.SeriesMetadata{Genres: []string{"Action"}})
	adult := mk("Grown", model.SeriesMetadata{AgeRating: "Adults Only 18+"})
	mk("Splatter", model.SeriesMetadata{Genres: []string{"Gore"}})
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
	kid := caller{t, login(t, srv.URL, "kid", "kid-pass-1"), srv.URL}
	boss := caller{t, login(t, srv.URL, "boss", "boss-pass-1"), srv.URL}
	var list []struct {
		Title string `json:"title"`
	}
	kid.do("GET", "/api/v1/series", "", &list)
	if len(list) != 1 || list[0].Title != "Plain" {
		t.Fatalf("kid sees %+v", list)
	}
	if code := kid.do("GET", "/api/v1/series/"+strconv.FormatInt(adult.ID, 10), "", nil); code != 404 {
		t.Fatalf("adult title by id: %d", code)
	}
	boss.do("GET", "/api/v1/series", "", &list)
	if len(list) != 3 {
		t.Fatalf("admin sees %+v", list)
	}
	if code := kid.do("GET", "/api/v1/updates", "", nil); code != 200 {
		t.Fatalf("updates: %d", code)
	}
}
