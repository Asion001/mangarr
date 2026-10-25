// Package native is the source module for mangarr's own sites: it adapts
// every site registered in internal/sources/sites to the module contract, so
// a library can be read and downloaded without Suwayomi. Sites behind a
// browser challenge go through FlareSolverr when its address is set.
package native

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/sources/sites"
	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// keep the sites package linked in; it registers them from its init.
var _ = sites.Count

type Settings struct {
	FlareSolverrURL string `json:"flareSolverrUrl" label:"FlareSolverr URL" type:"url" order:"1" advanced:"true" placeholder:"http://flaresolverr:8191" help:"Only needed for sites behind a browser check."`
	RequestTimeout  int    `json:"requestTimeoutSeconds" label:"Request timeout (seconds)" order:"2" advanced:"true"`
}

func init() {
	modules.Register(&modules.Implementation{
		Kind: modules.KindSource, Name: "native", DisplayName: "mangarr sources",
		Description: "Sites mangarr talks to itself, with no extension engine in between.",
		Settings:    func() any { return &Settings{RequestTimeout: 60} },
		New: func(deps modules.Deps, s any) (modules.Instance, error) {
			cfg := s.(*Settings)
			timeout := time.Duration(max(cfg.RequestTimeout, 10)) * time.Second
			hc := &http.Client{Timeout: timeout}
			if deps.HTTP != nil {
				c := *deps.HTTP
				c.Timeout = timeout
				hc = &c
			}
			client := sourcekit.NewClient(hc)
			client.Solver = sourcekit.NewSolver(strings.TrimRight(cfg.FlareSolverrURL, "/"))
			m := &Module{log: deps.Log, byID: map[string]sourcekit.Site{}, optionsPath: optionsPath(deps.DataDir, deps.ID)}
			for _, site := range sourcekit.Build(sourcekit.Deps{Client: client}) {
				m.sites = append(m.sites, site)
				m.byID[site.Info().ID] = site
			}
			m.loadOptions()
			return m, nil
		},
	})
}

func optionsPath(dataDir string, id int64) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "sources", fmt.Sprintf("native-%d.json", id))
}

// Module serves every registered site as one source module.
type Module struct {
	log   *slog.Logger
	sites []sourcekit.Site
	byID  map[string]sourcekit.Site

	// optionsPath keeps per-site options across restarts; sites hold their
	// own state, so there is nowhere else to put them.
	optionsPath string

	mu sync.Mutex
	// headers remembers what a page needed, so fetching it later (or handing
	// it to a worker) sends the same request the site asked for.
	headers map[string]map[string]string
}

func (m *Module) Test(ctx context.Context) error {
	if len(m.sites) == 0 {
		return errors.New("no sites are built into this version")
	}
	// ask the first site for something cheap: this is a real request, so a
	// broken network or a site that turns us away shows up here
	site := m.sites[0]
	if b, ok := site.(sourcekit.Browser); ok {
		if _, err := b.Popular(ctx, 1); err != nil {
			return fmt.Errorf("%s: %w", site.Info().Name, err)
		}
		return nil
	}
	_, err := site.Search(ctx, "a", 1)
	return err
}

