package api_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestEventsHello: the stream opens with a hello event naming the build,
// which the web app uses to spot a server update.
func TestEventsHello(t *testing.T) {
	srv, _ := newServer(t, true)
	resp, err := http.Get(srv.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	var event string
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			event = v
		}
		if v, ok := strings.CutPrefix(line, "data: "); ok {
			if event != "hello" {
				t.Fatalf("first event is %q, want hello", event)
			}
			var e struct {
				Payload struct {
					Build string `json:"build"`
				} `json:"payload"`
			}
			if err := json.Unmarshal([]byte(v), &e); err != nil || e.Payload.Build == "" {
				t.Fatalf("hello payload %s: %v", v, err)
			}
			return
		}
	}
	t.Fatal("stream ended before hello")
}
