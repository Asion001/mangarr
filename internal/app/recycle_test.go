package app_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

func TestRecycleAPIAndOriginalPipeline(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			name := "recycle-pipeline-" + dialect
			scenario := fakesource.NewScenario(name)
			scenario.PageWidth = 400
			scenario.Sources = []source.SourceInfo{{ID: "paper", Name: "Paper Source", Lang: "en"}}
			scenario.AddManga(&fakesource.Manga{SourceID: "paper", URL: "/amber", Title: "Amber Bridges", Status: source.StatusOngoing, Chapters: []fakesource.Chapter{{URL: "/one", Name: "Chapter 1", Number: 1, Uploaded: time.Now(), Pages: 2}}})
			e := newTestApp(t, dsn)
			moduleID := e.addFakeModule(t, name)
			ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Amber Bridges", RootFolderID: e.RFID, Monitor: model.MonitorAll, SearchMissing: true, Sources: []series.SourceLink{{ModuleID: moduleID, SourceID: "paper", URL: "/amber", SourceName: "Paper Source", Lang: "en"}}})
			if err != nil {
				t.Fatal(err)
			}
			e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
			waitFor(t, 20*time.Second, "original archive", func() bool { return len(e.chapterFiles(t, ser.ID)) == 1 })
			original := e.chapterFiles(t, ser.ID)["1"]
			var profile model.Profile
			if err := e.App.DB.NewSelect().Model(&profile).Where("id = ?", ser.ProfileID).Scan(e.Ctx); err != nil {
				t.Fatal(err)
			}
			cfg := profile.Config
			cfg.Pages = model.PageRules{JunkUnder: -1, SplitTall: true, SplitRatio: 1.2, SegmentRatio: 0.625}
			job, _, err := e.App.DLQueue.EnqueueConfigured(e.Ctx, ser.ID, original.ChapterID, original.ReleaseID, model.JobKindReprocess, true, 0, downloads.JobOptions{Config: &cfg})
			if err != nil {
				t.Fatal(err)
			}
			awaitRecycleJob(t, e, job.ID)
			split := e.chapterFiles(t, ser.ID)["1"]
			if split.PageCount != 6 || split.ID != original.ID {
				t.Fatalf("split: %+v", split)
			}
			srv := httptest.NewServer(api.New(e.App))
			defer srv.Close()
			general, _ := e.App.Settings.General(e.Ctx)
			request := func(method, path string, body, out any) int {
				t.Helper()
				var b bytes.Buffer
				if body != nil {
					if err := json.NewEncoder(&b).Encode(body); err != nil {
						t.Fatal(err)
					}
				}
				req, _ := http.NewRequest(method, srv.URL+path, &b)
				req.Header.Set("X-Api-Key", general.APIKey)
				req.Header.Set("Content-Type", "application/json")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				data, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				if resp.StatusCode >= 400 {
					t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, data)
				}
				if out != nil {
					reflect.ValueOf(out).Elem().SetZero()
					if err := json.Unmarshal(data, out); err != nil {
						t.Fatalf("decode %s: %v %s", path, err, data)
					}
				}
				return resp.StatusCode
			}
			var list api.RecycleList
			request("GET", "/api/v1/recycle-bin?reason=reprocessed&q=amber&pageSize=1", nil, &list)
			if list.Total != 1 || len(list.Items) != 1 || len(list.Groups) != 1 || !list.Items[0].CountChanged {
				t.Fatalf("list: %+v", list)
			}
			old := list.Items[0]
			var detail api.RecycledResource
			request("GET", fmt.Sprintf("/api/v1/recycle-bin/%d", old.ID), nil, &detail)
			if detail.PageCount != 2 || detail.Current == nil || detail.Current.PageCount != 6 || detail.JobKind != model.JobKindReprocess {
				t.Fatalf("detail: %+v", detail)
			}
			var read api.ReadChapter
			request("GET", fmt.Sprintf("/api/v1/recycle-bin/%d/read", old.ID), nil, &read)
			if len(read.Pages) != 2 || read.CanDownload || read.Progress.Page != 0 {
				t.Fatalf("read: %+v", read)
			}
			request("GET", fmt.Sprintf("/api/v1/recycle-bin/%d/pages/1", old.ID), nil, nil)
			var versions struct {
				Current  *model.ChapterFile
				Recycled []api.RecycledResource
			}
			request("GET", fmt.Sprintf("/api/v1/chapters/%d/versions", original.ChapterID), nil, &versions)
			if versions.Current == nil || len(versions.Recycled) != 1 {
				t.Fatalf("versions: %+v", versions)
			}
			// Starting at the original must produce two pages, not reprocess six split segments.
			cfg = profile.Config
			cfg.Pages = model.PageRules{JunkUnder: -1, MaxWidth: 200}
			var results []api.RecycleResult
			request("POST", "/api/v1/recycle-bin/reprocess", api.RecycleRun{IDs: []int64{old.ID}, Config: &cfg}, &results)
			assertRecycleResult(t, results)
			awaitRecycleJob(t, e, results[0].JobID)
			file := e.chapterFiles(t, ser.ID)["1"]
			if file.PageCount != 2 || file.AvgWidth != 200 || file.ID != original.ID {
				t.Fatalf("original processing: %+v", file)
			}
			var persisted model.Profile
			if err := e.App.DB.NewSelect().Model(&persisted).Where("id = ?", profile.ID).Scan(e.Ctx); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(profile.Config, persisted.Config) {
				t.Fatal("one-off processing changed series profile")
			}
			if _, err := os.Stat(filepath.Join(e.App.Library.RecycleDir(e.Ctx), old.RecycledPath)); err != nil {
				t.Fatalf("source removed: %v", err)
			}
			// Force a download even though upgrades are disabled and this release is current.
			for _, release := range []string{"same", "best"} {
				cfg = profile.Config
				cfg.Pages = model.PageRules{JunkUnder: -1, MaxWidth: 300}
				request("POST", "/api/v1/recycle-bin/reprocess", api.RecycleRun{IDs: []int64{old.ID}, StartFrom: "download", Release: release, Config: &cfg}, &results)
				assertRecycleResult(t, results)
				awaitRecycleJob(t, e, results[0].JobID)
				file = e.chapterFiles(t, ser.ID)["1"]
				if file.AvgWidth != 300 || file.PageCount != 2 || file.ID != original.ID {
					t.Fatalf("fresh download %s: %+v", release, file)
				}
			}
			request("POST", "/api/v1/recycle-bin/restore", api.RecycleIDs{IDs: []int64{old.ID}}, &results)
			assertRecycleResult(t, results)
			file = e.chapterFiles(t, ser.ID)["1"]
			if file.ID != original.ID || file.SHA256 != original.SHA256 || file.AvgWidth != 400 {
				t.Fatalf("restore: %+v", file)
			}
			request("GET", "/api/v1/recycle-bin?reason=restored_over", nil, &list)
			if list.Total != 1 {
				t.Fatalf("replacement was not recycled: %+v", list)
			}
			// Both clients use the existing media settings document.
			mm, err := e.App.Settings.MediaManagement(e.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			mm.RecycleBinDays = 0
			request("PUT", "/api/v1/settings/media", mm, nil)
			request("GET", "/api/v1/recycle-bin", nil, &list)
			if list.RetentionDays != 0 {
				t.Fatal("retention diverged")
			}
			for _, r := range list.Items {
				if r.PurgeAt != nil {
					t.Fatal("forever has a purge date")
				}
			}
			request("DELETE", "/api/v1/recycle-bin?all=1", nil, &results)
			for _, r := range results {
				if r.Error != "" {
					t.Fatal(r.Error)
				}
			}
			request("GET", "/api/v1/recycle-bin", nil, &list)
			if list.Total != 0 {
				t.Fatalf("empty bin: %+v", list)
			}
		})
	}
}

func assertRecycleResult(t *testing.T, results []api.RecycleResult) {
	t.Helper()
	if len(results) != 1 || results[0].Error != "" {
		t.Fatalf("results: %+v", results)
	}
}
func awaitRecycleJob(t *testing.T, e *testEnv, id int64) {
	t.Helper()
	waitFor(t, 20*time.Second, "recycle job", func() bool {
		var job model.DownloadJob
		if err := e.App.DB.NewSelect().Model(&job).Where("id = ?", id).Scan(e.Ctx); err != nil {
			t.Fatal(err)
		}
		if job.Status == model.JobFailed {
			t.Fatalf("job failed: %s", job.Error)
		}
		return job.Status == model.JobCompleted
	})
}
