// Package mangadex implements a metadata module backed by the public
// MangaDex API (no API key required). It names series in many languages,
// Ukrainian and Russian among them, and links each to its AniList,
// MyAnimeList, MangaUpdates and Kitsu ids.
package mangadex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
)

const (
	defaultEndpoint  = "https://api.mangadex.org"
	defaultUserAgent = "mangarr (https://github.com/Asion001/mangarr)"
	coversURL        = "https://uploads.mangadex.org/covers/"
	siteURL          = "https://mangadex.org/title/"
)

type Settings struct {
	Endpoint  string `json:"endpoint" label:"API endpoint" type:"url" order:"1" advanced:"true"`
	UserAgent string `json:"userAgent" label:"User-Agent" order:"2" help:"MangaDex refuses requests without an identifiable User-Agent." advanced:"true"`
}

func init() {
	modules.Register(&modules.Implementation{
		Kind:        modules.KindMetadata,
		Name:        "mangadex",
		DisplayName: "MangaDex",
		Description: "Series metadata from MangaDex: titles in many languages, synopsis, authors, genres, tags, cover and status, with links to AniList, MyAnimeList and MangaUpdates.",
		InfoURL:     "https://mangadex.org",
		Settings: func() any {
			return &Settings{Endpoint: defaultEndpoint, UserAgent: defaultUserAgent}
		},
		New: func(deps modules.Deps, raw any) (modules.Instance, error) {
			hc := deps.HTTP
			if hc == nil {
				hc = &http.Client{Timeout: 30 * time.Second}
			}
			s := raw.(*Settings)
			if strings.TrimSpace(s.Endpoint) == "" {
				s.Endpoint = defaultEndpoint
			}
			if strings.TrimSpace(s.UserAgent) == "" {
				s.UserAgent = defaultUserAgent
			}
			return &Module{s: s, http: hc}, nil
		},
	})
}

type Module struct {
	s    *Settings
	http *http.Client

	mu      sync.Mutex
	lastReq time.Time
	// backoff pauses calls while MangaDex is down.
	backoff metadata.Backoff
}

type manga struct {
	ID         string `json:"id"`
	Attributes struct {
		Title                  map[string]string   `json:"title"`
		AltTitles              []map[string]string `json:"altTitles"`
		Description            map[string]string   `json:"description"`
		Links                  map[string]string   `json:"links"`
		OriginalLanguage       string              `json:"originalLanguage"`
		LastChapter            string              `json:"lastChapter"`
		PublicationDemographic string              `json:"publicationDemographic"`
		Status                 string              `json:"status"`
		Year                   int                 `json:"year"`
		ContentRating          string              `json:"contentRating"`
		Tags                   []struct {
			Attributes struct {
				Name  map[string]string `json:"name"`
				Group string            `json:"group"`
			} `json:"attributes"`
		} `json:"tags"`
	} `json:"attributes"`
	Relationships []struct {
		Type       string `json:"type"`
		Attributes struct {
			Name     string `json:"name"`
			FileName string `json:"fileName"`
		} `json:"attributes"`
	} `json:"relationships"`
}

// includes and ratings go on every query: the people and cover come with the
// series, and adult series are found too (MangaDex leaves them out by default).
const includes = includesSafe + "&contentRating[]=erotica&contentRating[]=pornographic"

// includesSafe leaves the adult series out, for searches that hide them.
const includesSafe = "includes[]=author&includes[]=artist&includes[]=cover_art" +
	"&contentRating[]=safe&contentRating[]=suggestive"

// Test always asks MangaDex, even while calls are paused, so a health check
// or the settings test sees the real state (and a success ends the pause).
func (m *Module) Test(ctx context.Context) error {
	var out json.RawMessage
	err := m.fetch(ctx, "/manga?limit=1&title=One+Piece", &out)
	m.backoff.Record(err)
	return err
}

func (m *Module) Search(ctx context.Context, q string, limit int) ([]metadata.SeriesMetadata, error) {
	return m.SearchLanguage(ctx, q, "", limit)
}

// SearchLanguage names the results in language when MangaDex has it.
func (m *Module) SearchLanguage(ctx context.Context, q, language string, limit int) ([]metadata.SeriesMetadata, error) {
	if limit <= 0 || limit > 25 {
		limit = 10
	}
	values := url.Values{"title": {q}, "limit": {strconv.Itoa(limit)}, "order[relevance]": {"desc"}}
	var out struct {
		Data []manga `json:"data"`
	}
	with := includes
	if metadata.AdultHidden(ctx) {
		with = includesSafe
	}
	if err := m.call(ctx, "/manga?"+values.Encode()+"&"+with, &out); err != nil {
		return nil, err
	}
	res := make([]metadata.SeriesMetadata, 0, len(out.Data))
	for _, md := range out.Data {
		res = append(res, convert(md, language))
	}
	return res, nil
}

