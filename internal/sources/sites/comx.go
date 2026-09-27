package sites

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// Com-X is a Russian DLE site. Its catalogue and title pages are HTML; title
// pages embed chapter JSON and reader pages embed the image host and filenames.
// The id matches Keiyoushi's Com-X extension so Mihon imports link directly.
var comxID = sourcekit.KeiyoushiID("Com-X", "ru", 1)

const comxSite = "https://com-x.life"

func init() {
	sourcekit.Register(comxID, func(d sourcekit.Deps) sourcekit.Site {
		return &comx{c: d.Client, base: comxSite}
	})
}

type comx struct {
	c    *sourcekit.Client
	base string
}

func (c *comx) Info() sourcekit.Info {
	return sourcekit.Info{ID: comxID, Name: "Com-X", Lang: "ru", BaseURL: c.base, SupportsBrowse: true,
		IconURL: c.base + "/favicon.ico"}
}

// Keiyoushi limits Com-X to three requests a second. Keep native requests
// gentler because chapter pages and browser-challenge cookies are shared.
func (c *comx) Politeness() sourcekit.Politeness {
	return sourcekit.Politeness{RequestsPerMinute: 120, MaxConcurrent: 2}
}

func (c *comx) Search(ctx context.Context, query string, page int) (sourcekit.Results, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return c.Popular(ctx, page)
	}
	if page < 1 {
		page = 1
	}
	path := "/search/" + url.PathEscape(query) + "/"
	if page > 1 {
		path += "page/" + strconv.Itoa(page) + "/"
	}
	return c.list(ctx, sourcekit.Request{URL: c.base + path, Headers: c.headers(c.base + "/")})
}

func (c *comx) Popular(ctx context.Context, page int) (sourcekit.Results, error) {
	return c.browse(ctx, page, "rating")
}

func (c *comx) Latest(ctx context.Context, page int) (sourcekit.Results, error) {
	return c.browse(ctx, page, "editdate")
}

func (c *comx) browse(ctx context.Context, page int, sort string) (sourcekit.Results, error) {
	if page < 1 {
		page = 1
	}
	path := "/comix-read/"
	if page > 1 {
		path = "/comix-read/page/" + strconv.Itoa(page) + "/"
	}
	form := url.Values{
		"dlenewssortby":      {sort},
		"dledirection":       {"desc"},
		"set_new_sort":       {"dle_sort_cat_1"},
		"set_direction_sort": {"dle_direction_cat_1"},
	}.Encode()
	return c.list(ctx, sourcekit.Request{Method: http.MethodPost, URL: c.base + path, Body: []byte(form), Headers: c.formHeaders(c.base + "/")})
}

func (c *comx) list(ctx context.Context, req sourcekit.Request) (sourcekit.Results, error) {
	doc, err := c.c.Document(ctx, req)
	if err != nil {
		return sourcekit.Results{}, err
	}
	var out sourcekit.Results
	doc.Find("#dle-content > .readed").Each(func(_ int, card *goquery.Selection) {
		a := card.Find(".readed__title > a").First()
		path := sourcekit.Path(sourcekit.Abs(c.base, a.AttrOr("href", "")))
		title := strings.TrimSpace(a.Text())
		if i := strings.LastIndex(title, " / "); i >= 0 {
			title = strings.TrimSpace(title[i+3:])
		}
		if path == "" || path == "/" || title == "" || out.Has(path) {
			return
		}
		out.Mangas = append(out.Mangas, sourcekit.Manga{URL: path, ID: comxMangaID(path), Title: title,
			CoverURL: c.image(card.Find("img").First())})
	})
	pages := doc.Find("div.pagination__pages").First().Children()
	out.HasNext = pages.Length() > 0 && goquery.NodeName(pages.Last()) == "a"
	return out, nil
}

func (c *comx) Details(ctx context.Context, ref sourcekit.Ref) (sourcekit.Details, error) {
	doc, path, err := c.titlePage(ctx, ref)
	if err != nil {
		return sourcekit.Details{}, err
	}
	title := strings.TrimSpace(doc.Find("header.page__header h1").First().Text())
	if title == "" {
		return sourcekit.Details{}, fmt.Errorf("%w: no title at %s", sourcekit.ErrNotFound, path)
	}
	d := sourcekit.Details{Manga: sourcekit.Manga{URL: path, ID: comxMangaID(path), Title: title,
		CoverURL: c.image(doc.Find("div.page__poster img").First())}, WebURL: c.base + path, Status: sourcekit.StatusUnknown}
	d.Description = strings.TrimSpace(doc.Find("div.page__text").First().Text())
	d.Author = comxListItem(doc, "Автор")
	d.Artist = comxListItem(doc, "Художник")
	d.Genres = comxTexts(doc.Find("div.page__tags a"))
	status := strings.ToLower(comxListItem(doc, "Статус"))
	switch {
	case strings.Contains(status, "заверш"), strings.Contains(status, "лимитка"), strings.Contains(status, "ван шот"), strings.Contains(status, "графический роман"):
		d.Status = sourcekit.StatusCompleted
	case strings.Contains(status, "заморожен"), strings.Contains(status, "приостановлен"):
		d.Status = sourcekit.StatusHiatus
	case strings.Contains(status, "продолжается"), strings.Contains(status, "онгоинг"), strings.Contains(status, "перевод продолжается"):
		d.Status = sourcekit.StatusOngoing
	}
	return d, nil
}

