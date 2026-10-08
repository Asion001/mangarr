package api

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/genres"
	"github.com/Asion001/mangarr/internal/model"
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
}

type SeriesSearchQuery struct {
	Query        string `query:"q" maxLength:"200"`
	Filter       string `query:"filter" enum:"all,following,monitored,missing,ongoing,completed,unread,reading" default:"all"`
	Sort         string `query:"sort" enum:"title,added,latest,missing,size,read" default:"title"`
	RootFolderID int64  `query:"rootFolderId,omitempty"`
	Language     string `query:"language,omitempty" maxLength:"20"`
	Genre        string `query:"genre,omitempty" maxLength:"100" doc:"Only series with this genre or tag"`
	Page         int    `query:"page" minimum:"1" default:"1"`
	PageSize     int    `query:"pageSize" minimum:"12" maximum:"100" default:"36"`
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

func matchesSeriesQuery(item SeriesResource, query SeriesSearchQuery) bool {
	needle := strings.ToLower(strings.TrimSpace(query.Query))
	if needle != "" {
		// a genre's name, in any language, finds the series with it
		matched := strings.Contains(strings.ToLower(item.Title), needle) || hasGenre(item.Metadata, needle)
		for _, title := range item.Metadata.AltTitles {
			matched = matched || strings.Contains(strings.ToLower(title), needle)
		}
		for _, edition := range item.Editions {
			matched = matched || strings.Contains(strings.ToLower(edition.Title), needle)
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
	if query.Genre != "" && !hasGenre(item.Metadata, query.Genre) {
		return false
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

func sortSeriesSearch(items []SeriesResource, order string) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		var before bool
		switch order {
		case "added":
			before = a.AddedAt.After(b.AddedAt)
		case "latest":
			before = a.Stats.LastChapter > b.Stats.LastChapter
		case "missing":
			before = a.Stats.MissingCount > b.Stats.MissingCount
		case "size":
			before = a.Stats.SizeOnDisk > b.Stats.SizeOnDisk
		case "read":
			if a.Stats.LastReadAt == nil {
				before = false
			} else if b.Stats.LastReadAt == nil {
				before = true
			} else {
				before = a.Stats.LastReadAt.After(*b.Stats.LastReadAt)
			}
		default:
			before = a.SortTitle < b.SortTitle
		}
		return before
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
				visible = append(visible, series)
			}
			needle := strings.ToLower(strings.TrimSpace(in.Query))
			rawMatches := map[int64]bool{}
			for _, series := range visible {
				matched := strings.Contains(strings.ToLower(series.Title), needle)
				for _, title := range series.Metadata.AltTitles {
					matched = matched || strings.Contains(strings.ToLower(title), needle)
				}
				if matched && needle != "" {
					key := series.WorkID
					if key == 0 {
						key = -series.ID
					}
					rawMatches[key] = true
				}
			}
			grouped, err := s.groupedSeriesResources(ctx, visible, uiLanguage(in.AcceptLanguage))
			if err != nil {
				return nil, toHTTPError(err)
			}
			items := make([]SeriesResource, 0, len(grouped))
			filters := *in
			filters.Query, filters.RootFolderID, filters.Language = "", 0, ""
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
			sortSeriesSearch(items, in.Sort)
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
				Items: items[start:end], Page: page, PageSize: pageSize, Total: total, TotalSize: totalSize, Languages: langs, Genres: genreList,
			}}, nil
		})
}
