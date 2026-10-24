package sites

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// Senkuro answers one GraphQL endpoint and nothing else: there is no HTML to
// read and no page URLs to sign. It addresses everything by node id, so the
// ids travel with the manga and chapter refs; a slug alone is not a valid id
// there, and is looked up when that is all we have.
//
// Its search offers no "recently updated" ordering, so the site is
// searchable but not browsable.
var senkuroID = sourcekit.KeiyoushiID("Senkuro", "ru", 1)

const (
	senkuroSite = "https://senkuro.com"
	senkuroAPI  = "https://api.senkuro.com/graphql"
	// senkuroApp identifies the client to the API, which turns away requests
	// that don't look like one of its readers.
	senkuroApp        = "4026531840100"
	senkuroAppVersion = "060626"
	senkuroPageSize   = 10
)

func init() {
	sourcekit.Register(senkuroID, func(d sourcekit.Deps) sourcekit.Site {
		return &senkuro{c: d.Client, site: senkuroSite, api: senkuroAPI, title: "ru"}
	})
}

type senkuro struct {
	c *sourcekit.Client
	// site and api are the addresses to talk to (tests point them at a
	// recorded copy).
	site, api string
	// title is the language to prefer titles in.
	title string
}

func (s *senkuro) Info() sourcekit.Info {
	return sourcekit.Info{ID: senkuroID, Name: "Senkuro", Lang: "ru", BaseURL: s.site, IconURL: s.site + "/favicon.ico"}
}

// Politeness: the site's own reader asks for three requests at a time.
func (s *senkuro) Politeness() sourcekit.Politeness {
	return sourcekit.Politeness{RequestsPerMinute: 120, MaxConcurrent: 3}
}

func (s *senkuro) Options() []sourcekit.Option {
	return []sourcekit.Option{{Key: "title", Title: "Titles in", Type: "select", Value: s.title,
		Help: "Which of a manga's titles to use when linking and naming it.",
		Choices: []sourcekit.Choice{{Value: "ru", Label: "Russian"}, {Value: "en", Label: "English"},
			{Value: "original", Label: "Original"}}}}
}

func (s *senkuro) SetOption(key string, value any) error {
	if key != "title" {
		return fmt.Errorf("%w: no option %q", sourcekit.ErrUnsupported, key)
	}
	v, _ := value.(string)
	switch v {
	case "ru", "en", "original":
		s.title = v
		return nil
	}
	return fmt.Errorf("%q is not a title language", value)
}

// ---- the queries ------------------------------------------------------------

const senkuroSearchQuery = `query searchTachiyomiManga($query: String, $orderBy: MangaTachiyomiOrder, $offset: Int) {
  mangaTachiyomiSearch(query: $query, orderBy: $orderBy, offset: $offset) {
    mangas { id slug originalName { lang content } titles { lang content } cover { original { url } } }
  }
}`

const senkuroDetailsQuery = `query fetchTachiyomiManga($mangaId: ID!) {
  mangaTachiyomiInfo(mangaId: $mangaId) {
    id slug originalName { lang content } titles { lang content } alternativeNames { lang content }
    localizations { lang description } type rating status formats translationStatus
    labels { slug titles { lang content } } cover { original { url } }
    mainStaff { roles person { name } }
  }
}`

const senkuroChaptersQuery = `query fetchTachiyomiChapters($mangaId: ID!) {
  mangaTachiyomiChapters(mangaId: $mangaId) {
    chapters { id slug name teamIds number volume createdAt updatedAt }
    teams { id name }
  }
}`

const senkuroPagesQuery = `query fetchTachiyomiChapterPages($mangaId: ID!, $chapterId: ID!) {
  mangaTachiyomiChapterPages(mangaId: $mangaId, chapterId: $chapterId) { pages { url } }
}`

// localized is one of the site's per-language strings.
type senkuroText struct {
	Lang    string `json:"lang"`
	Content string `json:"content"`
}

type senkuroManga struct {
	ID           string        `json:"id"`
	Slug         string        `json:"slug"`
	OriginalName senkuroText   `json:"originalName"`
	Titles       []senkuroText `json:"titles"`
	Cover        struct {
		Original struct {
			URL string `json:"url"`
		} `json:"original"`
	} `json:"cover"`
}

