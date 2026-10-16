package sites

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// MangaBox is the engine behind the Mangakakalot network (Keiyoushi's
// "mangabox" theme): server-rendered lists and manga pages, a JSON chapter
// list, and chapter pages that name their images in a script. One engine
// serves every site on the network; a site is an mboxSite with its own name,
// address and quirks.

// mboxSites are the sites on the theme. Each id is the Keiyoushi one, so
// Mihon backups link here.
var mboxSites = []mboxSite{
	// Mangakakalot also answers on mangakakalove.com. Its extension allows
	// two requests a second and three at a time for images.
	{id: sourcekit.KeiyoushiID("Mangakakalot", "en", 1), name: "Mangakakalot", base: "https://www.mangakakalot.gg",
		perMinute: 120, concurrent: 3, oldIDSlugs: true},
	// Manganato runs the same site on several domains. manganato.gg is the
	// canonical mirror because natomanga.com and nelomanga.net currently put
	// non-browser clients behind Cloudflare. Its id is set in the extension's
	// build file. Entries from the old manganato.com family of domains can't
	// be read any more and need migrating, as in Mihon.
	{id: "1024627298672457456", name: "Manganato", base: "https://www.manganato.gg",
		legacyDomains: []string{"https://chapmanganato.to/", "https://manganato.com/", "https://readmanganato.com/"}},
}

func init() {
	for _, s := range mboxSites {
		sourcekit.Register(s.id, func(d sourcekit.Deps) sourcekit.Site {
			m := s
			m.c = d.Client
			return &m
		})
	}
}

type mboxSite struct {
	c    *sourcekit.Client
	id   string
	name string
	// base is the site's address (tests point it at a recorded copy).
	base string
	nsfw bool
	// perMinute and concurrent are the extension's limits (0: a
	// conservative default).
	perMinute, concurrent int
	// oldIDSlugs: links from the site's previous engine end in an id such as
	// "ht123456" rather than a slug; the slug is then made from the title.
	oldIDSlugs bool
	// legacyDomains are addresses of entries that need migrating.
	legacyDomains []string
}

func (m *mboxSite) Info() sourcekit.Info {
	return sourcekit.Info{ID: m.id, Name: m.name, Lang: "en", BaseURL: m.base, NSFW: m.nsfw, SupportsBrowse: true,
		IconURL: m.base + "/favicon.ico"}
}

func (m *mboxSite) Politeness() sourcekit.Politeness {
	p := sourcekit.Politeness{RequestsPerMinute: 60, MaxConcurrent: 2}
	if m.perMinute > 0 {
		p.RequestsPerMinute = m.perMinute
	}
	if m.concurrent > 0 {
		p.MaxConcurrent = m.concurrent
	}
	return p
}

// headers: the site's Cloudflare misses its cache when there's an Origin, so
// only the Referer is sent.
func (m *mboxSite) headers() map[string]string {
	return map[string]string{"Referer": m.base + "/"}
}

// ---- lists ------------------------------------------------------------------

const (
	mboxListSelector = "div.truyen-list > div.list-truyen-item-wrap:has(a[data-id]), " +
		"div.comic-list > .list-comic-item-wrap:has(a[data-id])"
	mboxListNext     = "div.group_page, div.group-page a:not([href]) + a:not(:contains(Last))"
	mboxSearchSel    = ".panel_story_list .story_item, div.list-truyen-item-wrap, div.list-comic-item-wrap"
	mboxSearchNext   = "a.page_select + a:not(.page_last), a.page-select + a:not(.page-last), .page_select ~ a.page_blue:not(.page_disabled)"
	mboxDetailsMain  = "div.manga-info-top, div.panel-story-info"
	mboxThumbnailSel = "div.manga-info-pic img, span.info-image img"
	mboxDescSel      = "div#noidungm, div#panel-story-info-description, div#contentBox"
	mboxAltNameSel   = ".story-alternative, tr:has(.info-alternative) h2"
)

func (m *mboxSite) Popular(ctx context.Context, page int) (sourcekit.Results, error) {
	return m.list(ctx, m.base+"/manga-list/hot-manga", page, nil, mboxListSelector, mboxListNext)
}

func (m *mboxSite) Latest(ctx context.Context, page int) (sourcekit.Results, error) {
	return m.list(ctx, m.base+"/manga-list/latest-manga", page, nil, mboxListSelector, mboxListNext)
}

