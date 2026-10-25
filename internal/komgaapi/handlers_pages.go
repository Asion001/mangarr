package komgaapi

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/reading"
)

type pageHandlers struct{ s *Service }

// book loads the book in the path (writing the error when it can't).
func (h *pageHandlers) book(w http.ResponseWriter, r *http.Request) (*reading.BookInfo, *reading.SeriesInfo, bool) {
	rid, ok := h.s.userID(w, r)
	if !ok {
		return nil, nil, false
	}
	b, err := h.s.deps.Reading.Book(r.Context(), rid, pathID(r, "id"))
	if err != nil {
		notFoundOr500(w, r, err)
		return nil, nil, false
	}
	si, err := h.s.deps.Reading.Series(r.Context(), rid, b.Chapter.SeriesID)
	if err != nil {
		notFoundOr500(w, r, err)
		return nil, nil, false
	}
	return b, si, true
}

// pageError answers a failed page request: 404 for unknown pages, 502 when
// the source failed (with the reason, which Paperback shows).
func pageError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, reading.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "page not found")
	case errors.Is(err, reading.ErrNoSource):
		writeError(w, r, http.StatusNotFound, err.Error())
	default:
		writeError(w, r, http.StatusBadGateway, "couldn't load the page from the source: "+err.Error())
	}
}

// list is GET /api/v1/books/{id}/pages.
func (h *pageHandlers) list(w http.ResponseWriter, r *http.Request) {
	b, _, ok := h.book(w, r)
	if !ok {
		return
	}
	pages, err := h.s.deps.Reading.Pages(r.Context(), b)
	if err != nil {
		pageError(w, r, err)
		return
	}
	out := make([]pageInfoDTO, len(pages))
	for i, p := range pages {
		out[i] = pageInfoDTO{Number: p.Number, FileName: p.FileName, MediaType: p.MediaType}
		if p.Size > 0 {
			size := p.Size
			out[i].SizeBytes, out[i].Size = &size, humanSize(size)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// pageNumber reads {n} (1-based, or 0-based with zero_based=true).
func pageNumber(r *http.Request) int {
	n, err := strconv.Atoi(chi.URLParam(r, "n"))
	if err != nil {
		return 0
	}
	if queryBool(r, "zero_based") {
		n++
	}
	return n
}

// image is GET /api/v1/books/{id}/pages/{n}[?convert=png|jpeg].
func (h *pageHandlers) image(w http.ResponseWriter, r *http.Request) {
	b, _, ok := h.book(w, r)
	if !ok {
		return
	}
	if to := r.URL.Query().Get("convert"); to == "png" || to == "jpeg" || to == "jpg" {
		data, ct, err := h.s.deps.Reading.Page(r.Context(), b, pageNumber(r))
		if err != nil {
			pageError(w, r, err)
			return
		}
		if ct != "image/"+to && !(to == "jpg" && ct == "image/jpeg") {
			out, oct, err := reading.Convert(data, to)
			if err != nil {
				writeError(w, r, http.StatusBadRequest, "can't convert this page: "+err.Error())
				return
			}
			data, ct = out, oct
		}
		writeImage(w, ct, data)
		return
	}
	page, err := h.s.deps.Reading.PageReader(r.Context(), b, pageNumber(r))
	if err != nil {
		pageError(w, r, err)
		return
	}
	defer page.Body.Close()
	streamPage(w, r, page)
}

// pageThumbnail is GET /api/v1/books/{id}/pages/{n}/thumbnail.
func (h *pageHandlers) pageThumbnail(w http.ResponseWriter, r *http.Request) {
	b, _, ok := h.book(w, r)
	if !ok {
		return
	}
	data, ct, err := h.s.deps.Reading.PageThumbnail(r.Context(), b, pageNumber(r))
	if err != nil {
		pageError(w, r, err)
		return
	}
	writeImage(w, ct, data)
}

// thumbnail is GET /api/v1/books/{id}/thumbnail.
func (h *pageHandlers) thumbnail(w http.ResponseWriter, r *http.Request) {
	b, si, ok := h.book(w, r)
	if !ok {
		return
	}
	data, ct, err := h.s.deps.Reading.BookThumbnail(r.Context(), b, &si.Series)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "no thumbnail")
		return
	}
	writeImage(w, ct, data)
}

// file is GET /api/v1/books/{id}/file: the CBZ of a downloaded chapter
// (KMReader's offline download).
func (h *pageHandlers) file(w http.ResponseWriter, r *http.Request) {
	if !PrincipalFrom(r.Context()).User.Can(access.Download) {
		writeError(w, r, http.StatusForbidden, "your account can't download files")
		return
	}
	b, _, ok := h.book(w, r)
	if !ok {
		return
	}
	f, st, err := h.s.deps.Reading.File(b)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "this chapter isn't downloaded")
		return
	}
	defer f.Close()
	name := filepath.Base(b.Path)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "private, max-age=0")
	w.Header().Set("ETag", fmt.Sprintf(`"%x-%x"`, st.ModTime().Unix(), st.Size()))
	http.ServeContent(w, r, name, st.ModTime(), f)
}

// streamPage sends a page without holding it in memory, with its length and
// an ETag so an app can skip pages it already has.
func streamPage(w http.ResponseWriter, r *http.Request, p *reading.PageContent) {
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if p.ETag != "" {
		w.Header().Set("ETag", p.ETag)
	}
	if !p.ModTime.IsZero() {
		w.Header().Set("Last-Modified", p.ModTime.UTC().Format(http.TimeFormat))
	}
	if reading.ETagMatches(r.Header.Get("If-None-Match"), p.ETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", p.ContentType)
	if p.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(p.Size, 10))
	}
	_, _ = io.Copy(w, p.Body)
}
