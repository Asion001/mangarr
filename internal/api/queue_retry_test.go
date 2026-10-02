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

// TestQueueRetryActiveChapter retries failed jobs whose chapter already has
// a job in the queue, or that share a chapter with each other: those stay
// failed and are reported as skipped instead of failing the whole request.
func TestQueueRetryActiveChapter(t *testing.T) {
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
			root := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
			if _, err := a.DB.NewInsert().Model(root).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			ser := &model.Series{Title: "Tin Rivers", SortTitle: "tin rivers", RootFolderID: root.ID, ProfileID: 1, Path: "tin", Tags: []int64{}, AddedAt: now, UpdatedAt: now}
			if _, err := a.DB.NewInsert().Model(ser).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			var chapters []int64
			for i := 0; i < 3; i++ {
				ch := &model.Chapter{SeriesID: ser.ID, NumberKey: fmt.Sprint(i + 1), NumberSort: float64(i + 1), State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
				if _, err := a.DB.NewInsert().Model(ch).Exec(ctx); err != nil {
					t.Fatal(err)
				}
				chapters = append(chapters, ch.ID)
			}
			enqueue := func(chapterID int64, fail bool) int64 {
				t.Helper()
				job, created, err := a.DLQueue.Enqueue(ctx, ser.ID, chapterID, nil, model.JobKindDownload, false)
				if err != nil || !created {
					t.Fatalf("enqueue: %v %v", created, err)
				}
				if fail {
					if _, err := a.DB.NewUpdate().Model((*model.DownloadJob)(nil)).Set("status = ?", model.JobFailed).Set("error = 'boom'").Where("id = ?", job.ID).Exec(ctx); err != nil {
						t.Fatal(err)
					}
				}
				return job.ID
			}
			// chapter 1: a failed job and a queued one; chapter 2: two failed
			// jobs; chapter 3: one failed job
			busy := enqueue(chapters[0], true)
			enqueue(chapters[0], false)
			older := enqueue(chapters[1], true)
			newer := enqueue(chapters[1], true)
			lone := enqueue(chapters[2], true)

			if code := c.do("POST", fmt.Sprintf("/api/v1/queue/%d/retry", busy), "", nil); code != http.StatusConflict {
				t.Fatalf("single retry of a busy chapter: %d", code)
			}
			var out api.QueueBulkOutput
			body := fmt.Sprintf(`{"ids":[%d,%d,%d,%d],"action":"retry"}`, busy, older, newer, lone)
			if code := c.do("POST", "/api/v1/queue/bulk", body, &out); code != 200 || out.Affected != 2 || out.Skipped != 2 {
				t.Fatalf("bulk retry: %d %+v", code, out)
			}
			want := map[int64]string{busy: model.JobFailed, older: model.JobFailed, newer: model.JobQueued, lone: model.JobQueued}
			for id, status := range want {
				var job model.DownloadJob
				if err := a.DB.NewSelect().Model(&job).Where("id = ?", id).Scan(ctx); err != nil {
					t.Fatal(err)
				}
				if job.Status != status {
					t.Fatalf("job %d: %s, want %s", id, job.Status, status)
				}
			}
		})
	}
}
