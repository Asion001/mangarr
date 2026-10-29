package sites

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// MangaLib is one of the lib.social sites: a JSON API shared by all of them
// (a "Site-Id" header picks which), chapters split into translation branches,
// and page images on separate servers the API names.
//
// Each branch is offered as its own chapter, so a series translated by two
// teams arrives as two releases and the profile's scanlator preference
// decides, instead of the site deciding for us.
var mangalibID = sourcekit.KeiyoushiID("MangaLib", "ru", 1)

const (
	mangalibSite   = "https://mangalib.me"
	mangalibAPI    = "https://api.cdnlibs.org"
	mangalibSiteID = 1
)

func init() {
	sourcekit.Register(mangalibID, func(d sourcekit.Deps) sourcekit.Site {
		return &mangalib{c: d.Client, site: mangalibSite, api: mangalibAPI, title: "en", server: "main"}
	})
}

type mangalib struct {
	c *sourcekit.Client
	// site and api are the addresses to talk to (tests point them at a
	// recorded copy).
	site, api string
	// title is the language to prefer titles in.
	title string
	// server is which of the site's image servers to read pages from.
	server string

	mu      sync.Mutex
	servers map[string]string
	fetched time.Time
}

func (m *mangalib) Info() sourcekit.Info {
	return sourcekit.Info{ID: mangalibID, Name: "MangaLib", Lang: "ru", BaseURL: m.site, SupportsBrowse: true,
		IconURL: m.site + "/favicon.ico"}
}

// Politeness: the site's own extension holds itself to one request a second.
func (m *mangalib) Politeness() sourcekit.Politeness {
	return sourcekit.Politeness{RequestsPerMinute: 60, MaxConcurrent: 2}
}

func (m *mangalib) Options() []sourcekit.Option {
	return []sourcekit.Option{
		{Key: "title", Title: "Titles in", Type: "select", Value: m.title,
			Help: "Which of a manga's titles to use when linking and naming it.",
			Choices: []sourcekit.Choice{{Value: "en", Label: "English"}, {Value: "ru", Label: "Russian"},
				{Value: "original", Label: "Original (romaji)"}}},
		{Key: "server", Title: "Image server", Type: "select", Value: m.server,
			Help: "Which of the site's page servers to download from.",
			Choices: []sourcekit.Choice{{Value: "main", Label: "First"}, {Value: "secondary", Label: "Second"},
				{Value: "compress", Label: "Compressed"}}},
	}
}

func (m *mangalib) SetOption(key string, value any) error {
	v, _ := value.(string)
	switch key {
	case "title":
		switch v {
		case "en", "ru", "original":
			m.title = v
			return nil
		}
	case "server":
		switch v {
		case "main", "secondary", "compress":
			m.server = v
			return nil
		}
	default:
		return fmt.Errorf("%w: no option %q", sourcekit.ErrUnsupported, key)
	}
	return fmt.Errorf("%q is not a value for %s", value, key)
}

func (m *mangalib) headers() map[string]string {
	return map[string]string{"Site-Id": strconv.Itoa(mangalibSiteID), "Referer": m.site + "/", "Accept": "application/json"}
}

// ---- results ----------------------------------------------------------------

// mangalibManga is one manga as every listing reports it.
type mangalibManga struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	RusName string `json:"rus_name"`
	EngName string `json:"eng_name"`
	SlugURL string `json:"slug_url"`
	Cover   struct {
		Default   string `json:"default"`
		Thumbnail string `json:"thumbnail"`
	} `json:"cover"`
	Status struct {
		ID    int    `json:"id"`
		Label string `json:"label"`
	} `json:"status"`
}

func (m *mangalib) pickTitle(x mangalibManga) string {
	pick := []string{x.EngName, x.Name, x.RusName}
	switch m.title {
	case "ru":
		pick = []string{x.RusName, x.EngName, x.Name}
	case "original":
		pick = []string{x.Name, x.EngName, x.RusName}
	}
	for _, t := range pick {
		if s := strings.TrimSpace(t); s != "" {
			return s
		}
	}
	return ""
}

