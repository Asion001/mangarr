package api_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/model"
)

// TestReadingLists: users put titles in a status and their own lists and
// filter the library by them; people who manage the library can filter by
// anyone's, and nobody else can.
func TestReadingLists(t *testing.T) {
	srv, a := newServer(t, false)
	ctx := context.Background()
	now := time.Now().UTC()
	rf := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
	if _, err := a.DB.NewInsert().Model(rf).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	one := &model.Series{Title: "One", SortTitle: "one", Status: model.StatusOngoing, RootFolderID: rf.ID, Path: "One", ProfileID: 1, Tags: []int64{}, AddedAt: now, UpdatedAt: now}
	two := &model.Series{Title: "Two", SortTitle: "two", Status: model.StatusOngoing, RootFolderID: rf.ID, Path: "Two", ProfileID: 1, Tags: []int64{}, AddedAt: now, UpdatedAt: now}
	for _, s := range []*model.Series{one, two} {
		if _, err := a.DB.NewInsert().Model(s).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Auth.CreateUser(ctx, auth.NewUser{Username: "boss", Password: "boss-pass-1"}); err != nil {
		t.Fatal(err)
	}
	curators := &model.Group{Name: "Curators", Permissions: []string{"library.manage"}, IncludeTags: []int64{}, ExcludeTags: []int64{}, RootFolders: []int64{}, CreatedAt: now}
	if _, err := a.DB.NewInsert().Model(curators).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	annUser, _ := a.Auth.CreateUser(ctx, auth.NewUser{Username: "ann", Password: "ann-pass-1"})
	_, _ = a.Auth.CreateUser(ctx, auth.NewUser{Username: "bob", Password: "bob-pass-1"})
	_, _ = a.Auth.CreateUser(ctx, auth.NewUser{Username: "cleo", Password: "cleo-pass-1", GroupID: curators.ID})
	ann := caller{t, login(t, srv.URL, "ann", "ann-pass-1"), srv.URL}
	bob := caller{t, login(t, srv.URL, "bob", "bob-pass-1"), srv.URL}
	cleo := caller{t, login(t, srv.URL, "cleo", "cleo-pass-1"), srv.URL}
	sid := func(s *model.Series) string { return strconv.FormatInt(s.ID, 10) }
	annID := strconv.FormatInt(annUser.ID, 10)

	type lists struct {
		Statuses []struct {
			Status string
			Count  int
		}
		Lists []struct {
			ID    int64
			Name  string
			Kind  string
			Count int
		}
	}
	var mine lists
	if code := ann.do("GET", "/api/v1/me/lists", "", &mine); code != 200 || len(mine.Statuses) != 5 || len(mine.Lists) != 1 || mine.Lists[0].Kind != "favorites" {
		t.Fatalf("a new user's lists %d %+v", code, mine)
	}
	fav := strconv.FormatInt(mine.Lists[0].ID, 10)
	var made struct{ ID int64 }
	if code := ann.do("POST", "/api/v1/me/lists", `{"name":"Sports"}`, &made); code != 200 || made.ID == 0 {
		t.Fatalf("make a list: %d", code)
	}
	sports := strconv.FormatInt(made.ID, 10)
	var entry struct {
		Status string
		Lists  []int64
	}
	if code := ann.do("PUT", "/api/v1/series/"+sid(one)+"/lists", `{"status":"reading","lists":[`+fav+`,`+sports+`]}`, &entry); code != 200 || entry.Status != "reading" || len(entry.Lists) != 2 {
		t.Fatalf("put one in lists: %d %+v", code, entry)
	}
	if code := ann.do("PUT", "/api/v1/series/"+sid(two)+"/lists", `{"status":"planned","lists":[`+sports+`]}`, nil); code != 200 {
		t.Fatalf("put two in lists: %d", code)
	}
	// one status at a time; lists are set as given
	ann.do("PUT", "/api/v1/series/"+sid(one)+"/lists", `{"status":"completed","lists":[`+fav+`]}`, &entry)
	if entry.Status != "completed" || len(entry.Lists) != 1 {
		t.Fatalf("after moving one: %+v", entry)
	}
	if code := bob.do("PUT", "/api/v1/series/"+sid(one)+"/lists", `{"status":"","lists":[`+fav+`]}`, nil); code != 404 {
		t.Fatalf("bob using ann's list: %d", code)
	}

	search := func(c caller, query string) []string {
		t.Helper()
		var res struct{ Items []struct{ Title string } }
		if code := c.do("GET", "/api/v1/series/search?"+query, "", &res); code != 200 {
			t.Fatalf("search %s: %d", query, code)
		}
		var out []string
		for _, it := range res.Items {
			out = append(out, it.Title)
		}
		return out
	}
	if got := search(ann, "list=completed"); len(got) != 1 || got[0] != "One" {
		t.Fatalf("ann's completed: %v", got)
	}
	if got := search(ann, "list="+sports); len(got) != 1 || got[0] != "Two" {
		t.Fatalf("ann's sports: %v", got)
	}
	if got := search(bob, "list=completed"); len(got) != 0 {
		t.Fatalf("bob's completed: %v", got)
	}
	if code := bob.do("GET", "/api/v1/series/search?list=planned&listUser="+annID, "", nil); code != 403 {
		t.Fatalf("bob filtering by ann's lists: %d", code)
	}
	if got := search(cleo, "list=planned&listUser="+annID); len(got) != 1 || got[0] != "Two" {
		t.Fatalf("a curator filtering by ann's planned: %v", got)
	}
	var owners []struct{ Username string }
	if code := cleo.do("GET", "/api/v1/reading-lists/owners", "", &owners); code != 200 || len(owners) != 1 || owners[0].Username != "ann" {
		t.Fatalf("owners %d %+v", code, owners)
	}
	if code := bob.do("GET", "/api/v1/reading-lists/owners", "", nil); code != 403 {
		t.Fatalf("bob listing owners: %d", code)
	}
	var hers lists
	if code := cleo.do("GET", "/api/v1/users/"+annID+"/lists", "", &hers); code != 200 || len(hers.Lists) != 2 || hers.Statuses[1].Count != 1 {
		t.Fatalf("ann's lists for a curator %d %+v", code, hers)
	}

	if code := ann.do("PUT", "/api/v1/me/lists/"+fav, `{"name":"Loved"}`, nil); code != 400 {
		t.Fatalf("renaming Favorites: %d", code)
	}
	if code := ann.do("PUT", "/api/v1/me/lists/"+sports, `{"name":"Sport"}`, nil); code != 200 {
		t.Fatalf("rename: %d", code)
	}
	if code := bob.do("DELETE", "/api/v1/me/lists/"+sports, "", nil); code != 404 {
		t.Fatalf("bob deleting ann's list: %d", code)
	}
	if code := ann.do("DELETE", "/api/v1/me/lists/"+sports, "", nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	ann.do("GET", "/api/v1/series/"+sid(two)+"/lists", "", &entry)
	if entry.Status != "planned" || len(entry.Lists) != 0 {
		t.Fatalf("two after deleting its list: %+v", entry)
	}

	// a selection moves in one go: into a list, a status, out of a list
	ids := `"seriesIds":[` + sid(one) + `,` + sid(two) + `]`
	var res struct{ Updated int }
	if code := ann.do("POST", "/api/v1/series/lists/bulk", `{`+ids+`,"action":"add","listId":`+fav+`}`, &res); code != 200 || res.Updated != 2 {
		t.Fatalf("bulk add to Favorites: %d %+v", code, res)
	}
	if code := ann.do("POST", "/api/v1/series/lists/bulk", `{`+ids+`,"action":"status","status":"reading"}`, nil); code != 200 {
		t.Fatalf("bulk status: %d", code)
	}
	if got := search(ann, "list=reading"); len(got) != 2 {
		t.Fatalf("ann's reading after bulk: %v", got)
	}
	ann.do("POST", "/api/v1/series/lists/bulk", `{"seriesIds":[`+sid(one)+`],"action":"remove","listId":`+fav+`}`, nil)
	if got := search(ann, "list="+fav); len(got) != 1 || got[0] != "Two" {
		t.Fatalf("ann's Favorites after bulk remove: %v", got)
	}
	ann.do("GET", "/api/v1/series/"+sid(one)+"/lists", "", &entry)
	if entry.Status != "reading" || len(entry.Lists) != 0 {
		t.Fatalf("one after bulk changes: %+v", entry)
	}
	if code := bob.do("POST", "/api/v1/series/lists/bulk", `{`+ids+`,"action":"add","listId":`+fav+`}`, nil); code != 404 {
		t.Fatalf("bob bulk adding to ann's list: %d", code)
	}
	if code := ann.do("POST", "/api/v1/series/lists/bulk", `{`+ids+`,"action":"status","status":"someday"}`, nil); code != 422 && code != 400 {
		t.Fatalf("bulk unknown status: %d", code)
	}
}
