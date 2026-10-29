package sites

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// MangaFire has a JSON API, but every call to it carries a "vrf": the
// canonical form of the request run through three fixed substitution
// stages, as the site's own script does. The stages are constants, so it is
// all computed here; nothing of the site's runs.
//
// It is one catalog per language, with the ids Mihon's MangaFire extension
// gives its sources, and the urls that extension stores ("/title/<hid>-<slug>"
// and "<manga>/<id>-chapter-<n>-<lang>"), so a Mihon backup links up here.
var mfLangs = []string{"en", "es", "es-419", "fr", "ja", "pt", "pt-BR"}

const (
	mfSite     = "https://mangafire.to"
	mfPageSize = "50"
)

// mfRatings are the site's content ratings, mildest first.
var mfRatings = []string{"safe", "suggestive", "erotica", "pornographic"}

func init() {
	sourcekit.RegisterLangs("MangaFire", 1, mfLangs, func(d sourcekit.Deps, lang string) sourcekit.Site {
		return &mangafire{c: d.Client, base: mfSite, code: lang, lang: mfLang(lang)}
	})
}

// mfLang is the site's code for one of Keiyoushi's language codes.
func mfLang(code string) string {
	switch code {
	case "es-419":
		return "es-la"
	case "pt-BR":
		return "pt-br"
	}
	return code
}

type mangafire struct {
	c *sourcekit.Client
	// base is the address to talk to (tests point it at a recorded copy).
	base string
	// code is the catalog's language as Keiyoushi writes it ("pt-BR"), lang
	// the site's code for it ("pt-br"): the chapters listed.
	code, lang string
	// ratings limit results to these content ratings (none: all of them).
	ratings []string
}

func (f *mangafire) Info() sourcekit.Info {
	return sourcekit.Info{ID: sourcekit.KeiyoushiID("MangaFire", f.code, 1), Name: "MangaFire", Lang: f.code, BaseURL: f.base,
		SupportsBrowse: true, IconURL: f.base + "/favicon.ico"}
}

// Politeness: the extension holds itself to two requests a second.
func (f *mangafire) Politeness() sourcekit.Politeness {
	return sourcekit.Politeness{RequestsPerMinute: 120, MaxConcurrent: 2}
}

func (f *mangafire) Options() []sourcekit.Option {
	choices := make([]sourcekit.Choice, 0, len(mfRatings))
	for _, r := range mfRatings {
		choices = append(choices, sourcekit.Choice{Value: r, Label: strings.ToUpper(r[:1]) + r[1:]})
	}
	value := append([]string{}, f.ratings...)
	return []sourcekit.Option{{Key: "content_rating", Title: "Content rating", Type: "multiselect", Value: value,
		Choices: choices, Help: "Only titles with these ratings in browse and search (none picked: all of them)."}}
}

func (f *mangafire) SetOption(key string, value any) error {
	if key != "content_rating" {
		return fmt.Errorf("%w: no option %q", sourcekit.ErrUnsupported, key)
	}
	var picked []string
	switch v := value.(type) {
	case []string:
		picked = v
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				picked = append(picked, s)
			}
		}
	case string:
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				picked = append(picked, s)
			}
		}
	case nil:
	default:
		return fmt.Errorf("%v is not a list of ratings", value)
	}
	// kept in the site's order, like the extension's filter
	f.ratings = nil
	for _, r := range mfRatings {
		for _, p := range picked {
			if p == r {
				f.ratings = append(f.ratings, r)
				break
			}
		}
	}
	return nil
}

// ---- signing ------------------------------------------------------------------

// mfParam is one query parameter; order matters to the signature.
type mfParam struct{ key, value string }

type mfStage struct {
	table, key []byte
	iv         byte
}

