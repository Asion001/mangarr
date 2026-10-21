package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEndStreamsOnShutdown(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	ended := make(chan string, 2)
	h := endStreamsOn(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			ended <- r.URL.Path + " cut"
		case <-time.After(200 * time.Millisecond):
			ended <- r.URL.Path + " finished"
		}
	}))
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/events", nil))
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/read/chapters/1/pages/2", nil))
	time.Sleep(20 * time.Millisecond)
	stop()
	got := map[string]bool{<-ended: true, <-ended: true}
	if !got["/api/v1/events cut"] || !got["/api/v1/read/chapters/1/pages/2 finished"] {
		t.Fatalf("shutdown should cut the stream and let the page finish, got %v", got)
	}
}
