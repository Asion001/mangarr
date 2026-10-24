// Package fakesource is an in-memory source module for tests. Importing it
// registers the "fake" source implementation; tests configure behavior
// through named scenarios.
package fakesource

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/source"
)

type Settings struct {
	Scenario string `json:"scenario" required:"true"`
}

type Chapter struct {
	URL       string
	Name      string
	Number    float64
	Scanlator string
	Uploaded  time.Time
	Pages     int
	// FailWith makes page listing fail for this chapter.
	FailWith error
}

type Manga struct {
	SourceID string
	URL      string
	Title    string
	Status   string
	Chapters []Chapter
}

type Scenario struct {
	mu        sync.Mutex
	Sources   []source.SourceInfo
	Mangas    map[string]*Manga // key: sourceID|url
	PageWidth int
	Fetches   int
	// Searches counts Search calls; SearchDelay slows them down; SearchErr
	// makes searches of a catalog fail.
	Searches    int
	SearchDelay time.Duration
	SearchErr   map[string]error
	// Browses counts Popular/Latest calls by catalog. BrowseDelay and
	// BrowseErr exercise aggregation, cancellation and partial failures.
	Browses     map[string]int
	BrowseDelay time.Duration
	BrowseErr   map[string]error
	// BrowsePages overrides numbered browse results by kind and catalog.
	BrowsePages map[string]map[int]*source.MangaPage
	BrowseKinds map[string]int
	// PageNoise fills pages with gray noise (large PNGs, like real scans).
	PageNoise bool
	// PageDelay slows down every page fetch (honoring cancellation).
	PageDelay time.Duration
	// ThumbErr makes thumbnail requests fail; Thumbs counts them.
	ThumbErr error
	Thumbs   int
	// Extensions makes the module an extension manager: installing one
	// adds its catalogs to Sources.
	Extensions []Extension
	// PageBase, when set, is an address another machine can fetch this
	// scenario's pages from (see ServePages): it makes the module Fetchable,
	// which is what lets a worker download for it.
	PageBase string
	// Served counts pages fetched over HTTP rather than in process.
	Served int
	// Account makes the module read site accounts (source.Accounts): the
	// first catalog's account answers AccountToken.
	Account      *source.Account
	AccountToken string
}

// Extension is an installable package offering catalogs.
type Extension struct {
	source.Extension
	Sources []source.SourceInfo
}

var (
	mu        sync.Mutex
	scenarios = map[string]*Scenario{}
)

// NewScenario registers and returns a scenario.
func NewScenario(name string) *Scenario {
	s := &Scenario{Mangas: map[string]*Manga{}, PageWidth: 200, Browses: map[string]int{}, BrowseErr: map[string]error{}}
	mu.Lock()
	scenarios[name] = s
	mu.Unlock()
	return s
}

func (m *Module) browse(ctx context.Context, sourceID, kind string, pageNumber int) (*source.MangaPage, error) {
	m.sc.mu.Lock()
	m.sc.Browses[sourceID]++
	delay := m.sc.BrowseDelay
	m.sc.mu.Unlock()
	if delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	m.sc.mu.Lock()
	defer m.sc.mu.Unlock()
	if err := m.sc.BrowseErr[sourceID]; err != nil {
		return nil, err
	}
	if m.sc.BrowseKinds == nil {
		m.sc.BrowseKinds = map[string]int{}
	}
	m.sc.BrowseKinds[kind]++
	if pages, ok := m.sc.BrowsePages[kind+"|"+sourceID]; ok {
		if page, ok := pages[pageNumber]; ok {
			return page, nil
		}
		return &source.MangaPage{Mangas: []source.Manga{}}, nil
	}
	page := &source.MangaPage{Mangas: []source.Manga{}}
	for _, manga := range m.sc.Mangas {
		if manga.SourceID == sourceID {
			page.Mangas = append(page.Mangas, source.Manga{MangaRef: source.MangaRef{SourceID: sourceID, URL: manga.URL},
				Title: manga.Title, ThumbnailURL: manga.URL + "/thumbnail"})
		}
	}
	sort.Slice(page.Mangas, func(i, j int) bool { return page.Mangas[i].Title < page.Mangas[j].Title })
	return page, nil
}

func (m *Module) Latest(ctx context.Context, sourceID string, page int) (*source.MangaPage, error) {
	return m.browse(ctx, sourceID, "latest", page)
}

func (m *Module) Popular(ctx context.Context, sourceID string, page int) (*source.MangaPage, error) {
	return m.browse(ctx, sourceID, "popular", page)
}

func (s *Scenario) AddManga(m *Manga) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Mangas[m.SourceID+"|"+m.URL] = m
}

// FetchCount is the number of pages fetched so far.
func (s *Scenario) FetchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Fetches
}

