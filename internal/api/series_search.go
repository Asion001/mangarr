package api

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/genres"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/readstate"
	"github.com/danielgtaylor/huma/v2"
)

func init() { register((*Server).registerSeriesSearch) }

type SeriesSearchResponse struct {
	Items     []SeriesResource `json:"items"`
	Page      int              `json:"page"`
	PageSize  int              `json:"pageSize"`
	Total     int              `json:"total"`
	TotalSize int64            `json:"totalSize"`
	Languages []string         `json:"languages"`
	// Genres are the genres and tags of the visible series, most common first.
	Genres []string `json:"genres"`
	// GenreCounts says how many visible series have each of Genres.
	GenreCounts map[string]int `json:"genreCounts"`
	// Requesters are the people who asked for a visible title, for the
	// "Requested by" filter (only for people who manage requests).
	Requesters []SeriesRequester `json:"requesters,omitempty"`
}

// SeriesRequester is a person who asked for a title.
type SeriesRequester struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type SeriesSearchQuery struct {
	Query        string   `query:"q" maxLength:"200"`
	Filter       string   `query:"filter" enum:"all,following,monitored,missing,ongoing,completed,unread,reading" default:"all"`
	Sort         string   `query:"sort" enum:"title,added,latest,missing,size,read,progress,chapters,year,status,requested" default:"title"`
	Order        string   `query:"order,omitempty" enum:"asc,desc" doc:"Sort direction; each sort has its own default"`
	Monitored    string   `query:"monitored,omitempty" enum:"yes,no"`
	Status       string   `query:"status,omitempty" enum:"ongoing,completed,hiatus,cancelled"`
	Downloads    string   `query:"downloads,omitempty" enum:"missing,complete,none" doc:"missing: monitored chapters without a file; complete: none missing; none: no files yet"`
	Reading      string   `query:"reading,omitempty" enum:"unstarted,reading,finished,unread"`
	Following    bool     `query:"following,omitempty" doc:"Only series you follow"`
	RequestedBy  string   `query:"requestedBy,omitempty" maxLength:"20" doc:"A user id, or none for titles nobody asked for (people who manage requests)"`
	RootFolderID int64    `query:"rootFolderId,omitempty"`
	Language     string   `query:"language,omitempty" maxLength:"20"`
	Genres       []string `query:"genre,omitempty,explode" maxItems:"50" doc:"Only series with these genres or tags; repeat the parameter for each"`
	GenreMatch   string   `query:"genreMatch" enum:"all,any" default:"all" doc:"Whether a series needs all the genres or any one of them"`
	List         string   `query:"list,omitempty" maxLength:"20" doc:"Only titles with this reading status (reading, planned, completed, onhold, dropped) or in the list with this id"`
	ListUser     int64    `query:"listUser,omitempty" doc:"Whose statuses and lists the list filter uses (yours when unset); others' need Manage the library"`
	Page         int      `query:"page" minimum:"1" default:"1"`
	PageSize     int      `query:"pageSize" minimum:"12" maximum:"100" default:"36"`
	// AcceptLanguage picks which edition's name and cover a title shows.
	AcceptLanguage string `header:"Accept-Language"`
}

// hasGenre says whether a series has the genre or tag, by any of its names.
func hasGenre(md model.SeriesMetadata, genre string) bool {
	name := genres.Name(genre)
	for _, g := range append(slices.Clone(md.Genres), md.Tags...) {
		if strings.EqualFold(genres.Name(g), name) {
			return true
		}
	}
	return false
}

// hasGenres says whether a series has all the genres, or with any set at
// least one of them; no genres match every series.
func hasGenres(md model.SeriesMetadata, list []string, any bool) bool {
	asked := false
	for _, genre := range list {
		if strings.TrimSpace(genre) == "" {
			continue
		}
		asked = true
		has := hasGenre(md, genre)
		if any && has {
			return true
		}
		if !any && !has {
			return false
		}
	}
	return !any || !asked
}

// titleMatches says whether a title contains the lowercased needle, also
// with punctuation and spacing ignored on both sides, so a pasted
// "@Einz:The Laid-Off Mage" still finds "Einz: The Laid-Off Mage".
func titleMatches(title, needle string) bool {
	title = strings.ToLower(title)
	if strings.Contains(title, needle) {
		return true
	}
	compactNeedle := compactTitle(needle)
	return compactNeedle != "" && strings.Contains(compactTitle(title), compactNeedle)
}

// compactTitle keeps only a title's letters and digits.
func compactTitle(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, s)
}

