package api_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/app"
	"github.com/Asion001/mangarr/internal/config"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/logging"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

// TestAttentionAndSourceFilter: failed downloads are counted per source in
// the attention summary, the queue lists only one source's entries when
// asked, stale workers and unverified backups show, and the failed-downloads
// health warning opens the filtered Downloads list.
func TestAttentionAndSourceFilter(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			_, ring := logging.Setup("error", io.Discard)
			a, err := app.New(ctx, &config.Config{DataDir: t.TempDir(), DB: dsn, AuthDisabled: true}, slog.New(slog.NewTextHandler(io.Discard, nil)), ring)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			if err := a.Settings.Set(ctx, settings.KeyQueueState, settings.QueueState{Paused: true}); err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(api.New(a))
			defer srv.Close()
			c := caller{t, http.DefaultClient, srv.URL}
			now := time.Now().UTC()
			mod := &model.ProviderDefinition{Kind: "source", Implementation: "none", Name: "Sites", Enabled: false, CreatedAt: now, UpdatedAt: now}
			if _, err := a.DB.NewInsert().Model(mod).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			root := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
			if _, err := a.DB.NewInsert().Model(root).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			ser := &model.Series{Title: "Tin Rivers", SortTitle: "tin rivers", RootFolderID: root.ID, ProfileID: 1, Path: "tin", Tags: []int64{}, AddedAt: now, UpdatedAt: now}
			if _, err := a.DB.NewInsert().Model(ser).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			links := map[string]int64{}
			for _, src := range []string{"md", "ck"} {
				ss := &model.SeriesSource{SeriesID: ser.ID, ModuleID: mod.ID, SourceID: src, SourceName: src + " site", Enabled: true, NextCheckAt: now, CreatedAt: now}
				if _, err := a.DB.NewInsert().Model(ss).Exec(ctx); err != nil {
					t.Fatal(err)
				}
				links[src] = ss.ID
			}
			// chapters 1-2 failed on md, chapter 3 on ck
			for i, src := range []string{"md", "md", "ck"} {
				ch := &model.Chapter{SeriesID: ser.ID, NumberKey: fmt.Sprint(i + 1), NumberSort: float64(i + 1), State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
				if _, err := a.DB.NewInsert().Model(ch).Exec(ctx); err != nil {
					t.Fatal(err)
				}
				rel := &model.ChapterRelease{SeriesID: ser.ID, ChapterID: &ch.ID, SeriesSourceID: links[src], ChapterURL: fmt.Sprint("/c", i), CreatedAt: now}
				if _, err := a.DB.NewInsert().Model(rel).Exec(ctx); err != nil {
					t.Fatal(err)
				}
				job, _, err := a.DLQueue.Enqueue(ctx, ser.ID, ch.ID, &rel.ID, model.JobKindDownload, false)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := a.DB.NewUpdate().Model((*model.DownloadJob)(nil)).Set("status = ?", model.JobFailed).Set("updated_at = ?", now).Where("id = ?", job.ID).Exec(ctx); err != nil {
					t.Fatal(err)
				}
			}
			seen := now.Add(-time.Hour)
			for _, w := range []model.Worker{{Name: "gone", Enabled: true, LastSeenAt: &seen}, {Name: "fresh", Enabled: true, LastSeenAt: &now}, {Name: "new", Enabled: true}, {Name: "off", Enabled: false, LastSeenAt: &seen}} {
				w.Roles, w.Info, w.CreatedAt, w.KeyHash, w.Prefix = []string{"download"}, map[string]any{}, now, w.Name, w.Name
				if _, err := a.DB.NewInsert().Model(&w).Exec(ctx); err != nil {
					t.Fatal(err)
				}
			}

			var att api.Attention
			if code := c.do("GET", "/api/v1/system/attention", "", &att); code != http.StatusOK {
				t.Fatalf("attention: %d", code)
			}
			fd := att.FailedDownloads
			md := fmt.Sprintf("%d:md", mod.ID)
			if fd.Count != 3 || len(fd.Sources) != 2 || fd.Sources[0].Source != md || fd.Sources[0].Count != 2 || fd.Sources[0].Name != "md site" {
				t.Fatalf("failed downloads %+v", fd)
			}
			if len(att.Workers.Stale) != 2 || att.Workers.Stale[0].Name != "gone" || att.Workers.Stale[1].Name != "new" || att.Workers.Enabled != 3 {
				t.Fatalf("workers %+v", att.Workers)
			}
			if att.Backups.Count != 0 || att.Backups.LatestAt != nil || att.Disk.Folders != 1 || att.Disk.Path == "" {
				t.Fatalf("backups %+v disk %+v", att.Backups, att.Disk)
			}

			var page struct {
				Total int `json:"total"`
			}
			if code := c.do("GET", "/api/v1/queue?status=failed&source="+md, "", &page); code != http.StatusOK || page.Total != 2 {
				t.Fatalf("queue for one source: %d, %+v", code, page)
			}

			var rel []map[string]any
			if code := c.do("GET", "/api/v1/catalogs/reliability?days=7", "", &rel); code != http.StatusOK {
				t.Fatalf("reliability: %d", code)
			}
			if len(rel) != 2 { // both linked catalogs, nothing requested yet
				t.Fatalf("reliability %+v", rel)
			}

			found := false
			for _, ch := range a.Health.Run(ctx) {
				if ch.Source == "Downloads" {
					found = true
					if ch.Link != "/activity/downloads?status=failed" || ch.Action != "Show 3 failed downloads" {
						t.Fatalf("check %+v", ch)
					}
				}
			}
			if !found {
				t.Fatal("no failed-downloads warning")
			}
		})
	}
}
