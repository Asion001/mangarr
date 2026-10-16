package sites

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// OniSaga (onisaga.com) is a Laravel Livewire site: lists and chapter lists
// are HTML the site renders in answer to Livewire calls, and a page's image
// address comes one at a time from an API that wants a reader token, which it
// hands on from one answer to the next.
//
// Keiyoushi has one "all" source and one per language the site translates
// into; the language only decides which chapters are listed.
var onisLangs = []string{"all", "en", "fr", "ja", "pt-BR", "pt", "es-419", "es"}

// onisChapterLangs are the site's codes, in the order the "all" catalog asks
// for them.
var onisChapterLangs = []string{"EN", "FR", "JA", "PT-BR", "PT", "ES-LA", "ES"}

const onisSite = "https://onisaga.com"

func init() {
	sourcekit.RegisterLangs("OniSaga", 1, onisLangs, func(d sourcekit.Deps, lang string) sourcekit.Site {
		return &onisaga{c: d.Client, base: onisSite, lang: lang, pageDelay: 2 * time.Second, tokens: map[string]string{}}
	})
}

type onisaga struct {
	c *sourcekit.Client
	// base is the site (tests point it at a recorded copy).
	base string
	// lang is the catalog's language as Keiyoushi writes it.
	lang string
	// nsfw lists titles the site marks 18+.
	nsfw bool
	// pageDelay spaces the page API's calls: it answers faster ones with a
	// 429 that lasts a quarter of an hour.
	pageDelay time.Duration

	mu     sync.Mutex
	tokens map[string]string // chapter id -> the reader token to use next

	apiMu sync.Mutex // one page API call at a time
	last  time.Time
}

func (o *onisaga) Info() sourcekit.Info {
	return sourcekit.Info{ID: sourcekit.KeiyoushiID("OniSaga", o.lang, 1), Name: "OniSaga", Lang: o.lang, BaseURL: o.base,
		SupportsBrowse: true, IconURL: o.base + "/favicon.ico"}
}

// Politeness: the extension allows four requests a second; page images are
// spaced further apart on top of that (see pageDelay).
func (o *onisaga) Politeness() sourcekit.Politeness {
	return sourcekit.Politeness{RequestsPerMinute: 240, MaxConcurrent: 2}
}

func (o *onisaga) Options() []sourcekit.Option {
	return []sourcekit.Option{
		{Key: "nsfw", Title: "Show 18+ titles", Type: "switch", Value: o.nsfw,
			Help: "Titles the site marks 18+ are left out of lists otherwise."},
		{Key: "pageDelay", Title: "Time between page images", Type: "select", Value: strconv.Itoa(int(o.pageDelay.Milliseconds())),
			Help: "Shorter can get the site to refuse pages for a quarter of an hour.", Choices: []sourcekit.Choice{
				{Value: "1500", Label: "1.5 seconds"}, {Value: "1750", Label: "1.75 seconds"}, {Value: "2000", Label: "2 seconds"},
				{Value: "2250", Label: "2.25 seconds"}, {Value: "2500", Label: "2.5 seconds"}}},
	}
}

func (o *onisaga) SetOption(key string, value any) error {
	switch key {
	case "nsfw":
		switch v := value.(type) {
		case bool:
			o.nsfw = v
		case string:
			o.nsfw = v == "true" || v == "1"
		default:
			return fmt.Errorf("%v is not a switch", value)
		}
	case "pageDelay":
		ms, err := strconv.Atoi(fmt.Sprint(value))
		if err != nil || ms < 1500 || ms > 2500 {
			return fmt.Errorf("%v is not a delay the site allows", value)
		}
		o.pageDelay = time.Duration(ms) * time.Millisecond
	default:
		return fmt.Errorf("%w: no option %q", sourcekit.ErrUnsupported, key)
	}
	return nil
}

