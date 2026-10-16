package sites

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// MangaCloud has a JSON API (api.mangacloud.org) for everything, and serves
// covers and pages from its CDN (pika.mangacloud.org). The id is the one
// Mihon's "MangaCloud" extension has.
//
// Urls are kept the way the extension writes them, so a Mihon backup links
// up: a manga is its bare id, a chapter a small JSON object
// ({"comicId":"...","chapterId":"..."}).
var mangacloudID = sourcekit.KeiyoushiID("MangaCloud", "en", 1)

const (
	mangacloudSite = "https://mangacloud.org"
	mangacloudAPI  = "https://api.mangacloud.org"
	mangacloudCDN  = "https://pika.mangacloud.org"
	// the site's page sizes: library search and latest updates
	mangacloudSearchSize = 10
	mangacloudLatestSize = 60
)

func init() {
	sourcekit.Register(mangacloudID, func(d sourcekit.Deps) sourcekit.Site {
		return &mangacloud{c: d.Client, site: mangacloudSite, api: mangacloudAPI, cdn: mangacloudCDN}
	})
}

type mangacloud struct {
	c *sourcekit.Client
	// site, api and cdn are the addresses to talk to (tests point them at a
	// recorded copy).
	site, api, cdn string
}

func (m *mangacloud) Info() sourcekit.Info {
	return sourcekit.Info{ID: mangacloudID, Name: "MangaCloud", Lang: "en", BaseURL: m.site, SupportsBrowse: true,
		IconURL: m.site + "/favicon.ico"}
}

// Politeness: the extension holds itself to one request a second.
func (m *mangacloud) Politeness() sourcekit.Politeness {
	return sourcekit.Politeness{RequestsPerMinute: 60, MaxConcurrent: 1}
}

// mangacloudImage is a cover or a page on the CDN.
type mangacloudImage struct {
	ID     string `json:"id"`
	Format string `json:"f"`
}

// mangacloudBrowse is a manga in a listing.
type mangacloudBrowse struct {
	ID    string          `json:"id"`
	Title string          `json:"title"`
	Cover mangacloudImage `json:"cover"`
}

// json calls the API. Every call answers 409 until the browser has passed a
// Turnstile check and posted its token to /auth/handshake, which mangarr
// can't do; say so instead of a bare status.
func (m *mangacloud) json(ctx context.Context, req sourcekit.Request, out any) error {
	err := m.c.JSON(ctx, req, out)
	var se *sourcekit.StatusError
	if err != nil && errorsAs(err, &se) && se.Code == http.StatusConflict {
		return fmt.Errorf("MangaCloud only answers a browser that passed its Turnstile check: %w", err)
	}
	return err
}

func (m *mangacloud) cover(id string, img mangacloudImage) string {
	if img.ID == "" {
		return ""
	}
	return m.cdn + "/" + id + "/" + img.ID + "." + img.Format
}

func (m *mangacloud) results(list []mangacloudBrowse) []sourcekit.Manga {
	var out []sourcekit.Manga
	for _, b := range list {
		if b.ID == "" {
			continue
		}
		out = append(out, sourcekit.Manga{URL: b.ID, ID: b.ID, Title: b.Title, CoverURL: m.cover(b.ID, b.Cover)})
	}
	return out
}

// Search asks the library endpoint, which wants at least three characters.
func (m *mangacloud) Search(ctx context.Context, query string, page int) (sourcekit.Results, error) {
	q := strings.TrimSpace(query)
	if q != "" && len([]rune(q)) < 3 {
		return sourcekit.Results{}, fmt.Errorf("the search needs at least three characters")
	}
	return m.library(ctx, q, page)
}

func (m *mangacloud) library(ctx context.Context, title string, page int) (sourcekit.Results, error) {
	if page < 1 {
		page = 1
	}
	body, _ := json.Marshal(struct {
		Title string `json:"title,omitempty"`
		Page  int    `json:"page"`
	}{title, page})
	var out struct {
		Data []mangacloudBrowse `json:"data"`
	}
	req := sourcekit.Request{Method: "POST", URL: m.api + "/comic/library", Body: body, Headers: m.headers(true)}
	if err := m.json(ctx, req, &out); err != nil {
		return sourcekit.Results{}, err
	}
	return sourcekit.Results{Mangas: m.results(out.Data), HasNext: len(out.Data) == mangacloudSearchSize}, nil
}

