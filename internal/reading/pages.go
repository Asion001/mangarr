package reading

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/gen2brain/avif" // decoders for convert
	_ "golang.org/x/image/webp"
	"golang.org/x/sync/singleflight"

	"github.com/Asion001/mangarr/internal/apitiming"
	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/imagecheck"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/sourcepriority"
)

// ErrNoSource means a chapter isn't downloaded and no enabled source has it.
var ErrNoSource = errors.New("chapter isn't downloaded and no enabled source has it")

// Grabber queues downloads (the download searcher).
type Grabber interface {
	Evaluate(ctx context.Context, seriesID int64, chapterIDs []int64, explicit bool) (int, error)
	// EvaluateAt is Evaluate with a queue priority (higher runs first).
	EvaluateAt(ctx context.Context, seriesID int64, chapterIDs []int64, explicit bool, priority int) (int, error)
}

// PageInfo is one page of a book.
type PageInfo struct {
	Number    int // 1-based
	FileName  string
	MediaType string
	Size      int64 // 0 when unknown
}

const (
	pageListTTL = 30 * time.Minute
	// streamed pages don't change; the cache cap keeps them in check
	pageTTL = 7 * 24 * time.Hour
	// PagesBucket is the image cache bucket of streamed pages.
	PagesBucket = "pages"
)

// stream is the page list of a chapter read from a source.
type stream struct {
	mod     source.Module
	release int64
	pages   []source.Page
	exp     time.Time
}

type streams struct {
	mu sync.Mutex
	m  map[int64]*stream
	sf singleflight.Group
}

func (st *streams) get(chapterID int64) *stream {
	st.mu.Lock()
	defer st.mu.Unlock()
	if e := st.m[chapterID]; e != nil && time.Now().Before(e.exp) {
		return e
	}
	return nil
}

func (st *streams) put(chapterID int64, e *stream) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.m == nil {
		st.m = map[int64]*stream{}
	}
	now := time.Now()
	for k, v := range st.m {
		if now.After(v.exp) {
			delete(st.m, k)
		}
	}
	st.m[chapterID] = e
}

// CachedPageCount is the page count of an undownloaded chapter whose page
// list is cached (0 when it isn't known yet).
func (s *Service) CachedPageCount(chapterID int64) int {
	if e := s.streams.get(chapterID); e != nil {
		return len(e.pages)
	}
	return 0
}

// Pages lists a book's pages: from its CBZ, else from the best source (which
// also queues the chapter's download when downloadOnOpen is on).
func (s *Service) Pages(ctx context.Context, b *BookInfo) ([]PageInfo, error) {
	if b.Path != "" {
		if entries, err := cbz.Entries(b.Path); err == nil {
			out := make([]PageInfo, len(entries))
			for i, e := range entries {
				out[i] = PageInfo{Number: i + 1, FileName: e.Name, MediaType: mediaType(e.Name), Size: e.Size}
			}
			return out, nil
		}
	}
	st, err := s.stream(ctx, b)
	if err != nil {
		return nil, err
	}
	out := make([]PageInfo, len(st.pages))
	for i, p := range st.pages {
		name := cbz.PageName(i, pageExt(p.URL))
		out[i] = PageInfo{Number: i + 1, FileName: name, MediaType: mediaType(name)}
	}
	return out, nil
}

// Page returns page n (1-based) of a book.
func (s *Service) Page(ctx context.Context, b *BookInfo, n int) ([]byte, string, error) {
	if b.Path != "" {
		defer apitiming.Span(ctx, "file")()
		if entries, err := cbz.Entries(b.Path); err == nil {
			if n < 1 || n > len(entries) {
				return nil, "", ErrNotFound
			}
			rc, _, err := cbz.OpenPage(b.Path, entries[n-1])
			if err != nil {
				return nil, "", err
			}
			data, err := io.ReadAll(io.LimitReader(rc, 128<<20))
			rc.Close()
			if err != nil {
				return nil, "", err
			}
			return data, contentType(data, entries[n-1].Name), nil
		}
	}
	st, err := s.stream(ctx, b)
	if err != nil {
		return nil, "", err
	}
	if n < 1 || n > len(st.pages) {
		return nil, "", ErrNotFound
	}
	p := st.pages[n-1]
	key := fmt.Sprintf("%d|%d|%d", b.Chapter.ID, st.release, n)
	defer apitiming.Span(ctx, "source")()
	data, _, _, err := s.ImageCache.Get(ctx, PagesBucket, key, pageTTL, func(ctx context.Context) (io.ReadCloser, string, error) {
		fctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		body, ct, err := st.mod.FetchPage(fctx, p)
		if err != nil {
			return nil, "", err
		}
		data, err := io.ReadAll(io.LimitReader(body, 20<<20))
		body.Close()
		if err != nil {
			return nil, "", err
		}
		if _, err := imagecheck.Detect(data); err != nil {
			return nil, "", fmt.Errorf("page %d: %w", n, err)
		}
		return io.NopCloser(bytes.NewReader(data)), ct, nil
	})
	if err != nil {
		return nil, "", err
	}
	return data, contentType(data, ""), nil
}

