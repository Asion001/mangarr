package sites

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// MangaHub is the engine behind the MangaHub network of sites (Keiyoushi's
// "mangahub" theme): one GraphQL API on api.mghcdn.com serves every site,
// told apart by a source code ("m01" for mangahub.io). The API wants a key,
// which the site hands out as the mhub_access cookie when a chapter page is
// opened; the engine fetches a fresh one whenever the API turns it down. One
// engine serves every site on the network; a site is an mhubSite with its
// own name, address and source code.

// mhubSites are the sites on the theme; each id is the one Keiyoushi derives
// from the extension's name, so Mihon backups link here.
var mhubSites = []mhubSite{
	{id: sourcekit.KeiyoushiID("MangaHub", "en", 1), name: "MangaHub", base: "https://mangahub.io", source: "m01"},
}

const (
	mhubAPI      = "https://api.mghcdn.com"
	mhubCDN      = "https://imgx.mghcdn.com"
	mhubThumbCDN = "https://thumb.mghcdn.com"
	// mhubPageSize is how many results the API gives a page.
	mhubPageSize = 30
)

func init() {
	for _, s := range mhubSites {
		sourcekit.Register(s.id, func(d sourcekit.Deps) sourcekit.Site {
			m := s
			m.c = d.Client
			m.api, m.cdn, m.thumbs = mhubAPI, mhubCDN, mhubThumbCDN
			m.refresh = &mhubRefresh{}
			return &m
		})
	}
}

type mhubSite struct {
	c    *sourcekit.Client
	id   string
	name string
	// base, api, cdn and thumbs are the addresses to talk to (tests point
	// them at a recorded copy).
	base, api, cdn, thumbs string
	// source is the network's code for the site.
	source string
	nsfw   bool

	refresh *mhubRefresh
}

// mhubRefresh keeps concurrent calls from all fetching a new key at once.
type mhubRefresh struct {
	mu   sync.Mutex
	last time.Time
}

func (m *mhubSite) Info() sourcekit.Info {
	return sourcekit.Info{ID: m.id, Name: m.name, Lang: "en", BaseURL: m.base, NSFW: m.nsfw, SupportsBrowse: true,
		IconURL: m.base + "/favicon.ico"}
}

// Politeness: the extension sets no limit of its own, and the API hands out
// keys grudgingly, so stay gentle.
func (m *mhubSite) Politeness() sourcekit.Politeness {
	return sourcekit.Politeness{RequestsPerMinute: 60, MaxConcurrent: 2}
}

// pageHeaders are what a browser sends opening one of the site's pages.
func (m *mhubSite) pageHeaders(referer string) map[string]string {
	if referer == "" {
		referer = m.base + "/"
	}
	return map[string]string{
		"Referer": referer, "Origin": m.base,
		"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.9",
		"Accept-Language": "en-US,en;q=0.5", "DNT": "1",
		"Sec-Fetch-Dest": "document", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Site": "same-origin",
		"Upgrade-Insecure-Requests": "1",
	}
}

// ---- the API ----------------------------------------------------------------

var mhubRetryRe = regexp.MustCompile(`rate\s*limit|api\s*key|encryption`)

// errMhubNoKey: the site hasn't handed out an API key yet.
var errMhubNoKey = fmt.Errorf("mangahub: mhub_access cookie not found")

// query runs one GraphQL query, fetching a new API key and trying again once
// when there is none or the API refuses the one there is.
func (m *mhubSite) query(ctx context.Context, q, refreshURL string, out any) error {
	err := m.post(ctx, q, out)
	if err == nil {
		return nil
	}
	if err != errMhubNoKey && !mhubRetryRe.MatchString(strings.ToLower(err.Error())) {
		return err
	}
	if err := m.newKey(ctx, refreshURL); err != nil {
		return err
	}
	return m.post(ctx, q, out)
}