func (c *comx) Chapters(ctx context.Context, ref sourcekit.Ref) ([]sourcekit.Chapter, error) {
	doc, _, err := c.titlePage(ctx, ref)
	if err != nil {
		return nil, err
	}
	if doc.Find(`.message-info__content:contains(не имеют доступа)`).Length() > 0 {
		return nil, fmt.Errorf("%w: sign-in is required", sourcekit.ErrUnsupported)
	}
	var data struct {
		ComicID  int `json:"news_id"`
		Chapters []struct {
			ID     int     `json:"id"`
			Title  string  `json:"title"`
			Number float64 `json:"number"`
			Date   string  `json:"date"`
		} `json:"chapters"`
	}
	if err := comxScriptJSON(doc, "window.__DATA__", &data); err != nil {
		return nil, err
	}
	out := make([]sourcekit.Chapter, 0, len(data.Chapters))
	for _, row := range data.Chapters {
		path := fmt.Sprintf("/reader/%d/%d", data.ComicID, row.ID)
		ch := sourcekit.Chapter{URL: path, ID: strconv.Itoa(row.ID), Name: strings.Join(strings.Fields(row.Title), " "), Number: row.Number, WebURL: c.base + path}
		if at, ok := comxDate(row.Date); ok {
			ch.UploadedAt = &at
		}
		out = append(out, ch)
	}
	return out, nil
}

func (c *comx) Pages(ctx context.Context, ch sourcekit.PageRef) ([]sourcekit.PageImage, error) {
	path := sourcekit.Path(ch.URL)
	if path == "" || !strings.HasPrefix(path, "/reader/") {
		return nil, fmt.Errorf("%q is not a Com-X chapter url", ch.URL)
	}
	doc, err := c.c.Document(ctx, sourcekit.Request{URL: c.base + path, Headers: c.headers(c.base + sourcekit.Path(ch.Manga.URL))})
	if err != nil {
		return nil, err
	}
	var data struct {
		Host   string   `json:"host"`
		Images []string `json:"images"`
	}
	if err := comxScriptJSON(doc, "window.__DATA__", &data); err != nil {
		return nil, err
	}
	host := strings.TrimRight(data.Host, "/")
	if !strings.Contains(host, "://") {
		host = "https://" + host
	}
	out := make([]sourcekit.PageImage, 0, len(data.Images))
	for i, image := range data.Images {
		out = append(out, sourcekit.PageImage{Index: i, URL: host + "/comix/" + strings.TrimLeft(image, "/"), Headers: c.headers(c.base + path)})
	}
	return out, nil
}

func (c *comx) titlePage(ctx context.Context, ref sourcekit.Ref) (*goquery.Document, string, error) {
	path := sourcekit.Path(strings.TrimSpace(ref.URL))
	if path == "" || path == "/" {
		return nil, "", fmt.Errorf("%q is not a Com-X title url", ref.URL)
	}
	doc, err := c.c.Document(ctx, sourcekit.Request{URL: c.base + path, Headers: c.headers(c.base + "/")})
	if err != nil {
		var se *sourcekit.StatusError
		if errorsAs(err, &se) && se.Code == http.StatusNotFound {
			return nil, "", fmt.Errorf("%w: %s", sourcekit.ErrNotFound, path)
		}
		return nil, "", err
	}
	return doc, path, nil
}

func (c *comx) headers(referer string) map[string]string {
	return map[string]string{"Accept-Language": "ru-RU,ru;q=0.9", "Referer": referer}
}

func (c *comx) formHeaders(referer string) map[string]string {
	h := c.headers(referer)
	h["Content-Type"] = "application/x-www-form-urlencoded"
	return h
}

func (c *comx) image(img *goquery.Selection) string {
	for _, attr := range []string{"data-src", "src"} {
		if raw := strings.TrimSpace(img.AttrOr(attr, "")); raw != "" {
			return sourcekit.Abs(c.base, raw)
		}
	}
	return ""
}

func comxListItem(doc *goquery.Document, label string) string {
	var found string
	doc.Find(".page__list > li").EachWithBreak(func(_ int, li *goquery.Selection) bool {
		if !strings.Contains(li.Find("div").First().Text(), label) {
			return true
		}
		found = strings.TrimSpace(li.Find("a").First().Text())
		if found == "" {
			found = strings.TrimSpace(li.Clone().ChildrenFiltered("div").Remove().End().Text())
		}
		return false
	})
	return found
}

func comxTexts(items *goquery.Selection) []string {
	seen := map[string]bool{}
	var out []string
	items.Each(func(_ int, item *goquery.Selection) {
		value := strings.TrimSpace(item.Text())
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	})
	return out
}

func comxScriptJSON(doc *goquery.Document, marker string, out any) error {
	var raw string
	doc.Find("script").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		body := s.Text()
		if !strings.Contains(body, marker) {
			return true
		}
		parts := strings.SplitN(body, marker+" =", 2)
		if len(parts) != 2 {
			return true
		}
		raw = strings.TrimSpace(parts[1])
		raw = strings.TrimSpace(strings.SplitN(raw, ";", 2)[0])
		return false
	})
	if raw == "" {
		return fmt.Errorf("%s data script not found", marker)
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("decode %s: %w", marker, err)
	}
	return nil
}

func comxMangaID(path string) string {
	name := strings.TrimSuffix(strings.Trim(path, "/"), ".html")
	if i := strings.IndexByte(name, '-'); i >= 0 {
		name = name[:i]
	}
	if _, err := strconv.Atoi(name); err == nil {
		return name
	}
	return ""
}

func comxDate(raw string) (time.Time, bool) {
	for _, layout := range []string{"2.1.2006", "02.01.2006", "2.01.2006"} {
		if at, err := time.Parse(layout, strings.TrimSpace(raw)); err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}
