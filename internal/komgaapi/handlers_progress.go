package komgaapi

import (
	"encoding/json"
	"net/http"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/reading"
)

type progressHandlers struct{ s *Service }

// by is who a progress update is from.
func by(r *http.Request) reading.By {
	p := PrincipalFrom(r.Context())
	return reading.By{Origin: model.EventOriginApp, Client: p.Client, Device: p.Device}
}

// patchBook is PATCH /api/v1/books/{id}/read-progress {page, completed}.
// KMReader sends one per page turn with a 1 s timeout, so this is one
// select and one write.
func (h *progressHandlers) patchBook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Page      *int  `json:"page"`
		Completed *bool `json:"completed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (body.Page == nil && body.Completed == nil) {
		writeError(w, r, http.StatusBadRequest, "page or completed is required")
		return
	}
	rid, ok := h.s.userID(w, r)
	if !ok {
		return
	}
	ch, pages, err := h.s.deps.Reading.ChapterPages(r.Context(), pathID(r, "id"))
	if err != nil {
		notFoundOr500(w, r, err)
		return
	}
	c := reading.Change{ChapterID: ch.ID, SeriesID: ch.SeriesID}
	if body.Page != nil {
		c.Page = *body.Page
	}
	if body.Completed != nil {
		c.Completed = *body.Completed
	}
	if pages > 0 && c.Page >= pages {
		c.Completed = true // the last page, as Komga does
	}
	if c.Completed && c.Page <= 0 {
		c.Page = pages
	}
	if _, err := h.s.deps.Reading.Record(r.Context(), rid, []reading.Change{c}, by(r)); err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteBook is DELETE /api/v1/books/{id}/read-progress (mark unread).
func (h *progressHandlers) deleteBook(w http.ResponseWriter, r *http.Request) {
	rid, ok := h.s.userID(w, r)
	if !ok {
		return
	}
	ch, _, err := h.s.deps.Reading.ChapterPages(r.Context(), pathID(r, "id"))
	if err != nil {
		notFoundOr500(w, r, err)
		return
	}
	c := reading.Change{ChapterID: ch.ID, SeriesID: ch.SeriesID, Unread: true}
	if _, err := h.s.deps.Reading.Record(r.Context(), rid, []reading.Change{c}, by(r)); err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// markSeries is POST (read) and DELETE (unread) /api/v1/series/{id}/read-progress.
func (h *progressHandlers) markSeries(read bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rid, ok := h.s.userID(w, r)
		if !ok {
			return
		}
		if _, err := h.s.deps.Reading.MarkSeries(r.Context(), rid, pathID(r, "id"), read, by(r)); err != nil {
			notFoundOr500(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// tachiyomiV2 is GET /api/v2/series/{id}/read-progress/tachiyomi (Mihon's
// Komga tracker, Paperback 0.9).
func (h *progressHandlers) tachiyomiV2(w http.ResponseWriter, r *http.Request) {
	rid, ok := h.s.userID(w, r)
	if !ok {
		return
	}
	p, err := h.s.deps.Reading.Progress(r.Context(), rid, pathID(r, "id"))
	if err != nil {
		notFoundOr500(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tachiyomiProgressDTO{BooksCount: p.Books, BooksReadCount: p.Read, BooksUnreadCount: p.Unread,
		BooksInProgressCount: p.InProgress, LastReadContinuousNumberSort: p.LastReadContinuous, MaxNumberSort: p.MaxNumber})
}

// putTachiyomiV2 is PUT /api/v2/series/{id}/read-progress/tachiyomi
// {lastBookNumberSortRead}: every chapter up to it is read.
func (h *progressHandlers) putTachiyomiV2(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LastBookNumberSortRead *float64 `json:"lastBookNumberSortRead"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.LastBookNumberSortRead == nil {
		writeError(w, r, http.StatusBadRequest, "lastBookNumberSortRead is required")
		return
	}
	rid, ok := h.s.userID(w, r)
	if !ok {
		return
	}
	if _, err := h.s.deps.Reading.MarkReadUpTo(r.Context(), rid, pathID(r, "id"), *body.LastBookNumberSortRead, by(r)); err != nil {
		notFoundOr500(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// tachiyomiV1 is the index-based GET /api/v1/series/{id}/read-progress/tachiyomi
// (older Mihon and Tachiyomi forks).
func (h *progressHandlers) tachiyomiV1(w http.ResponseWriter, r *http.Request) {
	rid, ok := h.s.userID(w, r)
	if !ok {
		return
	}
	p, err := h.s.deps.Reading.Progress(r.Context(), rid, pathID(r, "id"))
	if err != nil {
		notFoundOr500(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"booksCount": p.Books, "booksReadCount": p.Read, "booksUnreadCount": p.Unread,
		"booksInProgressCount": p.InProgress, "lastReadContinuousIndex": p.LastReadContinuousIndex})
}

// putTachiyomiV1 is PUT /api/v1/series/{id}/read-progress/tachiyomi {lastBookRead}.
func (h *progressHandlers) putTachiyomiV1(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LastBookRead *int `json:"lastBookRead"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.LastBookRead == nil {
		writeError(w, r, http.StatusBadRequest, "lastBookRead is required")
		return
	}
	rid, ok := h.s.userID(w, r)
	if !ok {
		return
	}
	if _, err := h.s.deps.Reading.MarkReadUpToIndex(r.Context(), rid, pathID(r, "id"), *body.LastBookRead, by(r)); err != nil {
		notFoundOr500(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