func (m *mhubSite) post(ctx context.Context, q string, out any) error {
	key := m.key()
	if key == "" {
		return errMhubNoKey
	}
	body, err := mhubJSON(map[string]any{"query": q})
	if err != nil {
		return err
	}
	h := m.pageHeaders("")
	delete(h, "Upgrade-Insecure-Requests")
	h["Accept"] = "application/json"
	h["Content-Type"] = "application/json"
	h["Sec-Fetch-Dest"], h["Sec-Fetch-Mode"], h["Sec-Fetch-Site"] = "empty", "cors", "cross-site"
	h["x-mhub-access"] = key
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := m.c.JSON(ctx, sourcekit.Request{Method: http.MethodPost, URL: m.api + "/graphql", Body: body, Headers: h}, &env); err != nil {
		return err
	}
	if len(env.Errors) > 0 {
		msgs := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("mangahub: %s", strings.Join(msgs, "; "))
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return fmt.Errorf("mangahub: empty answer")
	}
	return json.Unmarshal(env.Data, out)
}

// key is the API key the site last handed out.
func (m *mhubSite) key() string {
	u, err := url.Parse(m.base)
	if err != nil || m.c.HTTP.Jar == nil {
		return ""
	}
	for _, c := range m.c.HTTP.Jar.Cookies(u) {
		if c.Name == "mhub_access" && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

// newKey opens a chapter page, which is where the site hands out API keys:
// first as a browser would, then asking it outright to reload the key.
func (m *mhubSite) newKey(ctx context.Context, refreshURL string) error {
	m.refresh.mu.Lock()
	defer m.refresh.mu.Unlock()
	if time.Since(m.refresh.last) < 10*time.Second {
		return nil // another call just did
	}
	if refreshURL == "" {
		refreshURL = fmt.Sprintf("%s/chapter/martial-peak/chapter-%d", m.base, 1000+rand.IntN(2000))
	}
	u, err := url.Parse(refreshURL)
	if err != nil {
		return err
	}
	referer := m.base + "/"
	if parts := strings.Split(strings.Trim(u.Path, "/"), "/"); len(parts) > 1 {
		referer = m.base + "/manga/" + parts[1]
	}
	base, _ := url.Parse(m.base)
	old := m.key()
	for i := 1; i <= 2; i++ {
		if m.c.HTTP.Jar != nil && base != nil {
			m.c.HTTP.Jar.SetCookies(base, []*http.Cookie{{Name: "mhub_access", Value: "", Path: "/", MaxAge: -1}})
		}
		req := sourcekit.Request{URL: refreshURL, Headers: m.pageHeaders(referer)}
		if i == 2 {
			req.Query = url.Values{"reloadKey": {"1"}}
		}
		// the page's status doesn't matter, only the cookie it sets
		if _, err := m.c.Do(ctx, req); err != nil {
			var se *sourcekit.StatusError
			if !errorsAs(err, &se) {
				return fmt.Errorf("mangahub: could not get a new API key: %w", err)
			}
		}
		if k := m.key(); k != old {
			break
		}
	}
	m.refresh.last = time.Now()
	return nil
}

// mhubJSON encodes without escaping <, > and &, as the site's own client does.
func mhubJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

// mhubString is a GraphQL string literal's contents.
func mhubString(s string) string {
	b, _ := mhubJSON(s)
	return strings.TrimSuffix(strings.TrimPrefix(string(b), `"`), `"`)
}

// mhubFloat writes a number the way Kotlin prints a Float ("12.0", "12.5"):
// the extension builds chapter links and queries with it.
func mhubFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 32)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// ---- lists ------------------------------------------------------------------

func (m *mhubSite) Search(ctx context.Context, query string, page int) (sourcekit.Results, error) {
	return m.list(ctx, page, "POPULAR", strings.TrimSpace(query))
}

func (m *mhubSite) Popular(ctx context.Context, page int) (sourcekit.Results, error) {
	return m.list(ctx, page, "POPULAR", "")
}

func (m *mhubSite) Latest(ctx context.Context, page int) (sourcekit.Results, error) {
	return m.list(ctx, page, "LATEST", "")
}

func (m *mhubSite) list(ctx context.Context, page int, order, query string) (sourcekit.Results, error) {
	if page < 1 {
		page = 1
	}
	q := fmt.Sprintf(`{
    search(x: %s, q: "%s", genre: "all", mod: %s, offset: %d) {
        rows {
            title,
            slug,
            image
        }
    }
}`, m.source, mhubString(query), order, (page-1)*mhubPageSize)
	var out struct {
		Search *struct {
			Rows []struct {
				Title string `json:"title"`
				Slug  string `json:"slug"`
				Image string `json:"image"`
			} `json:"rows"`
		} `json:"search"`
	}
	if err := m.query(ctx, q, "", &out); err != nil {
		return sourcekit.Results{}, err
	}
	if out.Search == nil {
		return sourcekit.Results{}, fmt.Errorf("mangahub: no search results in the answer")
	}
	var res sourcekit.Results
	for _, r := range out.Search.Rows {
		if r.Slug == "" {
			continue
		}
		res.Mangas = append(res.Mangas, sourcekit.Manga{URL: "/manga/" + r.Slug, ID: r.Slug, Title: strings.TrimSpace(r.Title),
			CoverURL: m.thumb(r.Image)})
	}
	res.HasNext = len(out.Search.Rows) == mhubPageSize
	return res, nil
}

func (m *mhubSite) thumb(image string) string {
	if strings.TrimSpace(image) == "" {
		return ""
	}
	return m.thumbs + "/" + image
}

// ---- one manga --------------------------------------------------------------

// mhubManga is the API's manga, chapters included.
type mhubManga struct {
	Title            string `json:"title"`
	Slug             string `json:"slug"`
	Status           string `json:"status"`
	Image            string `json:"image"`
	Author           string `json:"author"`
	Artist           string `json:"artist"`
	Genres           string `json:"genres"`
	Description      string `json:"description"`
	AlternativeTitle string `json:"alternativeTitle"`
	Chapters         []struct {
		Number float64 `json:"number"`
		Title  string  `json:"title"`
		Date   string  `json:"date"`
	} `json:"chapters"`
}

func (m *mhubSite) manga(ctx context.Context, ref sourcekit.Ref) (string, *mhubManga, error) {
	slug := mhubSlug(ref)
	if slug == "" {
		return "", nil, fmt.Errorf("%q is not a manga url", ref.URL)
	}
	q := fmt.Sprintf(`{
    manga(x: %s, slug: "%s") {
            title,
            slug,
            status,
            image,
            author,
            artist,
            genres,
            description,
            alternativeTitle,
            chapters {
                number,
                title,
                date
            }
    }
}`, m.source, mhubString(slug))
	var out struct {
		Manga *mhubManga `json:"manga"`
	}
	if err := m.query(ctx, q, m.base+"/manga/"+slug, &out); err != nil {
		return "", nil, err
	}
	if out.Manga == nil || out.Manga.Title == "" {
		return "", nil, fmt.Errorf("%w: manga %s", sourcekit.ErrNotFound, slug)
	}
	return slug, out.Manga, nil
}

func (m *mhubSite) Details(ctx context.Context, ref sourcekit.Ref) (sourcekit.Details, error) {
	slug, x, err := m.manga(ctx, ref)
	if err != nil {
		return sourcekit.Details{}, err
	}
	d := sourcekit.Details{Manga: sourcekit.Manga{URL: "/manga/" + slug, ID: slug, Title: strings.TrimSpace(x.Title),
		CoverURL: m.thumb(x.Image)}, Author: strings.TrimSpace(x.Author), Artist: strings.TrimSpace(x.Artist),
		Status: sourcekit.StatusUnknown, WebURL: m.base + "/manga/" + slug}
	switch x.Status {
	case "ongoing":
		d.Status = sourcekit.StatusOngoing
	case "completed":
		d.Status = sourcekit.StatusCompleted
	}
	for _, g := range strings.Split(x.Genres, ",") {
		if g = strings.TrimSpace(g); g != "" {
			d.Genres = append(d.Genres, g)
		}
	}
	desc := strings.TrimSpace(x.Description)
	var alt []string
	for _, a := range strings.Split(x.AlternativeTitle, ";") {
		if a = strings.TrimSpace(a); a != "" {
			alt = append(alt, "- "+a)
		}
	}
	if len(alt) > 0 {
		if desc != "" {
			desc += "\n\n"
		}
		desc += "Alternative Names:\n" + strings.Join(alt, "\n")
	}
	d.Description = desc
	n := len(x.Chapters)
	d.Chapters = &n
	return d, nil
}

var mhubSpaceRe = regexp.MustCompile(`\s+`)

func (m *mhubSite) Chapters(ctx context.Context, ref sourcekit.Ref) ([]sourcekit.Chapter, error) {
	slug, x, err := m.manga(ctx, ref)
	if err != nil {
		return nil, err
	}
	// the API lists oldest first
	out := make([]sourcekit.Chapter, 0, len(x.Chapters))
	for i := len(x.Chapters) - 1; i >= 0; i-- {
		c := x.Chapters[i]
		number := strings.TrimSuffix(mhubFloat(c.Number), ".0")
		title := mhubSpaceRe.ReplaceAllString(strings.TrimSpace(c.Title), " ")
		name := "Chapter " + number
		switch {
		case strings.Contains(title, number):
			name = title
		case title != "":
			name = "Chapter " + number + " - " + title
		}
		path := "/" + slug + "/chapter-" + mhubFloat(c.Number)
		ch := sourcekit.Chapter{URL: path, Name: name, Number: c.Number, WebURL: m.base + "/chapter" + path}
		if t, err := time.Parse(time.RFC3339, c.Date); err == nil {
			ch.UploadedAt = &t
		}
		out = append(out, ch)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no chapters listed", sourcekit.ErrNotFound)
	}
	return out, nil
}

// Pages asks the API for the chapter's images. The extension also reports
// the view to the site (after looking up the machine's public address) to
// look more like a browser when it next asks for a key; that call is left out.
func (m *mhubSite) Pages(ctx context.Context, ch sourcekit.PageRef) ([]sourcekit.PageImage, error) {
	slug, number, ok := mhubChapter(ch.URL)
	if !ok {
		return nil, fmt.Errorf("%q is not a chapter url", ch.URL)
	}
	q := fmt.Sprintf(`{
    chapter(x: %s, slug: "%s", number: %s) {
            pages,
            mangaID,
            number
        }
}`, m.source, mhubString(slug), mhubFloat(number))
	var out struct {
		Chapter *struct {
			Pages   string  `json:"pages"`
			MangaID int     `json:"mangaID"`
			Number  float64 `json:"number"`
		} `json:"chapter"`
	}
	if err := m.query(ctx, q, m.base+"/chapter/"+slug+"/chapter-"+mhubFloat(number), &out); err != nil {
		return nil, err
	}
	if out.Chapter == nil {
		return nil, fmt.Errorf("%w: chapter %s of %s", sourcekit.ErrNotFound, mhubFloat(number), slug)
	}
	raw := out.Chapter.Pages
	if strings.HasPrefix(raw, "enc:v1:") {
		plain, err := m.decryptPages(ctx, raw)
		if err != nil {
			return nil, err
		}
		raw = plain
	}
	paths, err := mhubPagePaths(raw)
	if err != nil {
		return nil, err
	}
	m.markRecent(out.Chapter.MangaID, out.Chapter.Number)
	pages := make([]sourcekit.PageImage, 0, len(paths))
	for _, p := range paths {
		u := p
		if !strings.HasPrefix(p, "http://") && !strings.HasPrefix(p, "https://") {
			u = m.cdn + "/" + strings.TrimPrefix(p, "/")
		}
		pages = append(pages, sourcekit.PageImage{Index: len(pages), URL: u,
			Headers: map[string]string{"Referer": m.base + "/"}})
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("%w: the chapter has no pages", sourcekit.ErrNotFound)
	}
	return pages, nil
}

// mhubPagePaths reads the chapter's page list, which comes as {"p": prefix,
// "i": [names]}, a plain array, or an object of paths.
func mhubPagePaths(raw string) ([]string, error) {
	var list struct {
		Page   string   `json:"p"`
		Images []string `json:"i"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err == nil && list.Images != nil {
		out := make([]string, 0, len(list.Images))
		for _, img := range list.Images {
			out = append(out, list.Page+img)
		}
		return out, nil
	}
	var arr []string
	if err := json.Unmarshal([]byte(raw), &arr); err == nil {
		return arr, nil
	}
	var obj map[string]string
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, fmt.Errorf("mangahub: chapter pages: %w", err)
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, errA := strconv.Atoi(keys[i])
		b, errB := strconv.Atoi(keys[j])
		if errA == nil && errB == nil {
			return a < b
		}
		return keys[i] < keys[j]
	})
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, obj[k])
	}
	return out, nil
}

// decryptPages opens an "enc:v1:<key id>:<iv>:<tag>:<data>" page list: AES-GCM
// with a key the site serves from /api/chapter-crypto.
func (m *mhubSite) decryptPages(ctx context.Context, raw string) (string, error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 6 {
		return "", fmt.Errorf("mangahub: unexpected encrypted page list")
	}
	h := m.pageHeaders("")
	delete(h, "Upgrade-Insecure-Requests")
	h["Accept"] = "application/json"
	h["Sec-Fetch-Dest"], h["Sec-Fetch-Mode"], h["Sec-Fetch-Site"] = "empty", "cors", "same-origin"
	var params struct {
		Keys  map[string]string `json:"keys"`
		Key   string            `json:"key"`
		KeyID string            `json:"keyId"`
	}
	if err := m.c.JSON(ctx, sourcekit.Request{URL: m.base + "/api/chapter-crypto", Headers: h}, &params); err != nil {
		return "", fmt.Errorf("mangahub: page key: %w", err)
	}
	keyID := parts[2]
	key := params.Keys[keyID]
	if key == "" && (params.KeyID == "" || params.KeyID == keyID) {
		key = params.Key
	}
	if key == "" {
		return "", fmt.Errorf("mangahub: no page key %s", keyID)
	}
	dec := func(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "=")) }
	keyBytes, err1 := dec(key)
	iv, err2 := dec(parts[3])
	tag, err3 := dec(parts[4])
	data, err4 := dec(parts[5])
	for _, err := range []error{err1, err2, err3, err4} {
		if err != nil {
			return "", fmt.Errorf("mangahub: encrypted page list: %w", err)
		}
	}
	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return "", fmt.Errorf("mangahub: page key: %w", err)
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, len(iv))
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, iv, append(data, tag...), nil)
	if err != nil {
		return "", fmt.Errorf("mangahub: decrypt page list: %w", err)
	}
	return string(plain), nil
}

// markRecent sets the "recently read" cookie a browser would have after
// opening the chapter, which makes the site likelier to hand out a working
// key next time.
func (m *mhubSite) markRecent(mangaID int, number float64) {
	u, err := url.Parse(m.base)
	if err != nil || m.c.HTTP.Jar == nil {
		return
	}
	now := time.Now()
	recent, err := json.Marshal(map[string]any{strconv.FormatInt(now.UnixMilli(), 10): map[string]any{
		"mangaID": mangaID, "number": number}})
	if err != nil {
		return
	}
	m.c.HTTP.Jar.SetCookies(u, []*http.Cookie{{Name: "recently", Value: url.QueryEscape(string(recent)), Path: "/",
		Expires: now.Add(60 * 24 * time.Hour)}})
}

// mhubSlug is the manga's slug, from "/manga/<slug>" or a chapter link.
func mhubSlug(ref sourcekit.Ref) string {
	parts := strings.Split(strings.Trim(sourcekit.Path(ref.URL), "/"), "/")
	if len(parts) >= 2 && (parts[0] == "manga" || parts[0] == "chapter") {
		return parts[1]
	}
	if len(parts) == 1 && parts[0] != "" {
		return parts[0]
	}
	return ref.ID
}

// mhubChapter reads "/<slug>/chapter-<number>" (the site's own
// "/chapter/<slug>/chapter-<number>" is accepted too).
func mhubChapter(raw string) (string, float64, bool) {
	parts := strings.Split(strings.Trim(sourcekit.Path(raw), "/"), "/")
	if len(parts) == 3 && parts[0] == "chapter" {
		parts = parts[1:]
	}
	if len(parts) != 2 {
		return "", 0, false
	}
	_, num, ok := strings.Cut(parts[1], "-")
	if !ok {
		return "", 0, false
	}
	n, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return "", 0, false
	}
	return parts[0], n, true
}