func (s *senkuro) query(ctx context.Context, op, query string, vars map[string]any, out any) error {
	return s.queryAs(ctx, "", op, query, vars, out)
}

// queryAs runs a query signed in with an account's token (none when empty).
func (s *senkuro) queryAs(ctx context.Context, token, op, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"operationName": op, "query": query, "variables": vars})
	if err != nil {
		return err
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	req := sourcekit.Request{Method: http.MethodPost, URL: s.api, Body: body, Headers: map[string]string{
		"Content-Type": "application/json", "App-Id": senkuroApp, "App-Version": senkuroAppVersion}}
	if token != "" {
		req.Headers["Authorization"] = "Bearer " + token
	}
	if err := s.c.JSON(ctx, req, &env); err != nil {
		var se *sourcekit.StatusError
		if token != "" && errors.As(err, &se) && se.Code == http.StatusUnauthorized {
			return sourcekit.ErrUnauthorized
		}
		return err
	}
	if len(env.Errors) > 0 {
		if env.Errors[0].Extensions.Code == "NOT_AUTHORIZED" || env.Errors[0].Extensions.Code == "UNAUTHENTICATED" {
			return sourcekit.ErrUnauthorized
		}
		return fmt.Errorf("senkuro: %s", env.Errors[0].Message)
	}
	if len(env.Data) == 0 {
		return fmt.Errorf("%w: the site answered nothing", sourcekit.ErrNotFound)
	}
	return json.Unmarshal(env.Data, out)
}

// pick is the string to use out of the site's per-language ones.
func (s *senkuro) pick(titles []senkuroText, original senkuroText) string {
	want := strings.ToUpper(s.title)
	if s.title == "original" {
		want = strings.ToUpper(original.Lang)
	}
	for _, t := range titles {
		if strings.EqualFold(t.Lang, want) && t.Content != "" {
			return t.Content
		}
	}
	if original.Content != "" {
		return original.Content
	}
	for _, t := range titles {
		if t.Content != "" {
			return t.Content
		}
	}
	return ""
}

func (s *senkuro) manga(m senkuroManga) sourcekit.Manga {
	return sourcekit.Manga{URL: "/manga/" + m.Slug, ID: m.ID, Title: s.pick(m.Titles, m.OriginalName),
		CoverURL: m.Cover.Original.URL}
}

func (s *senkuro) Search(ctx context.Context, query string, page int) (sourcekit.Results, error) {
	if page < 1 {
		page = 1
	}
	var out struct {
		Search struct {
			Mangas []senkuroManga `json:"mangas"`
		} `json:"mangaTachiyomiSearch"`
	}
	vars := map[string]any{"offset": (page - 1) * senkuroPageSize,
		"orderBy": map[string]string{"direction": "DESC", "field": "POPULARITY_SCORE"}}
	if q := strings.TrimSpace(query); q != "" {
		vars["query"] = q
	}
	if err := s.query(ctx, "searchTachiyomiManga", senkuroSearchQuery, vars, &out); err != nil {
		return sourcekit.Results{}, err
	}
	var res sourcekit.Results
	for _, m := range out.Search.Mangas {
		if m.Slug == "" {
			continue
		}
		res.Mangas = append(res.Mangas, s.manga(m))
	}
	res.HasNext = len(res.Mangas) >= senkuroPageSize
	return res, nil
}

// mangaID is the node id of a manga: the stored one when we have it, else the
// search result whose slug matches, since the API refuses a bare slug.
func (s *senkuro) mangaID(ctx context.Context, ref sourcekit.Ref) (string, error) {
	if ref.ID != "" {
		return ref.ID, nil
	}
	slug := senkuroSlug(ref.URL)
	if slug == "" {
		return "", fmt.Errorf("%q is not a manga url", ref.URL)
	}
	res, err := s.Search(ctx, strings.ReplaceAll(slug, "-", " "), 1)
	if err != nil {
		return "", err
	}
	for _, m := range res.Mangas {
		if senkuroSlug(m.URL) == slug {
			return m.ID, nil
		}
	}
	return "", fmt.Errorf("%w: %s", sourcekit.ErrNotFound, ref.URL)
}

