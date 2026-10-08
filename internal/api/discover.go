package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/catalogs"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/reading"
	"github.com/Asion001/mangarr/internal/sourcecache"
)

func init() { register((*Server).registerDiscover) }

// DiscoverLibraryItem is a local series suggested to the current reader or
// shown because it recently changed.
type DiscoverLibraryItem struct {
	SeriesID       int64     `json:"seriesId"`
	Title          string    `json:"title"`
	Description    string    `json:"description,omitempty"`
	CoverURL       string    `json:"coverUrl"`
	Status         string    `json:"status"`
	Language       string    `json:"language"`
	Genres         []string  `json:"genres"`
	Unread         int       `json:"unread"`
	Books          int       `json:"books"`
	LatestChapter  string    `json:"latestChapter,omitempty"`
	ChangedAt      time.Time `json:"changedAt"`
	Reason         string    `json:"reason,omitempty" enum:"matches-genres,followed,recently-added,recent-update"`
	MatchingGenres []string  `json:"matchingGenres,omitempty"`
}

// DiscoverSourceItem is a popular title from one of the active, prioritized
// catalogs. ThumbnailURL is a signed Mangarr URL, safe for ordinary readers.
type DiscoverSourceItem struct {
	ModuleID         int64  `json:"moduleId"`
	ModuleName       string `json:"moduleName"`
	SourceID         string `json:"sourceId"`
	SourceName       string `json:"sourceName"`
	Language         string `json:"language"`
	Title            string `json:"title"`
	URL              string `json:"url"`
	EngineRef        string `json:"engineRef,omitempty"`
	ThumbnailURL     string `json:"thumbnailUrl,omitempty"`
	ChapterCount     *int   `json:"chapterCount,omitempty"`
	ExistingSeriesID int64  `json:"existingSeriesId,omitempty"`
}

type DiscoverSourceError struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Error  string `json:"error"`
}

// DiscoverResponse is intentionally useful even when one source is offline.
type DiscoverResponse struct {
	Recommendations []DiscoverLibraryItem `json:"recommendations"`
	Updates         []DiscoverLibraryItem `json:"updates"`
	Popular         []DiscoverSourceItem  `json:"popular"`
	SourceErrors    []DiscoverSourceError `json:"sourceErrors"`
	PopularCached   bool                  `json:"popularCached"`
	GeneratedAt     time.Time             `json:"generatedAt"`
}

type discoverThumbToken struct {
	ModuleID   int64  `json:"m"`
	SourceID   string `json:"s"`
	URL        string `json:"u"`
	EngineRef  string `json:"r,omitempty"`
	Generation int64  `json:"g"`
	Expires    int64  `json:"e"`
}

func discoverTitleKey(value string) string {
	var out strings.Builder
	space := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			if space && out.Len() > 0 {
				out.WriteByte(' ')
			}
			space = false
			out.WriteRune(r)
		default:
			space = true
		}
	}
	return out.String()
}

func discoverLibraryItem(info reading.SeriesInfo) DiscoverLibraryItem {
	ser := info.Series
	genres := append([]string{}, ser.Metadata.Genres...)
	return DiscoverLibraryItem{SeriesID: ser.ID, Title: ser.Title, Description: ser.Metadata.Description,
		CoverURL: seriesCoverURL(ser), Status: ser.Status, Language: ser.Language, Genres: genres,
		Unread: info.Unread(), Books: info.Books, ChangedAt: info.LastModified()}
}

func (s *Server) discoverLibrary(ctx context.Context, rootFolderID int64, lang string, limit int) ([]DiscoverLibraryItem, []DiscoverLibraryItem, map[string]int64, error) {
	readerID, err := s.readerOf(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	all, err := s.app.Reading.AllSeries(ctx, readerID, 0)
	if err != nil {
		return nil, nil, nil, err
	}
	filtered := all[:0]
	for _, info := range all {
		if rootFolderID > 0 && info.Series.RootFolderID != rootFolderID {
			continue
		}
		if lang != "" && !strings.EqualFold(info.Series.Language, lang) {
			continue
		}
		filtered = append(filtered, info)
	}
	all = filtered

	existing := map[string]int64{}
	for _, info := range all {
		existing[discoverTitleKey(info.Series.Title)] = info.Series.ID
		for _, title := range info.Series.Metadata.AltTitles {
			if key := discoverTitleKey(title); key != "" {
				existing[key] = info.Series.ID
			}
		}
	}

	candidates := discoverRecommendations(all, s.follows(ctx))
	recommendations := make([]DiscoverLibraryItem, 0, min(limit, len(candidates)))
	for _, candidate := range candidates[:min(limit, len(candidates))] {
		recommendations = append(recommendations, candidate.item)
	}

	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].LastModified().Equal(all[j].LastModified()) {
			return all[i].LastModified().After(all[j].LastModified())
		}
		return strings.ToLower(all[i].Series.Title) < strings.ToLower(all[j].Series.Title)
	})
	updates := []DiscoverLibraryItem{}
	for _, info := range all {
		if info.Books == 0 {
			continue
		}
		item := discoverLibraryItem(info)
		item.Reason = "recent-update"
		updates = append(updates, item)
		if len(updates) == limit {
			break
		}
	}
	if err := s.discoverLatestChapters(ctx, updates); err != nil {
		return nil, nil, nil, err
	}
	return recommendations, updates, existing, nil
}