func matchesSeriesQuery(item SeriesResource, query SeriesSearchQuery) bool {
	needle := strings.ToLower(strings.TrimSpace(query.Query))
	if needle != "" {
		// a genre's name, in any language, finds the series with it
		matched := titleMatches(item.Title, needle) || titleMatches(item.DisplayTitle, needle) || hasGenre(item.Metadata, needle)
		for _, title := range item.Metadata.AltTitles {
			matched = matched || titleMatches(title, needle)
		}
		for _, edition := range item.Editions {
			matched = matched || titleMatches(edition.Title, needle)
		}
		if !matched {
			return false
		}
	}
	if query.RootFolderID > 0 && item.RootFolderID != query.RootFolderID {
		return false
	}
	if query.Language != "" && item.Language != query.Language {
		return false
	}
	if !hasGenres(item.Metadata, query.Genres, query.GenreMatch == "any") {
		return false
	}
	if query.Monitored != "" && item.Monitored != (query.Monitored == "yes") {
		return false
	}
	if query.Status != "" && item.Status != query.Status {
		return false
	}
	if query.Following && !item.Following {
		return false
	}
	switch query.Downloads {
	case "missing":
		if item.Stats.MissingCount == 0 {
			return false
		}
	case "complete":
		if item.Stats.MissingCount > 0 || item.Stats.FileCount == 0 {
			return false
		}
	case "none":
		if item.Stats.FileCount > 0 {
			return false
		}
	}
	read, total := item.Stats.ReadCount, item.Stats.ChapterCount
	switch query.Reading {
	case "unstarted":
		if read > 0 || item.Stats.InProgressCount > 0 {
			return false
		}
	case "reading":
		if (read == 0 && item.Stats.InProgressCount == 0) || read >= total {
			return false
		}
	case "finished":
		if total == 0 || read < total {
			return false
		}
	case "unread":
		if read >= total {
			return false
		}
	}
	switch query.RequestedBy {
	case "":
	case "none":
		if len(item.RequestedBy) > 0 {
			return false
		}
	default:
		if !slices.ContainsFunc(item.RequestedBy, func(r SeriesRequester) bool { return strconv.FormatInt(r.ID, 10) == query.RequestedBy }) {
			return false
		}
	}
	switch query.Filter {
	case "following":
		return item.Following
	case "monitored":
		return item.Monitored
	case "missing":
		return item.Stats.MissingCount > 0
	case "ongoing", "completed":
		return item.Status == query.Filter
	case "unread":
		return item.Stats.ReadCount < item.Stats.ChapterCount
	case "reading":
		return item.Stats.ReadCount > 0 && item.Stats.ReadCount < item.Stats.ChapterCount
	default:
		return true
	}
}

// seriesStatusOrder ranks statuses from still coming out to stopped.
var seriesStatusOrder = map[string]int{"ongoing": 1, "hiatus": 2, "completed": 3, "cancelled": 4}

// descendingSorts are the sorts that list the biggest or newest first
// unless asked otherwise.
var descendingSorts = map[string]bool{"added": true, "latest": true, "missing": true, "size": true, "read": true, "progress": true, "chapters": true, "year": true, "requested": true}

// downloadedShare is the part of the monitored chapters that have a file.
func downloadedShare(s SeriesStats) float64 {
	if s.MonitoredCount == 0 {
		return 0
	}
	return float64(s.MonitoredCount-s.MissingCount) / float64(s.MonitoredCount)
}

// compareTimes orders two times, a missing one counting as the oldest.
func compareTimes(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}
	return a.Compare(*b)
}

// sortSeriesSearch orders items by a sort, in order ("asc" or "desc"; empty
// is the sort's own default); ties keep title order.
func sortSeriesSearch(items []SeriesResource, by, order string) {
	desc := descendingSorts[by]
	if order != "" {
		desc = order == "desc"
	}
	slices.SortStableFunc(items, func(a, b SeriesResource) int {
		var c int
		switch by {
		case "added":
			c = a.AddedAt.Compare(b.AddedAt)
		case "latest":
			c = cmp.Compare(a.Stats.LastChapter, b.Stats.LastChapter)
		case "missing":
			c = cmp.Compare(a.Stats.MissingCount, b.Stats.MissingCount)
		case "size":
			c = cmp.Compare(a.Stats.SizeOnDisk, b.Stats.SizeOnDisk)
		case "read":
			c = compareTimes(a.Stats.LastReadAt, b.Stats.LastReadAt)
		case "progress":
			c = cmp.Compare(downloadedShare(a.Stats), downloadedShare(b.Stats))
		case "chapters":
			c = cmp.Compare(a.Stats.ChapterCount, b.Stats.ChapterCount)
		case "year":
			c = cmp.Compare(a.Metadata.Year, b.Metadata.Year)
		case "status":
			c = cmp.Compare(seriesStatusOrder[a.Status], seriesStatusOrder[b.Status])
		case "requested":
			c = compareTimes(a.RequestedAt, b.RequestedAt)
		}
		if desc {
			c = -c
		}
		if c == 0 {
			c = strings.Compare(a.SortTitle, b.SortTitle)
			if by == "title" && desc {
				c = -c
			}
		}
		return c
	})
}