func (m *mboxSite) Search(ctx context.Context, query string, page int) (sourcekit.Results, error) {
	if q := mboxNormalize(query); q != "" {
		return m.list(ctx, m.base+"/search/story/"+url.PathEscape(q), page, nil, mboxSearchSel, mboxSearchNext)
	}
	// no text: the genre listing with the extension's default filters
	// (latest, any status, any genre)
	return m.list(ctx, m.base+"/genre/all", page, url.Values{"filter": {"4"}}, mboxSearchSel, mboxSearchNext)
}

func (m *mboxSite) list(ctx context.Context, endpoint string, page int, q url.Values, sel, next string) (sourcekit.Results, error) {
	if page < 1 {
		page = 1
	}
	if q == nil {
		q = url.Values{}
	}
	q.Set("page", strconv.Itoa(page))
	doc, err := m.c.Document(ctx, sourcekit.Request{URL: endpoint, Query: q, Headers: m.headers()})
	if err != nil {
		return sourcekit.Results{}, err
	}
	var res sourcekit.Results
	doc.Find(sel).Each(func(_ int, el *goquery.Selection) {
		a := el.Find("h3 a").First()
		href, ok := a.Attr("href")
		if !ok {
			return
		}
		slug := mboxLastSegment(href)
		if slug == "" || res.Has("/manga/"+slug) {
			return
		}
		res.Mangas = append(res.Mangas, sourcekit.Manga{URL: "/manga/" + slug, ID: slug, Title: text(a),
			CoverURL: sourcekit.Abs(m.base, el.Find("img").First().AttrOr("src", ""))})
	})
	res.HasNext = doc.Find(next).Length() > 0
	return res, nil
}

var (
	mboxAccents = []struct {
		re *regexp.Regexp
		to string
	}{
		{regexp.MustCompile(`[àáạảãâầấậẩẫăằắặẳẵ]`), "a"},
		{regexp.MustCompile(`[èéẹẻẽêềếệểễ]`), "e"},
		{regexp.MustCompile(`[ìíịỉĩ]`), "i"},
		{regexp.MustCompile(`[òóọỏõôồốộổỗơờớợởỡ]`), "o"},
		{regexp.MustCompile(`[ùúụủũưừứựửữ]`), "u"},
		{regexp.MustCompile(`[ỳýỵỷỹ]`), "y"},
		{regexp.MustCompile(`đ`), "d"},
	}
	// the "$" is an anchor, not a dollar sign, as in the site's own code
	mboxPunct      = regexp.MustCompile(`!|@|%|\^|\*|\(|\)|\+|=|<|>|\?|/|,|\.|:|;|'| |"|&|#|\[|]|~|-|$|_`)
	mboxUnderscore = regexp.MustCompile(`_+_`)
	mboxEdges      = regexp.MustCompile(`^_+|_+$`)
)

// mboxNormalize is the site's own change_alias: a search as the path segment
// the site expects ("One Piece!" → "one_piece").
func mboxNormalize(query string) string {
	s := strings.ToLower(query)
	for _, a := range mboxAccents {
		s = a.re.ReplaceAllString(s, a.to)
	}
	s = mboxPunct.ReplaceAllString(s, "_")
	s = mboxUnderscore.ReplaceAllString(s, "_")
	return mboxEdges.ReplaceAllString(s, "")
}

// ---- one manga --------------------------------------------------------------

var (
	mboxOldIDRe    = regexp.MustCompile(`^[a-z]{2}\d+$`)
	mboxSlugDropRe = regexp.MustCompile(`[^a-z0-9\s-]`)
	mboxSlugSepRe  = regexp.MustCompile(`[\s-]+`)
)

// slug is the manga's slug, which is all the site needs to find it.
func (m *mboxSite) slug(ref sourcekit.Ref) (string, error) {
	for _, d := range m.legacyDomains {
		if strings.HasPrefix(ref.URL, d) {
			return "", fmt.Errorf("migrate this entry from %q to %q to continue reading", m.name, m.name)
		}
	}
	slug := mboxLastSegment(ref.URL)
	if slug == "" {
		slug = ref.ID
	}
	if m.oldIDSlugs && mboxOldIDRe.MatchString(slug) && strings.TrimSpace(ref.Title) != "" {
		t := mboxSlugDropRe.ReplaceAllString(strings.ToLower(ref.Title), "")
		slug = strings.Trim(mboxSlugSepRe.ReplaceAllString(strings.TrimSpace(t), "-"), "-")
	}
	if slug == "" {
		return "", fmt.Errorf("%q is not a manga url", ref.URL)
	}
	return slug, nil
}

