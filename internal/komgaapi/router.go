package komgaapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Asion001/mangarr/internal/apitiming"
	"github.com/Asion001/mangarr/internal/reading"
)

// router builds the API. Routes sit at the root, like Komga's.
func (s *Service) router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, cors, middleware.StripSlashes, apitiming.Middleware, s.logRequests, middleware.Recoverer)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) { writeError(w, r, http.StatusNotFound, "Not Found") })
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusMethodNotAllowed, "Method Not Allowed")
	})

	s.webRoutes(r)
	// public: KMReader checks that this is a Komga server before logging in
	r.Get("/api/v1/client-settings/global/list", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, map[string]any{}) })
	// KOReader sync uses its own MD5-key headers, not Komga authentication.
	k := &koSyncHandlers{s}
	r.Post("/users/create", k.create)
	r.Get("/users/auth", k.auth)
	r.Put("/syncs/progress", k.putProgress)
	r.Get("/syncs/progress/{document}", k.getProgress)

	r.Group(func(r chi.Router) {
		r.Use(s.requireAuth)
		o := &opdsHandlers{s}
		r.Get("/opds", o.root)
		r.Get("/opds/", o.root)
		r.Get("/opds/search.xml", o.searchDescription)
		r.Get("/opds/search", o.search)
		r.Get("/opds/updated", o.updated)
		r.Get("/opds/libraries", o.libraries)
		r.Get("/opds/libraries/{id}", o.library)
		r.Get("/opds/series/{id}", o.series)
		r.Get("/opds/chapters/{id}", o.chapter)
		r.Get("/opds/chapters/{id}/epub", o.chapterEPUB)
		r.Get("/opds/covers/series/{id}", o.seriesCover)
		r.Get("/opds/covers/chapters/{id}", o.chapterCover)
		s.routes(r)
	})
	return r
}

// routes registers every protected route (see testdata/routes.txt).
func (s *Service) routes(r chi.Router) {
	a := &authHandlers{s}
	r.Get("/api/v2/users/me", a.me)
	r.Get("/api/v2/users/me/api-keys", a.listKeys)
	r.Post("/api/v2/users/me/api-keys", a.createKey)
	r.Delete("/api/v2/users/me/api-keys/{id}", a.deleteKey)
	r.Post("/api/logout", a.logout)
	r.Get("/api/v1/client-settings/user/list", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, map[string]any{}) })

	l := &libraryHandlers{s}
	r.Get("/api/v1/libraries", l.list)
	r.Get("/api/v1/libraries/{id}", l.get)

	sh := &seriesHandlers{s}
	bh := &bookHandlers{s}
	ch := &catalogHandlers{s}
	r.Get("/api/v1/series", sh.list)
	r.Post("/api/v1/series/list", sh.search)
	r.Get("/api/v1/series/new", sh.newest)
	r.Get("/api/v1/series/updated", sh.updated)
	r.Get("/api/v1/series/{id}", sh.get)
	r.Get("/api/v1/series/{id}/thumbnail", sh.thumbnail)
	r.Get("/api/v1/series/{id}/books", bh.seriesBooks)
	r.Get("/api/v1/series/{id}/collections", ch.seriesCollections)

	r.Get("/api/v1/books", bh.list)
	r.Post("/api/v1/books/list", bh.search)
	r.Get("/api/v1/books/ondeck", bh.onDeck)
	r.Get("/api/v1/books/{id}", bh.get)
	r.Get("/api/v1/books/{id}/next", bh.sibling(1))
	r.Get("/api/v1/books/{id}/previous", bh.sibling(-1))
	r.Get("/api/v1/books/{id}/readlists", ch.bookReadLists)

	ph := &pageHandlers{s}
	r.Get("/api/v1/books/{id}/pages", ph.list)
	r.Get("/api/v1/books/{id}/pages/{n}", ph.image)
	r.Get("/api/v1/books/{id}/pages/{n}/thumbnail", ph.pageThumbnail)
	r.Get("/api/v1/books/{id}/thumbnail", ph.thumbnail)
	r.Get("/api/v1/books/{id}/file", ph.file)
	r.Get("/api/v1/books/{id}/file/*", ph.file)

	r.Get("/sse/v1/events", s.events)

	pr := &progressHandlers{s}
	r.Patch("/api/v1/books/{id}/read-progress", pr.patchBook)
	r.Delete("/api/v1/books/{id}/read-progress", pr.deleteBook)
	r.Post("/api/v1/series/{id}/read-progress", pr.markSeries(true))
	r.Delete("/api/v1/series/{id}/read-progress", pr.markSeries(false))
	r.Get("/api/v2/series/{id}/read-progress/tachiyomi", pr.tachiyomiV2)
	r.Put("/api/v2/series/{id}/read-progress/tachiyomi", pr.putTachiyomiV2)
	r.Get("/api/v1/series/{id}/read-progress/tachiyomi", pr.tachiyomiV1)
	r.Put("/api/v1/series/{id}/read-progress/tachiyomi", pr.putTachiyomiV1)

	genres := ch.strings(func(si reading.SeriesInfo) []string { return si.Series.Metadata.Genres })
	tags := ch.strings(func(si reading.SeriesInfo) []string { return si.Series.Metadata.Tags })
	r.Get("/api/v1/genres", genres)
	r.Get("/api/v1/tags", ch.strings(func(si reading.SeriesInfo) []string {
		return append(append([]string{}, si.Series.Metadata.Genres...), si.Series.Metadata.Tags...)
	}))
	r.Get("/api/v1/tags/series", tags)
	r.Get("/api/v1/tags/book", emptyList)
	r.Get("/api/v1/publishers", ch.strings(func(si reading.SeriesInfo) []string { return []string{si.Series.Metadata.Publisher} }))
	r.Get("/api/v1/languages", ch.strings(func(si reading.SeriesInfo) []string { return []string{si.Series.Language} }))
	r.Get("/api/v1/age-ratings", emptyList)
	r.Get("/api/v1/sharing-labels", emptyList)
	r.Get("/api/v1/series/release-dates", ch.strings(func(si reading.SeriesInfo) []string {
		if si.FirstRelease == nil {
			return nil
		}
		return []string{si.FirstRelease.Format("2006")}
	}))
	r.Get("/api/v1/authors", ch.authorsV1)
	r.Get("/api/v2/authors", ch.authorsV2)
	r.Get("/api/v1/collections", ch.collections)
	r.Get("/api/v1/collections/{id}", ch.collection)
	r.Get("/api/v1/collections/{id}/series", ch.collectionSeries)
	r.Get("/api/v1/readlists", ch.readLists)
	r.Get("/api/v1/readlists/{id}", ch.readListGet)
	r.Get("/api/v1/readlists/{id}/books", ch.readListBooks)
	r.Get("/api/v1/history", emptyPage)
}

// cors lets browser-based clients call the API (credentials go in headers).
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-API-Key, X-Auth-Token")
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		h.Set("Access-Control-Expose-Headers", "X-Auth-Token, Content-Disposition")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// logRequests logs requests at debug level (errors at warn).
func (s *Service) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		st := ww.Status()
		args := []any{"method", r.Method, "path", r.URL.Path, "status", st, "duration", time.Since(start).Round(time.Millisecond),
			"client", ClientName(r.UserAgent())}
		switch {
		case st >= 500:
			s.deps.Log.Warn("Komga API request failed", args...)
		case st == http.StatusNotFound && !strings.Contains(r.URL.Path, "/thumbnail"):
			s.deps.Log.Info("Komga API route not supported", args...) // shows what apps still miss
		default:
			s.deps.Log.Debug("Komga API request", args...)
		}
	})
}