func (s *Server) registerSeriesSearch() {
	huma.Register(s.api, huma.Operation{OperationID: "series-query", Method: http.MethodGet, Path: "/api/v1/series/search", Tags: []string{"Series"},
		Summary: "Search, filter, sort and paginate visible series"},
		func(ctx context.Context, in *SeriesSearchQuery) (*struct{ Body SeriesSearchResponse }, error) {
			var list []model.Series
			if err := s.app.DB.NewSelect().Model(&list).Where("preview = ?", false).Order("sort_title").Scan(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			listed, err := s.listFilter(ctx, in.List, in.ListUser)
			if err != nil {
				return nil, err
			}
			principal := access.From(ctx)
			languages := map[string]bool{}
			genreCount := map[string]int{}
			visible := make([]model.Series, 0, len(list))
			for _, series := range list {
				if !principal.Sees(&series) {
					continue
				}
				if series.Language != "" {
					languages[series.Language] = true
				}
				seen := map[string]bool{}
				for _, g := range genres.Normalize(append(slices.Clone(series.Metadata.Genres), series.Metadata.Tags...)) {
					if !seen[g] {
						seen[g] = true
						genreCount[g]++
					}
				}
				if in.RootFolderID > 0 && series.RootFolderID != in.RootFolderID {
					continue
				}
				if in.Language != "" && series.Language != in.Language {
					continue
				}
				if listed != nil && !listed[readstate.TitleID(&series)] {
					continue
				}
				visible = append(visible, series)
			}
			needle := strings.ToLower(strings.TrimSpace(in.Query))
			rawMatches := map[int64]bool{}
			for _, series := range visible {
				matched := titleMatches(series.Title, needle)
				for _, title := range series.Metadata.AltTitles {
					matched = matched || titleMatches(title, needle)
				}
				for _, title := range series.Metadata.Titles {
					matched = matched || titleMatches(title, needle)
				}
				if matched && needle != "" {
					key := series.WorkID
					if key == 0 {
						key = -series.ID
					}
					rawMatches[key] = true
				}
			}
			filters := *in
			filters.Query, filters.RootFolderID, filters.Language = "", 0, ""
			grouped, err := s.groupedSeriesResources(ctx, visible, uiLanguage(in.AcceptLanguage))
			if err != nil {
				return nil, toHTTPError(err)
			}
			if err := s.addSourceCounts(ctx, grouped); err != nil {
				return nil, toHTTPError(err)
			}
			var requesters []SeriesRequester
			if principal.Can(access.RequestsManage) {
				if requesters, err = s.addRequesters(ctx, grouped); err != nil {
					return nil, toHTTPError(err)
				}
			} else {
				filters.RequestedBy = ""
			}
			items := make([]SeriesResource, 0, len(grouped))
			for _, item := range grouped {
				key := item.WorkID
				if key == 0 {
					key = -item.ID
				}
				if needle != "" && !rawMatches[key] && !matchesSeriesQuery(item, SeriesSearchQuery{Query: in.Query}) {
					continue
				}
				if matchesSeriesQuery(item, filters) {
					items = append(items, item)
				}
			}
			sortSeriesSearch(items, in.Sort, in.Order)
			total := len(items)
			var totalSize int64
			for _, item := range items {
				totalSize += item.Stats.SizeOnDisk
			}
			page, pageSize := max(in.Page, 1), min(max(in.PageSize, 12), 100)
			start := min((page-1)*pageSize, total)
			end := min(start+pageSize, total)
			langs := make([]string, 0, len(languages))
			for language := range languages {
				langs = append(langs, language)
			}
			sort.Strings(langs)
			genreList := make([]string, 0, len(genreCount))
			for g := range genreCount {
				genreList = append(genreList, g)
			}
			sort.Slice(genreList, func(i, j int) bool {
				a, b := genreList[i], genreList[j]
				return genreCount[a] > genreCount[b] || genreCount[a] == genreCount[b] && a < b
			})
			return &struct{ Body SeriesSearchResponse }{SeriesSearchResponse{
				Items: items[start:end], Page: page, PageSize: pageSize, Total: total, TotalSize: totalSize, Languages: langs, Genres: genreList, GenreCounts: genreCount, Requesters: requesters,
			}}, nil
		})
}