func (m *Module) Sources(ctx context.Context) ([]source.SourceInfo, error) {
	out := make([]source.SourceInfo, 0, len(m.sites))
	for _, s := range m.sites {
		i := s.Info()
		out = append(out, source.SourceInfo{ID: i.ID, Name: i.Name, Lang: i.Lang, DisplayName: i.Name,
			SupportsLatest: i.SupportsBrowse, NSFW: i.NSFW, IconURL: i.IconURL})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Module) site(id string) (sourcekit.Site, error) {
	if s, ok := m.byID[id]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("%w: no site %q", source.ErrNotFound, id)
}

func (m *Module) Search(ctx context.Context, sourceID, query string, page int) (*source.MangaPage, error) {
	s, err := m.site(sourceID)
	if err != nil {
		return nil, err
	}
	res, err := s.Search(ctx, query, page)
	if err != nil {
		return nil, err
	}
	return m.page(sourceID, res), nil
}

func (m *Module) Latest(ctx context.Context, sourceID string, page int) (*source.MangaPage, error) {
	return m.browse(ctx, sourceID, page, false)
}

func (m *Module) Popular(ctx context.Context, sourceID string, page int) (*source.MangaPage, error) {
	return m.browse(ctx, sourceID, page, true)
}

func (m *Module) browse(ctx context.Context, sourceID string, page int, popular bool) (*source.MangaPage, error) {
	s, err := m.site(sourceID)
	if err != nil {
		return nil, err
	}
	b, ok := s.(sourcekit.Browser)
	if !ok {
		return nil, source.ErrUnsupported
	}
	var res sourcekit.Results
	if popular {
		res, err = b.Popular(ctx, page)
	} else {
		res, err = b.Latest(ctx, page)
	}
	if err != nil {
		return nil, err
	}
	return m.page(sourceID, res), nil
}

func (m *Module) page(sourceID string, res sourcekit.Results) *source.MangaPage {
	out := &source.MangaPage{Mangas: make([]source.Manga, 0, len(res.Mangas)), HasNext: res.HasNext}
	for _, x := range res.Mangas {
		out.Mangas = append(out.Mangas, source.Manga{
			MangaRef:     source.MangaRef{SourceID: sourceID, URL: x.URL, EngineRef: x.ID, TitleHint: x.Title},
			Title:        x.Title,
			ThumbnailURL: x.CoverURL,
			ChapterCount: x.Chapters,
		})
	}
	return out
}

func (m *Module) Manga(ctx context.Context, ref source.MangaRef, withChapters bool) (*source.MangaDetails, []source.Chapter, error) {
	s, err := m.site(ref.SourceID)
	if err != nil {
		return nil, nil, err
	}
	d, err := s.Details(ctx, sourcekit.Ref{URL: ref.URL, ID: ref.EngineRef, Title: ref.TitleHint})
	if err != nil {
		return nil, nil, err
	}
	details := &source.MangaDetails{
		Manga: source.Manga{MangaRef: source.MangaRef{SourceID: ref.SourceID, URL: d.URL, EngineRef: d.ID, TitleHint: d.Title},
			Title: d.Title, ThumbnailURL: d.CoverURL, ChapterCount: d.Chapters},
		Author: d.Author, Artist: d.Artist, Description: d.Description, Genres: d.Genres,
		Status: status(d.Status), WebURL: publicURL(s.Info().BaseURL, d.WebURL),
	}
	if details.URL == "" {
		details.URL = ref.URL
	}
	if !withChapters {
		return details, nil, nil
	}
	chapters, err := s.Chapters(ctx, sourcekit.Ref{URL: details.URL, ID: details.EngineRef, Title: details.Title})
	if err != nil {
		return nil, nil, err
	}
	out := make([]source.Chapter, 0, len(chapters))
	for _, c := range chapters {
		out = append(out, source.Chapter{URL: c.URL, EngineRef: c.ID, Name: c.Name, Scanlator: c.Scanlator, Number: c.Number,
			UploadDate: c.UploadedAt, WebURL: publicURL(s.Info().BaseURL, c.WebURL)})
	}
	return details, out, nil
}

// publicURL is the last boundary before a source link reaches the browser.
// Native sites may return a path, but only absolute HTTP(S) links are safe to
// expose; malformed and API-only values are omitted instead of becoming links
// relative to mangarr itself.
func publicURL(base, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Host == "" && u.Path == "" && u.RawQuery == "" && u.Fragment == "" {
		return ""
	}
	if !u.IsAbs() {
		b, err := url.Parse(strings.TrimSpace(base))
		if err != nil || b.Host == "" || (b.Scheme != "http" && b.Scheme != "https") {
			return ""
		}
		u = b.ResolveReference(u)
	}
	if u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.String()
}

func status(s string) string {
	switch s {
	case sourcekit.StatusOngoing:
		return source.StatusOngoing
	case sourcekit.StatusCompleted:
		return source.StatusCompleted
	case sourcekit.StatusHiatus:
		return source.StatusHiatus
	case sourcekit.StatusCancelled:
		return source.StatusCancelled
	}
	return source.StatusUnknown
}

func (m *Module) Pages(ctx context.Context, ref source.ChapterRef) ([]source.Page, error) {
	s, err := m.site(ref.Manga.SourceID)
	if err != nil {
		return nil, err
	}
	pages, err := s.Pages(ctx, sourcekit.PageRef{URL: ref.URL, ID: ref.EngineRef,
		Manga: sourcekit.Ref{URL: ref.Manga.URL, ID: ref.Manga.EngineRef, Title: ref.Manga.TitleHint}})
	if err != nil {
		return nil, err
	}
	out := make([]source.Page, 0, len(pages))
	for _, p := range pages {
		m.remember(p.URL, p.Headers)
		u := p.URL
		if p.Decode != "" {
			u += decodeMark + url.QueryEscape(p.Decode)
		}
		out = append(out, source.Page{Index: p.Index, URL: u, SourceID: ref.Manga.SourceID})
	}
	return out, nil
}

// decodeMark carries a page's Decode in its URL's fragment: it survives in
// stored page lists and restarts, and a fragment is never sent to the site.
const decodeMark = "#mangarr-decode="

// splitDecode separates a page URL from the Decode it carries, if any.
func splitDecode(raw string) (string, string) {
	u, enc, ok := strings.Cut(raw, decodeMark)
	if !ok {
		return raw, ""
	}
	d, err := url.QueryUnescape(enc)
	if err != nil {
		return u, enc
	}
	return u, d
}

// remember keeps a page's request headers (bounded: chapters come and go).
func (m *Module) remember(url string, headers map[string]string) {
	if len(headers) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.headers == nil || len(m.headers) > 5000 {
		m.headers = map[string]map[string]string{}
	}
	m.headers[url] = headers
}

func (m *Module) headersFor(p source.Page) map[string]string {
	pageURL, _ := splitDecode(p.URL)
	m.mu.Lock()
	h := m.headers[pageURL]
	m.mu.Unlock()
	if h != nil {
		return h
	}
	// the chapter's page list has aged out: the site's own address is what
	// nearly every referer check wants
	if s, err := m.site(p.SourceID); err == nil {
		if base := s.Info().BaseURL; base != "" {
			return map[string]string{"Referer": strings.TrimRight(base, "/") + "/"}
		}
	}
	return nil
}

// PageRequest hands a page to another machine to fetch (a download worker).
func (m *Module) PageRequest(ctx context.Context, p source.Page) (source.PageRequest, error) {
	if p.URL == "" {
		return source.PageRequest{}, source.ErrNotFound
	}
	if _, decode := splitDecode(p.URL); decode != "" {
		return source.PageRequest{}, source.ErrUnsupported // only the site here can decode it
	}
	headers := map[string]string{"User-Agent": sourcekit.UserAgent}
	for k, v := range m.headersFor(p) {
		headers[k] = v
	}
	return source.PageRequest{URL: p.URL, Method: http.MethodGet, Headers: headers}, nil
}

func (m *Module) FetchPage(ctx context.Context, p source.Page) (io.ReadCloser, string, error) {
	s, err := m.site(p.SourceID)
	if err != nil {
		return nil, "", err
	}
	pageURL, decode := splitDecode(p.URL)
	body, ctype, err := fetch(ctx, s, pageURL, m.headersFor(p))
	if err != nil || decode == "" {
		return body, ctype, err
	}
	defer body.Close()
	dec, ok := s.(sourcekit.Decoder)
	if !ok {
		return nil, "", fmt.Errorf("%s: a page needs decoding the site can't do", s.Info().Name)
	}
	data, err := io.ReadAll(io.LimitReader(body, maxPageBytes))
	if err != nil {
		return nil, "", err
	}
	out, err := dec.DecodePage(ctx, decode, data)
	if err != nil {
		return nil, "", fmt.Errorf("%s: decode page: %w", s.Info().Name, err)
	}
	return io.NopCloser(bytes.NewReader(out)), http.DetectContentType(out), nil
}

// maxPageBytes bounds a page read into memory for decoding.
const maxPageBytes = 64 << 20

// Thumbnail fetches a cover for the UI.
func (m *Module) Thumbnail(ctx context.Context, ref source.MangaRef) (io.ReadCloser, string, error) {
	s, err := m.site(ref.SourceID)
	if err != nil {
		return nil, "", err
	}
	d, err := s.Details(ctx, sourcekit.Ref{URL: ref.URL, ID: ref.EngineRef, Title: ref.TitleHint})
	if err != nil {
		return nil, "", err
	}
	if d.CoverURL == "" {
		return nil, "", source.ErrNotFound
	}
	return fetch(ctx, s, d.CoverURL, map[string]string{"Referer": strings.TrimRight(s.Info().BaseURL, "/") + "/"})
}

// fetch gets an image from a site with the headers it expects.
func fetch(ctx context.Context, s sourcekit.Site, url string, headers map[string]string) (io.ReadCloser, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", sourcekit.UserAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, "", &sourcekit.StatusError{Code: resp.StatusCode, URL: url}
	}
	return resp.Body, resp.Header.Get("Content-Type"), nil
}

