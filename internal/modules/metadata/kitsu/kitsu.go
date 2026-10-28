// Package kitsu implements a metadata module backed by the public Kitsu API
// (no API key required). Kitsu maps its series to AniList, MyAnimeList and
// MangaUpdates ids, so it joins the other providers by id.
package kitsu

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/genres"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
)

const (
	defaultEndpoint  = "https://kitsu.app/api/edge"
	defaultUserAgent = "mangarr (https://github.com/Asion001/mangarr)"
	siteURL          = "https://kitsu.app/manga/"
)

type Settings struct {
	Endpoint  string `json:"endpoint" label:"API endpoint" type:"url" order:"1" advanced:"true"`
	UserAgent string `json:"userAgent" label:"User-Agent" order:"2" advanced:"true"`
}

func init() {
	modules.Register(&modules.Implementation{
		Kind:        modules.KindMetadata,
		Name:        "kitsu",
		DisplayName: "Kitsu",
		Description: "Series metadata from Kitsu: titles, synopsis, authors, genres, categories, cover and status, joined to AniList and MyAnimeList by id.",
		InfoURL:     "https://kitsu.app",
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
	// backoff pauses calls while Kitsu is down.
	backoff metadata.Backoff
}

type manga struct {
	ID         string `json:"id"`
	Attributes struct {
		Slug              string            `json:"slug"`
		Description       string            `json:"description"`
		Titles            map[string]string `json:"titles"`
		CanonicalTitle    string            `json:"canonicalTitle"`
		AbbreviatedTitles []string          `json:"abbreviatedTitles"`
		StartDate         string            `json:"startDate"`
		AgeRating         string            `json:"ageRating"`
		Subtype           string            `json:"subtype"`
		Status            string            `json:"status"`
		PosterImage       struct {
			Original string `json:"original"`
			Large    string `json:"large"`
		} `json:"posterImage"`
		ChapterCount int `json:"chapterCount"`
	} `json:"attributes"`
	Relationships struct {
		Categories relation `json:"categories"`
		Mappings   relation `json:"mappings"`
		Staff      relation `json:"staff"`
	} `json:"relationships"`
}

type relation struct {
	Data []ref `json:"data"`
}

type ref struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// included is any record a query's include brought: a category, mapping,
// staff entry or person.
type included struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	Attributes struct {
		Title        string `json:"title"`
		ExternalSite string `json:"externalSite"`
		ExternalID   string `json:"externalId"`
		Role         string `json:"role"`
		Name         string `json:"name"`
	} `json:"attributes"`
	Relationships struct {
		Person struct {
			Data *ref `json:"data"`
		} `json:"person"`
		Item struct {
			Data *ref `json:"data"`
		} `json:"item"`
	} `json:"relationships"`
}

// Test always asks Kitsu, even while calls are paused, so a health check or
// the settings test sees the real state (and a success ends the pause).
func (m *Module) Test(ctx context.Context) error {
	var out json.RawMessage
	err := m.fetch(ctx, "/manga?page%5Blimit%5D=1&filter%5Btext%5D=One+Piece", &out)
	m.backoff.Record(err)
	return err
}

func (m *Module) Search(ctx context.Context, q string, limit int) ([]metadata.SeriesMetadata, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	values := url.Values{"filter[text]": {q}, "page[limit]": {strconv.Itoa(limit)}}
	var out struct {
		Data []manga `json:"data"`
	}
	if err := m.call(ctx, "/manga?"+values.Encode(), &out); err != nil {
		return nil, err
	}
	res := make([]metadata.SeriesMetadata, 0, len(out.Data))
	for _, md := range out.Data {
		res = append(res, convert(md, nil))
	}
	return res, nil
}

var digits = regexp.MustCompile(`^[0-9]+$`)

func (m *Module) Get(ctx context.Context, id string) (*metadata.SeriesMetadata, error) {
	id = strings.TrimSpace(id)
	if !digits.MatchString(id) {
		return nil, fmt.Errorf("invalid Kitsu id %q", id)
	}
	var out struct {
		Data     manga      `json:"data"`
		Included []included `json:"included"`
	}
	if err := m.call(ctx, "/manga/"+id+"?include=categories,mappings,staff.person", &out); err != nil {
		return nil, err
	}
	md := convert(out.Data, out.Included)
	return &md, nil
}

// sites are Kitsu's names for the other providers in its mappings.
var sites = map[string]string{"mal": "myanimelist/manga", "anilist": "anilist/manga", "mangaupdates": "mangaupdates"}

func (m *Module) LookupExternal(ctx context.Context, provider, id string) (*metadata.SeriesMetadata, error) {
	if provider == "kitsu" {
		return m.Get(ctx, id)
	}
	site, ok := sites[provider]
	if !ok {
		return nil, metadata.ErrNotFound
	}
	if provider == "mangaupdates" {
		// Kitsu maps MangaUpdates by the base36 slug of its id
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, metadata.ErrNotFound
		}
		id = strconv.FormatInt(n, 36)
	}
	// a mapping names its series only when asked to include it
	values := url.Values{"filter[externalSite]": {site}, "filter[externalId]": {id}, "include": {"item"}}
	var out struct {
		Data []included `json:"data"`
	}
	if err := m.call(ctx, "/mappings?"+values.Encode(), &out); err != nil {
		return nil, err
	}
	for _, mapping := range out.Data {
		if item := mapping.Relationships.Item.Data; item != nil && item.Type == "manga" {
			return m.Get(ctx, item.ID)
		}
	}
	return nil, metadata.ErrNotFound
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
	// Kitsu documents no limit; keep a big refresh polite.
	for {
		m.mu.Lock()
		wait := 500*time.Millisecond - time.Since(m.lastReq)
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
	req.Header.Set("Accept", "application/vnd.api+json")
	req.Header.Set("User-Agent", m.s.UserAgent)
	resp, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("kitsu: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return metadata.ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return metadata.StatusError("kitsu", resp)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
		return fmt.Errorf("kitsu: %w", err)
	}
	return nil
}

// origins are where each kind of series comes from, and the language codes
// Kitsu uses for its original and romanized titles.
var origins = map[string]struct{ country, native, romanized string }{
	"JP": {"JP", "ja_jp", "en_jp"},
	"KR": {"KR", "ko_kr", "en_kr"},
	"CN": {"CN", "zh_cn", "en_cn"},
}

// titles are a series' names by language.
func titles(md manga, country string) map[string]string {
	o := origins[country]
	out := map[string]string{}
	for code, title := range md.Attributes.Titles {
		if title = strings.TrimSpace(title); title == "" {
			continue
		}
		key := strings.SplitN(code, "_", 2)[0]
		switch {
		case code == o.native:
			key = model.TitleNative
		case code == o.romanized:
			key = model.TitleRomanized
		case strings.HasPrefix(code, "en_") && code != "en_us":
			continue // another origin's romanization
		}
		if _, ok := out[key]; !ok || code == "en" {
			out[key] = title
		}
	}
	return out
}

func convert(md manga, inc []included) metadata.SeriesMetadata {
	a := md.Attributes
	out := metadata.SeriesMetadata{
		Provider: "kitsu", ID: md.ID, Description: strings.TrimSpace(a.Description), Year: year(a.StartDate),
		CoverURL: firstNonEmpty(a.PosterImage.Original, a.PosterImage.Large), Adult: a.AgeRating == "R18",
		TotalChapters: a.ChapterCount, URL: siteURL + firstNonEmpty(a.Slug, md.ID),
		ExternalIDs: map[string]string{"kitsu": md.ID},
	}
	out.Links = map[string]string{"Kitsu": out.URL}
	out.Format, out.Country = format(a.Subtype)
	out.Titles = titles(md, out.Country)
	out.Title = firstNonEmpty(out.Titles["en"], a.CanonicalTitle, out.Titles[model.TitleRomanized])
	alt := []string{a.CanonicalTitle}
	for _, k := range []string{"en", model.TitleRomanized, model.TitleNative} {
		alt = append(alt, out.Titles[k])
	}
	out.AltTitles = dedupeExcept(append(alt, a.AbbreviatedTitles...), out.Title)
	switch a.Status {
	case "current", "upcoming", "unreleased", "tba":
		out.Status = "ongoing"
	case "finished":
		out.Status = "completed"
	default:
		out.Status = "unknown"
	}

	byID := map[ref]included{}
	for _, i := range inc {
		byID[ref{i.Type, i.ID}] = i
	}
	r := md.Relationships
	for _, c := range r.Categories.Data {
		name := byID[c].Attributes.Title
		switch {
		case name == "":
		case genres.Known(name):
			out.Genres = append(out.Genres, name)
		default:
			out.Tags = append(out.Tags, name)
		}
	}
	for _, s := range r.Staff.Data {
		staff := byID[s]
		person := staff.Relationships.Person.Data
		if person == nil {
			continue
		}
		name := byID[*person].Attributes.Name
		role := strings.ToLower(staff.Attributes.Role)
		if name == "" {
			continue
		}
		if strings.Contains(role, "story") || strings.Contains(role, "original creator") {
			out.Authors = append(out.Authors, name)
		}
		if strings.Contains(role, "art") {
			out.Artists = append(out.Artists, name)
		}
	}
	out.Authors, out.Artists = dedupeExcept(out.Authors, ""), dedupeExcept(out.Artists, "")
	for _, mp := range r.Mappings.Data {
		mapping := byID[mp].Attributes
		id := strings.TrimSpace(mapping.ExternalID)
		switch mapping.ExternalSite {
		case "myanimelist/manga":
			if digits.MatchString(id) {
				out.ExternalIDs["mal"] = id
				out.Links["MyAnimeList"] = "https://myanimelist.net/manga/" + id
			}
		case "anilist/manga":
			if digits.MatchString(id) {
				out.ExternalIDs["anilist"] = id
				out.Links["AniList"] = "https://anilist.co/manga/" + id
			}
		case "mangaupdates":
			slug := strings.ToLower(id)
			if n, err := strconv.ParseInt(slug, 36, 64); err == nil && n > 0 && !digits.MatchString(slug) {
				out.ExternalIDs["mangaupdates"] = strconv.FormatInt(n, 10)
				out.Links["MangaUpdates"] = "https://www.mangaupdates.com/series/" + slug
			}
		}
	}
	return out
}

func format(subtype string) (string, string) {
	switch subtype {
	case "manhwa":
		return "manhwa", "KR"
	case "manhua":
		return "manhua", "CN"
	case "novel":
		return "novel", "JP"
	case "oneshot":
		return "oneshot", "JP"
	case "oel":
		return "comic", ""
	}
	return "manga", "JP"
}

func year(date string) int {
	if len(date) < 4 {
		return 0
	}
	n, _ := strconv.Atoi(date[:4])
	return n
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