// chapterLangs are the site's codes whose chapters the catalog lists.
func (o *onisaga) chapterLangs() []string {
	switch o.lang {
	case "en":
		return []string{"EN"}
	case "fr":
		return []string{"FR"}
	case "ja":
		return []string{"JA"}
	case "pt-BR":
		return []string{"PT-BR"}
	case "pt":
		return []string{"PT"}
	case "es-419":
		return []string{"ES-LA"}
	case "es":
		return []string{"ES"}
	}
	return onisChapterLangs
}

// ---- Livewire ---------------------------------------------------------------

// onisState is what a Livewire call needs: the component's snapshot and the
// page's CSRF token.
type onisState struct{ snapshot, token string }

// onisLivewire finds a component's state in a page.
func onisLivewire(doc *goquery.Document, component string) (onisState, bool) {
	token := strings.TrimSpace(doc.Find("meta[name=csrf-token]").First().AttrOr("content", ""))
	if token == "" {
		token = strings.TrimSpace(doc.Find("input[name=_token]").First().AttrOr("value", ""))
	}
	if token == "" {
		return onisState{}, false
	}
	var st onisState
	doc.Find("*").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		for _, a := range s.Nodes[0].Attr {
			if strings.HasSuffix(a.Key, "snapshot") && strings.Contains(a.Val, component) {
				st = onisState{snapshot: a.Val, token: token}
				return false
			}
		}
		return true
	})
	return st, st.snapshot != ""
}

// onisFilters are the browse component's properties, all sent every time.
type onisFilters struct {
	Platform     string   `json:"platform"`
	Status       string   `json:"status"`
	Sort         string   `json:"sort"`
	MinChapters  string   `json:"min_chapters"`
	Group        *string  `json:"group"`
	ReleaseStart *string  `json:"release_start"`
	ReleaseEnd   *string  `json:"release_end"`
	Genre        []string `json:"genre"`
	ExcludeGenre []string `json:"excludeGenre"`
}

type onisCall struct {
	Type   string   `json:"type"`
	Path   string   `json:"path"`
	Method string   `json:"method"`
	Params []string `json:"params"`
}

// call runs one Livewire method and returns the component's new snapshot
// and the HTML it rendered.
func (o *onisaga) call(ctx context.Context, st onisState, referer string, updates any, method string, params ...string) (string, string, error) {
	if params == nil {
		params = []string{}
	}
	body, err := json.Marshal(map[string]any{
		"_token": st.token,
		"components": []any{map[string]any{
			"snapshot": st.snapshot,
			"updates":  updates,
			"calls":    []onisCall{{Type: "call", Path: "", Method: method, Params: params}},
		}},
	})
	if err != nil {
		return "", "", err
	}
	var out struct {
		Components []struct {
			Snapshot string `json:"snapshot"`
			Effects  struct {
				HTML *string `json:"html"`
			} `json:"effects"`
		} `json:"components"`
	}
	req := sourcekit.Request{Method: "POST", URL: o.base + "/livewire/update", Body: body, Headers: map[string]string{
		"X-Livewire": "", "Accept": "application/json", "X-Requested-With": "XMLHttpRequest", "Content-Type": "application/json",
		"Origin": o.base, "Referer": referer,
	}}
	if err := o.c.JSON(ctx, req, &out); err != nil {
		return "", "", err
	}
	if len(out.Components) == 0 || out.Components[0].Effects.HTML == nil {
		return "", "", nil
	}
	return out.Components[0].Snapshot, *out.Components[0].Effects.HTML, nil
}

func (o *onisaga) headers(referer string) map[string]string {
	if referer == "" {
		referer = o.base + "/"
	}
	return map[string]string{"Referer": referer, "Origin": o.base}
}

// ---- lists ------------------------------------------------------------------

func (o *onisaga) Search(ctx context.Context, query string, page int) (sourcekit.Results, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return o.list(ctx, o.base+"/browse", page, nil)
	}
	return o.list(ctx, o.base+"/search/"+url.PathEscape(query), page, nil)
}

func (o *onisaga) Popular(ctx context.Context, page int) (sourcekit.Results, error) {
	return o.list(ctx, o.base+"/browse", page, &onisFilters{Sort: "view"})
}

