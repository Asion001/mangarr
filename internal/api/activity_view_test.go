package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/model"
)

// TestActivityViewIsReadOnly: the activity.view permission lists the queue
// (only the series the group sees) but can't pause, reorder or remove.
func TestActivityViewIsReadOnly(t *testing.T) {
	srv, a := newServer(t, false)
	ctx := context.Background()
	now := time.Now().UTC()
	rf := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
	if _, err := a.DB.NewInsert().Model(rf).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	tag := &model.Tag{Label: "adults"}
	if _, err := a.DB.NewInsert().Model(tag).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var jobs []int64
	for _, s := range []*model.Series{
		{Title: "Open", SortTitle: "open", RootFolderID: rf.ID, Path: "Open", ProfileID: 1, Tags: []int64{}, AddedAt: now, UpdatedAt: now},
		{Title: "Hidden", SortTitle: "hidden", RootFolderID: rf.ID, Path: "Hidden", ProfileID: 1, Tags: []int64{tag.ID}, AddedAt: now, UpdatedAt: now},
	} {
		if _, err := a.DB.NewInsert().Model(s).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		ch := &model.Chapter{SeriesID: s.ID, NumberKey: "1", NumberSort: 1, State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
		if _, err := a.DB.NewInsert().Model(ch).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		job, _, err := a.DLQueue.Enqueue(ctx, s.ID, ch.ID, nil, model.JobKindDownload, false)
		if err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, job.ID)
	}
	if _, err := a.Auth.CreateUser(ctx, auth.NewUser{Username: "boss", Password: "boss-pass-1"}); err != nil {
		t.Fatal(err)
	}
	watchers := &model.Group{Name: "Watchers", Permissions: []string{"activity.view"}, IncludeTags: []int64{}, ExcludeTags: []int64{tag.ID}, RootFolders: []int64{}, CreatedAt: now}
	if _, err := a.DB.NewInsert().Model(watchers).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Auth.CreateUser(ctx, auth.NewUser{Username: "wes", Password: "wes-pass-1", GroupID: watchers.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Auth.CreateUser(ctx, auth.NewUser{Username: "bob", Password: "bob-pass-1"}); err != nil {
		t.Fatal(err)
	}
	wes := caller{t, login(t, srv.URL, "wes", "wes-pass-1"), srv.URL}
	bob := caller{t, login(t, srv.URL, "bob", "bob-pass-1"), srv.URL}

	var page struct {
		Items []struct {
			SeriesTitle string `json:"seriesTitle"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if code := wes.do("GET", "/api/v1/queue", "", &page); code != http.StatusOK {
		t.Fatalf("queue-list: %d", code)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].SeriesTitle != "Open" {
		t.Fatalf("watcher sees %+v", page)
	}
	if code := bob.do("GET", "/api/v1/queue", "", nil); code != http.StatusForbidden {
		t.Fatalf("queue-list without the permission: %d", code)
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/v1/queue/pause", `{}`},
		{"POST", "/api/v1/queue/resume", ``},
		{"POST", "/api/v1/queue/bulk", `{"action":"pause","ids":[1]}`},
		{"POST", "/api/v1/queue/bulk", `{"action":"remove","ids":[1]}`},
		{"DELETE", "/api/v1/queue/1", ``},
		{"POST", "/api/v1/queue/1/retry", ``},
	} {
		if code := wes.do(c.method, c.path, c.body, nil); code != http.StatusForbidden {
			t.Errorf("%s %s: %d, want 403", c.method, c.path, code)
		}
	}
	left, _ := a.DB.NewSelect().Model((*model.DownloadJob)(nil)).Where("status = ?", model.JobQueued).Count(ctx)
	if left != len(jobs) {
		t.Fatalf("queued jobs %d, want %d", left, len(jobs))
	}
}