// ---- per-site options -------------------------------------------------------

func (m *Module) SourcePreferences(ctx context.Context, sourceID string) ([]source.Preference, error) {
	s, err := m.site(sourceID)
	if err != nil {
		return nil, err
	}
	c, ok := s.(sourcekit.Configurable)
	if !ok {
		return []source.Preference{}, nil
	}
	opts := c.Options()
	out := make([]source.Preference, 0, len(opts))
	for i, o := range opts {
		p := source.Preference{Key: o.Key, Position: i, Type: prefType(o.Type), Title: o.Title, Summary: o.Help,
			Value: o.Value, Visible: true, DefaultValue: o.Value}
		for _, ch := range o.Choices {
			p.Entries = append(p.Entries, ch.Label)
			p.EntryValues = append(p.EntryValues, ch.Value)
		}
		out = append(out, p)
	}
	return out, nil
}

func prefType(t string) string {
	switch t {
	case "switch":
		return "switch"
	case "select":
		return "list"
	case "multiselect":
		return "multiselect"
	default:
		return "edittext"
	}
}

func (m *Module) SetSourcePreference(ctx context.Context, sourceID string, position int, typ string, value any) error {
	s, err := m.site(sourceID)
	if err != nil {
		return err
	}
	c, ok := s.(sourcekit.Configurable)
	if !ok {
		return source.ErrUnsupported
	}
	opts := c.Options()
	if position < 0 || position >= len(opts) {
		return fmt.Errorf("no option at position %d", position)
	}
	if err := c.SetOption(opts[position].Key, value); err != nil {
		return err
	}
	m.saveOptions()
	return nil
}

