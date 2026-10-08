package api

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/catalogs"
	"github.com/Asion001/mangarr/internal/diskcache"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/library"
	"github.com/Asion001/mangarr/internal/modules/metadata"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/sourcecache"
	"github.com/Asion001/mangarr/internal/sourcegov"
	"github.com/Asion001/mangarr/internal/sourcesearch"
)

func init() {
	capabilityProbes = append(capabilityProbes,
		func(i modules.Instance) string { _, ok := i.(source.ExtensionManager); return capIf(ok, "extensions") },
		func(i modules.Instance) string { _, ok := i.(source.Preferences); return capIf(ok, "preferences") },
		func(i modules.Instance) string { _, ok := i.(source.Latest); return capIf(ok, "browse") },
		func(i modules.Instance) string { _, ok := i.(library.ProgressReader); return capIf(ok, "progress") },
		func(i modules.Instance) string {
			_, ok := i.(metadata.ExternalLookup)
			return capIf(ok, "externalLookup")
		},
	)
	register((*Server).registerSources)
}

func capIf(ok bool, name string) string {
	if ok {
		return name
	}
	return ""
}

// SourceResource is a catalog as returned by the API.
type SourceResource = catalogs.Catalog

// SearchResultGroup holds one catalog's results.
type SearchResultGroup = sourcesearch.SearchResultGroup

type imageOutput struct {
	Status       int
	ContentType  string `header:"Content-Type"`
	CacheControl string `header:"Cache-Control"`
	ETag         string `header:"ETag"`
	Body         []byte
}

// sourceThumbnail serves a catalog thumbnail through the shared disk cache.
// Callers are responsible for checking that the catalog and reference are
// allowed for the current request.
func (s *Server) sourceThumbnail(ctx context.Context, moduleID int64, ref source.MangaRef) (*imageOutput, error) {
	key := strconv.FormatInt(moduleID, 10) + "|" + ref.SourceID + "|" + ref.URL
	data, ct, stale, err := s.app.ImageCache.Get(ctx, "thumbs", key, 7*24*time.Hour, func(ctx context.Context) (io.ReadCloser, string, error) {
		mod, _, err := modules.GetAs[source.Thumbnails](s.app.Modules, moduleID)
		if err != nil {
			return nil, "", err
		}
		return mod.Thumbnail(ctx, ref)
	})
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	cc := cachePrivate(24 * time.Hour)
	if stale {
		cc = cachePrivate(5 * time.Minute)
	}
	return imageReply(ctx, data, ct, cc), nil
}

// Cache TTLs for catalog responses.
const browseTTL = 30 * time.Minute

// searchCatalog searches one catalog through the cache.
func (s *Server) searchCatalog(ctx context.Context, moduleID int64, sourceID, query string, page int) (*source.MangaPage, bool, error) {
	return s.app.Search.Search(ctx, moduleID, sourceID, query, page)
}

// mangaDetails fetches details and chapters through the cache.
func (s *Server) mangaDetails(ctx context.Context, moduleID int64, ref source.MangaRef, fresh bool) (*sourcecache.Details, bool, error) {
	return s.app.Search.Details(ctx, moduleID, ref, fresh)
}

type MangaDetailsResult struct {
	Details  *source.MangaDetails `json:"details"`
	Chapters []source.Chapter     `json:"chapters"`
	Cached   bool                 `json:"cached"`
}

// SearchSources runs a query against many catalogs in parallel.
func (s *Server) SearchSources(ctx context.Context, query string, targets []catalogs.Catalog, page int) []SearchResultGroup {
	return s.app.Search.SearchMany(ctx, query, targets, page)
}

// allowedCatalog returns a 404 for hidden catalogs.
func (s *Server) allowedCatalog(ctx context.Context, moduleID int64, sourceID string) error {
	if err := s.app.Catalogs.Allowed(ctx, moduleID, sourceID); err != nil {
		return huma.Error404NotFound(err.Error())
	}
	return nil
}

func sourceError(err error) error {
	if cd, ok := sourcegov.CoolingDown(err); ok {
		return huma.Error503ServiceUnavailable(cd.Error())
	}
	return huma.Error502BadGateway(err.Error())
}

type CatalogList struct {
	// Generation changes whenever the usable catalogs change; include it in cache keys.
	Generation int64              `json:"generation"`
	Items      []catalogs.Catalog `json:"items"`
	Errors     []string           `json:"errors"`
}

