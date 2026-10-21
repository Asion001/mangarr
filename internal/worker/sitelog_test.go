package worker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestPageRequestsAreLogged: every request for a page, the failed tries
// too, is kept for the server's source reliability.
func TestPageRequestsAreLogged(t *testing.T) {
	var calls atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("page"))
	}))
	defer site.Close()
	w := &Worker{cfg: Config{Fetch: site.Client()}}
	var log siteLog
	data, err := w.fetchPage(t.Context(), PageSpec{URL: site.URL + "/1.jpg"}, &log)
	if err != nil || string(data) != "page" {
		t.Fatalf("fetch: %q, %v", data, err)
	}
	reqs := log.take()
	if len(reqs) != 2 || !strings.Contains(reqs[0].Error, "503") || reqs[1].Error != "" {
		t.Fatalf("logged %+v", reqs)
	}
	if again := log.take(); len(again) != 0 {
		t.Fatalf("taken twice: %+v", again)
	}
}
