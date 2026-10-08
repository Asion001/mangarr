package api

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5/middleware"
)

// compressAPI gzips JSON and text answers under /api/ for browsers that
// accept it. Images are already compressed and live updates must reach the
// browser as they happen, so neither goes through it.
func compressAPI(next http.Handler) http.Handler {
	compressed := middleware.Compress(5, "application/json", "application/problem+json", "text/plain", "text/html", "text/csv")(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/v1/events" {
			compressed.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// gzipAssets keeps gzipped copies of the UI's text files, made the first
// time a browser asks for one, since the built files never change while the
// server runs.
type gzipAssets struct {
	root fs.FS
	mu   sync.Mutex
	m    map[string][]byte
}

func newGzipAssets(root fs.FS) *gzipAssets { return &gzipAssets{root: root, m: map[string][]byte{}} }

var compressibleExt = map[string]bool{".js": true, ".css": true, ".svg": true, ".json": true, ".webmanifest": true, ".html": true, ".txt": true, ".map": true}

// serve answers with the gzipped file when the browser takes gzip, and
// reports whether it did.
func (g *gzipAssets) serve(w http.ResponseWriter, r *http.Request, name string) bool {
	ext := path.Ext(name)
	if !compressibleExt[ext] {
		return false
	}
	w.Header().Add("Vary", "Accept-Encoding")
	if !acceptsGzip(r) {
		return false
	}
	data, ok := g.get(name)
	if !ok {
		return false
	}
	ct := mime.TypeByExtension(ext)
	if ext == ".webmanifest" {
		ct = "application/manifest+json"
	}
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
	return true
}

func (g *gzipAssets) get(name string) ([]byte, bool) {
	g.mu.Lock()
	data, ok := g.m[name]
	g.mu.Unlock()
	if ok {
		return data, true
	}
	raw, err := fs.ReadFile(g.root, name)
	if err != nil {
		return nil, false
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = zw.Write(raw)
	if zw.Close() != nil {
		return nil, false
	}
	g.mu.Lock()
	g.m[name] = buf.Bytes()
	g.mu.Unlock()
	return buf.Bytes(), true
}

// acceptsGzip reads Accept-Encoding, honouring an explicit gzip;q=0.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(name), "gzip") {
			q := strings.ReplaceAll(strings.TrimSpace(params), " ", "")
			return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
		}
	}
	return false
}