func (m *mboxSite) Details(ctx context.Context, ref sourcekit.Ref) (sourcekit.Details, error) {
	slug, err := m.slug(ref)
	if err != nil {
		return sourcekit.Details{}, err
	}
	doc, err := m.c.Document(ctx, sourcekit.Request{URL: m.base + "/manga/" + slug, Headers: m.headers()})
	if err != nil {
		return sourcekit.Details{}, notFound(err)
	}
	d := sourcekit.Details{Manga: sourcekit.Manga{URL: "/manga/" + slug, ID: slug}, Status: sourcekit.StatusUnknown,
		WebURL: m.base + "/manga/" + slug}
	info := doc.Find(mboxDetailsMain).First()
	if info.Length() == 0 {
		if strings.HasPrefix(strings.TrimSpace(doc.Find("body").Text()), "REDIRECT :") {
			return sourcekit.Details{}, fmt.Errorf("%s: the source URL has changed", slug)
		}
		return sourcekit.Details{}, fmt.Errorf("%w: %s has no manga info", sourcekit.ErrNotFound, slug)
	}
	d.Title = text(info.Find("h1, h2").First())
	var authors []string
	info.Find("li:contains(author) a, td:containsOwn(author) + td a").Each(func(_ int, a *goquery.Selection) {
		authors = append(authors, text(a))
	})
	d.Author = strings.Join(authors, ", ")
	status := madJoinText(info.Find("li:contains(status), td:containsOwn(status) + td"))
	switch {
	case strings.Contains(status, "Ongoing"):
		d.Status = sourcekit.StatusOngoing
	case strings.Contains(status, "Completed"):
		d.Status = sourcekit.StatusCompleted
	}
	// kakalot lists genres in an li, nelo in a table cell
	genres := info.Find("td:containsOwn(genres) + td a")
	if li := info.Find("div.manga-info-top li:contains(genres)").First(); li.Length() > 0 {
		genres = li.Find("a")
	}
	genres.Each(func(_ int, a *goquery.Selection) {
		if g := text(a); g != "" {
			d.Genres = append(d.Genres, g)
		}
	})

	desc := mboxOwnText(doc.Find(mboxDescSel).First())
	if d.Title != "" {
		desc = strings.TrimPrefix(desc, d.Title+" summary:")
	}
	desc = strings.TrimSpace(desc)
	if alt := mboxOwnText(doc.Find(mboxAltNameSel).First()); alt != "" {
		if desc != "" {
			desc += "\n\n"
		}
		desc += "Alternative Name: " + alt
	}
	d.Description = desc
	d.CoverURL = sourcekit.Abs(m.base, doc.Find(mboxThumbnailSel).First().AttrOr("src", ""))
	return d, nil
}

// mboxOwnText is jsoup's ownText(): the element's own text, not its children's.
func mboxOwnText(s *goquery.Selection) string {
	var b strings.Builder
	s.Contents().Each(func(_ int, n *goquery.Selection) {
		if goquery.NodeName(n) == "#text" {
			b.WriteString(n.Text())
		}
	})
	return strings.Join(strings.Fields(b.String()), " ")
}

