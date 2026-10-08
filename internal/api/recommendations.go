package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
	"github.com/Asion001/mangarr/internal/reading"
)

func init() { register((*Server).registerRecommendations) }

// RecommendationItem is a title shown under another one: in the library
// (existingSeriesId) or one to add or request.
type RecommendationItem struct {
	LookupResult
	// Relation is how a title in the same story relates: sequel, prequel,
	// side_story, spin_off, alternative, parent, source, ...
	Relation string `json:"relation,omitempty"`
	// Votes is how many of the provider's users recommend it.
	Votes int `json:"votes,omitempty"`
	// SharedGenres are the genres a library title has in common.
	SharedGenres []string `json:"sharedGenres,omitempty"`
	// SeriesCoverURL is the library's cover when the title is in it.
	SeriesCoverURL string `json:"seriesCoverUrl,omitempty"`
}

type SeriesRecommendations struct {
	// Related are titles in the same story, from the metadata provider.
	Related []RecommendationItem `json:"related"`
	// Similar are the provider's recommendations, then library titles with
	// the same genres.
	Similar []RecommendationItem `json:"similar"`
	// Errors are providers that couldn't be asked; the library part still shows.
	Errors []string `json:"errors"`
}

const (
	recommendationsTTL     = 12 * time.Hour
	similarFromLibraryMax  = 12
	recommendationsTimeout = 15 * time.Second
)

// recommendationCache keeps a provider's answer per title for a while, so
// opening a title page doesn't ask the provider each time.
type recommendationCache struct {
	mu      sync.Mutex
	entries map[string]recommendationEntry
}

type recommendationEntry struct {
	recs    *metadata.Recommendations
	fetched time.Time
}

func (c *recommendationCache) get(key string) *metadata.Recommendations {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok && time.Since(e.fetched) < recommendationsTTL {
		return e.recs
	}
	return nil
}

func (c *recommendationCache) put(key string, recs *metadata.Recommendations) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]recommendationEntry{}
	}
	for k, e := range c.entries {
		if time.Since(e.fetched) >= recommendationsTTL {
			delete(c.entries, k)
		}
	}
	c.entries[key] = recommendationEntry{recs: recs, fetched: time.Now()}
}