// Update runs fn with the scenario locked (to mutate chapters mid-test).
func (s *Scenario) Update(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
}

func init() {
	modules.Register(&modules.Implementation{
		Kind: modules.KindSource, Name: "fake", DisplayName: "Fake (tests)",
		Settings: func() any { return &Settings{} },
		New: func(deps modules.Deps, st any) (modules.Instance, error) {
			name := st.(*Settings).Scenario
			mu.Lock()
			sc := scenarios[name]
			mu.Unlock()
			if sc == nil {
				return nil, fmt.Errorf("unknown scenario %q", name)
			}
			if sc.Extensions != nil {
				return &ExtModule{Module{sc: sc}}, nil
			}
			if sc.Account != nil {
				return &AccountModule{Module{sc: sc}}, nil
			}
			return &Module{sc: sc}, nil
		},
	})
}

type Module struct{ sc *Scenario }

func (m *Module) Test(ctx context.Context) error { return nil }

// ExtModule is a Module with extensions.
type ExtModule struct{ Module }

func (m *ExtModule) Extensions(ctx context.Context, refresh bool) ([]source.Extension, error) {
	m.sc.mu.Lock()
	defer m.sc.mu.Unlock()
	out := []source.Extension{}
	for _, e := range m.sc.Extensions {
		out = append(out, e.Extension)
	}
	return out, nil
}

func (m *ExtModule) InstallExtension(ctx context.Context, pkg string) error {
	m.sc.mu.Lock()
	defer m.sc.mu.Unlock()
	for i := range m.sc.Extensions {
		e := &m.sc.Extensions[i]
		if e.Pkg == pkg {
			if !e.Installed {
				e.Installed = true
				m.sc.Sources = append(m.sc.Sources, e.Sources...)
			}
			return nil
		}
	}
	return fmt.Errorf("no extension %s", pkg)
}

func (m *ExtModule) UpdateExtension(ctx context.Context, pkg string) error    { return nil }
func (m *ExtModule) UninstallExtension(ctx context.Context, pkg string) error { return nil }
func (m *ExtModule) Stores(ctx context.Context) ([]string, error)             { return []string{}, nil }
func (m *ExtModule) AddStore(ctx context.Context, url string) error           { return nil }
func (m *ExtModule) RemoveStore(ctx context.Context, url string) error        { return nil }

// AccountModule is a Module whose first catalog has readable accounts.
type AccountModule struct{ Module }

func (m *AccountModule) AccountSources(ctx context.Context) ([]source.AccountSource, error) {
	m.sc.mu.Lock()
	defer m.sc.mu.Unlock()
	c := m.sc.Sources[0]
	return []source.AccountSource{{SourceID: c.ID, Name: c.Name, Lang: c.Lang, Field: "Token", Steps: []string{"Paste it."}}}, nil
}

func (m *AccountModule) ReadAccount(ctx context.Context, sourceID, token string) (*source.Account, error) {
	m.sc.mu.Lock()
	defer m.sc.mu.Unlock()
	if sourceID != m.sc.Sources[0].ID {
		return nil, source.ErrUnsupported
	}
	if token != m.sc.AccountToken {
		return nil, source.ErrUnauthorized
	}
	a := *m.sc.Account
	return &a, nil
}

func (m *Module) Sources(ctx context.Context) ([]source.SourceInfo, error) {
	m.sc.mu.Lock()
	defer m.sc.mu.Unlock()
	return append([]source.SourceInfo{}, m.sc.Sources...), nil
}

func (m *Module) Search(ctx context.Context, sourceID, query string, page int) (*source.MangaPage, error) {
	m.sc.mu.Lock()
	m.sc.Searches++
	delay := m.sc.SearchDelay
	m.sc.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	m.sc.mu.Lock()
	defer m.sc.mu.Unlock()
	if err := m.sc.SearchErr[sourceID]; err != nil {
		return nil, err
	}
	res := &source.MangaPage{Mangas: []source.Manga{}}
	for _, mg := range m.sc.Mangas {
		if mg.SourceID == sourceID && strings.Contains(strings.ToLower(mg.Title), strings.ToLower(query)) {
			res.Mangas = append(res.Mangas, source.Manga{MangaRef: source.MangaRef{SourceID: sourceID, URL: mg.URL}, Title: mg.Title})
		}
	}
	return res, nil
}

func (m *Module) Manga(ctx context.Context, ref source.MangaRef, withChapters bool) (*source.MangaDetails, []source.Chapter, error) {
	m.sc.mu.Lock()
	defer m.sc.mu.Unlock()
	mg := m.sc.Mangas[ref.SourceID+"|"+ref.URL]
	if mg == nil {
		return nil, nil, source.ErrNotFound
	}
	det := &source.MangaDetails{Manga: source.Manga{MangaRef: source.MangaRef{SourceID: ref.SourceID, URL: ref.URL, EngineRef: "e-" + ref.URL}, Title: mg.Title},
		Status: mg.Status, Author: "Fake Author", Genres: []string{"Action"}, Description: "fake description"}
	var chs []source.Chapter
	for _, c := range mg.Chapters {
		up := c.Uploaded
		chs = append(chs, source.Chapter{URL: c.URL, Name: c.Name, Number: c.Number, Scanlator: c.Scanlator, UploadDate: &up})
	}
	return det, chs, nil
}