// Popular is the site's own: today's, this week's and this month's most read
// (pages one to three), then the whole library.
func (m *mangacloud) Popular(ctx context.Context, page int) (sourcekit.Results, error) {
	if page < 1 {
		page = 1
	}
	if page > 3 {
		return m.library(ctx, "", page-3)
	}
	span := [...]string{"today", "week", "month"}[page-1]
	var out struct {
		Data struct {
			List []mangacloudBrowse `json:"list"`
		} `json:"data"`
	}
	if err := m.json(ctx, sourcekit.Request{URL: m.api + "/comic-popular-view/" + span, Headers: m.headers(false)}, &out); err != nil {
		return sourcekit.Results{}, err
	}
	return sourcekit.Results{Mangas: m.results(out.Data.List), HasNext: true}, nil
}

func (m *mangacloud) Latest(ctx context.Context, page int) (sourcekit.Results, error) {
	if page < 1 {
		page = 1
	}
	body, _ := json.Marshal(struct {
		Page int `json:"page"`
	}{page})
	var out struct {
		Data struct {
			List []mangacloudBrowse `json:"list"`
		} `json:"data"`
	}
	req := sourcekit.Request{Method: "POST", URL: m.api + "/comic-updates", Body: body, Headers: m.headers(true)}
	if err := m.json(ctx, req, &out); err != nil {
		return sourcekit.Results{}, err
	}
	return sourcekit.Results{Mangas: m.results(out.Data.List), HasNext: len(out.Data.List) == mangacloudLatestSize}, nil
}

// ---- one manga --------------------------------------------------------------

