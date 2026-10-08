package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestAPICacheDefaults(t *testing.T) {
	var seen string
	h := apiCacheDefaults(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = r.Context().Value(ifNoneMatchKey{}).(string)
		if r.URL.Path == "/api/v1/own" {
			w.Header().Set("Cache-Control", cachePrivate(60e9))
		}
	}))
	for path, want := range map[string]string{
		"/api/v1/series": cacheNever,            // no header of its own: never stored
		"/api/v1/own":    "private, max-age=60", // an operation's own header wins
		"/assets/x.js":   "",                    // not the API: the static handler decides
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("If-None-Match", `"abc"`)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Cache-Control"); got != want {
			t.Errorf("%s: Cache-Control %q, want %q", path, got, want)
		}
		if strings.HasPrefix(path, "/api/") && seen != `"abc"` {
			t.Errorf("%s: If-None-Match not passed on: %q", path, seen)
		}
	}
}

func TestImageReplyNotModified(t *testing.T) {
	data := []byte("not really a jpeg")
	first := imageReply(context.Background(), data, "image/jpeg", cachePrivate(3600e9))
	if first.Status != http.StatusOK || first.ETag == "" || string(first.Body) != string(data) {
		t.Fatalf("first reply: %+v", first)
	}
	ctx := context.WithValue(context.Background(), ifNoneMatchKey{}, first.ETag)
	again := imageReply(ctx, data, "image/jpeg", cachePrivate(3600e9))
	if again.Status != http.StatusNotModified || again.Body != nil || again.ETag != first.ETag {
		t.Fatalf("revalidation: %+v", again)
	}
	changed := imageReply(ctx, []byte("another image"), "image/jpeg", cachePrivate(3600e9))
	if changed.Status != http.StatusOK || changed.ETag == first.ETag {
		t.Fatalf("changed image must be sent: %+v", changed)
	}
}

// Only cache.go may say "public": every other response is behind a login,
// and a CDN in front of the server would hand a public one to anyone.
func TestNoPublicCacheOutsidePolicy(t *testing.T) {
	public := regexp.MustCompile(`"public,`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if f == "cache.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if public.Match(src) {
			t.Errorf("%s sets a public Cache-Control; use the policy in cache.go", f)
		}
	}
}
