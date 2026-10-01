package api

import (
	"bytes"
	"html"
	"io/fs"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/Asion001/mangarr/web"
)

const notBuiltPage = `<!doctype html><html><head><title>mangarr</title></head><body style="font-family:sans-serif;padding:2rem">
<h1>mangarr</h1><p>The web UI has not been built. Run <code>make web</code> (or use the Docker image).
The API is available at <a href="api/docs">api/docs</a>.</p></body></html>`

// staticHandler serves the SPA with index.html fallback for client routes.
func (s *Server) staticHandler() http.Handler {
	var root fs.FS
	if s.app.Cfg.WebDir != "" {
		root = os.DirFS(s.app.Cfg.WebDir)
	} else {
		sub, err := fs.Sub(web.Dist, "dist")
		if err != nil {
			panic(err)
		}
		root = sub
	}
	index, err := fs.ReadFile(root, "index.html")
	hasUI := err == nil
	if hasUI && s.app.Cfg.URLBase != "" {
		index = withBase(index, s.app.Cfg.URLBase)
	}
	files := http.FileServerFS(root)
	start := time.Now()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		if !hasUI {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(notBuiltPage))
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if f, err := root.Open(p); err == nil {
				_ = f.Close()
				if strings.HasSuffix(p, ".webmanifest") {
					w.Header().Set("Content-Type", "application/manifest+json")
				}
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, "index.html", start, bytes.NewReader(index))
	})
}

// baseTag matches the <base> tag the web build writes (Vite keeps index.html's
// `<base href="/" />`, self-closing and all).
var baseTag = regexp.MustCompile(`<base href="/"\s*/?>`)

// withBase lets the SPA know its base path, so its relative asset and page
// chunk URLs resolve under it.
func withBase(index []byte, urlBase string) []byte {
	tag := []byte(`<base href="` + html.EscapeString(urlBase) + `/" />`)
	return baseTag.ReplaceAllLiteral(index, tag)
}
