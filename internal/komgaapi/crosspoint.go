package komgaapi

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Asion001/mangarr/internal/reading"
)

// The CrossPoint Reader plugin's catalog (/crosspoint/v1). CrossPoint's
// on-device catalog draws text rows (a title and a subtitle) from small JSON
// pages and downloads each row's file, so every row is one chapter: the
// chapter the reader should open next in its title.

type crosspointHandlers struct{ s *Service }

// crosspointItem is one catalog row.
type crosspointItem struct {
	// ID is the chapter's id: the download and the progress events use it.
	ID int64 `json:"id"`
	// Title names the title and chapter; it is also the file's name.
	Title string `json:"title"`
	// Subtitle is the row's second line: where the reader is in the title.
	Subtitle    string `json:"subtitle"`
	Description string `json:"description,omitempty"`
}

type crosspointPage struct {
	Items []crosspointItem `json:"items"`
}

// crosspointMaxRows bounds a page: the device shows at most 16 rows.
const crosspointMaxRows = 16

func (s *Service) crosspointRoutes(r chi.Router) {
	h := &crosspointHandlers{s}
	r.Get("/crosspoint/v1/chapters", h.list)
	r.Get("/crosspoint/v1/chapters/{id}/xtch", (&opdsHandlers{s}).chapterXTCH)
}

// webtoon reports whether a title reads as one long strip, which reads badly
// on a small e-ink screen.
func webtoon(si reading.SeriesInfo) bool {
	d := strings.ToLower(si.Series.ReadingDirection)
	return d == "webtoon" || d == "vertical"
}

// list is GET /crosspoint/v1/chapters?list=continue|recent|library|search
// &q=&page=&limit=&webtoons=1. Titles that read as webtoons are left out
// unless webtoons is 1 or true.
func (h *crosspointHandlers) list(w http.ResponseWriter, r *http.Request) {
	rid, ok := h.s.userID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	page = max(page, 1)
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > crosspointMaxRows {
		limit = 8
	}
	withWebtoons := q.Get("webtoons") == "1" || strings.EqualFold(q.Get("webtoons"), "true")
	keep := func(si reading.SeriesInfo) bool { return withWebtoons || !webtoon(si) }

	ctx := r.Context()
	var items []crosspointItem
	switch list := q.Get("list"); list {
	case "", "continue":
		deck, err := h.s.deps.Reading.OnDeck(ctx, rid)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		var rows []reading.NextUp
		for _, n := range deck {
			if keep(n.Series) && n.Book.Path != "" {
				rows = append(rows, n)
			}
		}
		for _, n := range pageOf(rows, page, limit) {
			items = append(items, crosspointRow(n.Series, n.Book))
		}
	case "recent":
		all, err := h.s.deps.Reading.AllSeries(ctx, rid, 0)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		bySeries := map[int64]reading.SeriesInfo{}
		for _, si := range all {
			if keep(si) {
				bySeries[si.Series.ID] = si
			}
		}
		books, err := h.s.deps.Reading.Books(ctx, rid, 0)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		var rows []reading.BookInfo
		for _, b := range books {
			if _, ok := bySeries[b.Chapter.SeriesID]; ok && b.Path != "" {
				rows = append(rows, b)
			}
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Chapter.FirstSeenAt.After(rows[j].Chapter.FirstSeenAt) })
		for _, b := range pageOf(rows, page, limit) {
			items = append(items, crosspointRow(bySeries[b.Chapter.SeriesID], b))
		}
	case "library", "search":
		all, err := h.s.deps.Reading.AllSeries(ctx, rid, 0)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		term := strings.ToLower(strings.TrimSpace(q.Get("q")))
		var rows []reading.SeriesInfo
		for _, si := range all {
			if !keep(si) || si.Books == 0 {
				continue
			}
			if list == "search" && (term == "" || !strings.Contains(strings.ToLower(si.Series.Title), term) && !strings.Contains(strings.ToLower(si.Series.SortTitle), term)) {
				continue
			}
			rows = append(rows, si)
		}
		// Only this page's titles load their chapters.
		for _, si := range pageOf(rows, page, limit) {
			books, err := h.s.deps.Reading.Books(ctx, rid, si.Series.ID)
			if err != nil {
				writeError(w, r, http.StatusInternalServerError, err.Error())
				return
			}
			if b, ok := nextChapter(books); ok {
				items = append(items, crosspointRow(si, b))
			}
		}
	default:
		writeError(w, r, http.StatusBadRequest, "list is continue, recent, library or search")
		return
	}
	if items == nil {
		items = []crosspointItem{}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, crosspointPage{Items: items})
}

// pageOf is the 1-based page of list.
func pageOf[T any](list []T, page, limit int) []T {
	start := (page - 1) * limit
	if start >= len(list) {
		return nil
	}
	return list[start:min(start+limit, len(list))]
}

// nextChapter is the downloaded chapter to open next: the first unread one
// after the last one read, else (all read) the last one.
func nextChapter(books []reading.BookInfo) (reading.BookInfo, bool) {
	last := -1
	for i, b := range books {
		if b.State != nil && b.State.Completed {
			last = i
		}
	}
	for i := last + 1; i < len(books); i++ {
		if books[i].Path != "" && (books[i].State == nil || !books[i].State.Completed) {
			return books[i], true
		}
	}
	for i := len(books) - 1; i >= 0; i-- {
		if books[i].Path != "" {
			return books[i], true
		}
	}
	return reading.BookInfo{}, false
}

// crosspointRow is a chapter's row: "Title · Ch. 46" over
// "45 of 120 read · 3 new · Ongoing".
func crosspointRow(si reading.SeriesInfo, b reading.BookInfo) crosspointItem {
	title := si.Series.Title
	if title == "" {
		title = "Untitled"
	}
	name := title + " · Ch. " + b.Chapter.NumberKey
	parts := []string{fmt.Sprintf("%d of %d read", si.Read, si.Books)}
	if n := si.Unread(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d new", n))
	}
	if st := si.Series.Status; st != "" && st != "unknown" {
		parts = append(parts, strings.ToUpper(st[:1])+st[1:])
	}
	desc := ""
	if t := strings.TrimSpace(b.Chapter.Title); t != "" && t != "Chapter "+b.Chapter.NumberKey {
		desc = t + "\n\n"
	}
	desc += si.Series.Metadata.Description
	return crosspointItem{ID: b.Chapter.ID, Title: name, Subtitle: strings.Join(parts, " · "), Description: strings.TrimSpace(desc)}
}
