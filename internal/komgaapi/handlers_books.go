package komgaapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Asion001/mangarr/internal/reading"
)

type bookHandlers struct{ s *Service }

// seriesIndex loads the series books are mapped against.
type seriesIndex struct {
	byID map[int64]reading.SeriesInfo
}

func (h *bookHandlers) index(r *http.Request, rid int64) (seriesIndex, error) {
	all, err := h.s.deps.Reading.AllSeries(r.Context(), rid, 0)
	if err != nil {
		return seriesIndex{}, err
	}
	idx := seriesIndex{byID: make(map[int64]reading.SeriesInfo, len(all))}
	for _, si := range all {
		idx.byID[si.Series.ID] = si
	}
	return idx, nil
}

func (h *bookHandlers) dto(b reading.BookInfo, idx seriesIndex) bookDTO {
	si := idx.byID[b.Chapter.SeriesID]
	return toBook(b, &si.Series, si.Dir, h.s.pageCount(b))
}

// pageCount is the page count known for a book (0 until it's known).
func (s *Service) pageCount(b reading.BookInfo) int {
	if b.File != nil {
		return b.File.PageCount
	}
	return s.deps.Reading.CachedPageCount(b.Chapter.ID)
}

// respond filters, sorts and pages books (seriesID 0: all series).
func (h *bookHandlers) respond(w http.ResponseWriter, r *http.Request, seriesID int64, f bookFilter, p pageReq) {
	rid, ok := h.s.userID(w, r)
	if !ok {
		return
	}
	idx, err := h.index(r, rid)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	books, err := h.s.deps.Reading.Books(r.Context(), rid, seriesID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	f.seriesLib = map[int64]int64{}
	titles := map[int64]string{}
	for sid, si := range idx.byID {
		f.seriesLib[sid], titles[sid] = si.Series.RootFolderID, si.Series.SortTitle
	}
	if len(f.ReadListIDs) > 0 || f.Cond != nil {
		f.readList = h.s.continueReadingSet(r, rid)
	}
	matched := make([]reading.BookInfo, 0, len(books))
	for _, b := range books {
		if f.match(b) {
			matched = append(matched, b)
		}
	}
	sortBooks(matched, p.Sort, titles)
	pg := paginate(matched, p)
	out := pageDTO[bookDTO]{Content: make([]bookDTO, 0, len(pg.Content))}
	for _, b := range pg.Content {
		out.Content = append(out.Content, h.dto(b, idx))
	}
	copyPage(&out, pg)
	writeJSON(w, http.StatusOK, out)
}

// seriesBooks is GET /api/v1/series/{id}/books (Mihon, Paperback 0.8).
func (h *bookHandlers) seriesBooks(w http.ResponseWriter, r *http.Request) {
	p := parsePage(r, 20)
	if len(p.Sort) == 0 {
		p.Sort = []string{"metadata.numberSort,asc"}
	}
	h.respond(w, r, pathID(r, "id"), bookFilterFrom(r), p)
}

// list is GET /api/v1/books (Paperback 0.8's "continue reading").
func (h *bookHandlers) list(w http.ResponseWriter, r *http.Request) {
	p := parsePage(r, 20)
	if len(p.Sort) == 0 {
		p.Sort = []string{"series,metadata.numberSort"}
	}
	h.respond(w, r, 0, bookFilterFrom(r), p)
}

// search is POST /api/v1/books/list (KMReader, Paperback 0.9).
func (h *bookHandlers) search(w http.ResponseWriter, r *http.Request) {
	var body listBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	f := bookFilterFrom(r)
	f.Cond, f.Search = body.Condition, body.FullTextSearch
	p := parsePage(r, 20)
	if len(p.Sort) == 0 {
		p.Sort = []string{"series,metadata.numberSort"}
	}
	// a single series (the common case) loads only its chapters
	var seriesID int64
	if body.Condition != nil {
		seriesID = singleSeries(body.Condition)
	}
	h.respond(w, r, seriesID, f, p)
}

// singleSeries finds a top-level "seriesId is X" condition.
func singleSeries(c *condition) int64 {
	find := func(fields []fieldCond) int64 {
		for _, f := range fields {
			if f.Field == "seriesId" && f.Operator == "is" {
				n, _ := strconv.ParseInt(f.str(), 10, 64)
				return n
			}
		}
		return 0
	}
	if n := find(c.Fields); n > 0 {
		return n
	}
	for i := range c.AllOf {
		if n := find(c.AllOf[i].Fields); n > 0 {
			return n
		}
	}
	return 0
}

func (h *bookHandlers) get(w http.ResponseWriter, r *http.Request) {
	rid, ok := h.s.userID(w, r)
	if !ok {
		return
	}
	b, err := h.s.deps.Reading.Book(r.Context(), rid, pathID(r, "id"))
	if err != nil {
		notFoundOr500(w, r, err)
		return
	}
	si, err := h.s.deps.Reading.Series(r.Context(), rid, b.Chapter.SeriesID)
	if err != nil {
		notFoundOr500(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toBook(*b, &si.Series, si.Dir, h.s.pageCount(*b)))
}

// sibling is /books/{id}/next and /previous (404 at either end).
func (h *bookHandlers) sibling(step int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rid, ok := h.s.userID(w, r)
		if !ok {
			return
		}
		b, err := h.s.deps.Reading.Book(r.Context(), rid, pathID(r, "id"))
		if err != nil {
			notFoundOr500(w, r, err)
			return
		}
		books, err := h.s.deps.Reading.Books(r.Context(), rid, b.Chapter.SeriesID)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		for i := range books {
			if books[i].Chapter.ID == b.Chapter.ID {
				j := i + step
				if j < 0 || j >= len(books) {
					break
				}
				si, _ := h.s.deps.Reading.Series(r.Context(), rid, b.Chapter.SeriesID)
				writeJSON(w, http.StatusOK, toBook(books[j], &si.Series, si.Dir, h.s.pageCount(books[j])))
				return
			}
		}
		writeError(w, r, http.StatusNotFound, "no book")
	}
}

// onDeck is GET /api/v1/books/ondeck: the next chapter of each series in progress.
func (h *bookHandlers) onDeck(w http.ResponseWriter, r *http.Request) {
	rid, ok := h.s.userID(w, r)
	if !ok {
		return
	}
	next, err := h.s.deps.Reading.OnDeck(r.Context(), rid)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	libs := listParam(r, "library_id")
	dtos := make([]bookDTO, 0, len(next))
	for _, n := range next {
		if anyFold(libs, id(n.Series.Series.RootFolderID)) {
			dtos = append(dtos, toBook(n.Book, &n.Series.Series, n.Series.Dir, h.s.pageCount(n.Book)))
		}
	}
	writeJSON(w, http.StatusOK, paginate(dtos, parsePage(r, 20)))
}

// continueReadingSet is the chapters of the "Continue reading" read list.
func (s *Service) continueReadingSet(r *http.Request, rid int64) map[int64]bool {
	next, err := s.deps.Reading.OnDeck(r.Context(), rid)
	out := map[int64]bool{}
	if err != nil {
		return out
	}
	for _, n := range next {
		out[n.Book.Chapter.ID] = true
	}
	return out
}
