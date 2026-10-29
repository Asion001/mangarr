package sites

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// MangaDex speaks the site's official API, so there is nothing to scrape and
// pages come from its image servers directly.
//
// It is one catalog per chapter language, with the ids Mihon's MangaDex
// extension gives its sources, so a library imported from a Mihon backup
// links to the same language here instead of an unknown catalog.
var mangadexLangs = []string{
	"af", "sq", "ar", "az", "eu", "be", "bn", "bg", "my", "ca", "zh-Hans", "zh-Hant",
	"cv", "hr", "cs", "da", "nl", "en", "eo", "et", "fil", "fi", "fr", "ka", "de", "el",
	"he", "hi", "hu", "ga", "id", "it", "ja", "jv", "kk", "ko", "la", "lt", "ms", "mn",
	"ne", "no", "fa", "pl", "pt-BR", "pt", "ro", "ru", "sr", "sk", "es-419", "es", "sv",
	"ta", "te", "th", "tr", "uk", "ur", "uz", "vi",
}

// mangadexID is the English catalog's id.
var mangadexID = sourcekit.KeiyoushiID("MangaDex", "en", 1)

const (
	mangadexAPI  = "https://api.mangadex.org"
	mangadexSite = "https://mangadex.org"
	mangadexCDN  = "https://uploads.mangadex.org"
)

func init() {
	sourcekit.RegisterLangs("MangaDex", 1, mangadexLangs, func(d sourcekit.Deps, lang string) sourcekit.Site {
		return &mangadex{c: d.Client, api: mangadexAPI, site: mangadexSite, cdn: mangadexCDN,
			code: lang, lang: dexLang(lang), ratings: []string{"safe", "suggestive", "erotica"}}
	})
}

// dexLang is the API's code for one of Keiyoushi's language codes.
func dexLang(code string) string {
	switch code {
	case "zh-Hans":
		return "zh"
	case "zh-Hant":
		return "zh-hk"
	case "fil":
		return "tl"
	case "pt-BR":
		return "pt-br"
	case "es-419":
		return "es-la"
	}
	return code
}

type mangadex struct {
	c *sourcekit.Client
	// api, site and cdn are the addresses to talk to (tests point them at a
	// recorded copy).
	api, site, cdn string
	// code is the catalog's language as Keiyoushi writes it ("pt-BR"), lang
	// the API's code for it ("pt-br"): chapters and titles in that language.
	code, lang string
	// ratings are the content ratings to show.
	ratings []string
}

func (m *mangadex) Info() sourcekit.Info {
	return sourcekit.Info{ID: sourcekit.KeiyoushiID("MangaDex", m.code, 1), Name: "MangaDex", Lang: m.code, BaseURL: m.site,
		SupportsBrowse: true, IconURL: m.site + "/favicon.ico"}
}

// Politeness: MangaDex asks for at most 5 requests a second across its API.
func (m *mangadex) Politeness() sourcekit.Politeness {
	return sourcekit.Politeness{RequestsPerMinute: 240, MaxConcurrent: 3}
}

func (m *mangadex) Options() []sourcekit.Option {
	return []sourcekit.Option{
		{Key: "adult", Title: "Show pornographic titles", Type: "switch", Value: m.hasRating("pornographic")},
	}
}

func (m *mangadex) SetOption(key string, value any) error {
	switch key {
	case "adult":
		on, _ := value.(bool)
		m.ratings = []string{"safe", "suggestive", "erotica"}
		if on {
			m.ratings = append(m.ratings, "pornographic")
		}
	default:
		return fmt.Errorf("unknown option %q", key)
	}
	return nil
}

func (m *mangadex) hasRating(r string) bool {
	for _, x := range m.ratings {
		if x == r {
			return true
		}
	}
	return false
}

// ---- the API's shapes (only the fields we use) ------------------------------

type mdList struct {
	Data  []mdManga `json:"data"`
	Limit int       `json:"limit"`
	Total int       `json:"total"`
}

type mdManga struct {
	ID         string `json:"id"`
	Attributes struct {
		Title                   map[string]string   `json:"title"`
		AltTitles               []map[string]string `json:"altTitles"`
		Description             map[string]string   `json:"description"`
		Status                  string              `json:"status"`
		Year                    int                 `json:"year"`
		ContentRating           string              `json:"contentRating"`
		LastChapter             string              `json:"lastChapter"`
		AvailableTranslatedLang []string            `json:"availableTranslatedLanguages"`
		Tags                    []struct {
			Attributes struct {
				Name map[string]string `json:"name"`
			} `json:"attributes"`
		} `json:"tags"`
	} `json:"attributes"`
	Relationships []struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Name     string `json:"name"`
			FileName string `json:"fileName"`
		} `json:"attributes"`
	} `json:"relationships"`
}