type discoverCandidate struct {
	item  DiscoverLibraryItem
	score int
	added time.Time
}

func discoverRecommendations(all []reading.SeriesInfo, following map[int64]bool) []discoverCandidate {
	genreWeight := map[string]int{}
	genreDisplay := map[string]string{}
	for _, info := range all {
		if info.Read+info.InProgress == 0 && !following[info.Series.ID] {
			continue
		}
		weight := 1 + min(info.Read+info.InProgress, 5)
		if following[info.Series.ID] {
			weight += 3
		}
		for _, genre := range info.Series.Metadata.Genres {
			key := strings.ToLower(strings.TrimSpace(genre))
			if key != "" {
				genreWeight[key] += weight
				genreDisplay[key] = genre
			}
		}
	}

	candidates := []discoverCandidate{}
	for _, info := range all {
		if info.Books == 0 || info.Read+info.InProgress > 0 {
			continue
		}
		item := discoverLibraryItem(info)
		matches := []string{}
		score := 0
		for _, genre := range info.Series.Metadata.Genres {
			key := strings.ToLower(strings.TrimSpace(genre))
			if genreWeight[key] > 0 {
				score += genreWeight[key]
				matches = append(matches, genreDisplay[key])
			}
		}
		switch {
		case following[info.Series.ID]:
			item.Reason = "followed"
			score += 100
		case score > 0:
			item.Reason = "matches-genres"
			item.MatchingGenres = matches[:min(2, len(matches))]
		default:
			item.Reason = "recently-added"
		}
		candidates = append(candidates, discoverCandidate{item: item, score: score, added: info.Series.AddedAt})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if !candidates[i].added.Equal(candidates[j].added) {
			return candidates[i].added.After(candidates[j].added)
		}
		return strings.ToLower(candidates[i].item.Title) < strings.ToLower(candidates[j].item.Title)
	})
	return candidates
}

func (s *Server) discoverLatestChapters(ctx context.Context, items []DiscoverLibraryItem) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.SeriesID)
	}
	var chapters []model.Chapter
	if err := s.app.DB.NewSelect().Model(&chapters).Column("series_id", "number_key").Where("series_id IN (?)", bun.In(ids)).
		OrderExpr("COALESCE(release_date, updated_at) DESC").OrderExpr("number_sort DESC").Scan(ctx); err != nil {
		return err
	}
	latest := map[int64]string{}
	for _, chapter := range chapters {
		if _, ok := latest[chapter.SeriesID]; !ok {
			latest[chapter.SeriesID] = chapter.NumberKey
		}
	}
	for i := range items {
		items[i].LatestChapter = latest[items[i].SeriesID]
	}
	return nil
}

type discoverCatalogResult struct {
	items  []source.Manga
	cached bool
	err    error
}

func (s *Server) discoverPopular(ctx context.Context, rootFolderID int64, lang string, limit int, existing map[string]int64) ([]DiscoverSourceItem, []DiscoverSourceError, bool) {
	targets, catalogErrors := s.app.Catalogs.Select(ctx, catalogs.Filter{RootFolderID: rootFolderID, Scope: catalogs.ScopeActive, Lang: lang})
	targets = withoutAdultCatalogs(ctx, targets)
	if len(targets) > 8 {
		targets = targets[:8]
	}
	results := make([]discoverCatalogResult, len(targets))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	browseCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	for i, catalog := range targets {
		wg.Add(1)
		go func(index int, catalog catalogs.Catalog) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-browseCtx.Done():
				results[index].err = browseCtx.Err()
				return
			}
			defer func() { <-sem }()
			key := sourcecache.BrowseKey(s.app.Catalogs.Generation(), catalog.ModuleID, catalog.ID, "popular", 1)
			page, cached, err := sourcecache.Do(s.app.SourceCache, key, browseTTL, func() (*source.MangaPage, error) {
				provider, _, err := modules.GetAs[source.Latest](s.app.Modules, catalog.ModuleID)
				if err != nil {
					return nil, err
				}
				return provider.Popular(browseCtx, catalog.ID, 1)
			})
			results[index].cached, results[index].err = cached, err
			if page != nil {
				results[index].items = page.Mangas
			}
		}(i, catalog)
	}
	wg.Wait()

	errs := make([]DiscoverSourceError, 0, len(catalogErrors))
	for _, err := range catalogErrors {
		errs = append(errs, DiscoverSourceError{Source: "catalogs", Name: "Catalogs", Error: err})
	}
	allCached := len(targets) > 0
	seen := map[string]bool{}
	out := []DiscoverSourceItem{}
	for i, catalog := range targets {
		result := results[i]
		allCached = allCached && result.cached
		if result.err != nil {
			errs = append(errs, DiscoverSourceError{Source: catalog.Key(), Name: catalog.DisplayName, Error: result.err.Error()})
			continue
		}
		for _, manga := range result.items {
			key := discoverTitleKey(manga.Title)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			item := DiscoverSourceItem{ModuleID: catalog.ModuleID, ModuleName: catalog.ModuleName, SourceID: catalog.ID,
				SourceName: catalog.DisplayName, Language: catalog.Lang, Title: manga.Title, URL: manga.URL,
				EngineRef: manga.EngineRef, ChapterCount: manga.ChapterCount, ExistingSeriesID: existing[key]}
			if manga.ThumbnailURL != "" {
				item.ThumbnailURL = s.signDiscoverThumbnail(ctx, discoverThumbToken{ModuleID: catalog.ModuleID, SourceID: catalog.ID,
					URL: manga.URL, EngineRef: manga.EngineRef, Generation: s.app.Catalogs.Generation(), Expires: time.Now().Add(24 * time.Hour).Unix()})
			}
			out = append(out, item)
			if len(out) == limit {
				return out, errs, allCached
			}
		}
	}
	return out, errs, allCached
}