func (m *mboxSite) Chapters(ctx context.Context, ref sourcekit.Ref) ([]sourcekit.Chapter, error) {
	slug, err := m.slug(ref)
	if err != nil {
		return nil, err
	}
	var out struct {
		Success bool `json:"success"`
		Data    *struct {
			Chapters []struct {
				Name      *string  `json:"chapter_name"`
				Slug      *string  `json:"chapter_slug"`
				Num       *float64 `json:"chapter_num"`
				UpdatedAt *string  `json:"updated_at"`
			} `json:"chapters"`
		} `json:"data"`
	}
	req := sourcekit.Request{URL: m.base + "/api/manga/" + url.PathEscape(slug) + "/chapters",
		Query: url.Values{"limit": {"-1"}}, Headers: m.headers()}
	if err := m.c.JSON(ctx, req, &out); err != nil {
		return nil, notFound(err)
	}
	if !out.Success || out.Data == nil || len(out.Data.Chapters) == 0 {
		return nil, fmt.Errorf("%w: no chapters listed", sourcekit.ErrNotFound)
	}
	// the extension names the site's host as every chapter's scanlator
	host := m.base
	if u, err := url.Parse(m.base); err == nil && u.Host != "" {
		host = u.Host
	}
	chapters := make([]sourcekit.Chapter, 0, len(out.Data.Chapters))
	for _, c := range out.Data.Chapters {
		if c.Slug == nil || *c.Slug == "" {
			continue
		}
		path := "/manga/" + slug + "/" + *c.Slug
		ch := sourcekit.Chapter{URL: path, Name: "Chapter", Scanlator: host, WebURL: m.base + path}
		if c.Name != nil {
			ch.Name = strings.TrimSpace(*c.Name)
		}
		if c.Num != nil {
			ch.Number = *c.Num
		} else {
			ch.Number = chapterNumber(ch.Name)
		}
		if c.UpdatedAt != nil {
			if t, err := time.Parse(time.RFC3339, *c.UpdatedAt); err == nil {
				ch.UploadedAt = &t
			}
		}
		chapters = append(chapters, ch)
	}
	return chapters, nil
}

var (
	mboxCDNsRe   = regexp.MustCompile(`cdns\s*=\s*\[([^]]+)]`)
	mboxBackupRe = regexp.MustCompile(`backupImage\s*=\s*\[([^]]+)]`)
	mboxImagesRe = regexp.MustCompile(`chapterImages\s*=\s*\[([^]]+)]`)
)

// Pages reads the chapter page's script: a list of CDNs and the images'
// paths on them. The extension falls back to the other CDNs when one fails
// (and can merge split images, off by default); mangarr gets the first CDN.
func (m *mboxSite) Pages(ctx context.Context, ch sourcekit.PageRef) ([]sourcekit.PageImage, error) {
	location := ch.URL
	if !strings.HasPrefix(location, "http://") && !strings.HasPrefix(location, "https://") {
		location = m.base + sourcekit.Path(location)
	}
	doc, err := m.c.Document(ctx, sourcekit.Request{URL: location, Headers: m.headers()})
	if err != nil {
		return nil, notFound(err)
	}
	var scripts []string
	doc.Find("script").Each(func(_ int, s *goquery.Selection) {
		if t := s.Text(); strings.Contains(t, "cdns =") {
			scripts = append(scripts, t)
		}
	})
	content := strings.Join(scripts, "\n")
	cdns := append(mboxArray(content, mboxCDNsRe), mboxArray(content, mboxBackupRe)...)
	images := mboxArray(content, mboxImagesRe)

	var urls []string
	if len(images) > 0 && len(cdns) > 0 {
		cdn, err := url.Parse(cdns[0])
		if err != nil || cdn.Host == "" {
			return nil, fmt.Errorf("%s: bad cdn %q", location, cdns[0])
		}
		for _, p := range images {
			// the path is already encoded: it goes in as it is
			urls = append(urls, cdn.Scheme+"://"+cdn.Host+strings.ReplaceAll("/"+p, "//", "/"))
		}
	} else {
		doc.Find("div.container-chapter-reader > img").Each(func(_ int, img *goquery.Selection) {
			urls = append(urls, sourcekit.Abs(location, img.AttrOr("src", "")))
		})
	}
	pages := make([]sourcekit.PageImage, 0, len(urls))
	for _, u := range urls {
		if u != "" {
			pages = append(pages, sourcekit.PageImage{Index: len(pages), URL: u, Headers: m.headers()})
		}
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("%w: the chapter has no pages", sourcekit.ErrNotFound)
	}
	return pages, nil
}

// mboxArray reads a JavaScript array of strings out of a script.
func mboxArray(script string, re *regexp.Regexp) []string {
	g := re.FindStringSubmatch(script)
	if g == nil {
		return nil
	}
	var out []string
	for _, s := range strings.Split(g[1], ",") {
		s = strings.TrimSpace(s)
		if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
			s = s[1 : len(s)-1]
		}
		s = strings.TrimSuffix(strings.ReplaceAll(s, `\/`, "/"), "/")
		out = append(out, s)
	}
	return out
}

// mboxLastSegment is the last part of a link's path.
func mboxLastSegment(raw string) string {
	p := strings.TrimRight(sourcekit.Path(strings.TrimSpace(raw)), "/")
	if i := strings.Index(p, "?"); i >= 0 {
		p = p[:i]
	}
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	return p
}
