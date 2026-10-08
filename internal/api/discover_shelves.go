package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/catalogs"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/reading"
	"github.com/Asion001/mangarr/internal/sourcecache"
)

func init() { register((*Server).registerDiscoverShelves) }

type DiscoverShelfInput struct {
	Shelf        string `path:"shelf" enum:"recommendations,recently-updated,popular"`
	RootFolderID int64  `query:"rootFolderId" minimum:"0"`
	Lang         string `query:"lang" doc:"Library language or source catalog language (including multi-language catalogs)."`
	Genre        string `query:"genre" doc:"Exact metadata genre, case-insensitive; library shelves only."`
	Tag          string `query:"tag" doc:"Exact metadata tag, case-insensitive; library shelves only."`
	TagID        int64  `query:"tagId" minimum:"0" doc:"Library tag ID; library shelves only."`
	Format       string `query:"format" enum:",manga,manhwa,manhua"`
	Status       string `query:"status" enum:",unknown,ongoing,completed,hiatus,cancelled"`
	Source       string `query:"source" doc:"Catalog key moduleId:sourceId. Library shelves match linked sources; popular narrows active catalogs."`
	InLibrary    string `query:"inLibrary" enum:",true,false" doc:"Membership in the caller's visible library. Library shelves with false are empty."`
	Sort         string `query:"sort" enum:",recommended,popularity,recently-updated,newest,title"`
	PageSize     int    `query:"pageSize" default:"50" minimum:"1" maximum:"100"`
	Cursor       string `query:"cursor" maxLength:"128" doc:"Opaque continuation; repeat the same filters and sort. Expired cursors return 410; restart without a cursor."`
}

// DiscoverShelfPage retains the existing card resources and action identities.
type DiscoverShelfPage struct {
	Library      []DiscoverLibraryItem `json:"library"`
	Popular      []DiscoverSourceItem  `json:"popular"`
	SourceErrors []DiscoverSourceError `json:"sourceErrors"`
	NextCursor   string                `json:"nextCursor,omitempty"`
}

const discoverCursorTTL = 15 * time.Minute

// Cursor state is immutable in the bounded source cache. Library order is
// snapshotted, while visibility and reader state are evaluated on every page.
type discoverShelfCursor struct {
	Query    string
	Expires  time.Time
	IDs      []int64
	Catalogs []catalogs.Catalog
	Catalog  int
	Page     int
	Pending  []source.Manga
	HasNext  bool
	Seen     map[string]bool
}

func (s *Server) registerDiscoverShelves() {
	huma.Register(s.api, huma.Operation{OperationID: "discover-shelf", Method: http.MethodGet, Path: "/api/v1/discover/{shelf}", Tags: []string{"Discover"},
		Summary: "Browse a Discover shelf with cursor paging",
		Description: `Library shelves support lang, genre, metadata tag, library tagId, format, status, linked source and inLibrary. They only contain series with chapters; recommendations excludes started series. Default sorts are personalized recommended and recently-updated respectively. Library sorts: recommended (recommendations only), recently-updated (series/chapter modification time), newest (library added time), title (case-insensitive ascending). Popularity is unavailable for library series.

Popular supports lang, source, inLibrary, popularity (default) and recently-updated (the source's Latest feed). Catalogs are traversed in configured priority order; ranking is per catalog, not global. Catalogs without Latest support are reported in sourceErrors and skipped for recently-updated sorting. Genre, tag, tagId, format, status, recommended, newest and title are unsupported because source browse pages do not expose the necessary metadata or ordering. Unsupported combinations return 400. Existing HideNSFW settings always exclude hidden source catalogs; as in the combined endpoint, this setting does not filter local library metadata. There is no per-request override.

Library membership is matched against visible titles and alternative titles, including series without chapters, as in the combined endpoint. Hidden library IDs are never exposed. RootFolderId narrows library results/membership and selects source priorities. Cards retain the existing Read/Add/Request identities; mutation permissions are unchanged.

Continue with nextCursor and identical filters/sort; pageSize may change. Cursors snapshot library order and retain unread source page remainders, and deduplicate source titles across pages. Visibility, filters and membership are rechecked. Inserts do not shift library pages; changed or deleted series may disappear. Source providers use page numbers, so upstream reordering can still omit titles between fetched pages. At most eight source pages are fetched per request; an empty page can have a nextCursor. Source errors are returned alongside partial results and the failed catalog is skipped. Cursors expire after 15 minutes or cache eviction/restart (410); filter, caller, permission or catalog-setting changes invalidate them (400). No total count is provided.`},
		func(ctx context.Context, in *DiscoverShelfInput) (*struct{ Body DiscoverShelfPage }, error) {
			page, err := s.discoverShelf(ctx, *in)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &struct{ Body DiscoverShelfPage }{page}, nil
		})
}

