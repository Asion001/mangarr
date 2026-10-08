package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAcceptsGzip(t *testing.T) {
	for header, want := range map[string]bool{
		"":                      false,
		"gzip":                  true,
		"br, gzip, deflate":     true,
		"gzip;q=0":              false,
		"br;q=1.0, gzip; q=0.5": true,
		"deflate, identity":     false,
		"GZIP":                  true,
		"x-gzip-not-really, br": false,
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Accept-Encoding", header)
		if got := acceptsGzip(r); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestGzipAssets(t *testing.T) {
	js := strings.Repeat("console.log('mangarr');\n", 200)
	g := newGzipAssets(fstest.MapFS{"assets/app.js": {Data: []byte(js)}, "assets/logo.png": {Data: []byte("png")}})

	r := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	if !g.serve(w, r, "assets/app.js") {
		t.Fatal("a script should be served gzipped")
	}
	if w.Header().Get("Content-Encoding") != "gzip" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") || w.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("headers: %v", w.Header())
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(zr); string(got) != js {
		t.Fatal("gzipped script doesn't match the file")
	}

	r.Header.Del("Accept-Encoding")
	w = httptest.NewRecorder()
	if g.serve(w, r, "assets/app.js") || w.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatal("without gzip the file server answers, but caches must still know it varies")
	}
	r.Header.Set("Accept-Encoding", "gzip")
	if g.serve(httptest.NewRecorder(), r, "assets/logo.png") {
		t.Fatal("images are not gzipped")
	}
}

func TestCompressAPI(t *testing.T) {
	body := `{"items":[` + strings.Repeat(`{"title":"A long series title"},`, 100) + `{}]}`
	h := compressAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	for path, want := range map[string]string{"/api/v1/series": "gzip", "/api/v1/events": "", "/index.html": ""} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Accept-Encoding", "gzip")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if got := w.Header().Get("Content-Encoding"); got != want {
			t.Errorf("%s: Content-Encoding %q, want %q", path, got, want)
		}
	}
}