func (s *senkuro) Details(ctx context.Context, ref sourcekit.Ref) (sourcekit.Details, error) {
	id, err := s.mangaID(ctx, ref)
	if err != nil {
		return sourcekit.Details{}, err
	}
	var out struct {
		Info *struct {
			senkuroManga
			AlternativeNames []senkuroText `json:"alternativeNames"`
			Localizations    []struct {
				Lang        string `json:"lang"`
				Description string `json:"description"`
			} `json:"localizations"`
			Status string `json:"status"`
			Labels []struct {
				Titles []senkuroText `json:"titles"`
			} `json:"labels"`
			MainStaff []struct {
				Roles  []string `json:"roles"`
				Person struct {
					Name string `json:"name"`
				} `json:"person"`
			} `json:"mainStaff"`
		} `json:"mangaTachiyomiInfo"`
	}
	if err := s.query(ctx, "fetchTachiyomiManga", senkuroDetailsQuery, map[string]any{"mangaId": id}, &out); err != nil {
		return sourcekit.Details{}, err
	}
	if out.Info == nil {
		return sourcekit.Details{}, fmt.Errorf("%w: %s", sourcekit.ErrNotFound, ref.URL)
	}
	i := out.Info
	d := sourcekit.Details{Manga: s.manga(i.senkuroManga), Status: senkuroStatus(i.Status),
		WebURL: s.site + "/manga/" + i.Slug}
	for _, l := range i.Localizations {
		if strings.EqualFold(l.Lang, s.title) && l.Description != "" {
			d.Description = l.Description
			break
		}
		if d.Description == "" {
			d.Description = l.Description
		}
	}
	for _, l := range i.Labels {
		if v := s.pick(l.Titles, senkuroText{}); v != "" {
			d.Genres = append(d.Genres, v)
		}
	}
	var authors, artists []string
	for _, st := range i.MainStaff {
		for _, role := range st.Roles {
			switch strings.ToUpper(role) {
			case "STORY", "AUTHOR":
				authors = append(authors, st.Person.Name)
			case "ART", "ARTIST":
				artists = append(artists, st.Person.Name)
			case "STORY_AND_ART":
				authors = append(authors, st.Person.Name)
				artists = append(artists, st.Person.Name)
			}
		}
	}
	d.Author, d.Artist = strings.Join(authors, ", "), strings.Join(artists, ", ")
	return d, nil
}

func senkuroStatus(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ONGOING":
		return sourcekit.StatusOngoing
	case "FINISHED":
		return sourcekit.StatusCompleted
	case "HIATUS":
		return sourcekit.StatusHiatus
	case "CANCELLED", "CANCELED":
		return sourcekit.StatusCancelled
	}
	return sourcekit.StatusUnknown
}

