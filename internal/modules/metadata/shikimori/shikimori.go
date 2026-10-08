// Package shikimori implements Russian-capable manga metadata using the
// public Shikimori API.
package shikimori

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
)

const (
	defaultEndpoint  = "https://shikimori.one"
	defaultUserAgent = "mangarr (https://github.com/Asion001/mangarr)"
)

type Settings struct {
	TitleLanguage string `json:"titleLanguage" label:"Title language" options:"russian:Russian (fallback romanized),english:English (fallback romanized),romanized:Romanized" order:"1"`
	Endpoint      string `json:"endpoint" label:"API endpoint" type:"url" order:"2" advanced:"true"`
	UserAgent     string `json:"userAgent" label:"User-Agent" order:"3" help:"Shikimori requires an identifiable application User-Agent." advanced:"true"`
}

func init() {
	modules.Register(&modules.Implementation{
		Kind:        modules.KindMetadata,
		Name:        "shikimori",
		DisplayName: "Shikimori",
		Description: "Russian and international manga metadata from Shikimori: localized titles, synonyms, synopsis, genres, cover and status.",
		InfoURL:     "https://shikimori.one/api/doc",
		Settings: func() any {
			return &Settings{TitleLanguage: "russian", Endpoint: defaultEndpoint, UserAgent: defaultUserAgent}
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
}

type image struct {
	Original string `json:"original"`
	Preview  string `json:"preview"`
}

type manga struct {
	ID       int      `json:"id"`
	Name     string   `json:"name"`
	Russian  string   `json:"russian"`
	English  []string `json:"english"`
	Japanese []string `json:"japanese"`
	Synonyms []string `json:"synonyms"`
	Image    image    `json:"image"`
	URL      string   `json:"url"`
	Kind     string   `json:"kind"`
	Status   string   `json:"status"`
	Chapters int      `json:"chapters"`
	AiredOn  string   `json:"aired_on"`

	Description string `json:"description"`
	MyAnimeList int    `json:"myanimelist_id"`
	Genres      []struct {
		Name    string `json:"name"`
		Russian string `json:"russian"`
	} `json:"genres"`
	Publishers []struct {
		Name string `json:"name"`
	} `json:"publishers"`
}

func (m *Module) Test(ctx context.Context) error {
	_, err := m.Search(ctx, "Атака титанов", 1)
	return err
}

func (m *Module) Search(ctx context.Context, query string, limit int) ([]metadata.SeriesMetadata, error) {
	return m.SearchLanguage(ctx, query, "", limit)
}

// SearchLanguage lets the add flow request localized result titles without
// forcing one global display language on every search.
func (m *Module) SearchLanguage(ctx context.Context, query, language string, limit int) ([]metadata.SeriesMetadata, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	values := url.Values{"search": {query}, "limit": {strconv.Itoa(limit)}}
	var list []manga
	if err := m.get(ctx, "/api/mangas?"+values.Encode(), &list); err != nil {
		return nil, err
	}
	out := make([]metadata.SeriesMetadata, 0, len(list))
	for _, item := range list {
		out = append(out, m.convert(item, language))
	}
	return out, nil
}

func (m *Module) Get(ctx context.Context, id string) (*metadata.SeriesMetadata, error) {
	return m.GetLanguage(ctx, id, "")
}

func (m *Module) GetLanguage(ctx context.Context, id, language string) (*metadata.SeriesMetadata, error) {
	n, err := strconv.Atoi(id)
	if err != nil || n <= 0 {
		return nil, fmt.Errorf("invalid Shikimori id %q", id)
	}
	var item manga
	if err := m.get(ctx, "/api/mangas/"+strconv.Itoa(n), &item); err != nil {
		return nil, err
	}
	md := m.convert(item, language)
	return &md, nil
}

func (m *Module) LookupExternal(ctx context.Context, provider, id string) (*metadata.SeriesMetadata, error) {
	if provider != "mal" && provider != "shikimori" {
		return nil, metadata.ErrNotFound
	}
	return m.Get(ctx, id)
}

func (m *Module) get(ctx context.Context, path string, out any) error {
	m.mu.Lock()
	wait := 700*time.Millisecond - time.Since(m.lastReq)
	m.mu.Unlock()
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	m.mu.Lock()
	m.lastReq = time.Now()
	m.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(m.s.Endpoint, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", m.s.UserAgent)
	resp, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("shikimori: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return metadata.ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("shikimori: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
		return fmt.Errorf("shikimori: %w", err)
	}
	return nil
}

func (m *Module) convert(item manga, requestedLanguage string) metadata.SeriesMetadata {
	language := strings.ToLower(strings.TrimSpace(requestedLanguage))
	if language == "" {
		language = m.s.TitleLanguage
	}
	title := item.Name
	switch language {
	case "ru", "rus", "russian":
		title = firstNonEmpty(item.Russian, first(item.English), item.Name)
	case "en", "eng", "english":
		title = firstNonEmpty(first(item.English), item.Name, item.Russian)
	case "romanized":
		title = firstNonEmpty(item.Name, first(item.English), item.Russian)
	default:
		if containsCyrillic(title) || containsCyrillic(item.Russian) && containsCyrillic(requestedLanguage) {
			title = firstNonEmpty(item.Russian, item.Name)
		}
	}
	alt := append([]string{item.Name, item.Russian}, item.English...)
	alt = append(alt, item.Japanese...)
	alt = append(alt, item.Synonyms...)
	malID := item.MyAnimeList
	if malID == 0 {
		malID = item.ID
	}
	base := strings.TrimRight(m.s.Endpoint, "/")
	pageURL := absolute(base, item.URL)
	out := metadata.SeriesMetadata{
		Provider: "shikimori", ID: strconv.Itoa(item.ID), Title: title, AltTitles: dedupeExcept(alt, title),
		Description: item.Description, Status: status(item.Status), Year: year(item.AiredOn),
		Genres: genres(item.Genres), CoverURL: absolute(base, firstNonEmpty(item.Image.Original, item.Image.Preview)),
		Links:         map[string]string{"Shikimori": pageURL, "MyAnimeList": "https://myanimelist.net/manga/" + strconv.Itoa(malID)},
		ExternalIDs:   map[string]string{"shikimori": strconv.Itoa(item.ID), "mal": strconv.Itoa(malID)},
		TotalChapters: item.Chapters, Format: format(item.Kind), Country: country(item.Kind), URL: pageURL,
	}
	if len(item.Publishers) > 0 {
		out.Publisher = item.Publishers[0].Name
	}
	return out
}

func first(xs []string) string {
	if len(xs) > 0 {
		return xs[0]
	}
	return ""
}

func firstNonEmpty(xs ...string) string {
	for _, value := range xs {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func absolute(base, path string) string {
	if path == "" {
		return ""
	}
	if parsed, err := url.Parse(path); err == nil && parsed.IsAbs() {
		return path
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

func status(value string) string {
	switch value {
	case "released":
		return "completed"
	case "ongoing", "anons":
		return "ongoing"
	case "paused":
		return "hiatus"
	case "discontinued":
		return "cancelled"
	default:
		return "unknown"
	}
}

func format(kind string) string {
	switch kind {
	case "manhwa", "manhua", "manga":
		return kind
	case "one_shot":
		return "oneshot"
	case "light_novel", "novel":
		return "novel"
	default:
		return "manga"
	}
}

func country(kind string) string {
	switch kind {
	case "manhwa":
		return "KR"
	case "manhua":
		return "CN"
	default:
		return "JP"
	}
}

func year(date string) int {
	if len(date) < 4 {
		return 0
	}
	n, _ := strconv.Atoi(date[:4])
	return n
}

// genres are the English names, which mangarr keeps whatever the title's
// language; the web UI translates them.
func genres(in []struct {
	Name    string `json:"name"`
	Russian string `json:"russian"`
}) []string {
	out := make([]string, 0, len(in))
	for _, genre := range in {
		name := firstNonEmpty(genre.Name, genre.Russian)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func dedupeExcept(values []string, except string) []string {
	seen := map[string]bool{strings.ToLower(strings.TrimSpace(except)): true}
	out := []string{}
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		if key != "" && !seen[key] {
			seen[key] = true
			out = append(out, value)
		}
	}
	return out
}

func containsCyrillic(value string) bool {
	for _, r := range value {
		if unicode.In(r, unicode.Cyrillic) {
			return true
		}
	}
	return false
}

var _ metadata.ExternalLookup = (*Module)(nil)
var _ interface {
	SearchLanguage(context.Context, string, string, int) ([]metadata.SeriesMetadata, error)
} = (*Module)(nil)
