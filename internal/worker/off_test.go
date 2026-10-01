package worker

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A worker switched off in System → Workers waits to be switched on again
// instead of giving up, and says so in its status.
func TestSwitchedOffWorkerWaits(t *testing.T) {
	old := offPoll
	offPoll = 20 * time.Millisecond
	defer func() { offPoll = old }()

	var on atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !on.Load() && r.URL.Path != "/api/v1/worker/bye" {
			http.Error(rw, `{"detail":"this worker is switched off"}`, http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/api/v1/worker/hello":
			_, _ = io.WriteString(rw, `{"workerId":1,"name":"desk","roles":["download"],"leaseSeconds":120}`)
		case "/api/v1/worker/lease":
			time.Sleep(10 * time.Millisecond)
			_, _ = io.WriteString(rw, `{"concurrent":1}`)
		default:
			rw.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	st := NewStatus()
	w, err := New(Config{ServerURL: srv.URL, Key: "k", Roles: []string{"download"}, Status: st,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), HTTP: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	waitState := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for st.State() != want {
			if time.Now().After(deadline) {
				t.Fatalf("state %q, want %q", st.State(), want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitState(StateOff) // off from the start: hello waits
	on.Store(true)
	waitState(StateReady)
	on.Store(false) // switched off while running: leases wait
	waitState(StateOff)
	on.Store(true)
	waitState(StateReady)

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