func (s *senkuro) Chapters(ctx context.Context, ref sourcekit.Ref) ([]sourcekit.Chapter, error) {
	id, err := s.mangaID(ctx, ref)
	if err != nil {
		return nil, err
	}
	var out struct {
		Chapters struct {
			Chapters []struct {
				ID        string   `json:"id"`
				Slug      string   `json:"slug"`
				Name      string   `json:"name"`
				TeamIDs   []string `json:"teamIds"`
				Number    string   `json:"number"`
				Volume    string   `json:"volume"`
				CreatedAt string   `json:"createdAt"`
				UpdatedAt string   `json:"updatedAt"`
			} `json:"chapters"`
			Teams []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"teams"`
		} `json:"mangaTachiyomiChapters"`
	}
	if err := s.query(ctx, "fetchTachiyomiChapters", senkuroChaptersQuery, map[string]any{"mangaId": id}, &out); err != nil {
		return nil, err
	}
	teams := map[string]string{}
	for _, t := range out.Chapters.Teams {
		teams[t.ID] = t.Name
	}
	slug := senkuroSlug(ref.URL)
	chapters := make([]sourcekit.Chapter, 0, len(out.Chapters.Chapters))
	for _, c := range out.Chapters.Chapters {
		ch := sourcekit.Chapter{URL: fmt.Sprintf("/manga/%s/chapters/%s", slug, c.Slug), ID: c.ID, Number: -1,
			Name: strings.TrimSpace(c.Name), WebURL: fmt.Sprintf("%s/manga/%s/chapters/%s", s.site, slug, c.Slug)}
		if n, err := strconv.ParseFloat(c.Number, 64); err == nil {
			ch.Number = n
		}
		if ch.Name == "" {
			ch.Name = "Глава " + c.Number
		}
		var names []string
		for _, id := range c.TeamIDs {
			if n := teams[id]; n != "" {
				names = append(names, n)
			}
		}
		ch.Scanlator = strings.Join(names, ", ")
		if t := senkuroTime(c.UpdatedAt, c.CreatedAt); t != nil {
			ch.UploadedAt = t
		}
		chapters = append(chapters, ch)
	}
	if len(chapters) == 0 {
		return nil, fmt.Errorf("%w: no chapters listed", sourcekit.ErrNotFound)
	}
	return chapters, nil
}

// senkuroTime reads the site's timestamps, which carry no zone and are UTC.
func senkuroTime(values ...string) *time.Time {
	for _, v := range values {
		if v == "" {
			continue
		}
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05.999999", "2006-01-02T15:04:05"} {
			if t, err := time.Parse(layout, v); err == nil {
				t = t.UTC()
				return &t
			}
		}
	}
	return nil
}

func (s *senkuro) Pages(ctx context.Context, ch sourcekit.PageRef) ([]sourcekit.PageImage, error) {
	mangaID, err := s.mangaID(ctx, ch.Manga)
	if err != nil {
		return nil, err
	}
	chapterID := ch.ID
	if chapterID == "" {
		if chapterID, err = s.chapterID(ctx, ch, mangaID); err != nil {
			return nil, err
		}
	}
	var out struct {
		Pages struct {
			Pages []struct {
				URL string `json:"url"`
			} `json:"pages"`
		} `json:"mangaTachiyomiChapterPages"`
	}
	vars := map[string]any{"mangaId": mangaID, "chapterId": chapterID}
	if err := s.query(ctx, "fetchTachiyomiChapterPages", senkuroPagesQuery, vars, &out); err != nil {
		return nil, err
	}
	var pages []sourcekit.PageImage
	for _, p := range out.Pages.Pages {
		// the site puts its own banner in front of every chapter it serves a
		// reader app; it is not part of the manga
		if p.URL == "" || strings.Contains(p.URL, "/system/") {
			continue
		}
		pages = append(pages, sourcekit.PageImage{Index: len(pages), URL: p.URL,
			Headers: map[string]string{"Referer": s.site + "/"}})
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("%w: the chapter has no pages", sourcekit.ErrNotFound)
	}
	return pages, nil
}

// chapterID finds a chapter's node id from its url, for a chapter stored
// before the id was known.
func (s *senkuro) chapterID(ctx context.Context, ch sourcekit.PageRef, mangaID string) (string, error) {
	slug := chapterSlug(ch.URL)
	if slug == "" {
		return "", fmt.Errorf("%q is not a chapter url", ch.URL)
	}
	list, err := s.Chapters(ctx, sourcekit.Ref{URL: ch.Manga.URL, ID: mangaID})
	if err != nil {
		return "", err
	}
	for _, c := range list {
		if chapterSlug(c.URL) == slug {
			return c.ID, nil
		}
	}
	return "", fmt.Errorf("%w: %s", sourcekit.ErrNotFound, ch.URL)
}

// senkuroSlug is the "<slug>" of "/manga/<slug>[/chapters/<slug>]".
func senkuroSlug(raw string) string {
	parts := strings.Split(strings.Trim(sourcekit.Path(raw), "/"), "/")
	for i, p := range parts {
		if p == "manga" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// chapterSlug is the "<slug>" of ".../chapters/<slug>".
func chapterSlug(raw string) string {
	parts := strings.Split(strings.Trim(sourcekit.Path(raw), "/"), "/")
	for i, p := range parts {
		if p == "chapters" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}