func (s *Server) registerRecommendations() {
	huma.Register(s.api, huma.Operation{OperationID: "series-recommendations", Method: http.MethodGet, Path: "/api/v1/series/{id}/recommendations", Tags: []string{"Series"},
		Summary:     "Titles related to and recommended alongside a series",
		Description: "Related titles (sequels, prequels, side stories, ...) and recommendations come from the first metadata module that knows the series and can recommend; library titles sharing its genres follow. Titles past the caller's content limits are left out, and so are titles outside the library for people who can't add or request them."},
		func(ctx context.Context, in *struct {
			ID int64 `path:"id"`
		}) (*struct{ Body SeriesRecommendations }, error) {
			ser, err := s.visibleSeries(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			out := &struct{ Body SeriesRecommendations }{SeriesRecommendations{Related: []RecommendationItem{}, Similar: []RecommendationItem{}, Errors: []string{}}}
			readerID, err := s.readerOf(ctx)
			if err != nil {
				return nil, err
			}
			library, err := s.app.Reading.AllSeries(ctx, readerID, 0)
			if err != nil {
				return nil, toHTTPError(err)
			}
			// the title itself, in any language, isn't recommended
			own := func(other model.Series) bool {
				return other.ID == ser.ID || (ser.WorkID > 0 && other.WorkID == ser.WorkID)
			}
			byID := map[string]reading.SeriesInfo{}
			for _, info := range library {
				for k, v := range info.Series.Metadata.ExternalIDs {
					if k != "mal" && v != "" {
						byID[k+":"+v] = info
					}
				}
			}
			inLibrary := func(md metadata.SeriesMetadata) (reading.SeriesInfo, bool) {
				for k, v := range lookupIDs(md) {
					if info, ok := byID[k+":"+v]; ok && k != "mal" {
						return info, true
					}
				}
				return reading.SeriesInfo{}, false
			}
			p := access.From(ctx)
			outside := p.Can(access.LibraryAdd) || p.Can(access.LibraryEdit) || p.Can(access.RequestsCreate)
			sc := p.ContentScope()
			requested := s.requestedByExternalID(ctx)
			shown := map[int64]bool{}
			item := func(md metadata.SeriesMetadata, def model.ProviderDefinition) (RecommendationItem, bool) {
				if sc != nil && !sc.AllowsContent("", md.Adult, append(append([]string{}, md.Genres...), md.Tags...)) {
					return RecommendationItem{}, false // past the group's content limits
				}
				it := RecommendationItem{LookupResult: LookupResult{Candidate: metadataagg.Candidate{SeriesMetadata: md, ModuleID: def.ID, ModuleName: def.Name}}}
				if info, ok := inLibrary(md); ok {
					if own(info.Series) || shown[info.Series.ID] {
						return RecommendationItem{}, false
					}
					shown[info.Series.ID] = true
					it.ExistingSeriesID, it.SeriesCoverURL = info.Series.ID, seriesCoverURL(info.Series)
					return it, true
				}
				if !outside {
					return RecommendationItem{}, false
				}
				it.Request = requested(lookupIDs(md))
				return it, true
			}

			if recs, def, err := s.providerRecommendations(ctx, ser.Metadata); err != nil {
				out.Body.Errors = append(out.Body.Errors, def.Name+": "+err.Error())
			} else if recs != nil {
				for _, r := range recs.Related {
					if it, ok := item(r.SeriesMetadata, def); ok {
						it.Relation = r.Relation
						out.Body.Related = append(out.Body.Related, it)
					}
				}
				for _, r := range recs.Recommended {
					if it, ok := item(r.SeriesMetadata, def); ok {
						it.Votes = r.Votes
						out.Body.Similar = append(out.Body.Similar, it)
					}
				}
			}
			for _, m := range similarInLibrary(ser.Metadata, library, func(info reading.SeriesInfo) bool { return own(info.Series) || shown[info.Series.ID] }) {
				out.Body.Similar = append(out.Body.Similar, m)
			}
			return out, nil
		})
}

// providerRecommendations asks the first metadata module that knows the
// series (by its external id) and can recommend. Nil when none can.
func (s *Server) providerRecommendations(ctx context.Context, md model.SeriesMetadata) (*metadata.Recommendations, model.ProviderDefinition, error) {
	for _, mod := range modules.ActiveAs[metadata.Recommender](s.app.Modules, modules.KindMetadata) {
		id := md.ExternalIDs[mod.Def.Implementation]
		if id == "" {
			continue
		}
		key := strconv.FormatInt(mod.Def.ID, 10) + ":" + id
		if recs := s.recs.get(key); recs != nil {
			return recs, mod.Def, nil
		}
		// finish the fetch for the cache even when the page gives up first
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recommendationsTimeout)
		recs, err := mod.Instance.Recommendations(fetchCtx, id)
		cancel()
		if err != nil {
			return nil, mod.Def, err
		}
		s.recs.put(key, recs)
		return recs, mod.Def, nil
	}
	return nil, model.ProviderDefinition{}, nil
}

// similarInLibrary are library titles sharing the most genres (then tags)
// with md, at least two genres when md has two or more.
func similarInLibrary(md model.SeriesMetadata, library []reading.SeriesInfo, skip func(reading.SeriesInfo) bool) []RecommendationItem {
	genres, tags := lowerSet(md.Genres), lowerSet(md.Tags)
	need := min(2, len(genres))
	if need == 0 {
		return nil
	}
	type match struct {
		info   reading.SeriesInfo
		shared []string
		score  int
	}
	var matches []match
	for _, info := range library {
		if info.Books == 0 || skip(info) {
			continue
		}
		var shared []string
		for _, g := range info.Series.Metadata.Genres {
			if genres[strings.ToLower(strings.TrimSpace(g))] {
				shared = append(shared, g)
			}
		}
		if len(shared) < need {
			continue
		}
		score := len(shared) * 3
		for _, t := range info.Series.Metadata.Tags {
			if tags[strings.ToLower(strings.TrimSpace(t))] {
				score++
			}
		}
		matches = append(matches, match{info: info, shared: shared, score: score})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return strings.ToLower(matches[i].info.Series.Title) < strings.ToLower(matches[j].info.Series.Title)
	})
	out := []RecommendationItem{}
	for _, m := range matches[:min(similarFromLibraryMax, len(matches))] {
		ser := m.info.Series
		md := metadata.SeriesMetadata{Title: ser.Title, Year: ser.Metadata.Year, Genres: ser.Metadata.Genres, Format: ser.Metadata.Format,
			Status: ser.Status, ExternalIDs: ser.Metadata.ExternalIDs}
		out = append(out, RecommendationItem{LookupResult: LookupResult{Candidate: metadataagg.Candidate{SeriesMetadata: md}, ExistingSeriesID: ser.ID},
			SharedGenres: m.shared, SeriesCoverURL: seriesCoverURL(ser)})
	}
	return out
}

func lowerSet(xs []string) map[string]bool {
	out := map[string]bool{}
	for _, x := range xs {
		if k := strings.ToLower(strings.TrimSpace(x)); k != "" {
			out[k] = true
		}
	}
	return out
}