type mdChapters struct {
	Data []struct {
		ID         string `json:"id"`
		Attributes struct {
			Volume             string    `json:"volume"`
			Chapter            string    `json:"chapter"`
			Title              string    `json:"title"`
			TranslatedLanguage string    `json:"translatedLanguage"`
			ExternalURL        string    `json:"externalUrl"`
			PublishAt          time.Time `json:"publishAt"`
			Pages              int       `json:"pages"`
		} `json:"attributes"`
		Relationships []struct {
			ID         string `json:"id"`
			Type       string `json:"type"`
			Attributes struct {
				Name string `json:"name"`
			} `json:"attributes"`
		} `json:"relationships"`
	} `json:"data"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
	Total  int `json:"total"`
}

// ---- Site -------------------------------------------------------------------

const mangadexPageSize = 24

func (m *mangadex) Search(ctx context.Context, query string, page int) (sourcekit.Results, error) {
	q := m.listQuery(page)
	if strings.TrimSpace(query) != "" {
		q.Set("title", query)
	}
	q.Set("order[relevance]", "desc")
	return m.list(ctx, q)
}

func (m *mangadex) Popular(ctx context.Context, page int) (sourcekit.Results, error) {
	q := m.listQuery(page)
	q.Set("order[followedCount]", "desc")
	return m.list(ctx, q)
}

func (m *mangadex) Latest(ctx context.Context, page int) (sourcekit.Results, error) {
	q := m.listQuery(page)
	q.Set("order[latestUploadedChapter]", "desc")
	q.Set("hasAvailableChapters", "true")
	q.Add("availableTranslatedLanguage[]", m.lang)
	return m.list(ctx, q)
}

func (m *mangadex) listQuery(page int) url.Values {
	if page < 1 {
		page = 1
	}
	q := url.Values{}
	q.Set("limit", strconv.Itoa(mangadexPageSize))
	q.Set("offset", strconv.Itoa((page-1)*mangadexPageSize))
	q.Add("includes[]", "cover_art")
	for _, r := range m.ratings {
		q.Add("contentRating[]", r)
	}
	return q
}

func (m *mangadex) list(ctx context.Context, q url.Values) (sourcekit.Results, error) {
	var out mdList
	if err := m.c.JSON(ctx, sourcekit.Request{URL: m.api + "/manga", Query: q}, &out); err != nil {
		return sourcekit.Results{}, err
	}
	res := sourcekit.Results{Mangas: make([]sourcekit.Manga, 0, len(out.Data))}
	for _, x := range out.Data {
		res.Mangas = append(res.Mangas, m.manga(x))
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	res.HasNext = offset+len(out.Data) < out.Total
	return res, nil
}

func (m *mangadex) manga(x mdManga) sourcekit.Manga {
	out := sourcekit.Manga{URL: "/manga/" + x.ID, Title: pickText(x.Attributes.Title, m.lang)}
	if out.Title == "" {
		for _, alt := range x.Attributes.AltTitles {
			if t := pickText(alt, m.lang); t != "" {
				out.Title = t
				break
			}
		}
	}
	for _, rel := range x.Relationships {
		if rel.Type == "cover_art" && rel.Attributes.FileName != "" {
			out.CoverURL = fmt.Sprintf("%s/covers/%s/%s.512.jpg", m.cdn, x.ID, rel.Attributes.FileName)
		}
	}
	return out
}

func (m *mangadex) Details(ctx context.Context, ref sourcekit.Ref) (sourcekit.Details, error) {
	id, err := mangaID(ref.URL)
	if err != nil {
		return sourcekit.Details{}, err
	}
	var out struct {
		Data mdManga `json:"data"`
	}
	q := url.Values{}
	q.Add("includes[]", "cover_art")
	q.Add("includes[]", "author")
	q.Add("includes[]", "artist")
	if err := m.c.JSON(ctx, sourcekit.Request{URL: m.api + "/manga/" + id, Query: q}, &out); err != nil {
		return sourcekit.Details{}, notFound(err)
	}
	x := out.Data
	d := sourcekit.Details{Manga: m.manga(x), Description: pickText(x.Attributes.Description, m.lang),
		Status: mangadexStatus(x.Attributes.Status), WebURL: m.site + "/title/" + id}
	for _, rel := range x.Relationships {
		switch rel.Type {
		case "author":
			d.Author = rel.Attributes.Name
		case "artist":
			d.Artist = rel.Attributes.Name
		}
	}
	for _, t := range x.Attributes.Tags {
		if name := pickText(t.Attributes.Name, "en"); name != "" {
			d.Genres = append(d.Genres, name)
		}
	}
	d.Rating = m.rating(ctx, id)
	return d, nil
}

// rating is the title's score from MangaDex's statistics; a failure only
// leaves it out.
func (m *mangadex) rating(ctx context.Context, id string) *sourcekit.Rating {
	var out struct {
		Statistics map[string]struct {
			Rating struct {
				Bayesian     float64        `json:"bayesian"`
				Distribution map[string]int `json:"distribution"`
			} `json:"rating"`
		} `json:"statistics"`
	}
	if err := m.c.JSON(ctx, sourcekit.Request{URL: m.api + "/statistics/manga/" + id}, &out); err != nil {
		return nil
	}
	st := out.Statistics[id].Rating
	if st.Bayesian <= 0 {
		return nil
	}
	votes := 0
	for _, n := range st.Distribution {
		votes += n
	}
	return &sourcekit.Rating{Score: math.Round(st.Bayesian*100) / 100, Max: 10, Votes: votes}
}

func mangadexStatus(s string) string {
	switch s {
	case "ongoing":
		return sourcekit.StatusOngoing
	case "completed":
		return sourcekit.StatusCompleted
	case "hiatus":
		return sourcekit.StatusHiatus
	case "cancelled":
		return sourcekit.StatusCancelled
	}
	return sourcekit.StatusUnknown
}

func (m *mangadex) Chapters(ctx context.Context, ref sourcekit.Ref) ([]sourcekit.Chapter, error) {
	id, err := mangaID(ref.URL)
	if err != nil {
		return nil, err
	}
	var out []sourcekit.Chapter
	const limit = 500
	for offset := 0; ; offset += limit {
		q := url.Values{}
		q.Set("limit", strconv.Itoa(limit))
		q.Set("offset", strconv.Itoa(offset))
		q.Add("translatedLanguage[]", m.lang)
		q.Add("includes[]", "scanlation_group")
		q.Set("order[volume]", "desc")
		q.Set("order[chapter]", "desc")
		for _, r := range m.ratings {
			q.Add("contentRating[]", r)
		}
		var page mdChapters
		if err := m.c.JSON(ctx, sourcekit.Request{URL: m.api + "/manga/" + id + "/feed", Query: q}, &page); err != nil {
			return nil, notFound(err)
		}
		for _, c := range page.Data {
			if c.Attributes.ExternalURL != "" {
				continue // hosted somewhere else; mangarr can't download it
			}
			ch := sourcekit.Chapter{URL: "/chapter/" + c.ID, Number: -1, WebURL: m.site + "/chapter/" + c.ID}
			if n, err := strconv.ParseFloat(c.Attributes.Chapter, 64); err == nil {
				ch.Number = n
			}
			ch.Name = chapterName(c.Attributes.Volume, c.Attributes.Chapter, c.Attributes.Title)
			for _, rel := range c.Relationships {
				if rel.Type == "scanlation_group" && rel.Attributes.Name != "" {
					ch.Scanlator = rel.Attributes.Name
				}
			}
			at := c.Attributes.PublishAt
			if !at.IsZero() {
				ch.UploadedAt = &at
			}
			out = append(out, ch)
		}
		if offset+limit >= page.Total || len(page.Data) == 0 {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number > out[j].Number })
	return out, nil
}

// chapterName is what a chapter is called ("Vol. 2 Ch. 14 – The Duel").
func chapterName(volume, chapter, title string) string {
	var parts []string
	if volume != "" {
		parts = append(parts, "Vol. "+volume)
	}
	if chapter != "" {
		parts = append(parts, "Ch. "+chapter)
	}
	name := strings.Join(parts, " ")
	switch {
	case name == "" && title == "":
		return "Oneshot"
	case name == "":
		return title
	case title == "":
		return name
	}
	return name + " – " + title
}

func (m *mangadex) Pages(ctx context.Context, ch sourcekit.PageRef) ([]sourcekit.PageImage, error) {
	id := strings.TrimPrefix(sourcekit.Path(ch.URL), "/chapter/")
	if id == "" || strings.Contains(id, "/") {
		return nil, fmt.Errorf("%q is not a chapter url", ch.URL)
	}
	var out struct {
		BaseURL string `json:"baseUrl"`
		Chapter struct {
			Hash string   `json:"hash"`
			Data []string `json:"data"`
		} `json:"chapter"`
	}
	if err := m.c.JSON(ctx, sourcekit.Request{URL: m.api + "/at-home/server/" + id}, &out); err != nil {
		return nil, notFound(err)
	}
	pages := make([]sourcekit.PageImage, 0, len(out.Chapter.Data))
	for i, name := range out.Chapter.Data {
		pages = append(pages, sourcekit.PageImage{
			Index:   i,
			URL:     fmt.Sprintf("%s/data/%s/%s", strings.TrimRight(out.BaseURL, "/"), out.Chapter.Hash, name),
			Headers: map[string]string{"Referer": m.site + "/"},
		})
	}
	return pages, nil
}

// mangaID is the uuid in "/manga/<id>" (or a bare id).
func mangaID(raw string) (string, error) {
	p := strings.Trim(sourcekit.Path(raw), "/")
	p = strings.TrimPrefix(p, "manga/")
	p = strings.TrimPrefix(p, "title/")
	if p == "" || strings.Contains(p, "/") {
		return "", fmt.Errorf("%q is not a manga url", raw)
	}
	return p, nil
}

// notFound turns the site's 404 into the one mangarr knows.
func notFound(err error) error {
	var se *sourcekit.StatusError
	if errorsAs(err, &se) && se.Code == 404 {
		return fmt.Errorf("%w: %v", sourcekit.ErrNotFound, err)
	}
	return err
}

// pickText takes the wanted language, else English, else anything.
func pickText(m map[string]string, lang string) string {
	if v := strings.TrimSpace(m[lang]); v != "" {
		return v
	}
	if v := strings.TrimSpace(m["en"]); v != "" {
		return v
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v := strings.TrimSpace(m[k]); v != "" {
			return v
		}
	}
	return ""
}