// mfStages are the three substitution stages of the site's signer.
var mfStages = []mfStage{
	{mfB64("yINlmUNho8VYJT+ibTIP+9ESiULpVEtMOoD6U6lRE0R/xwXo/Xp9NrUgC4cw/Lmo33vUyjUE40kUoEWIr/fxfNNcq2s79ShQ5NhNrFnJ4hXPwOu/SuXzIbuTQKGFvfm08E9jvCfqAtoDqvQq3dVWPQFmJjgvkISBeXY3BgANR+yVnjGbcxZ47d6kLNfZPIayTq3/YGySb1KuVZodWp/WGNAO5pfMcpaK53Hhs0allBszaMaxuouOwdxbwgxIw6YunSsXjI05Yi0j9j4eHKfSXR8Ifo/Od+8iamRfCXTyvm7NGRGYdcQ0ywcK/u6RXhrbcCm4t2eCtrDgQVecJGkQ+A=="),
		mfB64("0Ec58JOY3uBzJK9m3zqIOpdlF7UFiax9DmA="), 0x5A},
	{mfB64("IUFltCxD3Oc2cwCgkJffthaOg9cgPUb0LgW6H/VtfcF0kc5F25t+aWj6JH9VOhOaY0rAFdUxlDnl5BLNvwEJvQtP5qcw7vdb/K+chnbwnspSHT8mz5lqwz41TezG0hkO06FTjJZhsyNuFLDpD2ZZxQj/QIRcF90zpmQ7Byu483WsQqUE0C342HL+JXngRB6fRzxRyVTaKu83h7UYTJ0QMt6ixFh6S3F8gqkKwrGTL3jHNBsD45UnifK8+RGtishQV2K3rujLKEkiZxpr2dYcudFW4oFsDKhad3CLBvuyTqsCo4B7mL5IKQ1vXo/MOOvq1I1d8ar9X6Ttu5KF4fZgiA=="),
		mfB64("AAdjb1iPY8CiDmq9H34tKTBF8a3oDQ=="), 0x35},
	{mfB64("NQHlu1/wVO5EmkwQymF810qqY2xG1k2obcas4Z9mCsPEIFl9pRIjFxbJ7ybMHbBckT5Ton85E0FOeHezbh/mjlEYpmpnlXOS8dgrqeq2KfxImTh1YK9y0PeMNhzA1OQzSY9brYOJq/l2QnE/hwOeZIhPixVSKIUlDb5vLcH6RWKxkIEMuP0bDwIqQ71AJJaEaMJL7A6YtyIwoRT+L5v4aZzodN/0+3nOGsfblFjgxSfPzVDjNFeNl5P26+kEC/8AHgdrpAbt3hHz3HrRN1Y6e+JHgF7ncFWnoF0y3THL1S71WgWGCa6KtSzTCCG58n68nTyj2T3Sshk7utqCtMi/ZQ=="),
		mfB64("DELOJgPsVaCcblDtTGMdHzM="), 0xBA},
}

func mfB64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic("mangafire: bad signer constant: " + err.Error())
	}
	return b
}

// mfSign is the vrf for a canonical request ("/titles/x?a=1&b=2").
func mfSign(canonical string) string {
	data := []byte(canonical)
	for _, st := range mfStages {
		out := make([]byte, len(data))
		prev := st.iv
		for i, b := range data {
			prev = st.table[b^st.key[i%len(st.key)]^prev]
			out[i] = prev
		}
		data = out
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

// mfURL builds a signed API url. Parameters are sorted by name (a stable
// sort: repeated ones keep their order) and the vrf signs the path without
// "/api", then the decoded parameters, with "name[]" numbered "name[0]",
// "name[1]"...
func (f *mangafire) mfURL(path string, params []mfParam) string {
	sorted := append([]mfParam{}, params...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].key < sorted[j].key })
	var canon, query strings.Builder
	canon.WriteString(strings.TrimPrefix(path, "/api"))
	lastKey, index := "", 0
	for i, p := range sorted {
		key := p.key
		if strings.HasSuffix(key, "[]") {
			if lastKey != key {
				index = 0
			}
			lastKey = key
			key = strings.Replace(key, "[]", "["+strconv.Itoa(index)+"]", 1)
			index++
		}
		if i == 0 {
			canon.WriteByte('?')
		} else {
			canon.WriteByte('&')
		}
		canon.WriteString(key + "=" + p.value)
		query.WriteString(mfEscape(p.key) + "=" + mfEscape(p.value) + "&")
	}
	query.WriteString("vrf=" + mfSign(canon.String()))
	return f.base + path + "?" + query.String()
}