func (o *onisaga) Latest(ctx context.Context, page int) (sourcekit.Results, error) {
	return o.list(ctx, o.base+"/browse", page, &onisFilters{Sort: "created_at"})
}

// list reads a page of the site's browse (or search) component. Its first
// page with no filters is in the page itself; anything else is a Livewire
// call to that component.
func (o *onisaga) list(ctx context.Context, endpoint string, page int, filters *onisFilters) (sourcekit.Results, error) {
	if page < 1 {
		page = 1
	}
	doc, err := o.c.Document(ctx, sourcekit.Request{URL: endpoint, Headers: o.headers("")})
	if err != nil {
		return sourcekit.Results{}, err
	}
	if page == 1 && filters == nil {
		return o.results(doc.Selection), nil
	}
	st, ok := onisLivewire(doc, "post-filter")
	if !ok {
		return sourcekit.Results{}, fmt.Errorf("%s: no list component to page through", endpoint)
	}
	if filters == nil {
		filters = &onisFilters{Sort: "created_at"}
	}
	if filters.Genre == nil {
		filters.Genre = []string{}
	}
	if filters.ExcludeGenre == nil {
		filters.ExcludeGenre = []string{}
	}
	_, html, err := o.call(ctx, st, endpoint, filters, "gotoPage", strconv.Itoa(page))
	if err != nil {
		return sourcekit.Results{}, err
	}
	frag, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return sourcekit.Results{}, err
	}
	return o.results(frag.Selection), nil
}

func (o *onisaga) results(s *goquery.Selection) sourcekit.Results {
	var res sourcekit.Results
	s.Find("div.relative.group").Each(func(_ int, item *goquery.Selection) {
		if m, ok := o.manga(item); ok && !res.Has(m.URL) {
			res.Mangas = append(res.Mangas, m)
		}
	})
	s.Find("*").EachWithBreak(func(_ int, el *goquery.Selection) bool {
		click, ok := el.Attr("wire:click")
		if ok && strings.Contains(click, "nextPage") {
			if _, disabled := el.Attr("disabled"); !disabled {
				res.HasNext = true
				return false
			}
		}
		return true
	})
	return res
}

// onisDropAdultCover takes away the "18+" overlay the site lays over a title,
// reporting whether there was one.
func onisDropAdultCover(s *goquery.Selection) bool {
	found := false
	s.Find("span").EachWithBreak(func(_ int, span *goquery.Selection) bool {
		if !strings.Contains(ownText(span), "18+") {
			return true
		}
		found = true
		span.Closest("div.absolute.inset-0.z-20").Remove()
		return false
	})
	return found
}

// manga reads one list item.
func (o *onisaga) manga(item *goquery.Selection) (sourcekit.Manga, bool) {
	if onisDropAdultCover(item) && !o.nsfw {
		return sourcekit.Manga{}, false
	}
	link := item
	if goquery.NodeName(item) != "a" {
		link = item.Find(`a[href*="/manga/"]`).First()
	}
	slug := onisSlug(link.AttrOr("href", ""))
	if slug == "" {
		return sourcekit.Manga{}, false
	}
	titleEl := item.Find("div[data-flux-heading], h3, h4").First()
	if titleEl.Length() == 0 {
		titleEl = item.Find("a[title]").First()
	}
	if titleEl.Length() == 0 {
		titleEl = link
	}
	title := strings.TrimSpace(titleEl.AttrOr("title", ""))
	if title == "" {
		title = text(titleEl)
	}
	if title == "" {
		return sourcekit.Manga{}, false
	}
	cover := o.image(item.Find("img[alt]:not([alt=''])").First())
	if cover == "" {
		cover = o.image(item.Find("img").First())
	}
	return sourcekit.Manga{URL: "/manga/" + slug, Title: title, CoverURL: cover}, true
}

// image reads an image's address, lazy-loaded or not.
func (o *onisaga) image(img *goquery.Selection) string {
	if img.Length() == 0 {
		return ""
	}
	src := ""
	for _, attr := range []string{"data-src", "data-lazy-src", "src"} {
		if src = strings.TrimSpace(img.AttrOr(attr, "")); src != "" {
			break
		}
	}
	if src == "" || strings.HasPrefix(src, "data:") {
		return ""
	}
	return sourcekit.Abs(o.base+"/", src)
}

