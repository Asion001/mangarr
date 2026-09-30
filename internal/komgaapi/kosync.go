package komgaapi

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/reading"
)

type koSyncHandlers struct{ s *Service }

func (h *koSyncHandlers) create(w http.ResponseWriter, _ *http.Request) {
	koJSON(w, http.StatusForbidden, map[string]any{"message": "mangarr accounts are created by an administrator"})
}

func (h *koSyncHandlers) authenticate(r *http.Request) (*access.Principal, bool) {
	username := strings.TrimSpace(r.Header.Get("X-Auth-User"))
	key := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Auth-Key")))
	if username == "" || key == "" {
		return nil, false
	}
	key = HashKey(key)
	var u model.User
	if err := h.s.deps.DB.NewSelect().Model(&u).Where("LOWER(username) = LOWER(?)", username).Where("disabled = ?", false).Scan(r.Context()); err != nil {
		return nil, false
	}
	var all []model.ReadingKey
	if err := h.s.deps.DB.NewSelect().Model(&all).Where("user_id = ?", u.ID).Where("koreader_hash <> ''").Scan(r.Context()); err != nil {
		return nil, false
	}
	matched := 0
	for _, rk := range all {
		if subtle.ConstantTimeCompare([]byte(rk.KOReaderHash), []byte(key)) == 1 {
			matched = 1
		}
	}
	if matched != 1 {
		return nil, false
	}
	p, err := h.s.deps.Auth.UserPrincipal(r.Context(), u.ID)
	if err != nil || p == nil || !p.Can(access.Apps) {
		return nil, false
	}
	return p, true
}

func koJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/vnd.koreader.v1+json;charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *koSyncHandlers) auth(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authenticate(r); !ok {
		koJSON(w, http.StatusUnauthorized, map[string]any{"message": "invalid credentials"})
		return
	}
	koJSON(w, http.StatusOK, map[string]any{"authorized": true})
}

type koProgressUpdate struct {
	Document   string          `json:"document"`
	Progress   json.RawMessage `json:"progress"`
	Percentage float64         `json:"percentage"`
	Device     string          `json:"device"`
	DeviceID   string          `json:"device_id"`
	Metadata   json.RawMessage `json:"metadata"`
}

func progressValue(raw json.RawMessage) (string, int) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", 0
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		n, _ := strconv.Atoi(strings.TrimSpace(s))
		return s, n
	}
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return strconv.Itoa(n), n
	}
	return "", 0
}

func (h *koSyncHandlers) putProgress(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(r)
	if !ok {
		koJSON(w, http.StatusUnauthorized, map[string]any{"message": "invalid credentials"})
		return
	}
	r = r.WithContext(access.With(r.Context(), p))
	var update koProgressUpdate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&update); err != nil || len(update.Document) != 32 {
		koJSON(w, http.StatusBadRequest, map[string]any{"message": "invalid progress"})
		return
	}
	readerID := p.ReaderID
	var doc model.KOReaderDocument
	if err := h.s.deps.DB.NewSelect().Model(&doc).Where("reader_id = ? AND document = ?", readerID, strings.ToLower(update.Document)).Scan(r.Context()); err != nil {
		koJSON(w, http.StatusNotFound, map[string]any{"message": "unknown document"})
		return
	}
	b, err := h.s.deps.Reading.Book(r.Context(), readerID, doc.ChapterID)
	if err != nil {
		koJSON(w, http.StatusNotFound, map[string]any{"message": "unknown document"})
		return
	}
	progress, page := progressValue(update.Progress)
	if page < 0 {
		koJSON(w, http.StatusBadRequest, map[string]any{"message": "invalid progress"})
		return
	}
	completed := update.Percentage >= 1
	if page == 0 && update.Percentage > 0 {
		page = int(update.Percentage * float64(h.s.deps.Reading.CachedPageCount(doc.ChapterID)))
	}
	_, err = h.s.deps.Reading.Record(r.Context(), readerID, []reading.Change{{ChapterID: b.Chapter.ID, SeriesID: b.EditionID, Completed: completed, Page: page}}, reading.By{Origin: model.EventOriginApp, Client: "KOReader", Device: update.Device})
	if err != nil {
		koJSON(w, http.StatusInternalServerError, map[string]any{"message": "couldn't save progress"})
		return
	}
	_ = progress
	koJSON(w, http.StatusOK, map[string]any{"updated": true})
}

func (h *koSyncHandlers) getProgress(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(r)
	if !ok {
		koJSON(w, http.StatusUnauthorized, map[string]any{"message": "invalid credentials"})
		return
	}
	r = r.WithContext(access.With(r.Context(), p))
	document := strings.ToLower(chi.URLParam(r, "document"))
	if len(document) != 32 {
		koJSON(w, http.StatusBadRequest, map[string]any{"message": "invalid document"})
		return
	}
	var doc model.KOReaderDocument
	if err := h.s.deps.DB.NewSelect().Model(&doc).Where("reader_id = ? AND document = ?", p.ReaderID, document).Scan(r.Context()); err != nil {
		koJSON(w, http.StatusOK, map[string]any{"document": document})
		return
	}
	b, err := h.s.deps.Reading.Book(r.Context(), p.ReaderID, doc.ChapterID)
	if err != nil {
		koJSON(w, http.StatusOK, map[string]any{"document": document})
		return
	}
	if b.State == nil {
		koJSON(w, http.StatusOK, map[string]any{"document": document})
		return
	}
	page := b.State.Page
	if b.State.Completed && page == 0 {
		if b.File != nil {
			page = max(b.File.PageCount, 1)
		} else {
			page = 1
		}
	}
	pages := 0
	if b.File != nil {
		pages = b.File.PageCount
	}
	if pages < 1 {
		pages = h.s.deps.Reading.CachedPageCount(doc.ChapterID)
	}
	percentage := float64(0)
	if b.State.Completed {
		percentage = 1
	} else if pages > 0 {
		percentage = float64(page) / float64(pages)
	}
	koJSON(w, http.StatusOK, map[string]any{"document": document, "progress": strconv.Itoa(page), "percentage": percentage, "device": "mangarr", "device_id": "mangarr", "timestamp": time.Now().Unix()})
}