func (m *mangalib) manga(x mangalibManga) sourcekit.Manga {
	cover := x.Cover.Default
	if cover == "" {
		cover = x.Cover.Thumbnail
	}
	return sourcekit.Manga{URL: "/" + x.SlugURL, ID: strconv.FormatInt(x.ID, 10), Title: m.pickTitle(x), CoverURL: cover}
}

// list reads one page of any listing endpoint.
func (m *mangalib) list(ctx context.Context, path string, q url.Values) (sourcekit.Results, error) {
	var out struct {
		Data []mangalibManga `json:"data"`
		Meta struct {
			HasNext bool `json:"has_next_page"`
		} `json:"meta"`
	}
	if err := m.c.JSON(ctx, sourcekit.Request{URL: m.api + path, Query: q, Headers: m.headers()}, &out); err != nil {
		return sourcekit.Results{}, err
	}
	res := sourcekit.Results{HasNext: out.Meta.HasNext}
	for _, x := range out.Data {
		if x.SlugURL == "" {
			continue
		}
		res.Mangas = append(res.Mangas, m.manga(x))
	}
	return res, nil
}

func (m *mangalib) Search(ctx context.Context, query string, page int) (sourcekit.Results, error) {
	q := m.listQuery(page)
	if s := strings.TrimSpace(query); s != "" {
		q.Set("q", s)
	}
	return m.list(ctx, "/api/manga", q)
}

func (m *mangalib) Popular(ctx context.Context, page int) (sourcekit.Results, error) {
	return m.list(ctx, "/api/manga", m.listQuery(page))
}

func (m *mangalib) Latest(ctx context.Context, page int) (sourcekit.Results, error) {
	return m.list(ctx, "/api/latest-updates", url.Values{"page": {strconv.Itoa(max(page, 1))}})
}

func (m *mangalib) listQuery(page int) url.Values {
	return url.Values{"page": {strconv.Itoa(max(page, 1))}, "site_id[]": {strconv.Itoa(mangalibSiteID)}}
}

// ---- one manga --------------------------------------------------------------

// mangalibSlug is the "<id>--<slug>" a manga is addressed by. It carries the
// numeric id, which is why the link survives a rename.
func mangalibSlug(raw string) string {
	parts := strings.Split(strings.Trim(sourcekit.Path(raw), "/"), "/")
	for _, p := range parts {
		if strings.Contains(p, "--") { // a whole site url was pasted
			return p
		}
	}
	return parts[0]
}

func (m *mangalib) Details(ctx context.Context, ref sourcekit.Ref) (sourcekit.Details, error) {
	slug := mangalibSlug(ref.URL)
	if slug == "" {
		return sourcekit.Details{}, fmt.Errorf("%q is not a manga url", ref.URL)
	}
	q := url.Values{}
	for _, f := range []string{"eng_name", "otherNames", "summary", "genres", "tags", "authors", "artists", "status_id", "manga_status_id", "rate_avg", "rate"} {
		q.Add("fields[]", f)
	}
	var out struct {
		Data struct {
			mangalibManga
			Summary json.RawMessage `json:"summary"`
			Genres  []struct {
				Name string `json:"name"`
			} `json:"genres"`
			Authors []struct {
				Name string `json:"name"`
			} `json:"authors"`
			Artists []struct {
				Name string `json:"name"`
			} `json:"artists"`
			Rating struct {
				Average string `json:"average"` // out of 10, as text ("9.42")
				Votes   int    `json:"votes"`
			} `json:"rating"`
		} `json:"data"`
	}
	if err := m.c.JSON(ctx, sourcekit.Request{URL: m.api + "/api/manga/" + slug, Query: q, Headers: m.headers()}, &out); err != nil {
		return sourcekit.Details{}, err
	}
	if out.Data.SlugURL == "" {
		return sourcekit.Details{}, fmt.Errorf("%w: %s", sourcekit.ErrNotFound, ref.URL)
	}
	d := sourcekit.Details{Manga: m.manga(out.Data.mangalibManga), Status: mangalibStatus(out.Data.Status.ID, out.Data.Status.Label),
		Description: mangalibText(out.Data.Summary), WebURL: m.site + "/ru/manga/" + out.Data.SlugURL}
	for _, g := range out.Data.Genres {
		d.Genres = append(d.Genres, g.Name)
	}
	var authors, artists []string
	for _, a := range out.Data.Authors {
		authors = append(authors, a.Name)
	}
	for _, a := range out.Data.Artists {
		artists = append(artists, a.Name)
	}
	d.Author, d.Artist = strings.Join(authors, ", "), strings.Join(artists, ", ")
	if score, err := strconv.ParseFloat(out.Data.Rating.Average, 64); err == nil && score > 0 {
		d.Rating = &sourcekit.Rating{Score: score, Max: 10, Votes: out.Data.Rating.Votes}
	}
	return d, nil
}