// mfEscape encodes a query component the way the extension's HTTP client
// does (a space is %20, not +).
func mfEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func (f *mangafire) get(ctx context.Context, path string, params []mfParam, out any) error {
	req := sourcekit.Request{URL: f.mfURL(path, params), Headers: map[string]string{"Referer": f.base + "/"}}
	if err := f.c.JSON(ctx, req, out); err != nil {
		var se *sourcekit.StatusError
		if errorsAs(err, &se) {
			switch {
			case se.Code == http.StatusForbidden && strings.Contains(se.Body, "captcha_required"):
				return fmt.Errorf("%w (MangaFire wants its shape captcha solved, which mangarr can't do; try again later)", err)
			case se.Code == http.StatusNotFound:
				return fmt.Errorf("%w: %v", sourcekit.ErrNotFound, err)
			}
		}
		return err
	}
	return nil
}

// ---- lists --------------------------------------------------------------------

type mfPoster struct {
	Small  string `json:"small"`
	Medium string `json:"medium"`
	Large  string `json:"large"`
}

func (p *mfPoster) best() string {
	if p == nil {
		return ""
	}
	return mpFirst(p.Large, p.Medium, p.Small)
}

type mfManga struct {
	HID    string    `json:"hid"`
	Slug   string    `json:"slug"`
	Title  string    `json:"title"`
	Poster *mfPoster `json:"poster"`
}

func (m mfManga) path() string {
	if m.Slug != "" {
		return "/title/" + m.HID + "-" + m.Slug
	}
	return "/title/" + m.HID
}

type mfMeta struct {
	LastPage int  `json:"lastPage"`
	HasNext  bool `json:"hasNext"`
}

func (f *mangafire) Popular(ctx context.Context, page int) (sourcekit.Results, error) {
	return f.list(ctx, f.browseParams(page, "order[views_30d]"))
}

func (f *mangafire) Latest(ctx context.Context, page int) (sourcekit.Results, error) {
	return f.list(ctx, f.browseParams(page, "order[chapter_updated_at]"))
}

func (f *mangafire) browseParams(page int, order string) []mfParam {
	if page < 1 {
		page = 1
	}
	params := []mfParam{{order, "desc"}, {"page", strconv.Itoa(page)}, {"limit", mfPageSize}}
	for _, r := range f.ratings {
		params = append(params, mfParam{"content_rating[]", r})
	}
	return params
}

// Search asks what the extension's search asks with its filters untouched:
// the query, the content ratings, genres and themes matched with AND, best
// match first.
func (f *mangafire) Search(ctx context.Context, query string, page int) (sourcekit.Results, error) {
	if page < 1 {
		page = 1
	}
	var params []mfParam
	if q := strings.TrimSpace(query); q != "" {
		params = append(params, mfParam{"keyword", q})
	}
	params = append(params, mfParam{"page", strconv.Itoa(page)}, mfParam{"limit", mfPageSize})
	for _, r := range f.ratings {
		params = append(params, mfParam{"content_rating[]", r})
	}
	params = append(params, mfParam{"genres_mode", "and"}, mfParam{"theme_mode", "and"}, mfParam{"order[relevance]", "desc"})
	return f.list(ctx, params)
}

func (f *mangafire) list(ctx context.Context, params []mfParam) (sourcekit.Results, error) {
	var out struct {
		Items []mfManga `json:"items"`
		Meta  *mfMeta   `json:"meta"`
	}
	if err := f.get(ctx, "/api/titles", params, &out); err != nil {
		return sourcekit.Results{}, err
	}
	res := sourcekit.Results{Mangas: make([]sourcekit.Manga, 0, len(out.Items))}
	for _, m := range out.Items {
		if m.HID == "" {
			continue
		}
		res.Mangas = append(res.Mangas, sourcekit.Manga{URL: m.path(), ID: m.HID, Title: m.Title, CoverURL: m.Poster.best()})
	}
	res.HasNext = out.Meta != nil && out.Meta.HasNext
	return res, nil
}

// ---- one manga --------------------------------------------------------------