func normalizeDiscoverShelf(in *DiscoverShelfInput) error {
	in.Lang = strings.ToLower(strings.TrimSpace(in.Lang))
	in.Genre = strings.ToLower(strings.TrimSpace(in.Genre))
	in.Tag = strings.ToLower(strings.TrimSpace(in.Tag))
	if in.Source != "" {
		mid, _, ok := catalogs.ParseKey(in.Source)
		if !ok || mid <= 0 {
			return badRequest("source must be moduleId:sourceId")
		}
	}
	if in.Sort == "" {
		switch in.Shelf {
		case "recommendations":
			in.Sort = "recommended"
		case "recently-updated":
			in.Sort = "recently-updated"
		case "popular":
			in.Sort = "popularity"
		}
	}
	if in.Shelf == "popular" {
		if in.Genre != "" || in.Tag != "" || in.TagID != 0 || in.Format != "" || in.Status != "" || (in.Sort != "popularity" && in.Sort != "recently-updated") {
			return badRequest("popular supports language, source, inLibrary and popularity/recently-updated sorting only")
		}
	} else if in.Sort == "popularity" || (in.Sort == "recommended" && in.Shelf != "recommendations") {
		return badRequest("unsupported sort for this library shelf")
	}
	return nil
}

func (s *Server) discoverShelfQuery(ctx context.Context, in DiscoverShelfInput) string {
	in.Cursor, in.PageSize = "", 0
	b, _ := json.Marshal(struct {
		Input      DiscoverShelfInput
		Viewer     *access.Principal
		Generation int64
	}{in, access.From(ctx), s.app.Catalogs.Generation()})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func (s *Server) discoverShelf(ctx context.Context, in DiscoverShelfInput) (DiscoverShelfPage, error) {
	out := DiscoverShelfPage{Library: []DiscoverLibraryItem{}, Popular: []DiscoverSourceItem{}, SourceErrors: []DiscoverSourceError{}}
	if err := normalizeDiscoverShelf(&in); err != nil {
		return out, err
	}
	query := s.discoverShelfQuery(ctx, in)
	state := discoverShelfCursor{Query: query, Expires: time.Now().Add(discoverCursorTTL), Page: 1, Seen: map[string]bool{}}
	if in.Cursor != "" {
		if _, err := hex.DecodeString(in.Cursor); err != nil || len(in.Cursor) != 32 {
			return out, badRequest("invalid discover cursor")
		}
		cached, ok := s.app.SourceCache.Get("discover-cursor|" + in.Cursor)
		if !ok {
			return out, huma.Error410Gone("discover cursor expired; restart the shelf")
		}
		state, ok = cached.(discoverShelfCursor)
		if !ok || state.Query != query {
			return out, badRequest("discover cursor does not match caller, filters or catalog settings")
		}
		if time.Now().After(state.Expires) {
			return out, huma.Error410Gone("discover cursor expired; restart the shelf")
		}
		state.Seen = maps.Clone(state.Seen)
	}
	readerID, err := s.readerOf(ctx)
	if err != nil {
		return out, err
	}
	all, err := s.app.Reading.AllSeries(ctx, readerID, 0)
	if err != nil {
		return out, err
	}
	if in.Shelf == "popular" {
		existing := map[string]int64{}
		for _, info := range all {
			if in.RootFolderID > 0 && info.Series.RootFolderID != in.RootFolderID {
				continue
			}
			// Membership is independent of the catalog language: translations and
			// multi-language catalogs may refer to the same visible title.
			for _, title := range append([]string{info.Series.Title}, info.Series.Metadata.AltTitles...) {
				if key := discoverTitleKey(title); key != "" {
					existing[key] = info.Series.ID
				}
			}
		}
		if in.Cursor == "" {
			targets, errs := s.app.Catalogs.Select(ctx, catalogs.Filter{RootFolderID: in.RootFolderID, Scope: catalogs.ScopeActive, Lang: in.Lang})
			targets = withoutAdultCatalogs(ctx, targets)
			for _, e := range errs {
				out.SourceErrors = append(out.SourceErrors, DiscoverSourceError{Source: "catalogs", Name: "Catalogs", Error: e})
			}
			for _, target := range targets {
				if in.Source == "" || target.Key() == in.Source {
					state.Catalogs = append(state.Catalogs, target)
				}
			}
		}
		s.discoverPopularPage(ctx, in, existing, &state, &out)
	} else {
		matching, err := s.discoverShelfLibrary(ctx, all, in)
		if err != nil {
			return out, err
		}
		if in.Cursor == "" {
			for _, item := range matching {
				state.IDs = append(state.IDs, item.SeriesID)
			}
		}
		byID := make(map[int64]DiscoverLibraryItem, len(matching))
		for _, item := range matching {
			byID[item.SeriesID] = item
		}
		for len(state.IDs) > 0 && len(out.Library) < in.PageSize {
			id := state.IDs[0]
			state.IDs = state.IDs[1:]
			if item, ok := byID[id]; ok {
				out.Library = append(out.Library, item)
			}
		}
		if in.Shelf == "recently-updated" {
			if err := s.discoverLatestChapters(ctx, out.Library); err != nil {
				return out, err
			}
		}
	}
	if len(state.IDs) > 0 || state.Catalog < len(state.Catalogs) {
		var nonce [16]byte
		_, _ = rand.Read(nonce[:])
		token := hex.EncodeToString(nonce[:])
		state.IDs = slices.Clone(state.IDs)
		state.Pending = slices.Clone(state.Pending)
		b, _ := json.Marshal(state)
		s.app.SourceCache.Set("discover-cursor|"+token, state, int64(len(b))*3+256, time.Until(state.Expires))
		if _, ok := s.app.SourceCache.Get("discover-cursor|" + token); !ok {
			return out, huma.Error503ServiceUnavailable("discover cursor cache is full")
		}
		out.NextCursor = token
	}
	return out, nil
}

func discoverContains(values []string, value string) bool {
	return slices.ContainsFunc(values, func(v string) bool { return strings.EqualFold(strings.TrimSpace(v), value) })
}

func (s *Server) discoverShelfLibrary(ctx context.Context, all []reading.SeriesInfo, in DiscoverShelfInput) ([]DiscoverLibraryItem, error) {
	linked := map[int64]bool{}
	if in.Source != "" {
		mid, sid, _ := catalogs.ParseKey(in.Source)
		var links []model.SeriesSource
		if err := s.app.DB.NewSelect().Model(&links).Column("series_id").Where("module_id = ? AND source_id = ?", mid, sid).Scan(ctx); err != nil {
			return nil, err
		}
		for _, link := range links {
			linked[link.SeriesID] = true
		}
	}
	// Recommendation affinity uses the same root/language scope as Discover,
	// before the additional result filters narrow the candidates.
	scoped := []reading.SeriesInfo{}
	for _, info := range all {
		if in.RootFolderID > 0 && info.Series.RootFolderID != in.RootFolderID {
			continue
		}
		if in.Lang != "" && !strings.EqualFold(info.Series.Language, in.Lang) {
			continue
		}
		scoped = append(scoped, info)
	}
	candidates := []discoverCandidate{}
	if in.Shelf == "recommendations" {
		candidates = discoverRecommendations(scoped, s.follows(ctx))
	} else {
		for _, info := range scoped {
			if info.Books > 0 {
				item := discoverLibraryItem(info)
				item.Reason = "recent-update"
				candidates = append(candidates, discoverCandidate{item: item, added: info.Series.AddedAt})
			}
		}
	}
	infoByID := map[int64]model.Series{}
	for _, info := range scoped {
		infoByID[info.Series.ID] = info.Series
	}
	candidates = slices.DeleteFunc(candidates, func(c discoverCandidate) bool {
		ser := infoByID[c.item.SeriesID]
		return in.InLibrary == "false" || (in.Genre != "" && !discoverContains(ser.Metadata.Genres, in.Genre)) ||
			(in.Tag != "" && !discoverContains(ser.Metadata.Tags, in.Tag)) || (in.TagID > 0 && !slices.Contains(ser.Tags, in.TagID)) ||
			(in.Format != "" && !strings.EqualFold(ser.Metadata.Format, in.Format)) || (in.Status != "" && ser.Status != in.Status) ||
			(in.Source != "" && !linked[ser.ID])
	})
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		switch in.Sort {
		case "recommended":
			if a.score != b.score {
				return a.score > b.score
			}
			if !a.added.Equal(b.added) {
				return a.added.After(b.added)
			}
		case "recently-updated":
			if !a.item.ChangedAt.Equal(b.item.ChangedAt) {
				return a.item.ChangedAt.After(b.item.ChangedAt)
			}
		case "newest":
			if !a.added.Equal(b.added) {
				return a.added.After(b.added)
			}
		}
		at, bt := strings.ToLower(a.item.Title), strings.ToLower(b.item.Title)
		if at != bt {
			return at < bt
		}
		return a.item.SeriesID < b.item.SeriesID
	})
	out := make([]DiscoverLibraryItem, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.item)
	}
	return out, nil
}