func mangalibStatus(id int, label string) string {
	switch id {
	case 1:
		return sourcekit.StatusOngoing
	case 2:
		return sourcekit.StatusCompleted
	case 3:
		return sourcekit.StatusHiatus
	case 4:
		return sourcekit.StatusCancelled
	}
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "онгоинг":
		return sourcekit.StatusOngoing
	case "завершён", "завершен":
		return sourcekit.StatusCompleted
	case "приостановлен":
		return sourcekit.StatusHiatus
	case "выпуск прекращён", "выпуск прекращен":
		return sourcekit.StatusCancelled
	}
	return sourcekit.StatusUnknown
}

// mangalibText reads a description, which the API sends either as a plain
// string or as a rich-text document.
func mangalibText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var node struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &node); err != nil {
		return ""
	}
	var parts []string
	if node.Text != "" {
		parts = append(parts, node.Text)
	}
	if len(node.Content) > 0 {
		var kids []json.RawMessage
		if err := json.Unmarshal(node.Content, &kids); err == nil {
			for _, k := range kids {
				if t := mangalibText(k); t != "" {
					parts = append(parts, t)
				}
			}
		}
	}
	sep := " "
	if node.Type == "doc" {
		sep = "\n\n"
	}
	return strings.TrimSpace(strings.Join(parts, sep))
}

// mangalibBranch is one team's translation of a chapter. A chapter with two
// branches is two releases here.
type mangalibBranch struct {
	ID        int64  `json:"id"`
	BranchID  *int64 `json:"branch_id"`
	CreatedAt string `json:"created_at"`
	Teams     []struct {
		Name string `json:"name"`
	} `json:"teams"`
	// RestrictedView is set on a paid chapter; it stays closed to a reader
	// who has not bought it, and the API then serves it with no pages.
	RestrictedView *struct {
		IsOpen bool `json:"is_open"`
	} `json:"restricted_view"`
}

func (m *mangalib) Chapters(ctx context.Context, ref sourcekit.Ref) ([]sourcekit.Chapter, error) {
	slug := mangalibSlug(ref.URL)
	if slug == "" {
		return nil, fmt.Errorf("%q is not a manga url", ref.URL)
	}
	var out struct {
		Data []struct {
			ID       int64            `json:"id"`
			Volume   string           `json:"volume"`
			Number   string           `json:"number"`
			Name     string           `json:"name"`
			Branches []mangalibBranch `json:"branches"`
		} `json:"data"`
	}
	if err := m.c.JSON(ctx, sourcekit.Request{URL: m.api + "/api/manga/" + slug + "/chapters", Headers: m.headers()}, &out); err != nil {
		return nil, err
	}
	var chapters []sourcekit.Chapter
	for _, c := range out.Data {
		number := -1.0
		if n, err := strconv.ParseFloat(c.Number, 64); err == nil {
			number = n
		}
		name := strings.TrimSpace(c.Name)
		if name == "" {
			name = "Глава " + c.Number
		}
		branches := c.Branches
		if len(branches) == 0 {
			branches = []mangalibBranch{{ID: c.ID}}
		}
		for _, b := range branches {
			if b.RestrictedView != nil && !b.RestrictedView.IsOpen {
				continue // paid: listing it would only queue a download that cannot work
			}
			q := url.Values{"number": {c.Number}, "volume": {c.Volume}}
			webQuery := url.Values{}
			if b.BranchID != nil {
				q.Set("branch_id", strconv.FormatInt(*b.BranchID, 10))
				webQuery.Set("bid", strconv.FormatInt(*b.BranchID, 10))
			}
			webURL := fmt.Sprintf("%s/ru/%s/read/v%s/c%s", m.site, slug, c.Volume, c.Number)
			if encoded := webQuery.Encode(); encoded != "" {
				webURL += "?" + encoded
			}
			ch := sourcekit.Chapter{URL: "/" + slug + "/chapter?" + q.Encode(), ID: strconv.FormatInt(b.ID, 10),
				Name: name, Number: number,
				WebURL: webURL}
			var teams []string
			for _, t := range b.Teams {
				teams = append(teams, t.Name)
			}
			ch.Scanlator = strings.Join(teams, ", ")
			if t, err := time.Parse("2006-01-02T15:04:05.000000Z", b.CreatedAt); err == nil {
				ch.UploadedAt = &t
			} else if t, err := time.Parse(time.RFC3339, b.CreatedAt); err == nil {
				ch.UploadedAt = &t
			}
			chapters = append(chapters, ch)
		}
	}
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("%w: no chapters listed", sourcekit.ErrNotFound)
	}
	return chapters, nil
}