func (f *mangafire) Details(ctx context.Context, ref sourcekit.Ref) (sourcekit.Details, error) {
	hid := mfHID(ref.URL, ref.ID)
	if hid == "" {
		return sourcekit.Details{}, fmt.Errorf("%q is not a title url", ref.URL)
	}
	type entity struct {
		Title string `json:"title"`
	}
	var out struct {
		Data struct {
			mfManga
			Type         string   `json:"type"`
			Status       string   `json:"status"`
			SynopsisHTML string   `json:"synopsisHtml"`
			Authors      []entity `json:"authors"`
			Artists      []entity `json:"artists"`
			Genres       []entity `json:"genres"`
			Themes       []entity `json:"themes"`
			// Rating is the users' score out of 10.
			Rating      float64 `json:"rating"`
			RatingCount int     `json:"ratingCount"`
		} `json:"data"`
	}
	if err := f.get(ctx, "/api/titles/"+hid, nil, &out); err != nil {
		return sourcekit.Details{}, err
	}
	x := out.Data
	if x.HID == "" {
		return sourcekit.Details{}, fmt.Errorf("%w: title %s", sourcekit.ErrNotFound, hid)
	}
	names := func(es []entity) []string {
		var s []string
		for _, e := range es {
			s = append(s, e.Title)
		}
		return s
	}
	d := sourcekit.Details{Manga: sourcekit.Manga{URL: x.path(), ID: x.HID, Title: x.Title, CoverURL: x.Poster.best()},
		Author: strings.Join(names(x.Authors), ", "), Artist: strings.Join(names(x.Artists), ", "),
		Status: mfStatus(x.Status), WebURL: f.base + x.path()}
	if x.SynopsisHTML != "" {
		if doc, err := goquery.NewDocumentFromReader(strings.NewReader(x.SynopsisHTML)); err == nil {
			d.Description = text(doc.Find("body"))
		}
	}
	if x.Type != "" {
		d.Genres = append(d.Genres, strings.ToUpper(x.Type[:1])+x.Type[1:])
	}
	d.Genres = append(d.Genres, names(x.Genres)...)
	d.Genres = append(d.Genres, names(x.Themes)...)
	if x.Rating > 0 {
		d.Rating = &sourcekit.Rating{Score: math.Round(x.Rating*100) / 100, Max: 10, Votes: x.RatingCount}
	}
	return d, nil
}

func mfStatus(s string) string {
	switch strings.ToLower(s) {
	case "releasing":
		return sourcekit.StatusOngoing
	case "finished":
		return sourcekit.StatusCompleted
	case "on_hiatus":
		return sourcekit.StatusHiatus
	case "discontinued":
		return sourcekit.StatusCancelled
	}
	return sourcekit.StatusUnknown
}

type mfChapter struct {
	ID        int     `json:"id"`
	Number    float32 `json:"number"`
	Name      *string `json:"name"`
	CreatedAt *int64  `json:"createdAt"`
	Type      *string `json:"type"`
}

// mfChapterNo finds the number in a chapter's own name ("Ch. 12.5", "Episode 3").
var mfChapterNo = regexp.MustCompile(`(?i)\b(?:ch(?:\.|apter)?|ep(?:\.|isode)?)\s?(\d+(?:\.\d+)?)`)

func (f *mangafire) Chapters(ctx context.Context, ref sourcekit.Ref) ([]sourcekit.Chapter, error) {
	hid := mfHID(ref.URL, ref.ID)
	if hid == "" {
		return nil, fmt.Errorf("%q is not a title url", ref.URL)
	}
	mangaPath := strings.TrimRight(sourcekit.Path(ref.URL), "/")
	if !strings.HasPrefix(mangaPath, "/") {
		mangaPath = "/title/" + hid
	}
	var all []mfChapter
	for page, last := 1, 1; page <= last; page++ {
		var out struct {
			Items []mfChapter `json:"items"`
			Meta  *mfMeta     `json:"meta"`
		}
		params := []mfParam{{"language", f.lang}, {"sort", "number"}, {"order", "desc"},
			{"page", strconv.Itoa(page)}, {"limit", "200"}}
		if err := f.get(ctx, "/api/titles/"+hid+"/chapters", params, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Items...)
		if page == 1 && out.Meta != nil {
			last = out.Meta.LastPage
		}
	}
	chapters := make([]sourcekit.Chapter, 0, len(all))
	for _, c := range all {
		chapters = append(chapters, f.chapter(mangaPath, c))
	}
	return chapters, nil
}

