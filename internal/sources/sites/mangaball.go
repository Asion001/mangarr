package sites

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// Manga Ball serves everything from a JSON API: lists, a manga's details, its
// chapters (every language at once) and a chapter's pages.
//
// It is one catalog per language, with the ids Keiyoushi's "Manga Ball"
// sources have.
var mballLangs = []string{
	"ar", "bg", "bn", "ca", "cs", "da", "de", "el", "en", "es", "fa", "fi", "fr", "he", "hi", "hu",
	"id", "it", "is", "ja", "ko", "kn", "ml", "ms", "ne", "nl", "no", "pl", "pt-BR", "ro", "ru", "sk",
	"sl", "sq", "sr", "sv", "ta", "th", "tr", "uk", "vi", "zh",
}

// mballSiteLangs are the site's codes for a language where they differ from
// Keiyoushi's; a catalog asks for every regional variant ("es" also takes
// "es-mx", "es-ar", ...), as the extension does.
var mballSiteLangs = map[string][]string{
	"ca":    {"ca", "ca-ad", "ca-es", "ca-fr", "ca-it", "ca-pt"},
	"es":    {"es", "es-ar", "es-mx", "es-es", "es-la", "es-419"},
	"it":    {"it", "it-it"},
	"is":    {"ib", "ib-is", "is"},
	"ja":    {"ja", "jp"},
	"ko":    {"ko", "kr"},
	"kn":    {"kn", "kn-in", "kn-my", "kn-sg", "kn-tw"},
	"ml":    {"ml", "ml-in", "ml-my", "ml-sg", "ml-tw"},
	"nl":    {"nl", "nl-be"},
	"pt-BR": {"pt-br", "pt-pt"},
	"sr":    {"sr", "sr-cyrl"},
	"th":    {"th", "th-hk", "th-kh", "th-la", "th-my", "th-sg"},
	"zh":    {"zh", "zh-cn", "zh-hk", "zh-mo", "zh-sg", "zh-tw", "cn"},
}

const mballSite = "https://mangaball.com"

// mballCovers is where the site keeps the covers it hosts itself.
const mballCovers = "https://bulbasaur.poke-black-and-white.net/covers/"

func init() {
	sourcekit.RegisterLangs("Manga Ball", 1, mballLangs, func(d sourcekit.Deps, lang string) sourcekit.Site {
		return &mangaball{c: d.Client, base: mballSite, lang: lang, adult: true}
	})
}

type mangaball struct {
	c *sourcekit.Client
	// base is the site (tests point it at a recorded copy).
	base string
	// lang is the catalog's language as Keiyoushi writes it.
	lang string
	// adult shows 18+ titles.
	adult bool
}

func (m *mangaball) Info() sourcekit.Info {
	return sourcekit.Info{ID: sourcekit.KeiyoushiID("Manga Ball", m.lang, 1), Name: "Manga Ball", Lang: m.lang,
		BaseURL: m.base, SupportsBrowse: true, IconURL: m.base + "/favicon.ico"}
}

// Politeness: the extension sets no limit of its own, so stay gentle.
func (m *mangaball) Politeness() sourcekit.Politeness {
	return sourcekit.Politeness{RequestsPerMinute: 60, MaxConcurrent: 2}
}

func (m *mangaball) Options() []sourcekit.Option {
	return []sourcekit.Option{{Key: "adult", Title: "Show 18+ titles", Type: "switch", Value: m.adult,
		Help: "On by default, as in the extension."}}
}

func (m *mangaball) SetOption(key string, value any) error {
	if key != "adult" {
		return fmt.Errorf("%w: no option %q", sourcekit.ErrUnsupported, key)
	}
	switch v := value.(type) {
	case bool:
		m.adult = v
	case string:
		m.adult = v == "true" || v == "1"
	default:
		return fmt.Errorf("%v is not a switch", value)
	}
	return nil
}

// siteLangs are the site's codes for the catalog's language.
func (m *mangaball) siteLangs() []string {
	if l, ok := mballSiteLangs[m.lang]; ok {
		return l
	}
	return []string{m.lang}
}

func (m *mangaball) headers() map[string]string {
	return map[string]string{"Referer": m.base + "/", "Accept": "application/json"}
}

// ---- lists ------------------------------------------------------------------

func (m *mangaball) Search(ctx context.Context, query string, page int) (sourcekit.Results, error) {
	query = strings.TrimSpace(query)
	sortBy := "" // the API orders by relevance only when no sort is given
	if query == "" {
		sortBy = "lastupdate"
	}
	return m.list(ctx, max(page, 1), query, sortBy)
}

func (m *mangaball) Popular(ctx context.Context, page int) (sourcekit.Results, error) {
	return m.list(ctx, max(page, 1), "", "views")
}

func (m *mangaball) Latest(ctx context.Context, page int) (sourcekit.Results, error) {
	return m.list(ctx, max(page, 1), "", "lastupdate")
}