// storedOptions is what a restart reads back: site id -> option key -> value.
type storedOptions map[string]map[string]any

func (m *Module) loadOptions() {
	if m.optionsPath == "" {
		return
	}
	data, err := os.ReadFile(m.optionsPath)
	if err != nil {
		return
	}
	var stored storedOptions
	if err := json.Unmarshal(data, &stored); err != nil {
		return
	}
	for id, opts := range stored {
		s, ok := m.byID[id]
		if !ok {
			continue
		}
		c, ok := s.(sourcekit.Configurable)
		if !ok {
			continue
		}
		for k, v := range opts {
			if err := c.SetOption(k, v); err != nil && m.log != nil {
				m.log.Debug("a stored site option no longer applies", "site", id, "option", k, "err", err)
			}
		}
	}
}

func (m *Module) saveOptions() {
	if m.optionsPath == "" {
		return
	}
	stored := storedOptions{}
	for id, s := range m.byID {
		c, ok := s.(sourcekit.Configurable)
		if !ok {
			continue
		}
		vals := map[string]any{}
		for _, o := range c.Options() {
			vals[o.Key] = o.Value
		}
		stored[id] = vals
	}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(m.optionsPath), 0o775); err != nil {
		return
	}
	tmp := m.optionsPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o664); err != nil {
		return
	}
	if err := os.Rename(tmp, m.optionsPath); err != nil && m.log != nil {
		m.log.Warn("couldn't save the site options", "err", err)
	}
}

// Politeness is what the sites ask for, so the core can pace itself.
func (m *Module) Politeness(sourceID string) (requestsPerMinute, maxConcurrent int, ok bool) {
	s, err := m.site(sourceID)
	if err != nil {
		return 0, 0, false
	}
	p, ok := s.(sourcekit.Polite)
	if !ok {
		return 0, 0, false
	}
	pol := p.Politeness()
	return pol.RequestsPerMinute, pol.MaxConcurrent, true
}

func (m *Module) AccountSources(ctx context.Context) ([]source.AccountSource, error) {
	var out []source.AccountSource
	for _, s := range m.sites {
		a, ok := s.(sourcekit.AccountReader)
		if !ok {
			continue
		}
		i, h := s.Info(), a.AccountHelp()
		out = append(out, source.AccountSource{SourceID: i.ID, Name: i.Name, Lang: i.Lang, Field: h.Field, Secret: h.Secret, Steps: h.Steps})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Module) ReadAccount(ctx context.Context, sourceID, token string) (*source.Account, error) {
	s, err := m.site(sourceID)
	if err != nil {
		return nil, err
	}
	a, ok := s.(sourcekit.AccountReader)
	if !ok {
		return nil, fmt.Errorf("%w: %s accounts can't be read", source.ErrUnsupported, s.Info().Name)
	}
	acc, err := a.ReadAccount(ctx, token)
	if errors.Is(err, sourcekit.ErrUnauthorized) {
		return nil, source.ErrUnauthorized
	}
	if errors.Is(err, sourcekit.ErrAccount) {
		return nil, fmt.Errorf("%w%s", source.ErrAccount, strings.TrimPrefix(err.Error(), sourcekit.ErrAccount.Error()))
	}
	if err != nil {
		return nil, err
	}
	out := &source.Account{User: acc.User, Lists: []source.AccountList{}, Entries: []source.AccountEntry{}}
	for _, l := range acc.Lists {
		out.Lists = append(out.Lists, source.AccountList{ID: l.ID, Name: l.Name, Status: l.Status})
	}
	for _, e := range acc.Entries {
		out.Entries = append(out.Entries, source.AccountEntry{
			Manga: source.Manga{MangaRef: source.MangaRef{SourceID: sourceID, URL: e.URL, EngineRef: e.ID, TitleHint: e.Title},
				Title: e.Title, ThumbnailURL: publicURL(s.Info().BaseURL, e.CoverURL)},
			Status: status(e.Status), AltTitles: e.AltTitles, Lists: e.Lists, Chapter: e.Chapter, Page: e.Page, ChapterRef: e.ChapterRef, ChapterPlace: e.ChapterPlace})
	}
	return out, nil
}
