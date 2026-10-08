package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/imagedeliver"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/reading"
)

func init() { register((*Server).registerRead) }

// ReadChapter is what the web reader needs to show a chapter.
type ReadChapter struct {
	ID          int64  `json:"id"`
	SeriesID    int64  `json:"seriesId"`
	SeriesTitle string `json:"seriesTitle"`
	Number      string `json:"number"`
	Title       string `json:"title,omitempty"`
	Volume      string `json:"volume,omitempty"`
	// ReadingDirection is the series' own (rtl, ltr, vertical, webtoon or empty).
	ReadingDirection string `json:"readingDirection"`
	Downloaded       bool   `json:"downloaded"`
	// CanDownload: the account may download the CBZ.
	CanDownload bool         `json:"canDownload"`
	Pages       []ReadPage   `json:"pages"`
	Prev        *ChapterLink `json:"prev,omitempty"`
	Next        *ChapterLink `json:"next,omitempty"`
	Progress    ReadProgress `json:"progress"`
}

// ReadPage is one page of a chapter.
type ReadPage struct {
	Number    int    `json:"number"`
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size,omitempty"`
}

// ChapterLink points at a neighbouring chapter.
type ChapterLink struct {
	ID     int64  `json:"id"`
	Number string `json:"number"`
	Title  string `json:"title,omitempty"`
}

// ReadProgress is where the reader left off (page is 1-based; 0 = not started).
type ReadProgress struct {
	Page      int  `json:"page"`
	Completed bool `json:"completed"`
}

// PageBounds is one page's border box, for the whole-chapter request.
type PageBounds struct {
	Number int `json:"number"`
	reading.Bounds
}

// boundsBudget caps how long measuring a chapter's pages may take; the reader
// asks for what's missing page by page.
const boundsBudget = 3 * time.Second

// ReaderSettingsView is the account's reader settings: defaults and the
// series' own (nil when it has none). The objects belong to the UI.
type ReaderSettingsView struct {
	Defaults json.RawMessage `json:"defaults"`
	Series   json.RawMessage `json:"series,omitempty"`
}

// readerOf is whose progress the web reader shows: the account's, or (API
// key, logins off) the reader apps use.
func (s *Server) readerOf(ctx context.Context) (int64, error) {
	if p := access.From(ctx); p != nil && p.ReaderID > 0 {
		return p.ReaderID, nil
	}
	return s.app.Reading.ReaderID(ctx)
}

func readError(err error) error {
	switch {
	case errors.Is(err, reading.ErrNotFound):
		return huma.Error404NotFound("chapter or page not found")
	case errors.Is(err, reading.ErrNoSource):
		return huma.Error404NotFound(err.Error())
	default:
		return huma.Error502BadGateway("couldn't load the page from the source: " + err.Error())
	}
}

var uaBrowser = regexp.MustCompile(`(Edg|Firefox|Chrome|Safari)/`)

// browserName names the browser for sync health ("Safari on iPad").
func browserName(ua string) string {
	b := "Browser"
	if m := uaBrowser.FindStringSubmatch(ua); m != nil {
		b = map[string]string{"Edg": "Edge"}[m[1]]
		if b == "" {
			b = m[1]
		}
	}
	for _, os := range []string{"iPad", "iPhone", "Android", "Mac OS X", "Windows", "Linux"} {
		if strings.Contains(ua, os) {
			if os == "Mac OS X" {
				os = "macOS"
			}
			return b + " on " + os
		}
	}
	return b
}