// onisSlug is the slug in "/manga/<slug>" (as Keiyoushi stores the url, or
// the site links it).
func onisSlug(raw string) string {
	parts := strings.Split(strings.Trim(sourcekit.Path(raw), "/"), "/")
	for i, p := range parts {
		if strings.EqualFold(p, "manga") && i+1 < len(parts) && parts[i+1] != "" {
			return parts[i+1]
		}
	}
	return ""
}

// ---- one manga --------------------------------------------------------------

func (o *onisaga) page(ctx context.Context, ref sourcekit.Ref) (*goquery.Document, string, error) {
	slug := onisSlug(ref.URL)
	if slug == "" {
		return nil, "", fmt.Errorf("%q is not a manga url", ref.URL)
	}
	doc, err := o.c.Document(ctx, sourcekit.Request{URL: o.base + "/manga/" + slug, Headers: o.headers("")})
	if err != nil {
		return nil, "", notFound(err)
	}
	onisDropAdultCover(doc.Selection)
	return doc, slug, nil
}

func (o *onisaga) Details(ctx context.Context, ref sourcekit.Ref) (sourcekit.Details, error) {
	doc, slug, err := o.page(ctx, ref)
	if err != nil {
		return sourcekit.Details{}, err
	}
	title := text(doc.Find("h1").First())
	if title == "" {
		title = text(doc.Find("[data-flux-heading]").First())
	}
	if title == "" {
		return sourcekit.Details{}, fmt.Errorf("%w: no title for %s", sourcekit.ErrNotFound, slug)
	}
	d := sourcekit.Details{Manga: sourcekit.Manga{URL: "/manga/" + slug, Title: title,
		CoverURL: o.image(doc.Find(".w-32 > picture:nth-child(1) > img:nth-child(3)").First())},
		WebURL: o.base + "/manga/" + slug, Status: onisStatus(doc)}
	info := doc.Find(`div.flex.flex-col.md\:flex-row`).First()
	var authors []string
	info.Find(`a[href*="/author/"]`).Each(func(_ int, a *goquery.Selection) { authors = append(authors, text(a)) })
	d.Author = strings.Join(authors, ", ")
	doc.Find("div.flex.items-center.gap-2.justify-center.mb-2").First().Find("div[data-flux-badge]").Each(func(_ int, b *goquery.Selection) {
		switch t := strings.ToLower(text(b)); t {
		case "manga", "manhwa", "manhua", "shounen", "seinen", "shoujo", "josei":
			d.Genres = append(d.Genres, strings.ToUpper(t[:1])+t[1:])
		}
	})
	info.Find(`a[href*="/genre/"]`).Each(func(_ int, a *goquery.Selection) { d.Genres = append(d.Genres, text(a)) })
	d.Description = text(doc.Find("p.leading-relaxed").First())
	return d, nil
}

// onisStatus reads the status badge (a span with a coloured dot in it).
func onisStatus(doc *goquery.Document) string {
	status := ""
	doc.Find("span").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if s.ChildrenFiltered("span.size-1\\.5").Length() > 0 {
			status = text(s)
			return false
		}
		return true
	})
	if status == "" {
		doc.Find("span.inline-flex").EachWithBreak(func(_ int, s *goquery.Selection) bool {
			own := ownText(s)
			for _, w := range []string{"Completed", "Ongoing", "Hiatus", "Cancelled"} {
				if strings.Contains(own, w) {
					status = own
					return false
				}
			}
			return true
		})
	}
	s := strings.ToLower(status)
	switch {
	case strings.Contains(s, "ongoing"), strings.Contains(s, "releasing"):
		return sourcekit.StatusOngoing
	case strings.Contains(s, "completed"):
		return sourcekit.StatusCompleted
	case strings.Contains(s, "hiatus"):
		return sourcekit.StatusHiatus
	case strings.Contains(s, "cancelled"), strings.Contains(s, "dropped"):
		return sourcekit.StatusCancelled
	}
	return sourcekit.StatusUnknown
}