func (s *Server) discoverPopularPage(ctx context.Context, in DiscoverShelfInput, existing map[string]int64, state *discoverShelfCursor, out *DiscoverShelfPage) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	fetches := 0
	for state.Catalog < len(state.Catalogs) && len(out.Popular) < in.PageSize {
		catalog := state.Catalogs[state.Catalog]
		if in.Sort == "recently-updated" && !catalog.SupportsLatest {
			out.SourceErrors = append(out.SourceErrors, DiscoverSourceError{Source: catalog.Key(), Name: catalog.DisplayName, Error: "catalog does not support recently-updated sorting"})
			state.Catalog++
			state.Page = 1
			continue
		}
		if len(state.Pending) == 0 {
			if fetches == 8 || ctx.Err() != nil {
				break
			}
			fetches++
			kind := "popular"
			if in.Sort == "recently-updated" {
				kind = "latest"
			}
			key := sourcecache.BrowseKey(s.app.Catalogs.Generation(), catalog.ModuleID, catalog.ID, kind, state.Page)
			page, _, err := sourcecache.Do(s.app.SourceCache, key, browseTTL, func() (*source.MangaPage, error) {
				provider, _, err := modules.GetAs[source.Latest](s.app.Modules, catalog.ModuleID)
				if err != nil {
					return nil, err
				}
				if kind == "latest" {
					return provider.Latest(ctx, catalog.ID, state.Page)
				}
				return provider.Popular(ctx, catalog.ID, state.Page)
			})
			if err == nil && page == nil {
				err = fmt.Errorf("source returned an empty response")
			}
			if err != nil {
				out.SourceErrors = append(out.SourceErrors, DiscoverSourceError{Source: catalog.Key(), Name: catalog.DisplayName, Error: err.Error()})
				state.Catalog++
				state.Page = 1
				continue
			}
			state.Pending, state.HasNext = page.Mangas, page.HasNext
		}
		for len(state.Pending) > 0 && len(out.Popular) < in.PageSize {
			manga := state.Pending[0]
			state.Pending = state.Pending[1:]
			key := discoverTitleKey(manga.Title)
			if key == "" || state.Seen[key] {
				continue
			}
			state.Seen[key] = true
			id := existing[key]
			if (in.InLibrary == "true" && id == 0) || (in.InLibrary == "false" && id != 0) {
				continue
			}
			item := DiscoverSourceItem{ModuleID: catalog.ModuleID, ModuleName: catalog.ModuleName, SourceID: catalog.ID,
				SourceName: catalog.DisplayName, Language: catalog.Lang, Title: manga.Title, URL: manga.URL,
				EngineRef: manga.EngineRef, ChapterCount: manga.ChapterCount, ExistingSeriesID: id}
			if manga.ThumbnailURL != "" {
				item.ThumbnailURL = s.signDiscoverThumbnail(ctx, discoverThumbToken{ModuleID: catalog.ModuleID, SourceID: catalog.ID,
					URL: manga.URL, EngineRef: manga.EngineRef, Generation: s.app.Catalogs.Generation(), Expires: time.Now().Add(24 * time.Hour).Unix()})
			}
			out.Popular = append(out.Popular, item)
		}
		if len(state.Pending) == 0 {
			if state.HasNext {
				state.Page++
			} else {
				state.Catalog++
				state.Page = 1
			}
		}
	}
}
