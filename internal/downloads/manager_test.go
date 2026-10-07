package downloads

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/settings"
)

type pageSource struct {
	source.Module
	data []byte
}

func (s pageSource) FetchPage(context.Context, source.Page) (io.ReadCloser, string, error) {
	return io.NopCloser(bytes.NewReader(s.data)), "image/png", nil
}

func TestFetchPagePublishesValidatedImage(t *testing.T) {
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 4, 8))); err != nil {
		t.Fatal(err)
	}
	m := &Manager{}
	dir := t.TempDir()
	page, err := m.fetchPage(t.Context(), pageSource{data: data.Bytes()}, source.Page{}, 1, dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Name != "0002.png" || page.Format != "png" || page.Width != 4 || page.Height != 8 {
		t.Fatalf("page metadata: %+v", page)
	}
	got, err := os.ReadFile(page.Path)
	if err != nil || !bytes.Equal(got, data.Bytes()) {
		t.Fatalf("staged image differs: %v", err)
	}
	if _, err := os.Stat(page.Path + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary file remains: %v", err)
	}
	if _, err := m.fetchPage(t.Context(), pageSource{data: []byte("<html>unavailable</html>")}, source.Page{}, 2, dir, 1); err == nil {
		t.Fatal("invalid image accepted")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("invalid image left a staged file: %v %v", entries, err)
	}
	_, err = m.fetchPage(t.Context(), pageSource{data: data.Bytes()}, source.Page{}, 0, filepath.Join(dir, "missing"), 1)
	var infra infraError
	if !errors.As(err, &infra) {
		t.Fatalf("disk error must be an infrastructure failure: %v", err)
	}
}

func TestManagerFailurePolicy(t *testing.T) {
	cases := []struct {
		name       string
		kind       string
		attempt    int
		err        error
		cancelled  bool
		wantStatus string
		wantDelay  time.Duration
	}{
		{"download retry", model.JobKindDownload, 0, errors.New("page unavailable"), false, model.JobQueued, 30 * time.Second},
		{"download exhausted", model.JobKindDownload, 2, errors.New("page unavailable"), false, model.JobFailed, 0},
		{"permanent download", model.JobKindDownload, 0, permanent(errors.New("invalid chapter")), false, model.JobFailed, 0},
		{"infrastructure retry", model.JobKindDownload, 2, infraError{errors.New("disk unavailable")}, false, model.JobQueued, 15 * time.Minute},
		{"infrastructure cap", model.JobKindDownload, 20, infraError{errors.New("disk unavailable")}, false, model.JobQueued, time.Hour},
		{"reprocess failure", model.JobKindReprocess, 0, errors.New("processing failed"), false, model.JobFailed, 0},
		{"reprocess infrastructure", model.JobKindReprocess, 0, infraError{errors.New("engine unavailable")}, false, model.JobQueued, 5 * time.Minute},
		{"cancelled", model.JobKindDownload, 0, context.Canceled, true, model.JobDownloading, 0},
	}
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		q, chapters := rankFixture(t, d, len(cases))
		m := NewManager(d, q.bus, nil, settings.NewStore(d), nil, q, nil,
			slog.New(slog.NewTextHandler(io.Discard, nil)), t.TempDir())
		for i, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ctx := t.Context()
				ch := chapters[i]
				job, _, err := q.Enqueue(ctx, ch.SeriesID, ch.ID, nil, tc.kind, false)
				if err != nil {
					t.Fatal(err)
				}
				job.Status, job.Attempt = model.JobDownloading, tc.attempt
				if _, err := d.NewUpdate().Model(job).Column("status", "attempt").WherePK().Exec(ctx); err != nil {
					t.Fatal(err)
				}
				failCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				if tc.cancelled {
					cancel()
				}
				before := time.Now().UTC()
				m.fail(failCtx, job, nil, tc.err)
				var got model.DownloadJob
				if err := d.NewSelect().Model(&got).Where("id = ?", job.ID).Scan(ctx); err != nil {
					t.Fatal(err)
				}
				wantAttempt := tc.attempt + 1
				wantError := tc.err.Error()
				if tc.cancelled {
					wantAttempt, wantError = tc.attempt, ""
				}
				if got.Status != tc.wantStatus || got.Attempt != wantAttempt || got.Error != wantError {
					t.Fatalf("failure state: %+v", got)
				}
				if tc.wantDelay > 0 && (got.NotBefore.Before(before.Add(tc.wantDelay)) || got.NotBefore.After(time.Now().UTC().Add(tc.wantDelay))) {
					t.Fatalf("retry time %v outside expected delay %v", got.NotBefore, tc.wantDelay)
				}
			})
		}
	})
}

