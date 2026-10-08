package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/version"
)

// buildID names the running build. The web app compares it across
// reconnects and offers a reload when the server comes back as a new build.
func buildID() string {
	return version.Version + "+" + version.Build + "+" + version.Commit
}

// handleEvents streams bus events to the UI as Server-Sent Events.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := make(chan events.Event, 256)
	// people who aren't admins only get cache invalidation (no titles of
	// series outside what they may see, no system events), and the queue's
	// live progress when they may watch it (it carries only numbers)
	who := access.From(r.Context())
	admin := who.IsAdmin()
	watches := who.Can(access.ActivityView)
	unsub := s.app.Bus.Subscribe(func(e events.Event) {
		if !admin && e.Type != events.ResourceChanged && (!watches || e.Type != downloads.EventProgress) {
			return
		}
		select {
		case ch <- e:
		default: // slow client: drop; the UI refetches on reconnect
		}
	})
	defer unsub()

	fmt.Fprint(w, "retry: 3000\n\n")
	// hello carries the build, for every user, on every (re)connect
	if b, err := json.Marshal(events.Event{Type: "hello", Time: time.Now(), Payload: map[string]string{"build": buildID()}}); err == nil {
		fmt.Fprintf(w, "event: hello\ndata: %s\n\n", b)
	}
	flusher.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case e := <-ch:
			b, err := json.Marshal(e)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, b)
			flusher.Flush()
		}
	}
}