func (f *mangafire) chapter(mangaPath string, c mfChapter) sourcekit.Chapter {
	num := strconv.FormatFloat(float64(c.Number), 'f', -1, 32)
	path := fmt.Sprintf("%s/%d-chapter-%s-%s", mangaPath, c.ID, num, f.lang)
	ch := sourcekit.Chapter{URL: path, ID: strconv.Itoa(c.ID), Number: mfNumber(c.Number), Scanlator: "Unknown",
		WebURL: f.base + path}
	name := ""
	if c.Name != nil {
		name = strings.TrimSpace(*c.Name)
	}
	switch m := mfChapterNo.FindStringSubmatch(name); {
	case name == "":
		ch.Name = "Ch. " + num
	case m != nil:
		// the name has its own number, which wins over the listed one
		if n, err := strconv.ParseFloat(m[1], 32); err == nil {
			ch.Number, ch.Name = mfNumber(float32(n)), *c.Name
			break
		}
		ch.Name = "Ch. " + num + " - " + *c.Name
	default:
		ch.Name = "Ch. " + num + " - " + *c.Name
	}
	if c.Type != nil {
		ch.Scanlator = *c.Type
	}
	if c.CreatedAt != nil && *c.CreatedAt > 0 {
		t := time.Unix(*c.CreatedAt, 0).UTC()
		ch.UploadedAt = &t
	}
	return ch
}

// mfNumber turns the API's single-precision number into the decimal it
// stands for (12.1, not 12.100000381).
func mfNumber(n float32) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatFloat(float64(n), 'f', -1, 32), 64)
	return v
}

func (f *mangafire) Pages(ctx context.Context, ch sourcekit.PageRef) ([]sourcekit.PageImage, error) {
	parts := strings.Split(strings.Trim(sourcekit.Path(ch.URL), "/"), "/")
	last := parts[len(parts)-1]
	var endpoint string
	isVolume := false
	for _, p := range parts {
		if p == "volume" {
			isVolume = true
		}
	}
	if isVolume {
		endpoint = "/api/volumes/" + last
	} else {
		id, _, _ := strings.Cut(last, "-")
		if _, err := strconv.ParseInt(id, 10, 64); err != nil {
			if ch.ID == "" {
				return nil, fmt.Errorf("%q is not a chapter url", ch.URL)
			}
			id = ch.ID
		}
		endpoint = "/api/chapters/" + id
	}
	var out struct {
		Data struct {
			Pages []struct {
				URL string `json:"url"`
			} `json:"pages"`
		} `json:"data"`
	}
	if err := f.get(ctx, endpoint, nil, &out); err != nil {
		return nil, err
	}
	var pages []sourcekit.PageImage
	for _, p := range out.Data.Pages {
		if p.URL == "" {
			continue
		}
		pages = append(pages, sourcekit.PageImage{Index: len(pages), URL: sourcekit.Abs(f.base, p.URL),
			Headers: map[string]string{"Referer": f.base + "/"}})
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("%w: the chapter has no pages", sourcekit.ErrNotFound)
	}
	return pages, nil
}

// mfHID is a title's id in its url: "/title/<hid>-<slug>", or the older
// "/manga/<slug>.<hid>", falling back to the id stored with the link.
func mfHID(raw, stored string) string {
	parts := strings.Split(strings.Trim(sourcekit.Path(raw), "/"), "/")
	seg, kind := parts[len(parts)-1], ""
	// a chapter url goes on past the title: take the segment after it
	for i, s := range parts {
		if (s == "title" || s == "manga") && i+1 < len(parts) {
			seg, kind = parts[i+1], s
			break
		}
	}
	switch {
	case kind == "title":
		// the slug may hold a "." ("dr.-stone"): the id is before the first "-"
		seg, _, _ = strings.Cut(seg, "-")
	case strings.Contains(seg, "."):
		seg = seg[strings.LastIndex(seg, ".")+1:]
	case strings.Contains(seg, "-"):
		seg, _, _ = strings.Cut(seg, "-")
	}
	if seg == "" {
		return stored
	}
	return seg
}
