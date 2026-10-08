package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Asion001/mangarr/internal/imagedeliver"
	"github.com/Asion001/mangarr/internal/reading"
)

// Signed image links let a CDN in front of mangarr cache covers and
// downloaded pages. Such a link needs no login: its signature is the
// permission, given only to someone who could see the image, so a cache
// may keep it and serve it to anyone who has the link. The key lives in
// the settings store; resetting it ends every link handed out. A link names
// the month it was made in and works through the next one, so links handed
// out stop working on their own within two months.

const (
	keyImageLinks   = "imageLinks"
	imageLinkPeriod = 30 * 24 * time.Hour
	imageLinksPath  = "/api/v1/img/"
)

type imageLinkKey struct {
	Key string `json:"key"`
}

type imageLinks struct {
	mu  sync.Mutex
	key []byte
}

// imageKey returns the signing key, made the first time it's needed.
func (s *Server) imageKey(ctx context.Context) ([]byte, error) {
	s.links.mu.Lock()
	defer s.links.mu.Unlock()
	if s.links.key != nil {
		return s.links.key, nil
	}
	var stored imageLinkKey
	if err := s.app.Settings.Get(ctx, keyImageLinks, &stored); err != nil {
		return nil, err
	}
	key, err := base64.RawURLEncoding.DecodeString(stored.Key)
	if err != nil || len(key) < 32 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := s.app.Settings.Set(ctx, keyImageLinks, imageLinkKey{Key: base64.RawURLEncoding.EncodeToString(key)}); err != nil {
			return nil, err
		}
	}
	s.links.key = key
	return key, nil
}

// resetImageLinks replaces the key, so every signed link stops working.
func (s *Server) resetImageLinks(ctx context.Context) error {
	s.links.mu.Lock()
	defer s.links.mu.Unlock()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	if err := s.app.Settings.Set(ctx, keyImageLinks, imageLinkKey{Key: base64.RawURLEncoding.EncodeToString(key)}); err != nil {
		return err
	}
	s.links.key = key
	return nil
}

// cdnImages reports whether covers and pages get signed links.
func (s *Server) cdnImages(ctx context.Context) bool {
	cfg, err := s.app.Settings.Reading(ctx)
	return err == nil && cfg.CDNImages
}

func imagePeriod(t time.Time) int64 { return t.Unix() / int64(imageLinkPeriod/time.Second) }

// signImage is the link token for what (a cover or a file) in period:
// "<period>.<signature>".
func signImage(key []byte, what string, period int64) string {
	p := strconv.FormatInt(period, 10)
	m := hmac.New(sha256.New, key)
	m.Write([]byte(what + "|" + p))
	return p + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:16])
}

// verifyImage checks a token for what, made this period or the one before.
func verifyImage(key []byte, what, token string, now time.Time) bool {
	p, _, ok := strings.Cut(token, ".")
	period, err := strconv.ParseInt(p, 10, 64)
	if !ok || err != nil {
		return false
	}
	if cur := imagePeriod(now); period != cur && period != cur-1 {
		return false
	}
	return hmac.Equal([]byte(token), []byte(signImage(key, what, period)))
}

func coverSubject(seriesID int64, version string) string {
	return "cover|" + strconv.FormatInt(seriesID, 10) + "|" + version
}

func fileSubject(fileID int64, sha string) string {
	return "file|" + strconv.FormatInt(fileID, 10) + "|" + sha
}

// signedCoverURL is a cover's signed link (relative to the URL base), or ""
// when they are off.
func (s *Server) signedCoverURL(ctx context.Context, seriesID int64, version string) string {
	if !s.cdnImages(ctx) {
		return ""
	}
	key, err := s.imageKey(ctx)
	if err != nil {
		return ""
	}
	return "api/v1/img/c/" + strconv.FormatInt(seriesID, 10) + "/" + version + "/" + signImage(key, coverSubject(seriesID, version), imagePeriod(time.Now()))
}

// signedPagesURL is where a downloaded chapter's signed pages live (add
// "/<n>"), or "" when they are off or the chapter isn't downloaded.
func (s *Server) signedPagesURL(ctx context.Context, b *reading.BookInfo) string {
	if b.File == nil || b.File.SHA256 == "" || b.Path == "" || !s.cdnImages(ctx) {
		return ""
	}
	key, err := s.imageKey(ctx)
	if err != nil {
		return ""
	}
	return "api/v1/img/p/" + strconv.FormatInt(b.File.ID, 10) + "/" + signImage(key, fileSubject(b.File.ID, b.File.SHA256), imagePeriod(time.Now()))
}

// registerImageLinks serves signed links. They are plain routes outside the
// API description: nobody calls them but the links the API hands out.
func (s *Server) registerImageLinks(r chi.Router) {
	r.Get(imageLinksPath+"c/{id}/{v}/{token}", s.serveSignedCover)
	r.Get(imageLinksPath+"p/{file}/{token}/{n}", s.serveSignedPage)
}

func (s *Server) serveSignedCover(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	key, kerr := s.imageKey(ctx)
	if err != nil || kerr != nil || !s.cdnImages(ctx) || !verifyImage(key, coverSubject(id, chi.URLParam(r, "v")), chi.URLParam(r, "token"), time.Now()) {
		http.NotFound(w, r)
		return
	}
	ser, err := s.app.Series.Get(ctx, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, ct, err := s.app.Reading.Cover(ctx, ser)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	out := imageReply(context.WithValue(ctx, ifNoneMatchKey{}, r.Header.Get("If-None-Match")), data, ct, cacheSignedCover)
	writeImage(w, out)
}

func (s *Server) serveSignedPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fileID, ferr := strconv.ParseInt(chi.URLParam(r, "file"), 10, 64)
	n, nerr := strconv.Atoi(chi.URLParam(r, "n"))
	key, kerr := s.imageKey(ctx)
	if ferr != nil || nerr != nil || n < 1 || kerr != nil || !s.cdnImages(ctx) {
		http.NotFound(w, r)
		return
	}
	b, err := s.app.Reading.FileBook(ctx, fileID)
	if err != nil || b.File.SHA256 == "" || !verifyImage(key, fileSubject(fileID, b.File.SHA256), chi.URLParam(r, "token"), time.Now()) {
		http.NotFound(w, r)
		return
	}
	width, _ := strconv.Atoi(r.URL.Query().Get("w"))
	etag := variantETag(s.app.Reading.PageETag(b, n), imagedeliver.Width(width))
	w.Header().Set("Cache-Control", cacheForever) // the link changes with the file
	if reading.ETagMatches(r.Header.Get("If-None-Match"), etag) {
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	page, err := s.app.Reading.PageReader(ctx, b, n)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if small := s.smallerPage(ctx, b, n, page, width); small != nil {
		page = small
	}
	defer page.Body.Close()
	if page.ETag != "" {
		w.Header().Set("ETag", page.ETag)
	}
	w.Header().Set("Content-Type", page.ContentType)
	if page.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(page.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, page.Body)
	}
}

// writeImage sends an imageReply outside huma.
func writeImage(w http.ResponseWriter, out *imageOutput) {
	w.Header().Set("Cache-Control", out.CacheControl)
	w.Header().Set("ETag", out.ETag)
	if out.Status == http.StatusNotModified {
		w.WriteHeader(out.Status)
		return
	}
	w.Header().Set("Content-Type", out.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(out.Body)))
	w.WriteHeader(out.Status)
	_, _ = w.Write(out.Body)
}