func (m *Module) find(ref source.ChapterRef) (*Chapter, error) {
	mg := m.sc.Mangas[ref.Manga.SourceID+"|"+ref.Manga.URL]
	if mg == nil {
		return nil, source.ErrNotFound
	}
	for i := range mg.Chapters {
		if mg.Chapters[i].URL == ref.URL {
			return &mg.Chapters[i], nil
		}
	}
	return nil, source.ErrNotFound
}

func (m *Module) Pages(ctx context.Context, ref source.ChapterRef) ([]source.Page, error) {
	m.sc.mu.Lock()
	defer m.sc.mu.Unlock()
	c, err := m.find(ref)
	if err != nil {
		return nil, err
	}
	if c.FailWith != nil {
		return nil, c.FailWith
	}
	n := c.Pages
	if n == 0 {
		n = 3
	}
	pages := make([]source.Page, n)
	for i := range pages {
		pages[i] = source.Page{Index: i, URL: fmt.Sprintf("%s#%d", c.URL, i)}
	}
	return pages, nil
}

func (m *Module) FetchPage(ctx context.Context, p source.Page) (io.ReadCloser, string, error) {
	m.sc.mu.Lock()
	m.sc.Fetches++
	delay := m.sc.PageDelay
	m.sc.mu.Unlock()
	if delay > 0 {
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(delay):
		}
	}
	data, err := m.sc.PagePNG(p.Index)
	if err != nil {
		return nil, "", err
	}
	return io.NopCloser(bytes.NewReader(data)), "image/png", nil
}

// PageRequest makes the module Fetchable when the scenario serves its pages
// over HTTP, which is how a worker is handed a chapter to download.
func (m *Module) PageRequest(ctx context.Context, p source.Page) (source.PageRequest, error) {
	m.sc.mu.Lock()
	base := m.sc.PageBase
	m.sc.mu.Unlock()
	if base == "" {
		return source.PageRequest{}, source.ErrUnsupported
	}
	return source.PageRequest{URL: fmt.Sprintf("%s/page/%d", base, p.Index), Method: "GET",
		Headers: map[string]string{"X-Fake-Source": "1"}}, nil
}

// PagePNG renders one page of this scenario.
func (s *Scenario) PagePNG(index int) ([]byte, error) {
	s.mu.Lock()
	w := s.PageWidth
	noise := s.PageNoise
	s.mu.Unlock()
	if w == 0 {
		w = 200 // a small but real page: 200×300 is above the default junk size
	}
	img := image.NewRGBA(image.Rect(0, 0, w, w*3/2))
	img.Set(0, 0, color.Black)
	if noise {
		seed := uint32(index*7919 + 17)
		for y := 0; y < w*3/2; y++ {
			for x := 0; x < w; x++ {
				seed = seed*1664525 + 1013904223
				v := uint8(128 + int(seed>>24)%64)
				if (x/8+y/8)%5 == 0 {
					v = 20
				}
				img.Set(x, y, color.RGBA{v, v, v, 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ServePages puts this scenario's pages on an HTTP server and points the
// module at it, so another process can fetch them. The caller closes it.
func (s *Scenario) ServePages() *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Fake-Source") == "" {
			http.Error(w, "this site expects the headers the module asked for", http.StatusForbidden)
			return
		}
		index, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/page/"))
		if err != nil {
			http.Error(w, "no such page", http.StatusNotFound)
			return
		}
		data, err := s.PagePNG(index)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.mu.Lock()
		s.Served++
		delay := s.PageDelay
		s.mu.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(data)
	}))
	s.mu.Lock()
	s.PageBase = srv.URL
	s.mu.Unlock()
	return srv
}

// ErrForbidden is a convenient page failure.
var ErrForbidden = errors.New("HTTP 403 from source")

// Thumbnail serves a tiny PNG.
func (m *Module) Thumbnail(ctx context.Context, ref source.MangaRef) (io.ReadCloser, string, error) {
	m.sc.mu.Lock()
	defer m.sc.mu.Unlock()
	m.sc.Thumbs++
	if m.sc.ThumbErr != nil {
		return nil, "", m.sc.ThumbErr
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewGray(image.Rect(0, 0, 2, 3)))
	return io.NopCloser(&buf), "image/png", nil
}

var _ source.Thumbnails = (*Module)(nil)