func (s *Server) discoverTokenSecret(ctx context.Context) []byte {
	general, err := s.app.Settings.General(ctx)
	if err != nil {
		return nil
	}
	return []byte(general.APIKey)
}

func (s *Server) signDiscoverThumbnail(ctx context.Context, payload discoverThumbToken) string {
	secret := s.discoverTokenSecret(ctx)
	data, err := json.Marshal(payload)
	if err != nil || len(secret) == 0 {
		return ""
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return "/api/v1/discover/thumbnail?token=" + encoded + "." + signature
}

func (s *Server) verifyDiscoverThumbnail(ctx context.Context, token string) (discoverThumbToken, error) {
	var payload discoverThumbToken
	encoded, signature, ok := strings.Cut(token, ".")
	if !ok {
		return payload, fmt.Errorf("invalid thumbnail token")
	}
	secret := s.discoverTokenSecret(ctx)
	want, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || len(secret) == 0 {
		return payload, fmt.Errorf("invalid thumbnail token")
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(encoded))
	if !hmac.Equal(want, mac.Sum(nil)) {
		return payload, fmt.Errorf("invalid thumbnail token")
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || json.Unmarshal(data, &payload) != nil || payload.Expires < time.Now().Unix() || payload.Generation != s.app.Catalogs.Generation() {
		return discoverThumbToken{}, fmt.Errorf("expired thumbnail token")
	}
	return payload, nil
}

func (s *Server) registerDiscover() {
	tags := []string{"Discover"}
	huma.Register(s.api, huma.Operation{OperationID: "discover", Method: http.MethodGet, Path: "/api/v1/discover", Tags: tags,
		Summary: "Personal recommendations, recent library updates and popular titles from prioritized sources"},
		func(ctx context.Context, in *struct {
			RootFolderID int64  `query:"rootFolderId"`
			Lang         string `query:"lang"`
			Limit        int    `query:"limit" default:"18" minimum:"1" maximum:"50"`
		}) (*struct{ Body DiscoverResponse }, error) {
			recommendations, updates, existing, err := s.discoverLibrary(ctx, in.RootFolderID, strings.TrimSpace(strings.ToLower(in.Lang)), in.Limit)
			if err != nil {
				return nil, toHTTPError(err)
			}
			popular, sourceErrors, cached := s.discoverPopular(ctx, in.RootFolderID, strings.TrimSpace(strings.ToLower(in.Lang)), in.Limit, existing)
			return &struct{ Body DiscoverResponse }{DiscoverResponse{Recommendations: recommendations, Updates: updates,
				Popular: popular, SourceErrors: sourceErrors, PopularCached: cached, GeneratedAt: time.Now().UTC()}}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "discover-thumbnail", Method: http.MethodGet, Path: "/api/v1/discover/thumbnail", Tags: tags,
		Summary: "A signed thumbnail returned by the discover feed"},
		func(ctx context.Context, in *struct {
			Token string `query:"token" minLength:"1"`
		}) (*imageOutput, error) {
			payload, err := s.verifyDiscoverThumbnail(ctx, in.Token)
			if err != nil {
				return nil, huma.Error404NotFound(err.Error())
			}
			if err := s.allowedCatalog(ctx, payload.ModuleID, payload.SourceID); err != nil {
				return nil, err
			}
			return s.sourceThumbnail(ctx, payload.ModuleID, source.MangaRef{SourceID: payload.SourceID, URL: payload.URL, EngineRef: payload.EngineRef})
		})
}