func (m *Module) Get(ctx context.Context, id string) (*metadata.SeriesMetadata, error) {
	return m.GetLanguage(ctx, id, "")
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (m *Module) GetLanguage(ctx context.Context, id, language string) (*metadata.SeriesMetadata, error) {
	id = strings.TrimSpace(id)
	if !uuidRe.MatchString(id) {
		return nil, fmt.Errorf("invalid MangaDex id %q", id)
	}
	var out struct {
		Data manga `json:"data"`
	}
	if err := m.call(ctx, "/manga/"+id+"?"+includes, &out); err != nil {
		return nil, err
	}
	md := convert(out.Data, language)
	return &md, nil
}

// Covers lists a series' volume covers, the latest volume first.
func (m *Module) Covers(ctx context.Context, id string) ([]metadata.Cover, error) {
	id = strings.TrimSpace(id)
	if !uuidRe.MatchString(id) {
		return nil, fmt.Errorf("invalid MangaDex id %q", id)
	}
	values := url.Values{"manga[]": {id}, "limit": {"24"}, "order[volume]": {"desc"}}
	var out struct {
		Data []struct {
			Attributes struct {
				FileName string `json:"fileName"`
				Volume   string `json:"volume"`
				Locale   string `json:"locale"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := m.call(ctx, "/cover?"+values.Encode(), &out); err != nil {
		return nil, err
	}
	covers := []metadata.Cover{}
	for _, c := range out.Data {
		if c.Attributes.FileName == "" {
			continue
		}
		cv := metadata.Cover{URL: coversURL + id + "/" + c.Attributes.FileName}
		cv.Thumb = cv.URL + ".256.jpg"
		if v := c.Attributes.Volume; v != "" {
			cv.Label = "vol. " + v
		}
		if l := c.Attributes.Locale; l != "" && l != "ja" {
			cv.Label = strings.TrimSpace(cv.Label + " " + l)
		}
		covers = append(covers, cv)
	}
	return covers, nil
}

// LookupExternal only knows MangaDex's own ids: its API can't find a series
// by another provider's id, so other providers join it by title.
func (m *Module) LookupExternal(ctx context.Context, provider, id string) (*metadata.SeriesMetadata, error) {
	if provider != "mangadex" {
		return nil, metadata.ErrNotFound
	}
	return m.Get(ctx, id)
}

func (m *Module) call(ctx context.Context, path string, out any) error {
	if err := m.backoff.Check(); err != nil {
		return err
	}
	err := m.fetch(ctx, path, out)
	m.backoff.Record(err)
	return err
}

func (m *Module) fetch(ctx context.Context, path string, out any) error {
	// MangaDex allows about five requests a second.
	for {
		m.mu.Lock()
		wait := 250*time.Millisecond - time.Since(m.lastReq)
		if wait <= 0 {
			m.lastReq = time.Now()
			m.mu.Unlock()
			break
		}
		m.mu.Unlock()
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(m.s.Endpoint, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", m.s.UserAgent)
	resp, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("mangadex: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return metadata.ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return metadata.StatusError("mangadex", resp)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
		return fmt.Errorf("mangadex: %w", err)
	}
	return nil
}

// titles are a series' names by language: MangaDex's codes ("en", "uk"),
// the original's romanization ("ja-ro") and the original itself.
func titles(md manga) map[string]string {
	out := map[string]string{}
	orig := md.Attributes.OriginalLanguage
	add := func(lang, title string) {
		title = strings.TrimSpace(title)
		if title == "" {
			return
		}
		key := lang
		switch {
		case lang == orig:
			key = model.TitleNative
		case strings.HasSuffix(lang, "-ro"):
			key = model.TitleRomanized
		}
		if _, ok := out[key]; !ok {
			out[key] = title
		}
	}
	// the main title first: it is the one MangaDex shows
	for lang, title := range md.Attributes.Title {
		add(lang, title)
	}
	for _, alt := range md.Attributes.AltTitles {
		for lang, title := range alt {
			add(lang, title)
		}
	}
	return out
}

func convert(md manga, language string) metadata.SeriesMetadata {
	a := md.Attributes
	names := titles(md)
	language = strings.ToLower(strings.TrimSpace(language))
	title := firstNonEmpty(names[language], names["en"], names[model.TitleRomanized], first(a.Title), names[model.TitleNative])
	alt := []string{}
	for _, t := range a.Title {
		alt = append(alt, t)
	}
	for _, m := range a.AltTitles {
		for _, t := range m {
			alt = append(alt, t)
		}
	}
	out := metadata.SeriesMetadata{
		Provider: "mangadex", ID: md.ID, Title: title, AltTitles: dedupeExcept(alt, title), Titles: names,
		Description: cleanDescription(firstNonEmpty(a.Description[language], a.Description["en"], first(a.Description))),
		Year:        a.Year, URL: siteURL + md.ID, Adult: a.ContentRating == "erotica" || a.ContentRating == "pornographic",
		Links:       map[string]string{"MangaDex": siteURL + md.ID},
		ExternalIDs: map[string]string{"mangadex": md.ID},
	}
	out.Format, out.Country = format(a.OriginalLanguage)
	switch a.Status {
	case "ongoing", "completed", "hiatus", "cancelled":
		out.Status = a.Status
	default:
		out.Status = "unknown"
	}
	if out.Status == "completed" {
		out.TotalChapters, _ = strconv.Atoi(a.LastChapter)
	}
	if d := a.PublicationDemographic; d != "" && d != "none" {
		out.Genres = append(out.Genres, strings.ToUpper(d[:1])+d[1:])
	}
	for _, t := range a.Tags {
		name := firstNonEmpty(t.Attributes.Name["en"], first(t.Attributes.Name))
		if name == "" {
			continue
		}
		if t.Attributes.Group == "genre" {
			out.Genres = append(out.Genres, name)
		} else {
			out.Tags = append(out.Tags, name)
		}
	}
	for _, r := range md.Relationships {
		switch r.Type {
		case "author":
			out.Authors = append(out.Authors, r.Attributes.Name)
		case "artist":
			out.Artists = append(out.Artists, r.Attributes.Name)
		case "cover_art":
			if r.Attributes.FileName != "" {
				out.CoverURL = coversURL + md.ID + "/" + r.Attributes.FileName
			}
		}
	}
	out.Authors, out.Artists = dedupeExcept(out.Authors, ""), dedupeExcept(out.Artists, "")
	links(a.Links, &out)
	return out
}

var digits = regexp.MustCompile(`^[0-9]+$`)

// links adds the other providers' ids MangaDex knows.
func links(l map[string]string, out *metadata.SeriesMetadata) {
	if id := l["al"]; digits.MatchString(id) {
		out.ExternalIDs["anilist"] = id
		out.Links["AniList"] = "https://anilist.co/manga/" + id
	}
	if id := l["mal"]; digits.MatchString(id) {
		out.ExternalIDs["mal"] = id
		out.Links["MyAnimeList"] = "https://myanimelist.net/manga/" + id
	}
	// MangaUpdates is linked by the base36 slug of its id; an all-digit one
	// is a retired id from its old site.
	if slug := strings.ToLower(l["mu"]); slug != "" && !digits.MatchString(slug) {
		if n, err := strconv.ParseInt(slug, 36, 64); err == nil && n > 0 {
			out.ExternalIDs["mangaupdates"] = strconv.FormatInt(n, 10)
			out.Links["MangaUpdates"] = "https://www.mangaupdates.com/series/" + slug
		}
	}
	// Kitsu is linked by id or by slug; only an id joins.
	if id := l["kt"]; digits.MatchString(id) {
		out.ExternalIDs["kitsu"] = id
	}
}

func format(lang string) (string, string) {
	switch lang {
	case "ko":
		return "manhwa", "KR"
	case "zh", "zh-hk":
		return "manhua", "CN"
	case "ja":
		return "manga", "JP"
	}
	return "comic", strings.ToUpper(lang)
}

var (
	mdLink     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdEmphasis = regexp.MustCompile(`\*\*|__`)
	blankLines = regexp.MustCompile(`\n{3,}`)
)

// cleanDescription turns MangaDex's Markdown into plain text.
func cleanDescription(s string) string {
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdEmphasis.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimSpace(blankLines.ReplaceAllString(s, "\n\n"))
}

// first is a map's value in a stable order: English, else any.
func first(m map[string]string) string {
	if v := m["en"]; v != "" {
		return v
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	return m[slices.Min(keys)]
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

func dedupeExcept(values []string, except string) []string {
	seen := map[string]bool{strings.ToLower(strings.TrimSpace(except)): true}
	out := []string{}
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		if key != "" && !seen[key] {
			seen[key] = true
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}

var _ metadata.ExternalLookup = (*Module)(nil)
var _ metadata.LanguageSearcher = (*Module)(nil)
var _ metadata.LanguageGetter = (*Module)(nil)
