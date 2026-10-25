package komgaapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Asion001/mangarr/internal/reading"
)

type seriesHandlers struct{ s *Service }

// listBody is the POST /…/list search body (Komga 1.19+).
type listBody struct {
	Condition      *condition `json:"condition"`
	FullTextSearch string     `json:"fullTextSearch"`
}

// userID is the caller's account (whose progress it is).
func (s *Service) userID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if u := PrincipalFrom(r.Context()).User; u != nil && u.UserID > 0 {
		return u.UserID, true
	}
	rid, err := s.deps.Reading.DefaultUserID(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return 0, false
	}
	return rid, true
}

func (h *seriesHandlers) respond(w http.ResponseWriter, r *http.Request, f seriesFilter, p pageReq) {
	rid, ok := h.s.userID(w, r)
	if !ok {
		return
	}
	all, err := h.s.deps.Reading.AllSeries(r.Context(), rid, 0)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	matched := make([]reading.SeriesInfo, 0, len(all))
	for _, si := range all {
		if f.match(si) {
			matched = append(matched, si)
		}
	}
	sortSeries(matched, p.Sort)
	pg := paginate(matched, p)
	out := pageDTO[seriesDTO]{Content: make([]seriesDTO, 0, len(pg.Content))}
	for _, si := range pg.Content {
		out.Content = append(out.Content, toSeries(si))
	}
	copyPage(&out, pg)
	writeJSON(w, http.StatusOK, out)
}

// copyPage copies paging fields from one page to another of a different type.
func copyPage[A, B any](dst *pageDTO[A], src pageDTO[B]) {
	dst.Pageable, dst.TotalElements, dst.TotalPages, dst.Last, dst.Size = src.Pageable, src.TotalElements, src.TotalPages, src.Last, src.Size
	dst.Number, dst.Sort, dst.NumberOfElements, dst.First, dst.Empty = src.Number, src.Sort, len(dst.Content), src.First, len(dst.Content) == 0
}

// list is GET /api/v1/series (Mihon's extension, Paperback 0.8).
func (h *seriesHandlers) list(w http.ResponseWriter, r *http.Request) {
	p := parsePage(r, 20)
	if len(p.Sort) == 0 {
		p.Sort = []string{"metadata.titleSort,asc"}
	}
	h.respond(w, r, seriesFilterFrom(r), p)
}

// search is POST /api/v1/series/list (KMReader, Paperback 0.9).
func (h *seriesHandlers) search(w http.ResponseWriter, r *http.Request) {
	var body listBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	f := seriesFilterFrom(r)
	f.Cond, f.Search = body.Condition, body.FullTextSearch
	p := parsePage(r, 20)
	if len(p.Sort) == 0 {
		p.Sort = []string{"metadata.titleSort,asc"}
	}
	h.respond(w, r, f, p)
}

func (h *seriesHandlers) newest(w http.ResponseWriter, r *http.Request) {
	p := parsePage(r, 20)
	p.Sort = []string{"created,desc"}
	h.respond(w, r, seriesFilterFrom(r), p)
}

func (h *seriesHandlers) updated(w http.ResponseWriter, r *http.Request) {
	p := parsePage(r, 20)
	p.Sort = []string{"lastModified,desc"}
	h.respond(w, r, seriesFilterFrom(r), p)
}

func pathID(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return n
}

func (h *seriesHandlers) get(w http.ResponseWriter, r *http.Request) {
	rid, ok := h.s.userID(w, r)
	if !ok {
		return
	}
	si, err := h.s.deps.Reading.Series(r.Context(), rid, pathID(r, "id"))
	if err != nil {
		notFoundOr500(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toSeries(*si))
}

func notFoundOr500(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, reading.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not found")
		return
	}
	writeError(w, r, http.StatusInternalServerError, err.Error())
}

func (h *seriesHandlers) thumbnail(w http.ResponseWriter, r *http.Request) {
	rid, ok := h.s.userID(w, r)
	if !ok {
		return
	}
	si, err := h.s.deps.Reading.Series(r.Context(), rid, pathID(r, "id"))
	if err != nil {
		notFoundOr500(w, r, err)
		return
	}
	data, ct, err := h.s.deps.Reading.Cover(r.Context(), &si.Series)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "no cover")
		return
	}
	writeImage(w, ct, data)
}

func writeImage(w http.ResponseWriter, ct string, data []byte) {
	if ct == "" {
		ct = http.DetectContentType(data)
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data)
}