// mangacloudManga is a manga with its chapters, as the API sends it.
type mangacloudManga struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	AltTitles   string          `json:"alt_titles"`
	NatTitles   string          `json:"nat_titles"`
	Description string          `json:"description"`
	Status      string          `json:"status"`
	StartYear   int             `json:"start_year"`
	EndYear     int             `json:"end_year"`
	Type        string          `json:"type"`
	Authors     string          `json:"authors"`
	Artists     string          `json:"artists"`
	Cover       mangacloudImage `json:"cover"`
	Tags        []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"tags"`
	Chapters []struct {
		ID          string  `json:"id"`
		Number      float64 `json:"number"`
		Name        *string `json:"name"`
		CreatedDate string  `json:"created_date"`
	} `json:"chapters"`
}

func (m *mangacloud) comic(ctx context.Context, ref sourcekit.Ref) (*mangacloudManga, error) {
	id := mangacloudComicID(ref)
	if id == "" {
		return nil, fmt.Errorf("%q is not a manga url", ref.URL)
	}
	var out struct {
		Data *mangacloudManga `json:"data"`
	}
	if err := m.json(ctx, sourcekit.Request{URL: m.api + "/comic/" + url.PathEscape(id), Headers: m.headers(false)}, &out); err != nil {
		var se *sourcekit.StatusError
		if errorsAs(err, &se) && se.Code == 404 {
			return nil, fmt.Errorf("%w: manga %s", sourcekit.ErrNotFound, id)
		}
		return nil, err
	}
	if out.Data == nil || out.Data.ID == "" {
		return nil, fmt.Errorf("%w: manga %s", sourcekit.ErrNotFound, id)
	}
	return out.Data, nil
}

func (m *mangacloud) Details(ctx context.Context, ref sourcekit.Ref) (sourcekit.Details, error) {
	c, err := m.comic(ctx, ref)
	if err != nil {
		return sourcekit.Details{}, err
	}
	d := sourcekit.Details{
		Manga:  sourcekit.Manga{URL: c.ID, ID: c.ID, Title: c.Title, CoverURL: m.cover(c.ID, c.Cover)},
		Author: mangacloudPeople(c.Authors), Artist: mangacloudPeople(c.Artists),
		Status: mangacloudStatus(c.Status), WebURL: m.site + "/comic/" + c.ID,
	}
	n := len(c.Chapters)
	d.Chapters = &n
	var desc strings.Builder
	if s := strings.TrimSpace(c.Description); s != "" {
		desc.WriteString(s + "\n\n")
	}
	if c.StartYear > 0 {
		desc.WriteString("Year: " + strconv.Itoa(c.StartYear))
		if c.EndYear > 0 {
			desc.WriteString(" - " + strconv.Itoa(c.EndYear))
		}
		desc.WriteString("\n\n")
	}
	var alt []string
	for _, group := range []struct{ s, sep string }{{c.AltTitles, "•"}, {c.NatTitles, "、"}} {
		if group.s == "" {
			continue
		}
		for _, t := range strings.Split(group.s, group.sep) {
			if t = strings.TrimSpace(t); t != "" {
				alt = append(alt, t)
			}
		}
	}
	if len(alt) > 0 {
		desc.WriteString("Alternative Name(s):\n")
		for _, t := range alt {
			desc.WriteString("- " + t + "\n")
		}
	}
	d.Description = strings.TrimSpace(desc.String())
	if c.Type != "" {
		d.Genres = append(d.Genres, c.Type)
	}
	tags := append(c.Tags[:0:0], c.Tags...)
	sort.SliceStable(tags, func(i, j int) bool { return tags[i].Type < tags[j].Type })
	for _, t := range tags {
		d.Genres = append(d.Genres, t.Name)
	}
	return d, nil
}

// mangacloudPeople turns "A • B" into "A, B".
func mangacloudPeople(s string) string {
	var out []string
	for _, p := range strings.Split(s, "•") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
}

func mangacloudStatus(s string) string {
	switch s {
	case "Ongoing":
		return sourcekit.StatusOngoing
	case "Completed":
		return sourcekit.StatusCompleted
	case "Cancelled":
		return sourcekit.StatusCancelled
	case "Hiatus":
		return sourcekit.StatusHiatus
	}
	return sourcekit.StatusUnknown
}

// mangacloudChapterURL is a chapter's identity as the extension stores it.
type mangacloudChapterURL struct {
	ComicID   string `json:"comicId"`
	ChapterID string `json:"chapterId"`
}

func (m *mangacloud) Chapters(ctx context.Context, ref sourcekit.Ref) ([]sourcekit.Chapter, error) {
	c, err := m.comic(ctx, ref)
	if err != nil {
		return nil, err
	}
	var out []sourcekit.Chapter
	for _, ch := range c.Chapters {
		if ch.ID == "" {
			continue
		}
		u, _ := json.Marshal(mangacloudChapterURL{ComicID: c.ID, ChapterID: ch.ID})
		name := "Chapter " + strconv.FormatFloat(ch.Number, 'f', -1, 32)
		if ch.Name != nil {
			name += " - " + *ch.Name
		}
		chapter := sourcekit.Chapter{URL: string(u), ID: ch.ID, Name: name, Number: ch.Number,
			WebURL: m.site + "/comic/" + c.ID + "/chapter/" + ch.ID}
		// the date is "2006-01-02T15:04:05" in UTC, sometimes with more after it
		if s := ch.CreatedDate; len(s) >= 19 {
			if t, err := time.Parse("2006-01-02T15:04:05", s[:19]); err == nil {
				chapter.UploadedAt = &t
			}
		}
		out = append(out, chapter)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no chapters listed", sourcekit.ErrNotFound)
	}
	return out, nil
}

func (m *mangacloud) Pages(ctx context.Context, ch sourcekit.PageRef) ([]sourcekit.PageImage, error) {
	id := ch.ID
	var u mangacloudChapterURL
	if err := json.Unmarshal([]byte(ch.URL), &u); err == nil && u.ChapterID != "" {
		id = u.ChapterID
	} else if parts := strings.Split(strings.Trim(sourcekit.Path(ch.URL), "/"), "/"); len(parts) == 4 && parts[2] == "chapter" {
		id = parts[3] // a web link, "/comic/<comic>/chapter/<chapter>"
	}
	if id == "" {
		return nil, fmt.Errorf("%q is not a chapter url", ch.URL)
	}
	var out struct {
		Data struct {
			ID      string            `json:"id"`
			ComicID string            `json:"comic_id"`
			Images  []mangacloudImage `json:"images"`
		} `json:"data"`
	}
	if err := m.json(ctx, sourcekit.Request{URL: m.api + "/chapters/" + url.PathEscape(id), Headers: m.headers(false)}, &out); err != nil {
		return nil, err
	}
	var pages []sourcekit.PageImage
	for _, img := range out.Data.Images {
		if img.ID == "" {
			continue
		}
		pages = append(pages, sourcekit.PageImage{Index: len(pages),
			URL:     m.cdn + "/" + out.Data.ComicID + "/" + out.Data.ID + "/" + img.ID + "." + img.Format,
			Headers: map[string]string{"Referer": m.site + "/"}})
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("%w: the chapter has no pages", sourcekit.ErrNotFound)
	}
	return pages, nil
}

func (m *mangacloud) headers(post bool) map[string]string {
	h := map[string]string{"Referer": m.site + "/"}
	if post {
		h["Content-Type"] = "application/json"
	}
	return h
}

// mangacloudComicID reads a manga's id: the stored url is the bare id, but a
// "/comic/<id>" web link works too.
func mangacloudComicID(ref sourcekit.Ref) string {
	raw := strings.TrimSpace(ref.URL)
	parts := strings.Split(strings.Trim(sourcekit.Path(raw), "/"), "/")
	for i, p := range parts {
		if p == "comic" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	if raw != "" && !strings.ContainsAny(raw, "/?#") {
		return raw
	}
	return ref.ID
}