func (s *Server) registerRead() {
	tags := []string{"Reader"}

	book := func(ctx context.Context, id int64) (*reading.BookInfo, error) {
		rid, err := s.readerOf(ctx)
		if err != nil {
			return nil, toHTTPError(err)
		}
		b, err := s.app.Reading.Book(ctx, rid, id)
		if err != nil {
			return nil, readError(err)
		}
		return b, nil
	}

	huma.Register(s.api, huma.Operation{OperationID: "read-chapter", Method: http.MethodGet, Path: "/api/v1/read/chapters/{id}", Tags: tags,
		Summary: "A chapter for the web reader: pages, neighbours and your progress"},
		func(ctx context.Context, in *IDPath) (*struct{ Body ReadChapter }, error) {
			rid, err := s.readerOf(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			b, err := s.app.Reading.Book(ctx, rid, in.ID)
			if err != nil {
				return nil, readError(err)
			}
			books, err := s.app.Reading.Books(ctx, rid, b.Chapter.SeriesID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			ser, err := s.app.Series.Get(ctx, b.Chapter.SeriesID)
			if err != nil {
				return nil, seriesError(err)
			}
			pages, err := s.app.Reading.Pages(ctx, b)
			if err != nil {
				return nil, readError(err)
			}
			ch := b.Chapter
			out := ReadChapter{ID: ch.ID, SeriesID: ser.ID, SeriesTitle: ser.Title, Number: ch.NumberKey, Title: ch.Title, Volume: ch.Volume,
				ReadingDirection: ser.ReadingDirection, Downloaded: b.File != nil, CanDownload: b.File != nil && access.From(ctx).Can(access.Download),
				Pages: make([]ReadPage, len(pages))}
			for i, p := range pages {
				out.Pages[i] = ReadPage{Number: p.Number, MediaType: p.MediaType, Size: p.Size}
			}
			for i := range books {
				if books[i].Chapter.ID != ch.ID {
					continue
				}
				if i > 0 {
					c := books[i-1].Chapter
					out.Prev = &ChapterLink{ID: c.ID, Number: c.NumberKey, Title: c.Title}
				}
				if i+1 < len(books) {
					c := books[i+1].Chapter
					out.Next = &ChapterLink{ID: c.ID, Number: c.NumberKey, Title: c.Title}
				}
			}
			if b.State != nil {
				out.Progress = ReadProgress{Page: b.State.Page, Completed: b.State.Completed}
			}
			return &struct{ Body ReadChapter }{out}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "read-page", Method: http.MethodGet, Path: "/api/v1/read/chapters/{id}/pages/{n}", Tags: tags,
		Summary: "A page image (from the file, or streamed from the source)"},
		func(ctx context.Context, in *struct {
			ID          int64  `path:"id"`
			N           int    `path:"n" minimum:"1"`
			Width       int    `query:"w" doc:"Serve a copy at most this many pixels wide (for phones and tablets)"`
			IfNoneMatch string `header:"If-None-Match"`
		}) (*huma.StreamResponse, error) {
			b, err := book(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			cc := cachePrivate(24 * time.Hour)
			if etag := s.app.Reading.PageETag(b, in.N); etag != "" && in.IfNoneMatch != "" {
				// the browser has the page, or the resized copy of it
				for _, e := range []string{etag, variantETag(etag, imagedeliver.Width(in.Width))} {
					if reading.ETagMatches(in.IfNoneMatch, e) {
						return notModified(e, cc), nil
					}
				}
			}
			page, err := s.app.Reading.PageReader(ctx, b, in.N)
			if err != nil {
				return nil, readError(err)
			}
			if small := s.smallerPage(ctx, b, in.N, page, in.Width); small != nil {
				page = small
			}
			return streamImage(page, in.IfNoneMatch, cc), nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "read-page-bounds", Method: http.MethodGet, Path: "/api/v1/read/chapters/{id}/pages/{n}/bounds", Tags: tags,
		Summary: "A page's size and the box inside its borders (for cropping them)"},
		func(ctx context.Context, in *struct {
			ID int64 `path:"id"`
			N  int   `path:"n" minimum:"1"`
		}) (*struct {
			CacheControl string `header:"Cache-Control"`
			Body         reading.Bounds
		}, error) {
			b, err := book(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			bd, err := s.app.Reading.PageBounds(ctx, b, in.N)
			if err != nil {
				return nil, readError(err)
			}
			return &struct {
				CacheControl string `header:"Cache-Control"`
				Body         reading.Bounds
			}{"private, max-age=86400", bd}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "read-chapter-bounds", Method: http.MethodGet, Path: "/api/v1/read/chapters/{id}/bounds", Tags: tags,
		Summary: "The border boxes of a chapter's pages, in one request (pages still being measured come back later)"},
		func(ctx context.Context, in *IDPath) (*struct {
			CacheControl string `header:"Cache-Control"`
			Body         []PageBounds
		}, error) {
			b, err := book(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			pages, err := s.app.Reading.Pages(ctx, b)
			if err != nil {
				return nil, readError(err)
			}
			numbers := make([]int, len(pages))
			for i := range pages {
				numbers[i] = i + 1
			}
			out := []PageBounds{}
			for n, bd := range s.app.Reading.PageBoundsMany(ctx, b, numbers, boundsBudget) {
				out = append(out, PageBounds{Number: n, Bounds: bd})
			}
			sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
			return &struct {
				CacheControl string `header:"Cache-Control"`
				Body         []PageBounds
			}{"private, max-age=86400", out}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "read-progress", Method: http.MethodPut, Path: "/api/v1/read/chapters/{id}/progress", Tags: tags,
		Summary: "Save where you are in a chapter (the last page finishes it)"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Page      int  `json:"page" minimum:"0"`
				Completed bool `json:"completed,omitempty"`
			}
		}) (*struct{}, error) {
			rid, err := s.readerOf(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			ch, pages, err := s.app.Reading.ChapterPages(ctx, in.ID)
			if err != nil {
				return nil, readError(err)
			}
			c := reading.Change{ChapterID: ch.ID, SeriesID: ch.SeriesID, Page: in.Body.Page, Completed: in.Body.Completed}
			if pages > 0 && c.Page >= pages {
				c.Completed = true
			}
			by := reading.By{Origin: model.EventOriginApp, Client: "Web reader", Device: browserName(access.ClientFrom(ctx).UserAgent)}
			if _, err := s.app.Reading.Record(ctx, rid, []reading.Change{c}, by); err != nil {
				return nil, toHTTPError(err)
			}
			return nil, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "read-time", Method: http.MethodPost, Path: "/api/v1/read/chapters/{id}/time", Tags: tags,
		Summary: "Record cumulative active time for one web-reader session"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				SessionID     string `json:"sessionId" minLength:"16" maxLength:"64"`
				ActiveSeconds int    `json:"activeSeconds" minimum:"1" maximum:"43200"`
			}
		}) (*struct{}, error) {
			rid, err := s.readerOf(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			if err := s.app.Reading.RecordTime(ctx, rid, in.ID, in.Body.SessionID, in.Body.ActiveSeconds); err != nil {
				if errors.Is(err, reading.ErrNotFound) {
					return nil, readError(err)
				}
				return nil, toHTTPError(err)
			}
			return nil, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "read-mark", Method: http.MethodPut, Path: "/api/v1/read/chapters/{id}/mark", Tags: tags,
		Summary: "Mark this chapter, or every previous chapter, read or unread"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Read  bool   `json:"read"`
				Scope string `json:"scope" enum:"chapter,previous"`
			}
		}) (*struct {
			Body struct {
				Changed int `json:"changed"`
			}
		}, error) {
			rid, err := s.readerOf(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			chapter, _, err := s.app.Reading.ChapterPages(ctx, in.ID)
			if err != nil {
				return nil, readError(err)
			}
			by := reading.By{Origin: model.EventOriginApp, Client: "Web reader", Device: browserName(access.ClientFrom(ctx).UserAgent)}
			var outcomes []reading.Outcome
			if in.Body.Scope == "previous" {
				outcomes, err = s.app.Reading.MarkBeforeChapter(ctx, rid, chapter.SeriesID, chapter.ID, in.Body.Read, by)
			} else {
				change := reading.Change{ChapterID: chapter.ID, SeriesID: chapter.SeriesID, Completed: in.Body.Read, Unread: !in.Body.Read}
				outcomes, err = s.app.Reading.Record(ctx, rid, []reading.Change{change}, by)
			}
			if errors.Is(err, reading.ErrNotFound) {
				return nil, readError(err)
			}
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &struct {
				Body struct {
					Changed int `json:"changed"`
				}
			}{}
			for _, outcome := range outcomes {
				if outcome.Result == model.OutcomeApplied || outcome.Result == model.OutcomeUnread {
					out.Body.Changed++
				}
			}
			return out, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "read-file", Method: http.MethodGet, Path: "/api/v1/read/chapters/{id}/file", Tags: tags,
		Summary: "Download a downloaded chapter's CBZ"},
		func(ctx context.Context, in *IDPath) (*huma.StreamResponse, error) {
			b, err := book(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			f, st, err := s.app.Reading.File(b)
			if err != nil {
				return nil, huma.Error404NotFound("this chapter isn't downloaded")
			}
			name := filepath.Base(b.Path)
			return &huma.StreamResponse{Body: func(hctx huma.Context) {
				defer f.Close()
				hctx.SetHeader("Content-Type", "application/zip")
				hctx.SetHeader("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
				hctx.SetHeader("Content-Length", strconv.FormatInt(st.Size(), 10))
				_, _ = io.Copy(hctx.BodyWriter(), f)
			}}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "read-settings", Method: http.MethodGet, Path: "/api/v1/read/settings", Tags: tags,
		Summary: "Your web reader settings: defaults, and a series' own"},
		func(ctx context.Context, in *struct {
			SeriesID int64 `query:"seriesId"`
		}) (*struct{ Body ReaderSettingsView }, error) {
			prefs, err := s.readerPrefs(ctx, in.SeriesID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := ReaderSettingsView{Defaults: json.RawMessage("{}")}
			if d := prefs[0]; d != "" {
				out.Defaults = json.RawMessage(d)
			}
			if d := prefs[in.SeriesID]; in.SeriesID != 0 && d != "" {
				out.Series = json.RawMessage(d)
			}
			return &struct{ Body ReaderSettingsView }{out}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "read-settings-save", Method: http.MethodPut, Path: "/api/v1/read/settings", Tags: tags,
		Summary: "Save reader settings as your defaults (seriesId 0) or for one series (no data: back to the defaults)"},
		func(ctx context.Context, in *struct {
			Body struct {
				SeriesID int64          `json:"seriesId,omitempty"`
				Data     map[string]any `json:"data,omitempty"`
			}
		}) (*struct{}, error) {
			var data []byte
			if in.Body.Data != nil {
				var err error
				if data, err = json.Marshal(in.Body.Data); err != nil || len(data) > 8<<10 {
					return nil, huma.Error400BadRequest("settings too large")
				}
			}
			return nil, toHTTPError(s.saveReaderPrefs(ctx, in.Body.SeriesID, data))
		})
}

// Callers without an account (auth disabled, the API key) share one set of
// reader settings, kept in the settings store under this key as
// {"<seriesId>": {...}} with "0" for the defaults.
const keySharedReaderPrefs = "reader_prefs"

var sharedPrefsMu sync.Mutex

func prefsOwner(ctx context.Context) int64 {
	if p := access.From(ctx); p != nil && p.Kind == access.KindUser {
		return p.UserID
	}
	return 0
}

// readerPrefs are the caller's defaults (series 0) and a series' own settings.
func (s *Server) readerPrefs(ctx context.Context, seriesID int64) (map[int64]string, error) {
	out := map[int64]string{}
	user := prefsOwner(ctx)
	if user == 0 {
		shared := map[string]json.RawMessage{}
		if err := s.app.Settings.Get(ctx, keySharedReaderPrefs, &shared); err != nil {
			return nil, err
		}
		for _, id := range []int64{0, seriesID} {
			if d, ok := shared[strconv.FormatInt(id, 10)]; ok {
				out[id] = string(d)
			}
		}
		return out, nil
	}
	var prefs []model.ReaderPrefs
	if err := s.app.DB.NewSelect().Model(&prefs).Where("user_id = ? AND series_id IN (0, ?)", user, seriesID).Scan(ctx); err != nil {
		return nil, err
	}
	for _, x := range prefs {
		out[x.SeriesID] = x.Data
	}
	return out, nil
}

// saveReaderPrefs stores the caller's settings for a series (0: defaults);
// nil data removes them.
func (s *Server) saveReaderPrefs(ctx context.Context, seriesID int64, data []byte) error {
	user := prefsOwner(ctx)
	if user == 0 {
		sharedPrefsMu.Lock()
		defer sharedPrefsMu.Unlock()
		shared := map[string]json.RawMessage{}
		if err := s.app.Settings.Get(ctx, keySharedReaderPrefs, &shared); err != nil {
			return err
		}
		key := strconv.FormatInt(seriesID, 10)
		if data == nil {
			delete(shared, key)
		} else {
			shared[key] = data
		}
		return s.app.Settings.Set(ctx, keySharedReaderPrefs, shared)
	}
	if data == nil {
		_, err := s.app.DB.NewDelete().Model((*model.ReaderPrefs)(nil)).Where("user_id = ? AND series_id = ?", user, seriesID).Exec(ctx)
		return err
	}
	pr := &model.ReaderPrefs{UserID: user, SeriesID: seriesID, Data: string(data), UpdatedAt: time.Now().UTC()}
	_, err := s.app.DB.NewInsert().Model(pr).On("CONFLICT (user_id, series_id) DO UPDATE").
		Set("data = EXCLUDED.data").Set("updated_at = EXCLUDED.updated_at").Exec(ctx)
	return err
}

// streamImage sends a page without holding it in memory, with what a browser
// needs to cache it: its length, an ETag and a 304 when it already has it.
func streamImage(p *reading.PageContent, ifNoneMatch, cacheControl string) *huma.StreamResponse {
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		defer p.Body.Close()
		hctx.SetHeader("Cache-Control", cacheControl)
		if p.ETag != "" {
			hctx.SetHeader("ETag", p.ETag)
		}
		if !p.ModTime.IsZero() {
			hctx.SetHeader("Last-Modified", p.ModTime.UTC().Format(http.TimeFormat))
		}
		if reading.ETagMatches(ifNoneMatch, p.ETag) {
			hctx.SetStatus(http.StatusNotModified)
			return
		}
		hctx.SetHeader("Content-Type", p.ContentType)
		if p.Size > 0 {
			hctx.SetHeader("Content-Length", strconv.FormatInt(p.Size, 10))
		}
		hctx.SetStatus(http.StatusOK)
		_, _ = io.Copy(hctx.BodyWriter(), p.Body)
	}}
}

// variantETag is the ETag of a page's copy at width (the page's own when
// width is 0).
func variantETag(etag string, width int) string {
	if width == 0 {
		return etag
	}
	return strings.TrimSuffix(etag, `"`) + fmt.Sprintf(`-w%d"`, width)
}

// notModified tells the browser its copy of an image is still good.
func notModified(etag, cacheControl string) *huma.StreamResponse {
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		hctx.SetHeader("Cache-Control", cacheControl)
		hctx.SetHeader("ETag", etag)
		hctx.SetStatus(http.StatusNotModified)
	}}
}

// smallerPage is a display-sized copy of a page, or nil to send the page as
// it is (the client asked for no size, resizing is off, or the copy wouldn't
// be smaller).
func (s *Server) smallerPage(ctx context.Context, b *reading.BookInfo, n int, page *reading.PageContent, want int) *reading.PageContent {
	width := imagedeliver.Width(want)
	if width == 0 || page.ETag == "" {
		return nil
	}
	if cfg, err := s.app.Settings.Reading(ctx); err != nil || !cfg.ResizePages {
		return nil
	}
	data, ct, ok := s.app.ImageDeliver.Variant(ctx, page.ETag, width, func(ctx context.Context) ([]byte, error) {
		raw, _, err := s.app.Reading.Page(ctx, b, n)
		return raw, err
	})
	if !ok {
		return nil
	}
	_ = page.Body.Close() // the copy replaces it
	etag := variantETag(page.ETag, width)
	return &reading.PageContent{Body: io.NopCloser(bytes.NewReader(data)), Size: int64(len(data)), ContentType: ct,
		ETag: etag, ModTime: page.ModTime}
}
