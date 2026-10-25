package komgaapi

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/reading"
)

type catalogHandlers struct{ s *Service }

func (h *catalogHandlers) allSeries(w http.ResponseWriter, r *http.Request) ([]reading.SeriesInfo, int64, bool) {
	rid, ok := h.s.userID(w, r)
	if !ok {
		return nil, 0, false
	}
	all, err := h.s.deps.Reading.AllSeries(r.Context(), rid, 0)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return nil, 0, false
	}
	return all, rid, true
}

// strings answers the string-list endpoints (genres, tags, publishers…).
func (h *catalogHandlers) strings(pick func(reading.SeriesInfo) []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		all, _, ok := h.allSeries(w, r)
		if !ok {
			return
		}
		libs := listParam(r, "library_id")
		seen := map[string]bool{}
		out := []string{}
		for _, si := range all {
			if !anyFold(libs, id(si.Series.RootFolderID)) {
				continue
			}
			for _, v := range pick(si) {
				if k := strings.ToLower(strings.TrimSpace(v)); k != "" && !seen[k] {
					seen[k] = true
					out = append(out, strings.TrimSpace(v))
				}
			}
		}
		sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
		writeJSON(w, http.StatusOK, out)
	}
}

func (h *catalogHandlers) authorList(w http.ResponseWriter, r *http.Request) []authorDTO {
	all, _, ok := h.allSeries(w, r)
	if !ok {
		return nil
	}
	search := strings.ToLower(r.URL.Query().Get("search"))
	role := r.URL.Query().Get("role")
	seen := map[string]bool{}
	out := []authorDTO{}
	for _, si := range all {
		for _, a := range seriesAuthors(si.Series.Metadata) {
			k := a.Role + "\x00" + strings.ToLower(a.Name)
			if seen[k] || (search != "" && !strings.Contains(strings.ToLower(a.Name), search)) || (role != "" && role != a.Role) {
				continue
			}
			seen[k] = true
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func (h *catalogHandlers) authorsV1(w http.ResponseWriter, r *http.Request) {
	if out := h.authorList(w, r); out != nil {
		writeJSON(w, http.StatusOK, out)
	}
}

func (h *catalogHandlers) authorsV2(w http.ResponseWriter, r *http.Request) {
	if out := h.authorList(w, r); out != nil {
		writeJSON(w, http.StatusOK, paginate(out, parsePage(r, 20)))
	}
}

// collections are mangarr's tags.
func (h *catalogHandlers) collectionList(w http.ResponseWriter, r *http.Request) ([]collectionDTO, bool) {
	all, _, ok := h.allSeries(w, r)
	if !ok {
		return nil, false
	}
	var tags []model.Tag
	if err := h.s.deps.DB.NewSelect().Model(&tags).Order("label").Scan(r.Context()); err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	now := komgaTime(time.Now())
	out := make([]collectionDTO, 0, len(tags))
	for _, t := range tags {
		c := collectionDTO{ID: id(t.ID), Name: t.Label, SeriesIDs: []string{}, CreatedDate: now, LastModifiedDate: now}
		for _, si := range all {
			for _, tid := range si.Series.Tags {
				if tid == t.ID {
					c.SeriesIDs = append(c.SeriesIDs, id(si.Series.ID))
				}
			}
		}
		if len(c.SeriesIDs) > 0 {
			out = append(out, c)
		}
	}
	return out, true
}

func (h *catalogHandlers) collections(w http.ResponseWriter, r *http.Request) {
	list, ok := h.collectionList(w, r)
	if !ok {
		return
	}
	if q := r.URL.Query().Get("search"); q != "" {
		kept := list[:0]
		for _, c := range list {
			if searchMatches(q, c.Name) {
				kept = append(kept, c)
			}
		}
		list = kept
	}
	writeJSON(w, http.StatusOK, paginate(list, parsePage(r, 20)))
}

func (h *catalogHandlers) collection(w http.ResponseWriter, r *http.Request) {
	list, ok := h.collectionList(w, r)
	if !ok {
		return
	}
	for _, c := range list {
		if c.ID == chi.URLParam(r, "id") {
			writeJSON(w, http.StatusOK, c)
			return
		}
	}
	writeError(w, r, http.StatusNotFound, "collection not found")
}

func (h *catalogHandlers) collectionSeries(w http.ResponseWriter, r *http.Request) {
	f := seriesFilterFrom(r)
	f.Collections = []string{chi.URLParam(r, "id")}
	p := parsePage(r, 20)
	if len(p.Sort) == 0 {
		p.Sort = []string{"metadata.titleSort,asc"}
	}
	(&seriesHandlers{h.s}).respond(w, r, f, p)
}

func (h *catalogHandlers) seriesCollections(w http.ResponseWriter, r *http.Request) {
	list, ok := h.collectionList(w, r)
	if !ok {
		return
	}
	sid := chi.URLParam(r, "id")
	out := []collectionDTO{}
	for _, c := range list {
		for _, s := range c.SeriesIDs {
			if s == sid {
				out = append(out, c)
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ContinueReadingID is the id of the "Continue reading" read list.
const ContinueReadingID = "continue-reading"

func (h *catalogHandlers) readList(w http.ResponseWriter, r *http.Request) (readListDTO, bool) {
	rid, ok := h.s.userID(w, r)
	if !ok {
		return readListDTO{}, false
	}
	next, err := h.s.deps.Reading.OnDeck(r.Context(), rid)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return readListDTO{}, false
	}
	now := komgaTime(time.Now())
	rl := readListDTO{ID: ContinueReadingID, Name: "Continue reading", Summary: "The next chapter of every series you're reading",
		Ordered: true, BookIDs: []string{}, CreatedDate: now, LastModifiedDate: now}
	for _, n := range next {
		rl.BookIDs = append(rl.BookIDs, id(n.Book.Chapter.ID))
	}
	return rl, true
}

func (h *catalogHandlers) readLists(w http.ResponseWriter, r *http.Request) {
	rl, ok := h.readList(w, r)
	if !ok {
		return
	}
	list := []readListDTO{}
	if len(rl.BookIDs) > 0 {
		list = append(list, rl)
	}
	writeJSON(w, http.StatusOK, paginate(list, parsePage(r, 20)))
}

func (h *catalogHandlers) readListGet(w http.ResponseWriter, r *http.Request) {
	if chi.URLParam(r, "id") != ContinueReadingID {
		writeError(w, r, http.StatusNotFound, "read list not found")
		return
	}
	if rl, ok := h.readList(w, r); ok {
		writeJSON(w, http.StatusOK, rl)
	}
}

func (h *catalogHandlers) readListBooks(w http.ResponseWriter, r *http.Request) {
	if chi.URLParam(r, "id") != ContinueReadingID {
		writeError(w, r, http.StatusNotFound, "read list not found")
		return
	}
	rid, ok := h.s.userID(w, r)
	if !ok {
		return
	}
	next, err := h.s.deps.Reading.OnDeck(r.Context(), rid)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	dtos := make([]bookDTO, 0, len(next))
	for _, n := range next {
		dtos = append(dtos, toBook(n.Book, &n.Series.Series, n.Series.Dir, h.s.pageCount(n.Book)))
	}
	writeJSON(w, http.StatusOK, paginate(dtos, parsePage(r, 20)))
}

func (h *catalogHandlers) bookReadLists(w http.ResponseWriter, r *http.Request) {
	rl, ok := h.readList(w, r)
	if !ok {
		return
	}
	out := []readListDTO{}
	for _, b := range rl.BookIDs {
		if b == chi.URLParam(r, "id") {
			out = append(out, rl)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func emptyPage(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, paginate([]struct{}{}, parsePage(r, 20)))
}

func emptyList(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, []string{}) }
