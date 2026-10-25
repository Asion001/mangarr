package komgaapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/reading"
)

// sseHeartbeat keeps idle streams (and proxies) open.
const sseHeartbeat = 15 * time.Second

// sseEvent is one Komga server-sent event.
type sseEvent struct {
	Name string
	Data any
}

// events is GET /sse/v1/events: Komga's event stream, which KMReader
// listens to for live updates (it retries every 5 s when missing).
func (s *Service) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	ch := make(chan events.Event, 256)
	unsub := s.deps.Bus.Subscribe(func(e events.Event) {
		select {
		case ch <- e:
		default: // slow client: drop; apps refetch on reconnect
		}
	}, events.ResourceChanged, events.ChapterImported, events.SeriesAdded, events.SeriesDeleted, reading.ProgressChanged)
	defer unsub()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ":ok\n\n")
	flusher.Flush()

	ping := time.NewTicker(sseHeartbeat)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ":heartbeat\n\n")
			flusher.Flush()
		case e := <-ch:
			for _, ev := range s.komgaEvents(r.Context(), e) {
				b, err := json.Marshal(ev.Data)
				if err != nil {
					continue
				}
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Name, b)
			}
			flusher.Flush()
		}
	}
}

// userID is the id of the single Komga user (see users/me).
const userID = "1"

// komgaEvents maps a bus event to Komga's events.
func (s *Service) komgaEvents(ctx context.Context, e events.Event) []sseEvent {
	switch e.Type {
	case reading.ProgressChanged:
		p, ok := e.Payload.(reading.ProgressPayload)
		if !ok {
			return nil
		}
		if u := PrincipalFrom(ctx).User; u == nil || u.UserID != p.UserID {
			return nil // someone else's progress
		}
		name, seriesName := "ReadProgressChanged", "ReadProgressSeriesChanged"
		if p.Deleted {
			name, seriesName = "ReadProgressDeleted", "ReadProgressSeriesDeleted"
		}
		out := make([]sseEvent, 0, len(p.ChapterIDs)+1)
		for _, c := range p.ChapterIDs {
			out = append(out, sseEvent{name, map[string]string{"bookId": id(c), "userId": userID}})
		}
		return append(out, sseEvent{seriesName, map[string]string{"seriesId": id(e.SeriesID), "userId": userID}})
	case events.SeriesAdded:
		if e.SeriesID > 0 {
			return []sseEvent{{"SeriesAdded", s.seriesRef(ctx, e.SeriesID)}}
		}
	case events.SeriesDeleted:
		if e.SeriesID > 0 {
			return []sseEvent{{"SeriesDeleted", map[string]string{"seriesId": id(e.SeriesID), "libraryId": ""}}}
		}
	case events.ChapterImported:
		if e.SeriesID > 0 {
			return []sseEvent{{"SeriesChanged", s.seriesRef(ctx, e.SeriesID)}}
		}
	case events.ResourceChanged:
		res, ok := e.Payload.(events.Resource)
		if !ok || res.ID == 0 {
			return nil
		}
		switch {
		case res.Name == "series" && res.Action == "updated":
			return []sseEvent{{"SeriesChanged", s.seriesRef(ctx, res.ID)}}
		case res.Name == "chapter" && res.Action == "updated":
			var ch model.Chapter
			if err := s.deps.DB.NewSelect().Model(&ch).Column("id", "series_id").Where("id = ?", res.ID).Scan(ctx); err != nil {
				return nil
			}
			ref := s.seriesRef(ctx, ch.SeriesID)
			ref["bookId"] = id(ch.ID)
			return []sseEvent{{"BookChanged", ref}}
		}
	}
	return nil
}

// seriesRef is {seriesId, libraryId} for series events.
func (s *Service) seriesRef(ctx context.Context, seriesID int64) map[string]string {
	var ser model.Series
	lib := ""
	if err := s.deps.DB.NewSelect().Model(&ser).Column("id", "root_folder_id").Where("id = ?", seriesID).Scan(ctx); err == nil {
		lib = id(ser.RootFolderID)
	}
	return map[string]string{"seriesId": id(seriesID), "libraryId": lib}
}