// PageContent is a page ready to send: a stream, its size, and what a
// client needs to cache it.
type PageContent struct {
	Body        io.ReadCloser
	Size        int64
	ContentType string
	// ETag changes when the page's bytes change (a new file, another release).
	ETag    string
	ModTime time.Time
}

// PageReader opens page n (1-based) for streaming. A downloaded page comes
// straight out of its CBZ; a page of a chapter that isn't downloaded is
// streamed from the source once and then served from the cache.
func (s *Service) PageReader(ctx context.Context, b *BookInfo, n int) (*PageContent, error) {
	if b.Path != "" {
		defer apitiming.Span(ctx, "file")()
		if entries, err := cbz.Entries(b.Path); err == nil {
			if n < 1 || n > len(entries) {
				return nil, ErrNotFound
			}
			e := entries[n-1]
			rc, size, err := cbz.OpenPage(b.Path, e)
			if err != nil {
				return nil, err
			}
			out := &PageContent{Body: rc, Size: size, ContentType: mediaType(e.Name), ETag: fileETag(b, n)}
			if st, err := os.Stat(b.Path); err == nil {
				out.ModTime = st.ModTime()
			}
			return out, nil
		}
	}
	// the downloader may already have this page on disk: don't ask the source
	// for it a second time
	if s.Staged != nil {
		if path := s.Staged(ctx, b.Chapter.ID, n); path != "" {
			if f, err := os.Open(path); err == nil {
				if st, err := f.Stat(); err == nil {
					return &PageContent{Body: f, Size: st.Size(), ContentType: mediaType(path),
						ETag: fmt.Sprintf("%q", fmt.Sprintf("d%d-%d-%d", b.Chapter.ID, st.ModTime().UnixNano(), n)), ModTime: st.ModTime()}, nil
				}
				f.Close()
			}
		}
	}
	data, ct, err := s.Page(ctx, b, n)
	if err != nil {
		return nil, err
	}
	st, err := s.stream(ctx, b)
	release := int64(0)
	if err == nil {
		release = st.release
	}
	return &PageContent{Body: io.NopCloser(bytes.NewReader(data)), Size: int64(len(data)), ContentType: ct,
		ETag: fmt.Sprintf("%q", fmt.Sprintf("s%d-%d-%d", b.Chapter.ID, release, n))}, nil
}

// PageETag is the ETag PageReader gives page n of a downloaded book, worked
// out without opening the file, so a browser that already has the page gets
// its 304 cheaply. It is "" when the book isn't downloaded.
func (s *Service) PageETag(b *BookInfo, n int) string {
	if b.Path == "" {
		return ""
	}
	return fileETag(b, n)
}