func (s *Server) registerSources() {
	tags := []string{"Sources"}
	huma.Register(s.api, huma.Operation{OperationID: "sources-list", Method: http.MethodGet, Path: "/api/v1/sources", Tags: tags,
		Summary: "List usable catalogs of all active source modules (hidden NSFW catalogs excluded)"},
		func(ctx context.Context, in *struct {
			Refresh bool `query:"refresh"`
		}) (*struct{ Body []SourceResource }, error) {
			all, errs := s.app.Catalogs.List(ctx, in.Refresh)
			out := []SourceResource{}
			for _, c := range all {
				if !c.Hidden {
					out = append(out, c)
				}
			}
			if len(out) == 0 && len(errs) > 0 {
				return nil, huma.Error502BadGateway(strings.Join(errs, "; "))
			}
			return &struct{ Body []SourceResource }{out}, nil
		})

	ctags := []string{"Catalogs"}
	huma.Register(s.api, huma.Operation{OperationID: "catalogs-list", Method: http.MethodGet, Path: "/api/v1/catalogs", Tags: ctags,
		Summary: "Every catalog with its preferences, throttling state and the catalogs generation"},
		func(ctx context.Context, in *struct {
			Refresh bool `query:"refresh"`
		}) (*struct{ Body CatalogList }, error) {
			all, errs := s.app.Catalogs.List(ctx, in.Refresh)
			if all == nil {
				all = []catalogs.Catalog{}
			}
			if errs == nil {
				errs = []string{}
			}
			return &struct{ Body CatalogList }{CatalogList{Generation: s.app.Catalogs.Generation(), Items: all, Errors: errs}}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "catalogs-update", Method: http.MethodPut, Path: "/api/v1/catalogs", Tags: ctags,
		Summary: "Change preferences of several catalogs, keyed by moduleId:sourceId"},
		func(ctx context.Context, in *struct{ Body map[string]catalogs.Patch }) (*struct{ Body CatalogList }, error) {
			if err := s.app.Catalogs.Update(ctx, in.Body); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			all, errs := s.app.Catalogs.List(ctx, false)
			if errs == nil {
				errs = []string{}
			}
			return &struct{ Body CatalogList }{CatalogList{Generation: s.app.Catalogs.Generation(), Items: all, Errors: errs}}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "sources-search", Method: http.MethodGet, Path: "/api/v1/sources/search", Tags: tags,
		Summary: "Search several catalogs at once: scope=active (enabled, default languages), all, or source=moduleId:sourceId (repeatable)"},
		func(ctx context.Context, in *struct {
			Query   string   `query:"q" minLength:"1"`
			Scope   string   `query:"scope" enum:"active,all" default:"active"`
			Sources []string `query:"source,explode" doc:"Catalogs (moduleId:sourceId) for scope=custom; repeat the parameter or separate with commas"`
			Lang    string   `query:"lang"`
			Page    int      `query:"page" default:"1"`
		}) (*struct{ Body []SearchResultGroup }, error) {
			targets, _ := s.app.Catalogs.Select(ctx, catalogs.Filter{Scope: catalogs.Scope(in.Scope), Lang: in.Lang, Keys: splitList(in.Sources)})
			if len(targets) > 40 {
				return nil, huma.Error400BadRequest("too many catalogs selected (max 40); pick catalogs or a language")
			}
			return &struct{ Body []SearchResultGroup }{s.SearchSources(ctx, in.Query, targets, in.Page)}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "sources-browse", Method: http.MethodGet, Path: "/api/v1/sources/{moduleId}/{sourceId}/browse", Tags: tags},
		func(ctx context.Context, in *struct {
			ModuleID int64  `path:"moduleId"`
			SourceID string `path:"sourceId"`
			Type     string `query:"type" enum:"latest,popular,search" default:"popular"`
			Query    string `query:"q"`
			Page     int    `query:"page" default:"1"`
		}) (*struct{ Body *source.MangaPage }, error) {
			if err := s.allowedCatalog(ctx, in.ModuleID, in.SourceID); err != nil {
				return nil, err
			}
			var res *source.MangaPage
			var err error
			if in.Type == "search" {
				res, _, err = s.searchCatalog(ctx, in.ModuleID, in.SourceID, in.Query, in.Page)
			} else {
				key := sourcecache.BrowseKey(s.app.Catalogs.Generation(), in.ModuleID, in.SourceID, in.Type, in.Page)
				res, _, err = sourcecache.Do(s.app.SourceCache, key, browseTTL, func() (*source.MangaPage, error) {
					l, _, err := modules.GetAs[source.Latest](s.app.Modules, in.ModuleID)
					if err != nil {
						return nil, err
					}
					if in.Type == "latest" {
						return l.Latest(ctx, in.SourceID, in.Page)
					}
					return l.Popular(ctx, in.SourceID, in.Page)
				})
			}
			if err != nil {
				return nil, sourceError(err)
			}
			return &struct{ Body *source.MangaPage }{res}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "sources-manga", Method: http.MethodGet, Path: "/api/v1/sources/{moduleId}/{sourceId}/manga", Tags: tags,
		Summary: "Fetch details and chapters of a manga at a source (preview before adding); cached for an hour unless fresh=true"},
		func(ctx context.Context, in *struct {
			ModuleID  int64  `path:"moduleId"`
			SourceID  string `path:"sourceId"`
			URL       string `query:"url" minLength:"1"`
			EngineRef string `query:"engineRef"`
			Fresh     bool   `query:"fresh"`
		}) (*struct{ Body *MangaDetailsResult }, error) {
			if err := s.allowedCatalog(ctx, in.ModuleID, in.SourceID); err != nil {
				return nil, err
			}
			res, cached, err := s.mangaDetails(ctx, in.ModuleID, source.MangaRef{SourceID: in.SourceID, URL: in.URL, EngineRef: in.EngineRef}, in.Fresh)
			if err != nil {
				return nil, sourceError(err)
			}
			return &struct{ Body *MangaDetailsResult }{&MangaDetailsResult{Details: res.Details, Chapters: res.Chapters, Cached: cached}}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "sources-thumbnail", Method: http.MethodGet, Path: "/api/v1/sources/{moduleId}/{sourceId}/thumbnail", Tags: tags,
		Summary: "Proxy (and cache) a manga thumbnail through its source module"},
		func(ctx context.Context, in *struct {
			ModuleID  int64  `path:"moduleId"`
			SourceID  string `path:"sourceId"`
			URL       string `query:"url" minLength:"1"`
			EngineRef string `query:"engineRef"`
		}) (*imageOutput, error) {
			if err := s.allowedCatalog(ctx, in.ModuleID, in.SourceID); err != nil {
				return nil, err
			}
			return s.sourceThumbnail(ctx, in.ModuleID, source.MangaRef{SourceID: in.SourceID, URL: in.URL, EngineRef: in.EngineRef})
		})

	huma.Register(s.api, huma.Operation{OperationID: "modules-asset", Method: http.MethodGet, Path: "/api/v1/modules/{id}/asset", Tags: tags,
		Summary: "Proxy a module-relative image (e.g. extension icons)"},
		func(ctx context.Context, in *struct {
			ID   int64  `path:"id"`
			Path string `query:"path" minLength:"1"`
		}) (*imageOutput, error) {
			key := strconv.FormatInt(in.ID, 10) + "|" + in.Path
			data, ct, err := s.cachedImage(ctx, "assets", key, 7*24*time.Hour, func(ctx context.Context) (io.ReadCloser, string, error) {
				a, _, err := modules.GetAs[source.Assets](s.app.Modules, in.ID)
				if err != nil {
					return nil, "", err
				}
				return a.FetchAsset(ctx, in.Path)
			})
			if err != nil {
				return nil, huma.Error404NotFound(err.Error())
			}
			return imageReply(ctx, data, ct, cachePrivate(7*24*time.Hour)), nil
		})

	// ---- extensions / stores / preferences

	etags := []string{"Extensions"}
	huma.Register(s.api, huma.Operation{OperationID: "extensions-list", Method: http.MethodGet, Path: "/api/v1/modules/{id}/extensions", Tags: etags},
		func(ctx context.Context, in *struct {
			ID      int64 `path:"id"`
			Refresh bool  `query:"refresh"`
		}) (*struct{ Body []source.Extension }, error) {
			em, _, err := modules.GetAs[source.ExtensionManager](s.app.Modules, in.ID)
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			list, err := em.Extensions(ctx, in.Refresh)
			if err != nil {
				return nil, huma.Error502BadGateway(err.Error())
			}
			return &struct{ Body []source.Extension }{list}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "extensions-action", Method: http.MethodPost, Path: "/api/v1/modules/{id}/extensions/{pkg}/{action}", Tags: etags},
		func(ctx context.Context, in *struct {
			ID     int64  `path:"id"`
			Pkg    string `path:"pkg"`
			Action string `path:"action" enum:"install,update,uninstall"`
		}) (*struct{}, error) {
			em, _, err := modules.GetAs[source.ExtensionManager](s.app.Modules, in.ID)
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			switch in.Action {
			case "install":
				err = em.InstallExtension(ctx, in.Pkg)
			case "update":
				err = em.UpdateExtension(ctx, in.Pkg)
			case "uninstall":
				err = em.UninstallExtension(ctx, in.Pkg)
			}
			if err != nil {
				return nil, huma.Error502BadGateway(err.Error())
			}
			s.app.Catalogs.Invalidate(in.ID)
			s.app.Bus.Changed("extension", "updated", in.ID)
			return nil, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "stores-list", Method: http.MethodGet, Path: "/api/v1/modules/{id}/stores", Tags: etags},
		func(ctx context.Context, in *IDPath) (*struct{ Body []string }, error) {
			em, _, err := modules.GetAs[source.ExtensionManager](s.app.Modules, in.ID)
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			list, err := em.Stores(ctx)
			if err != nil {
				return nil, huma.Error502BadGateway(err.Error())
			}
			return &struct{ Body []string }{list}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "stores-add", Method: http.MethodPost, Path: "/api/v1/modules/{id}/stores", Tags: etags},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				URL string `json:"url" minLength:"1"`
			}
		}) (*struct{}, error) {
			em, _, err := modules.GetAs[source.ExtensionManager](s.app.Modules, in.ID)
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			if err := em.AddStore(ctx, in.Body.URL); err != nil {
				return nil, huma.Error502BadGateway(err.Error())
			}
			return nil, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "stores-remove", Method: http.MethodDelete, Path: "/api/v1/modules/{id}/stores", Tags: etags},
		func(ctx context.Context, in *struct {
			ID  int64  `path:"id"`
			URL string `query:"url" minLength:"1"`
		}) (*struct{}, error) {
			em, _, err := modules.GetAs[source.ExtensionManager](s.app.Modules, in.ID)
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			if err := em.RemoveStore(ctx, in.URL); err != nil {
				return nil, huma.Error502BadGateway(err.Error())
			}
			return nil, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "source-preferences", Method: http.MethodGet, Path: "/api/v1/sources/{moduleId}/{sourceId}/preferences", Tags: tags},
		func(ctx context.Context, in *struct {
			ModuleID int64  `path:"moduleId"`
			SourceID string `path:"sourceId"`
		}) (*struct{ Body []source.Preference }, error) {
			p, _, err := modules.GetAs[source.Preferences](s.app.Modules, in.ModuleID)
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			list, err := p.SourcePreferences(ctx, in.SourceID)
			if err != nil {
				return nil, huma.Error502BadGateway(err.Error())
			}
			return &struct{ Body []source.Preference }{list}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "source-preferences-set", Method: http.MethodPut, Path: "/api/v1/sources/{moduleId}/{sourceId}/preferences", Tags: tags},
		func(ctx context.Context, in *struct {
			ModuleID int64  `path:"moduleId"`
			SourceID string `path:"sourceId"`
			Body     struct {
				Position int    `json:"position"`
				Type     string `json:"type"`
				Value    any    `json:"value"`
			}
		}) (*struct{}, error) {
			p, _, err := modules.GetAs[source.Preferences](s.app.Modules, in.ModuleID)
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			if err := p.SetSourcePreference(ctx, in.SourceID, in.Body.Position, in.Body.Type, in.Body.Value); err != nil {
				return nil, huma.Error502BadGateway(err.Error())
			}
			return nil, nil
		})
}

// cachedImage serves an image from the image cache, fetching when the
// cached copy is missing or older than ttl.
func (s *Server) cachedImage(ctx context.Context, bucket, key string, ttl time.Duration, fetch diskcache.Fetch) (data []byte, ct string, err error) {
	data, ct, _, err = s.app.ImageCache.Get(ctx, bucket, key, ttl, fetch)
	return data, ct, err
}
