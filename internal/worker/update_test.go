package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const offer = `{"version":"v1.3.0","image":"ghcr.io/asion001/mangarr:1.3.0","url":"https://example.invalid/w.zip","checksumUrl":"https://example.invalid/w.zip.sha256"}`

// updateServer hands out one download task and, from the first lease on,
// says it runs a newer version.
func updateServer(t *testing.T, byes *atomic.Int32, completed *atomic.Int32) *httptest.Server {
	var leased atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/worker/hello":
			_, _ = io.WriteString(rw, `{"workerId":1,"name":"desk","roles":["download"],"leaseSeconds":120,"concurrent":2}`)
		case "/api/v1/worker/lease":
			if leased.CompareAndSwap(false, true) {
				_, _ = io.WriteString(rw, `{"concurrent":2,"update":`+offer+`,"task":{"id":7,"kind":"nothing","spec":{}}}`)
				return
			}
			_, _ = io.WriteString(rw, `{"concurrent":2,"update":`+offer+`}`)
		case "/api/v1/worker/bye":
			byes.Add(1)
			rw.WriteHeader(http.StatusNoContent)
		case "/api/v1/worker/tasks/7/fail", "/api/v1/worker/tasks/7/complete":
			completed.Add(1)
			rw.WriteHeader(http.StatusNoContent)
		default:
			rw.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Told the server is newer, a worker that can replace itself finishes the
// task in hand, says goodbye and stops with the update.
func TestWorkerStopsToUpdate(t *testing.T) {
	var byes, completed atomic.Int32
	srv := updateServer(t, &byes, &completed)
	st := NewStatus()
	w, err := New(Config{ServerURL: srv.URL, Key: "k", Roles: []string{"download"}, Status: st, Version: "v1.2.0",
		AutoUpdate: true, SelfUpdate: true, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), HTTP: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = w.Run(ctx)
	var update *UpdateError
	if !errors.As(err, &update) {
		t.Fatalf("Run = %v, want an update", err)
	}
	if update.Offer.Version != "v1.3.0" {
		t.Errorf("update to %q", update.Offer.Version)
	}
	if completed.Load() != 1 {
		t.Errorf("the task in hand was reported %d times, want once", completed.Load())
	}
	if byes.Load() != 1 {
		t.Errorf("%d goodbyes, want 1", byes.Load())
	}
	if st.State() != StateUpdating || st.Snapshot(0).Update != "v1.3.0" {
		t.Errorf("status %q / %q", st.State(), st.Snapshot(0).Update)
	}
}

// A worker that can't replace itself (a container), has updates off, or
// was rolled back from that version keeps working and only says so.
func TestWorkerKeepsWorkingWithoutUpdate(t *testing.T) {
	for name, cfg := range map[string]Config{
		"container": {AutoUpdate: true},
		"off":       {SelfUpdate: true},
		"skipped":   {AutoUpdate: true, SelfUpdate: true, Skip: "v1.3.0"},
	} {
		t.Run(name, func(t *testing.T) {
			var byes, completed atomic.Int32
			srv := updateServer(t, &byes, &completed)
			st := NewStatus()
			cfg.ServerURL, cfg.Key, cfg.Roles, cfg.Status, cfg.Version = srv.URL, "k", []string{"download"}, st, "v1.2.0"
			cfg.Log, cfg.HTTP = slog.New(slog.NewTextHandler(io.Discard, nil)), srv.Client()
			w, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- w.Run(ctx) }()
			deadline := time.Now().Add(5 * time.Second)
			for completed.Load() == 0 || st.Snapshot(0).Update == "" {
				if time.Now().After(deadline) {
					t.Fatal("the task was never finished")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if st.State() != StateReady {
				t.Errorf("state %q, want ready", st.State())
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("Run = %v", err)
			}
		})
	}
}