// mballImage is a title's cover in its several mirrors.
type mballImage struct {
	File           string `json:"file"`
	CDNMangadex    string `json:"cdn_mangadex"`
	CDNMangaupdate string `json:"cdn_mangaupdate"`
	Cover          *struct {
		Path string `json:"path"`
	} `json:"cover"`
}

// url prefers the cover the site hosts itself, as the site does.
func (i *mballImage) url() string {
	if i == nil {
		return ""
	}
	if i.Cover != nil {
		if p := strings.TrimSpace(strings.ReplaceAll(i.Cover.Path, `\`, "/")); p != "" {
			if strings.HasPrefix(p, "http") {
				return p
			}
			return mballCovers + p
		}
	}
	for _, u := range []string{i.File, i.CDNMangadex, i.CDNMangaupdate} {
		if strings.TrimSpace(u) != "" {
			return u
		}
	}
	return ""
}

func (m *mangaball) list(ctx context.Context, page int, query, sortBy string) (sourcekit.Results, error) {
	q := url.Values{"page": {strconv.Itoa(page)}, "limit": {"24"}, "tag_mode": {"AND"}, "adult_mode": {"all"}}
	if !m.adult {
		q.Set("adult_mode", "no_18")
	}
	if sortBy != "" {
		q.Set("sort_by", sortBy)
		q.Set("sort_order", "desc")
	}
	if query != "" {
		q.Set("keyword", query)
	}
	var out struct {
		Data []struct {
			ID    string      `json:"id"`
			Slug  string      `json:"slug"`
			Name  string      `json:"name"`
			Image *mballImage `json:"image"`
		} `json:"data"`
		Pagination struct {
			Page       int `json:"page"`
			TotalPages int `json:"total_pages"`
		} `json:"pagination"`
	}
	if err := m.c.JSON(ctx, sourcekit.Request{URL: m.base + "/api/v1/title/search-advanced", Query: q, Headers: m.headers()}, &out); err != nil {
		return sourcekit.Results{}, err
	}
	var res sourcekit.Results
	for _, x := range out.Data {
		if x.Slug == "" || res.Has(x.Slug) {
			continue
		}
		res.Mangas = append(res.Mangas, sourcekit.Manga{URL: x.Slug, ID: x.ID, Title: strings.TrimSpace(x.Name), CoverURL: x.Image.url()})
	}
	res.HasNext = out.Pagination.Page < out.Pagination.TotalPages
	return res, nil
}

// mballSlug is what Keiyoushi stores as a manga's url: the slug in
// "/title-detail/<slug>/" (a bare slug is taken as it is).
func mballSlug(raw string) string {
	parts := strings.Split(strings.Trim(sourcekit.Path(raw), "/"), "/")
	switch {
	case len(parts) >= 2 && (parts[0] == "title-detail" || parts[0] == "chapter-detail"):
		return parts[1]
	case len(parts) == 1:
		return parts[0]
	}
	return ""
}

// mballOldID is the title id the site's older links ended in
// ("berserk-68514a...").
var mballOldID = regexp.MustCompile(`-([0-9a-f]{24})$`)

// titleKey is what the API knows a manga by: its id when there is one,
// otherwise its slug.
func mballTitleKey(ref sourcekit.Ref) string {
	if ref.ID != "" {
		return ref.ID
	}
	slug := mballSlug(ref.URL)
	if m := mballOldID.FindStringSubmatch(slug); m != nil {
		return m[1]
	}
	return slug
}

// ---- one manga --------------------------------------------------------------

func (m *mangaball) title(ctx context.Context, ref sourcekit.Ref) (sourcekit.Details, error) {
	key := mballTitleKey(ref)
	if key == "" {
		return sourcekit.Details{}, fmt.Errorf("%q is not a manga url", ref.URL)
	}
	var out struct {
		Data struct {
			ID            string      `json:"id"`
			Slug          string      `json:"slug"`
			Name          string      `json:"name"`
			Image         *mballImage `json:"image"`
			Description   []string    `json:"description"`
			AlternateName []string    `json:"alternateName"`
			Status        string      `json:"status"`
			Tags          []struct {
				Name string `json:"name"`
			} `json:"tags"`
			Author []struct {
				Name string `json:"name"`
			} `json:"author"`
		} `json:"data"`
	}
	if err := m.c.JSON(ctx, sourcekit.Request{URL: m.base + "/api/v1/title/detail/" + url.PathEscape(key), Headers: m.headers()}, &out); err != nil {
		return sourcekit.Details{}, notFound(err)
	}
	t := out.Data
	if t.ID == "" || t.Name == "" {
		return sourcekit.Details{}, fmt.Errorf("%w: no title %s", sourcekit.ErrNotFound, key)
	}
	d := sourcekit.Details{Manga: sourcekit.Manga{URL: t.Slug, ID: t.ID, Title: strings.TrimSpace(t.Name), CoverURL: t.Image.url()},
		WebURL: m.base + "/title-detail/" + t.ID, Status: sourcekit.StatusUnknown}
	for _, g := range t.Tags {
		if g.Name != "" {
			d.Genres = append(d.Genres, g.Name)
		}
	}
	authors := make([]string, 0, len(t.Author))
	for _, a := range t.Author {
		authors = append(authors, a.Name)
	}
	d.Author = strings.Join(authors, ", ")
	desc := strings.TrimSpace(strings.Join(t.Description, "\n\n"))
	if len(t.AlternateName) > 0 {
		desc += "\n\nAlternative Names:\n- " + strings.Join(t.AlternateName, "\n- ")
	}
	d.Description = strings.TrimSpace(desc)
	switch t.Status {
	case "ongoing":
		d.Status = sourcekit.StatusOngoing
	case "completed":
		d.Status = sourcekit.StatusCompleted
	case "hiatus":
		d.Status = sourcekit.StatusHiatus
	case "cancelled":
		d.Status = sourcekit.StatusCancelled
	}
	return d, nil
}

func (m *mangaball) Details(ctx context.Context, ref sourcekit.Ref) (sourcekit.Details, error) {
	return m.title(ctx, ref)
}

func (m *mangaball) Chapters(ctx context.Context, ref sourcekit.Ref) ([]sourcekit.Chapter, error) {
	id := ref.ID
	if id == "" {
		key := mballTitleKey(ref)
		if mballOldID.MatchString("-" + key) {
			id = key
		} else {
			d, err := m.title(ctx, ref)
			if err != nil {
				return nil, err
			}
			id = d.ID
		}
	}
	body, err := json.Marshal(map[string]string{"title_id": id})
	if err != nil {
		return nil, err
	}
	h := m.headers()
	h["Content-Type"] = "application/json"
	var out struct {
		Data []struct {
			ID        string   `json:"id"`
			Name      string   `json:"name"`
			Number    *float64 `json:"number"`
			Volume    float64  `json:"volume"`
			Lang      string   `json:"lang"`
			CreatedAt string   `json:"created_at"`
			Group     *struct {
				Name string `json:"name"`
			} `json:"group"`
		} `json:"data"`
	}
	if err := m.c.JSON(ctx, sourcekit.Request{Method: "POST", URL: m.base + "/api/v1/chapter/chapter-listing-by-title-id", Headers: h, Body: body}, &out); err != nil {
		return nil, notFound(err)
	}
	wanted := map[string]bool{}
	for _, l := range m.siteLangs() {
		wanted[l] = true
	}
	var chapters []sourcekit.Chapter
	for _, c := range out.Data {
		if !wanted[c.Lang] || c.ID == "" {
			continue
		}
		title := strings.TrimSpace(c.Name)
		number := -1.0
		numberText := "null"
		if c.Number != nil {
			number = *c.Number
			numberText = strconv.FormatFloat(number, 'f', -1, 32)
		}
		var name strings.Builder
		if c.Volume > 0 {
			name.WriteString("Vol. " + strconv.FormatFloat(c.Volume, 'f', -1, 32) + " ")
		}
		if strings.Contains(title, numberText) {
			name.WriteString(title)
		} else {
			name.WriteString("Ch. " + numberText + " " + title)
		}
		ch := sourcekit.Chapter{URL: c.ID, ID: c.ID, Name: strings.TrimSpace(name.String()), Number: number,
			WebURL: m.base + "/chapter-detail/" + c.ID}
		if c.Group != nil {
			ch.Scanlator = strings.TrimSpace(c.Group.Name)
		}
		if at, err := time.Parse("2006-01-02T15:04:05.999999", c.CreatedAt); err == nil {
			ch.UploadedAt = &at
		}
		chapters = append(chapters, ch)
	}
	return chapters, nil
}

func (m *mangaball) Pages(ctx context.Context, ch sourcekit.PageRef) ([]sourcekit.PageImage, error) {
	id := mballSlug(ch.URL)
	if id == "" {
		id = ch.ID
	}
	if id == "" {
		return nil, fmt.Errorf("%q is not a chapter url", ch.URL)
	}
	var out struct {
		Data struct {
			Chapter struct {
				Pages []string `json:"pages"`
			} `json:"chapter"`
		} `json:"data"`
	}
	req := sourcekit.Request{URL: m.base + "/api/v1/chapter-detail", Query: url.Values{"chapter_id": {id}}, Headers: m.headers()}
	if err := m.c.JSON(ctx, req, &out); err != nil {
		return nil, notFound(err)
	}
	var pages []sourcekit.PageImage
	for _, img := range out.Data.Chapter.Pages {
		if img = strings.TrimSpace(img); img == "" {
			continue
		}
		pages = append(pages, sourcekit.PageImage{Index: len(pages), URL: sourcekit.Abs(m.base+"/", img),
			Headers: map[string]string{"Referer": m.base + "/"}})
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("%w: the chapter has no pages", sourcekit.ErrNotFound)
	}
	return pages, nil
}
