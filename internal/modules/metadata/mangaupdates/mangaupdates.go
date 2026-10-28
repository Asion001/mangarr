// Package mangaupdates implements a metadata module backed by the public
// MangaUpdates (Baka-Updates) API (no API key required). It knows manhwa,
// manhua and novels that AniList often lacks.
package mangaupdates

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
)

const (
	defaultEndpoint  = "https://api.mangaupdates.com"
	defaultUserAgent = "mangarr (https://github.com/Asion001/mangarr)"
	// maxTags keeps the most voted categories; a popular series has hundreds.
	maxTags = 25
)

type Settings struct {
	MinCategoryVotes int    `json:"minCategoryVotes" label:"Minimum category votes" order:"1" help:"Only MangaUpdates categories with at least this many votes are imported as tags." advanced:"true"`
	Endpoint         string `json:"endpoint" label:"API endpoint" type:"url" order:"2" advanced:"true"`
	UserAgent        string `json:"userAgent" label:"User-Agent" order:"3" advanced:"true"`
}

func init() {
	modules.Register(&modules.Implementation{
		Kind:        modules.KindMetadata,
		Name:        "mangaupdates",
		DisplayName: "MangaUpdates",
		Description: "Series metadata from MangaUpdates: titles, synopsis, authors, genres, categories, cover and status. Strong on manhwa, manhua and novels.",
		InfoURL:     "https://www.mangaupdates.com",
		Settings: func() any {
			return &Settings{MinCategoryVotes: 10, Endpoint: defaultEndpoint, UserAgent: defaultUserAgent}
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
	// backoff pauses calls while MangaUpdates is down.
	backoff metadata.Backoff
}

type series struct {
	ID         int64  `json:"series_id"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	Associated []struct {
		Title string `json:"title"`
	} `json:"associated"`
	Description string `json:"description"`
	Image       struct {
		URL struct {
			Original string `json:"original"`
			Thumb    string `json:"thumb"`
		} `json:"url"`
	} `json:"image"`
	Type   string `json:"type"`
	Year   string `json:"year"`
	Genres []struct {
		Genre string `json:"genre"`
	} `json:"genres"`
	Categories []struct {
		Category string `json:"category"`
		Votes    int    `json:"votes"`
	} `json:"categories"`
	LatestChapter int    `json:"latest_chapter"`
	Status        string `json:"status"`
	Completed     bool   `json:"completed"`
	Authors       []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"authors"`
	Publishers []struct {
		Name string `json:"publisher_name"`
		Type string `json:"type"`
	} `json:"publishers"`
}

// Test always asks MangaUpdates, even while calls are paused, so a health
// check or the settings test sees the real state (and a success ends the pause).
func (m *Module) Test(ctx context.Context) error {
	var out json.RawMessage
	err := m.fetch(ctx, http.MethodPost, "/v1/series/search", map[string]any{"search": "One Piece", "perpage": 1}, &out)
	m.backoff.Record(err)
	return err
}

func (m *Module) Search(ctx context.Context, q string, limit int) ([]metadata.SeriesMetadata, error) {
	if limit <= 0 || limit > 25 {
		limit = 10
	}
	var out struct {
		Results []struct {
			Record series `json:"record"`
		} `json:"results"`
	}
	if err := m.call(ctx, http.MethodPost, "/v1/series/search", map[string]any{"search": q, "perpage": limit}, &out); err != nil {
		return nil, err
	}
	res := make([]metadata.SeriesMetadata, 0, limit)
	for _, r := range out.Results {
		if len(res) == limit {
			break
		}
		res = append(res, m.convert(r.Record))
	}
	return res, nil
}

func (m *Module) Get(ctx context.Context, id string) (*metadata.SeriesMetadata, error) {
	n, err := parseID(id)
	if err != nil {
		return nil, err
	}
	var s series
	if err := m.call(ctx, http.MethodGet, "/v1/series/"+strconv.FormatInt(n, 10), nil, &s); err != nil {
		return nil, err
	}
	md := m.convert(s)
	return &md, nil
}

// LookupExternal only knows MangaUpdates' own ids: the site links no other
// provider, so other providers join it by title.
func (m *Module) LookupExternal(ctx context.Context, provider, id string) (*metadata.SeriesMetadata, error) {
	if provider != "mangaupdates" {
		return nil, metadata.ErrNotFound
	}
	return m.Get(ctx, id)
}

// parseID takes a series id, or the base36 slug of one that the site's urls
// and some trackers use ("pb8uwds").
func parseID(id string) (int64, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		n, err = strconv.ParseInt(id, 36, 64)
	}
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid MangaUpdates id %q", id)
	}
	return n, nil
}

func (m *Module) call(ctx context.Context, method, path string, body, out any) error {
	if err := m.backoff.Check(); err != nil {
		return err
	}
	err := m.fetch(ctx, method, path, body, out)
	m.backoff.Record(err)
	return err
}

func (m *Module) fetch(ctx context.Context, method, path string, body, out any) error {
	// MangaUpdates documents no limit; a second apart keeps a big refresh polite.
	for {
		m.mu.Lock()
		wait := time.Second - time.Since(m.lastReq)
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

	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(m.s.Endpoint, "/")+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", m.s.UserAgent)
	resp, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("mangaupdates: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return metadata.ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return metadata.StatusError("mangaupdates", resp)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
		return fmt.Errorf("mangaupdates: %w", err)
	}
	return nil
}

func (m *Module) convert(s series) metadata.SeriesMetadata {
	id := strconv.FormatInt(s.ID, 10)
	alt := []string{}
	for _, a := range s.Associated {
		alt = append(alt, a.Title)
	}
	year, _ := strconv.Atoi(strings.TrimSpace(s.Year))
	out := metadata.SeriesMetadata{
		Provider: "mangaupdates", ID: id, Title: strings.TrimSpace(s.Title), AltTitles: dedupeExcept(alt, s.Title),
		Description: cleanDescription(s.Description), Year: year, URL: s.URL,
		CoverURL: firstNonEmpty(s.Image.URL.Original, s.Image.URL.Thumb), TotalChapters: s.LatestChapter,
		Links:       map[string]string{"MangaUpdates": s.URL},
		ExternalIDs: map[string]string{"mangaupdates": id},
	}
	out.Format, out.Country = format(s.Type)
	// search results carry no status, authors or categories
	if s.Status != "" || s.Completed {
		out.Status = status(s.Status, s.Completed)
	}
	for _, g := range s.Genres {
		out.Genres = append(out.Genres, g.Genre)
		switch g.Genre {
		case "Hentai", "Adult":
			out.Adult, out.AgeRating = true, metadata.RatingAdult
		case "Smut", "Mature":
			if !out.Adult {
				out.AgeRating = metadata.RatingMature
			}
		}
	}
	cats := s.Categories
	sort.SliceStable(cats, func(i, j int) bool { return cats[i].Votes > cats[j].Votes })
	for _, c := range cats {
		if c.Votes >= m.s.MinCategoryVotes && len(out.Tags) < maxTags {
			out.Tags = append(out.Tags, c.Category)
		}
	}
	for _, a := range s.Authors {
		switch a.Type {
		case "Author":
			out.Authors = append(out.Authors, a.Name)
		case "Artist":
			out.Artists = append(out.Artists, a.Name)
		}
	}
	out.Authors, out.Artists = dedupeExcept(out.Authors, ""), dedupeExcept(out.Artists, "")
	for _, p := range s.Publishers {
		if p.Type == "Original" {
			out.Publisher = p.Name
			break
		}
	}
	return out
}

var parenStatus = regexp.MustCompile(`\(([^)]*)\)`)

// status reads MangaUpdates' free-text status, like "115 Volumes (Ongoing)".
func status(s string, completed bool) string {
	if completed {
		return "completed"
	}
	word := ""
	if m := parenStatus.FindAllStringSubmatch(s, -1); len(m) > 0 {
		word = strings.ToLower(m[0][1])
	}
	switch {
	case strings.Contains(word, "ongoing"):
		return "ongoing"
	case strings.Contains(word, "complete"):
		return "completed"
	case strings.Contains(word, "hiatus"):
		return "hiatus"
	case strings.Contains(word, "cancel"), strings.Contains(word, "discontinued"), strings.Contains(word, "dropped"):
		return "cancelled"
	}
	return "unknown"
}

func format(kind string) (string, string) {
	switch kind {
	case "Manhwa":
		return "manhwa", "KR"
	case "Manhua":
		return "manhua", "CN"
	case "Novel":
		return "novel", ""
	case "OEL":
		return "comic", ""
	case "Manga", "Doujinshi":
		return "manga", "JP"
	}
	return "manga", ""
}

var (
	mdLink     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdEmphasis = regexp.MustCompile(`\*\*|__`)
	blankLines = regexp.MustCompile(`\n{3,}`)
)

// cleanDescription turns MangaUpdates' Markdown into plain text: links keep
// their text, and the line-break spaces go.
func cleanDescription(s string) string {
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdEmphasis.ReplaceAllString(s, "")
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(blankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
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