// onisMaxLoads bounds "load more" rounds for one language.
const onisMaxLoads = 200

func (o *onisaga) Chapters(ctx context.Context, ref sourcekit.Ref) ([]sourcekit.Chapter, error) {
	doc, slug, err := o.page(ctx, ref)
	if err != nil {
		return nil, err
	}
	st, ok := onisLivewire(doc, "manga.chapter-list")
	if !ok {
		return nil, nil // the site lists no chapters for it
	}
	referer := o.base + "/manga/" + slug
	all := o.lang == "all"
	var out []sourcekit.Chapter
	seen := map[string]bool{}
	for _, code := range o.chapterLangs() {
		// every "load more" answers with the whole list so far; stop when it
		// no longer grows
		snapshot, prev := st.snapshot, 0
		var chapters []sourcekit.Chapter
		for i := 0; i < onisMaxLoads; i++ {
			next, html, err := o.call(ctx, onisState{snapshot: snapshot, token: st.token}, referer,
				map[string]string{"language": code}, "loadMoreChapters")
			if err != nil {
				return nil, err
			}
			if html == "" && next == "" {
				break
			}
			frag, err := goquery.NewDocumentFromReader(strings.NewReader(html))
			if err != nil {
				return nil, err
			}
			chapters = o.chapters(frag.Selection, code, all)
			if len(chapters) <= prev || next == "" {
				break
			}
			prev, snapshot = len(chapters), next
		}
		for _, c := range chapters {
			if !seen[c.URL] {
				seen[c.URL] = true
				out = append(out, c)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return max(out[i].Number, 0) > max(out[j].Number, 0) })
	return out, nil
}

var (
	onisInterpunct   = regexp.MustCompile(`\s*·\s*`)
	onisChapterNum   = regexp.MustCompile(`Chapter\s+([\d.]+)`)
	onisRelativeDate = regexp.MustCompile(`(\d+)\s+(minute|hour|day|week|month|year)s?\s+ago`)
)

// chapters reads a rendered chapter list: a chapter is a link, or a
// dropdown with a link per group that translated it.
func (o *onisaga) chapters(s *goquery.Selection, code string, all bool) []sourcekit.Chapter {
	var out []sourcekit.Chapter
	add := func(href, number, dateText, scanlator string) {
		abs := sourcekit.Abs(o.base+"/", href)
		if !strings.Contains(abs, "/read/") {
			return
		}
		path := sourcekit.Path(abs)
		ch := sourcekit.Chapter{URL: path, Name: "Chapter " + number, Scanlator: scanlator, Number: -1, WebURL: o.base + path}
		if m := onisChapterNum.FindStringSubmatch(ch.Name); m != nil {
			if n, err := strconv.ParseFloat(strings.TrimRight(m[1], "."), 64); err == nil {
				ch.Number = n
			}
		}
		if t, ok := onisDate(dateText, time.Now()); ok {
			ch.UploadedAt = &t
		}
		out = append(out, ch)
	}
	s.Find("a.gap-4").Has("div[data-flux-heading]").Each(func(_ int, a *goquery.Selection) {
		number := onisNumber(a)
		if number == "" {
			return
		}
		scanlator := ""
		if all {
			scanlator = code
		}
		add(a.AttrOr("href", ""), number, onisDateText(a), scanlator)
	})
	s.Find("ui-dropdown").Each(func(_ int, dd *goquery.Selection) {
		button := dd.Find("button").First()
		if button.Find("div[data-flux-heading]").Length() == 0 {
			return
		}
		number := onisNumber(button)
		if number == "" {
			return
		}
		dateText := onisDateText(button)
		unknown := 1
		dd.Find("ui-menu a[data-flux-menu-item]").Each(func(_ int, a *goquery.Selection) {
			if !strings.Contains(a.AttrOr("href", ""), "/read/") {
				return
			}
			group := text(a.Find("span.text-sm").First())
			if group == "" {
				group = text(a.Find("div.flex.items-center.gap-2 > span:not(.ml-auto)").First())
			}
			if group == "" || strings.EqualFold(group, "Unknown group") {
				group = "Unknown " + strconv.Itoa(unknown)
				unknown++
			}
			if all {
				group = code + " - " + group
			}
			add(a.AttrOr("href", ""), number, dateText, group)
		})
	})
	return out
}

// onisNumber is the chapter number a list entry shows.
func onisNumber(s *goquery.Selection) string {
	if n := strings.TrimSpace(strings.Replace(text(s.Find("div[data-flux-heading]").First()), "Chapter ", "", 1)); n != "" {
		return n
	}
	return text(s.Find("div.w-10").First())
}

// onisDateText is the "3 days ago" part of an entry's details line.
func onisDateText(s *goquery.Selection) string {
	details := strings.ReplaceAll(text(s.Find("p[data-flux-text]").First()), " - ", " · ")
	for _, part := range onisInterpunct.Split(details, -1) {
		l := strings.ToLower(part)
		if strings.Contains(l, "ago") || strings.Contains(l, "today") || strings.Contains(l, "yesterday") {
			return part
		}
	}
	return ""
}

// onisDate turns "today", "yesterday" and "3 days ago" into a time.
func onisDate(s string, now time.Time) (time.Time, bool) {
	s = strings.ToLower(s)
	switch {
	case s == "":
		return time.Time{}, false
	case strings.Contains(s, "today"):
		return now, true
	case strings.Contains(s, "yesterday"):
		return now.Add(-24 * time.Hour), true
	}
	m := onisRelativeDate.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	n, _ := strconv.Atoi(m[1])
	unit := map[string]time.Duration{"minute": time.Minute, "hour": time.Hour, "day": 24 * time.Hour,
		"week": 7 * 24 * time.Hour, "month": 30 * 24 * time.Hour, "year": 365 * 24 * time.Hour}[m[2]]
	return now.Add(-time.Duration(n) * unit), true
}

// ---- pages ------------------------------------------------------------------

var (
	onisReaderToken = regexp.MustCompile(`readerToken["']?\s*:\s*["']([^"']+)["']`)
	onisPageOrder   = regexp.MustCompile(`["']?order["']?\s*:\s*(\d+)`)
)

// Pages lists a chapter's pages. Their image addresses only come from the
// page API one at a time, paced and each with the token the answer before it
// handed on, so they are looked up when a page is fetched: every page points
// at the chapter's reader and carries "<number> <reader path>" in Decode, and
// DecodePage asks the API for the image and returns it.
func (o *onisaga) Pages(ctx context.Context, ch sourcekit.PageRef) ([]sourcekit.PageImage, error) {
	path := sourcekit.Path(ch.URL)
	if !strings.Contains(path, "/read/") {
		return nil, fmt.Errorf("%q is not a chapter url", ch.URL)
	}
	reader := o.base + path
	body, err := o.c.Do(ctx, sourcekit.Request{URL: reader, Headers: o.headers("")})
	if err != nil {
		return nil, notFound(err)
	}
	m := onisReaderToken.FindSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("%s: no reader token in the chapter page", reader)
	}
	o.setToken(onisChapterID(reader), string(m[1]))
	count := len(onisPageOrder.FindAllIndex(body, -1))
	pages := make([]sourcekit.PageImage, 0, count)
	for i := 0; i < count; i++ {
		pages = append(pages, sourcekit.PageImage{Index: i, URL: reader, Decode: strconv.Itoa(i) + " " + path,
			Headers: map[string]string{"Referer": o.base + "/"}})
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("%w: the chapter has no pages", sourcekit.ErrNotFound)
	}
	return pages, nil
}

