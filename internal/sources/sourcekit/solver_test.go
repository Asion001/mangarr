package sourcekit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSolverFetchesThroughWhenCookiesAreRefused: a site that turns the
// solved cookies away from anything but a browser is read through the
// solver's browser, without solving again for every page.
func TestSolverFetchesThroughWhenCookiesAreRefused(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<title>Just a moment...</title>`))
	}))
	t.Cleanup(site.Close)
	var asked []string
	flare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req flareRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		asked = append(asked, req.URL)
		resp := `<html><body>page</body></html>`
		if strings.Contains(req.URL, "/api") {
			resp = `<html><head><meta name="color-scheme" content="light dark"></head><body><pre>{"a":"&lt;b&gt;"}</pre></body></html>`
		}
		out, _ := json.Marshal(map[string]any{"status": "ok", "solution": map[string]any{"status": 200, "response": resp,
			"userAgent": "browser", "cookies": []map[string]any{{"name": "cf_clearance", "value": "x", "path": "/"}}}})
		_, _ = w.Write(out)
	}))
	t.Cleanup(flare.Close)

	c := NewClient(nil)
	c.Solver = NewSolver(flare.URL)
	ctx := context.Background()
	body, err := c.Do(ctx, Request{URL: site.URL + "/list", Query: map[string][]string{"page": {"2"}}})
	if err != nil || string(body) != `<html><body>page</body></html>` {
		t.Fatalf("page: %v %q", err, body)
	}
	// solved once, then fetched through once
	if len(asked) != 2 || asked[1] != site.URL+"/list?page=2" {
		t.Fatalf("solver asked for %v", asked)
	}
	var out struct{ A string }
	if err := c.JSON(ctx, Request{URL: site.URL + "/api"}, &out); err != nil || out.A != "<b>" {
		t.Fatalf("json: %v %+v", err, out)
	}
	if len(asked) != 3 {
		t.Fatalf("the host should not be solved again: %v", asked)
	}
	// a form post can't go through it
	if _, err := c.Do(ctx, Request{Method: http.MethodPost, URL: site.URL + "/form"}); err == nil {
		t.Fatal("a refused post should fail")
	}
}