func (m *mangalib) Pages(ctx context.Context, ch sourcekit.PageRef) ([]sourcekit.PageImage, error) {
	path := sourcekit.Path(ch.URL)
	slug, query, ok := strings.Cut(strings.TrimPrefix(path, "/"), "/chapter?")
	if !ok || slug == "" {
		return nil, fmt.Errorf("%q is not a chapter url", ch.URL)
	}
	q, err := url.ParseQuery(query)
	if err != nil {
		return nil, fmt.Errorf("%q is not a chapter url: %w", ch.URL, err)
	}
	var out struct {
		Data struct {
			Pages []struct {
				Slug int    `json:"slug"`
				URL  string `json:"url"`
			} `json:"pages"`
		} `json:"data"`
	}
	if err := m.c.JSON(ctx, sourcekit.Request{URL: m.api + "/api/manga/" + slug + "/chapter", Query: q, Headers: m.headers()}, &out); err != nil {
		return nil, err
	}
	server, err := m.imageServer(ctx)
	if err != nil {
		return nil, err
	}
	pages := out.Data.Pages
	sort.SliceStable(pages, func(i, j int) bool { return pages[i].Slug < pages[j].Slug })
	images := make([]sourcekit.PageImage, 0, len(pages))
	for _, p := range pages {
		if p.URL == "" {
			continue
		}
		images = append(images, sourcekit.PageImage{Index: len(images), URL: server + p.URL,
			Headers: map[string]string{"Referer": m.site + "/"}})
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("%w: the chapter has no pages", sourcekit.ErrNotFound)
	}
	return images, nil
}

// imageServer is where pages are served from. The API names its servers per
// site, and moves them, so the answer is re-read every so often.
func (m *mangalib) imageServer(ctx context.Context) (string, error) {
	m.mu.Lock()
	if s := m.servers[m.server]; s != "" && time.Since(m.fetched) < time.Hour {
		m.mu.Unlock()
		return s, nil
	}
	m.mu.Unlock()

	var out struct {
		Data struct {
			ImageServers []struct {
				ID      string `json:"id"`
				URL     string `json:"url"`
				SiteIDs []int  `json:"site_ids"`
			} `json:"imageServers"`
		} `json:"data"`
	}
	q := url.Values{"fields[]": {"imageServers"}}
	if err := m.c.JSON(ctx, sourcekit.Request{URL: m.api + "/api/constants", Query: q, Headers: m.headers()}, &out); err != nil {
		return "", err
	}
	found := map[string]string{}
	for _, s := range out.Data.ImageServers {
		for _, id := range s.SiteIDs {
			if id == mangalibSiteID && s.URL != "" {
				found[s.ID] = strings.TrimRight(s.URL, "/")
			}
		}
	}
	m.mu.Lock()
	m.servers, m.fetched = found, time.Now()
	m.mu.Unlock()
	for _, id := range []string{m.server, "main", "secondary", "compress"} {
		if s := found[id]; s != "" {
			return s, nil
		}
	}
	return "", fmt.Errorf("%w: the site named no image server", sourcekit.ErrNotFound)
}