// onisChapterID is the last part of a reader address.
func onisChapterID(reader string) string {
	p := strings.Trim(sourcekit.Path(reader), "/")
	if i := strings.Index(p, "?"); i >= 0 {
		p = p[:i]
	}
	return p[strings.LastIndex(p, "/")+1:]
}

func (o *onisaga) token(cid string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.tokens[cid]
}

func (o *onisaga) setToken(cid, token string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.tokens) > 200 {
		o.tokens = map[string]string{} // chapters come and go
	}
	o.tokens[cid] = token
}

// DecodePage gets a page's image. data is the chapter's reader page, fetched
// for the page: it carries a fresh reader token, used when the one the site
// last handed on is refused.
func (o *onisaga) DecodePage(ctx context.Context, decode string, data []byte) ([]byte, error) {
	num, path, _ := strings.Cut(decode, " ")
	order, err := strconv.Atoi(num)
	if err != nil || order < 0 || !strings.Contains(path, "/read/") {
		return nil, fmt.Errorf("%q is not a page of a chapter", decode)
	}
	reader := o.base + path
	cid := onisChapterID(reader)
	fresh := ""
	if m := onisReaderToken.FindSubmatch(data); m != nil {
		fresh = string(m[1])
	}
	token := o.token(cid)
	if token == "" {
		token, fresh = fresh, ""
	}
	api := fmt.Sprintf("%s/api/chapter/%s/page/%d", o.base, cid, order)
	for attempt := 0; attempt < 3; attempt++ {
		if err := o.pace(ctx, 0); err != nil {
			return nil, err
		}
		code, header, body, err := o.raw(ctx, api, map[string]string{"X-Reader-Token": token, "Sec-Fetch-Mode": "cors",
			"Sec-Fetch-Site": "same-origin", "Referer": reader, "Origin": o.base, "Accept": "application/json"})
		if err != nil {
			return nil, err
		}
		if code == http.StatusTooManyRequests {
			wait := o.pageDelay
			if s, err := strconv.Atoi(header.Get("Retry-After")); err == nil {
				wait = time.Duration(s) * time.Second
			}
			if err := o.pace(ctx, wait); err != nil {
				return nil, err
			}
			continue
		}
		if next := strings.TrimSpace(header.Get("X-Reader-Token-Next")); next != "" {
			o.setToken(cid, next)
		}
		var out struct {
			URL     string `json:"url"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &out)
		if out.URL != "" {
			return o.c.Do(ctx, sourcekit.Request{URL: sourcekit.Abs(o.base+"/", out.URL), Headers: map[string]string{"Referer": reader}})
		}
		if code >= 200 && code < 300 && !strings.Contains(strings.ToLower(out.Message), "expired") {
			return nil, fmt.Errorf("onisaga: page %d: %s", order, out.Message)
		}
		// the token was refused: try the one that came with this fetch, then
		// a new one from the reader
		if fresh != "" && fresh != token {
			token, fresh = fresh, ""
			continue
		}
		page, err := o.c.Do(ctx, sourcekit.Request{URL: reader, Headers: o.headers("")})
		if err != nil {
			return nil, err
		}
		m := onisReaderToken.FindSubmatch(page)
		if m == nil {
			return nil, fmt.Errorf("onisaga: could not refresh the reader token (HTTP %d)", code)
		}
		token = string(m[1])
		o.setToken(cid, token)
	}
	return nil, fmt.Errorf("onisaga: page %d: no image after 3 tries", order)
}

// pace waits for the page API's turn (and extra, after a 429).
func (o *onisaga) pace(ctx context.Context, extra time.Duration) error {
	o.apiMu.Lock()
	defer o.apiMu.Unlock()
	wait := time.Until(o.last.Add(o.pageDelay))
	if extra > wait {
		wait = extra
	}
	if wait > 0 {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	o.last = time.Now()
	return nil
}

// raw is a GET whose status and headers matter (the page API hands the next
// token on in a header, and answers a refusal with a JSON message).
func (o *onisaga) raw(ctx context.Context, u string, headers map[string]string) (int, http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("User-Agent", o.c.UserAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := o.c.HTTP.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, resp.Header, body, err
}

// ownText is an element's own text, without its children's.
func ownText(s *goquery.Selection) string {
	var b strings.Builder
	for _, n := range s.Nodes {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.TextNode {
				b.WriteString(c.Data)
			}
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