// fileETag identifies a downloaded page: its file, when it was imported, and
// the page number.
func fileETag(b *BookInfo, n int) string {
	if b.File != nil {
		return fmt.Sprintf("%q", fmt.Sprintf("f%d-%d-%d", b.File.ID, b.File.ImportedAt.Unix(), n))
	}
	st, err := os.Stat(b.Path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%q", fmt.Sprintf("p%d-%d-%d", st.ModTime().UnixNano(), st.Size(), n))
}

// ETagMatches reports whether an If-None-Match header covers etag, so a
// caller can answer 304 instead of sending the page again.
func ETagMatches(header, etag string) bool {
	if header == "" || etag == "" {
		return false
	}
	for _, want := range strings.Split(header, ",") {
		want = strings.TrimSpace(want)
		if want == "*" || strings.TrimPrefix(want, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}

// PageThumbnail is a small JPEG of page n (from the thumbnail cache).
func (s *Service) PageThumbnail(ctx context.Context, b *BookInfo, n int) ([]byte, string, error) {
	var key string
	if b.File != nil && b.Path != "" {
		key = fmt.Sprintf("page|%s|%d|%d|%d", b.Path, b.File.ImportedAt.Unix(), b.File.Size, n)
	} else {
		key = fmt.Sprintf("page|ch%d|%d", b.Chapter.ID, n)
	}
	data, ct, _, err := s.ImageCache.Get(ctx, "thumbs", key, 30*24*time.Hour, func(ctx context.Context) (io.ReadCloser, string, error) {
		data, ct, err := s.Page(ctx, b, n)
		if err != nil {
			return nil, "", err
		}
		return io.NopCloser(bytes.NewReader(data)), ct, nil
	})
	return data, ct, err
}

// BookThumbnail is the first page of a downloaded book, else the series cover.
func (s *Service) BookThumbnail(ctx context.Context, b *BookInfo, ser *model.Series) ([]byte, string, error) {
	if b.Path != "" {
		if data, ct, err := s.PageThumbnail(ctx, b, 1); err == nil {
			return data, ct, nil
		}
	}
	return s.Cover(ctx, ser)
}

// stream loads (or reuses) the page list of an undownloaded chapter.
func (s *Service) stream(ctx context.Context, b *BookInfo) (*stream, error) {
	if e := s.streams.get(b.Chapter.ID); e != nil {
		return e, nil
	}
	v, err, _ := s.streams.sf.Do(strconv.FormatInt(b.Chapter.ID, 10), func() (any, error) {
		// shared by concurrent requests: don't let one client's cancel fail the others
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
		defer cancel()
		e, err := s.loadStream(lctx, b.Chapter)
		if err != nil {
			return nil, err
		}
		s.streams.put(b.Chapter.ID, e)
		s.downloadOnOpen(lctx, b)
		return e, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*stream), nil
}

func (s *Service) loadStream(ctx context.Context, ch model.Chapter) (*stream, error) {
	var rels []model.ChapterRelease
	if err := s.DB.NewSelect().Model(&rels).
		Join("JOIN series_sources AS ss ON ss.id = chapter_release.series_source_id").
		Where("chapter_release.chapter_id = ?", ch.ID).Where("chapter_release.removed = ?", false).Where("ss.enabled = ?", true).
		Where("NOT EXISTS (SELECT 1 FROM blocklist AS b WHERE b.series_source_id = chapter_release.series_source_id AND b.chapter_url = chapter_release.chapter_url)").
		OrderExpr("ss.priority, chapter_release.id").Scan(ctx); err != nil {
		return nil, err
	}
	var ser model.Series
	if err := s.DB.NewSelect().Model(&ser).Where("id = ?", ch.SeriesID).Scan(ctx); err != nil {
		return nil, err
	}
	var sources []model.SeriesSource
	if err := s.DB.NewSelect().Model(&sources).Where("series_id = ?", ch.SeriesID).Scan(ctx); err != nil {
		return nil, err
	}
	ranks, err := sourcepriority.Ranks(ctx, s.DB, ser, sources)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(rels, func(i, j int) bool {
		a, b := ranks[rels[i].SeriesSourceID], ranks[rels[j].SeriesSourceID]
		if a != b {
			return a < b
		}
		return rels[i].ID < rels[j].ID
	})
	lastErr := ErrNoSource
	links := map[int64]*model.SeriesSource{}
	for _, rel := range rels {
		link, ok := links[rel.SeriesSourceID]
		if !ok {
			link = &model.SeriesSource{}
			if err := s.DB.NewSelect().Model(link).Where("id = ?", rel.SeriesSourceID).Scan(ctx); err != nil {
				link = nil
			}
			links[rel.SeriesSourceID] = link
		}
		if link == nil {
			continue
		}
		mod, _, err := modules.GetAs[source.Module](s.Mods, link.ModuleID)
		if err != nil {
			continue
		}
		title := link.Title
		if title == "" {
			title = ser.Title
		}
		ref := source.ChapterRef{
			Manga:     source.MangaRef{SourceID: link.SourceID, URL: link.MangaURL, EngineRef: link.EngineRef, TitleHint: title},
			URL:       rel.ChapterURL,
			EngineRef: rel.EngineRef,
		}
		pages, err := mod.Pages(ctx, ref)
		if err != nil || len(pages) == 0 {
			if err == nil {
				err = fmt.Errorf("%s has no pages for this chapter", link.SourceName)
			}
			lastErr = err
			s.Log.Debug("streaming: source failed", "chapter", ch.ID, "source", link.SourceName, "error", err)
			continue
		}
		return &stream{mod: mod, release: rel.ID, pages: pages, exp: time.Now().Add(pageListTTL)}, nil
	}
	return nil, lastErr
}

// downloadOnOpen queues an undownloaded chapter someone started reading.
func (s *Service) downloadOnOpen(ctx context.Context, b *BookInfo) {
	if s.Downloads == nil || b.File != nil {
		return
	}
	cfg, err := s.Settings.Reading(ctx)
	if err != nil || !cfg.DownloadOnOpen {
		return
	}
	n, err := s.Downloads.EvaluateAt(ctx, b.EditionID, []int64{b.Chapter.ID}, true, model.PriorityReading)
	if err != nil {
		s.Log.Warn("download on open failed", "chapter", b.Chapter.ID, "error", err)
		return
	}
	if n > 0 {
		s.Log.Info("reading an undownloaded chapter: download queued", "series", b.Chapter.SeriesID, "chapter", b.Chapter.ID)
	}
}

// File is a downloaded book's CBZ (ErrNotFound when not downloaded).
func (s *Service) File(b *BookInfo) (*os.File, os.FileInfo, error) {
	if b.Path == "" {
		return nil, nil, ErrNotFound
	}
	f, err := os.Open(b.Path)
	if err != nil {
		return nil, nil, ErrNotFound
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, st, nil
}

// Convert re-encodes an image to png or jpeg (for clients that can't show
// the original format).
func Convert(data []byte, to string) ([]byte, string, error) {
	img, err := Decode(data)
	if err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	if to == "jpeg" || to == "jpg" {
		rgba := image.NewRGBA(img.Bounds())
		draw.Draw(rgba, rgba.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
		draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Over)
		err = jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: 92})
		return buf.Bytes(), "image/jpeg", err
	}
	err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img)
	return buf.Bytes(), "image/png", err
}

// Decode decodes a page in any format a chapter can hold.
func Decode(data []byte) (image.Image, error) {
	if info, _ := imagecheck.Detect(data); info.Format == "jxl" {
		return decodeJXL(data)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

// decodeJXL decodes JPEG XL through libjxl's djxl, when installed.
func decodeJXL(data []byte) (image.Image, error) {
	bin, err := exec.LookPath("djxl")
	if err != nil {
		return nil, errors.New("JPEG XL pages need djxl (libjxl-tools) to be converted")
	}
	dir, err := os.MkdirTemp("", "mangarr-jxl-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, out := filepath.Join(dir, "in.jxl"), filepath.Join(dir, "out.png")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return nil, err
	}
	if msg, err := exec.Command(bin, in, out).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("djxl: %v: %s", err, strings.TrimSpace(string(msg)))
	}
	f, err := os.Open(out)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

// pageExt guesses a page's extension from its URL (".jpg" when unknown).
func pageExt(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	switch ext := strings.ToLower(filepath.Ext(u)); ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".jxl":
		return ext
	}
	return ".jpg"
}

func mediaType(name string) string {
	switch ext := strings.ToLower(filepath.Ext(name)); ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".jxl":
		return "image/jxl"
	case ".avif":
		return "image/avif"
	case ".webp":
		return "image/webp"
	default:
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
		return "image/jpeg"
	}
}

// contentType is the type of an image from its bytes (name as a fallback).
func contentType(data []byte, name string) string {
	if info, err := imagecheck.Detect(data); err == nil {
		return "image/" + info.Format
	}
	if name != "" {
		return mediaType(name)
	}
	return "application/octet-stream"
}