func TestManagerPausedJobRejectsStaleTransitions(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		q, chapters := rankFixture(t, d, 1)
		m := NewManager(d, q.bus, nil, settings.NewStore(d), nil, q, nil,
			slog.New(slog.NewTextHandler(io.Discard, nil)), t.TempDir())
		ctx := t.Context()
		ch := chapters[0]
		job, _, err := q.Enqueue(ctx, ch.SeriesID, ch.ID, nil, model.JobKindDownload, false)
		if err != nil {
			t.Fatal(err)
		}
		if !m.claim(ctx, job) || m.claim(ctx, job) {
			t.Fatal("a queued job must be claimed only once")
		}
		if n, err := m.Bulk(ctx, []int64{job.ID}, "pause"); err != nil || n != 1 {
			t.Fatalf("pause: %d %v", n, err)
		}
		m.setStatus(ctx, job, model.JobProcessing, model.ChapterProcessing)
		m.completeUnchanged(ctx, job, "nothing to change")
		m.fail(ctx, job, nil, infraError{errors.New("engine unavailable")})
		var got model.DownloadJob
		if err := d.NewSelect().Model(&got).Where("id = ?", job.ID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if got.Status != model.JobPaused || m.claim(ctx, job) {
			t.Fatalf("stale worker changed a paused job: %+v", got)
		}
		if err := d.NewSelect().Model(&ch).WherePK().Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if ch.State != model.ChapterQueued {
			t.Fatalf("paused chapter state = %s", ch.State)
		}
	})
}

// TestLocalTaskLimit: this server's own task limit counts only what it runs
// itself, follows the setting without a restart, and 0 sets no cap.
func TestLocalTaskLimit(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		q, _ := rankFixture(t, d, 1)
		st := settings.NewStore(d)
		m := NewManager(d, q.bus, nil, st, nil, q, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), t.TempDir())
		ctx := t.Context()
		set := func(n int) {
			dl, _ := st.Downloads(ctx)
			dl.MaxLocalTasks = n
			if err := st.Set(ctx, settings.KeyDownloads, dl); err != nil {
				t.Fatal(err)
			}
		}
		set(1)
		if !m.takeLocal(ctx, model.JobKindDownload) || m.takeLocal(ctx, model.JobKindDownload) {
			t.Fatal("a limit of 1 should give exactly one slot")
		}
		set(2)
		if !m.takeLocal(ctx, model.JobKindDownload) || m.takeLocal(ctx, model.JobKindDownload) {
			t.Fatal("raising the limit should free one more slot")
		}
		m.releaseLocal()
		if !m.takeLocal(ctx, model.JobKindDownload) {
			t.Fatal("a released slot should be free again")
		}
		set(0)
		for range 5 {
			if !m.takeLocal(ctx, model.JobKindDownload) {
				t.Fatal("0 should set no cap")
			}
		}
		set(-1)
		if m.takeLocal(ctx, model.JobKindDownload) {
			t.Fatal("-1 should keep downloads off this server")
		}
		if !m.takeLocal(ctx, model.JobKindReprocess) {
			t.Fatal("reprocessing hands its image work to the workers and still runs")
		}
	})
}

// TestRemovedJobFailsQuietly: a job removed from the queue while it ran
// (its worker task can no longer be stored) is not a failed download.
func TestRemovedJobFailsQuietly(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		q, chapters := rankFixture(t, d, 1)
		m := NewManager(d, q.bus, nil, settings.NewStore(d), nil, q, nil,
			slog.New(slog.NewTextHandler(io.Discard, nil)), t.TempDir())
		ctx := t.Context()
		ch := chapters[0]
		job, _, err := q.Enqueue(ctx, ch.SeriesID, ch.ID, nil, model.JobKindDownload, false)
		if err != nil {
			t.Fatal(err)
		}
		if !m.claim(ctx, job) {
			t.Fatal("claim")
		}
		jc, err := m.load(ctx, job)
		if err != nil {
			t.Fatal(err)
		}
		var failed atomic.Int32
		q.bus.Subscribe(func(events.Event) { failed.Add(1) }, events.DownloadFailed)
		if err := q.Remove(ctx, job.ID, false); err != nil {
			t.Fatal(err)
		}
		m.fail(ctx, job, jc, permanent(errors.New(`violates foreign key constraint "worker_tasks_job_id_fkey"`)))
		if n := failed.Load(); n != 0 {
			t.Fatalf("removed job announced %d failures", n)
		}
		var h []model.History
		if err := d.NewSelect().Model(&h).Where("event_type = ?", model.HistoryFailed).Scan(ctx); err != nil || len(h) != 0 {
			t.Fatalf("removed job recorded as failed: %v %+v", err, h)
		}
	})
}
