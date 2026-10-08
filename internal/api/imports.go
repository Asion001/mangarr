package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/imports"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/netguard"
)

// backupCovers fetches cover links found in uploaded backups (public
// addresses only).
var backupCovers = netguard.Client(30 * time.Second)

func init() { register((*Server).registerImports) }

// ImportResource is an import with its entry counts.
type ImportResource struct {
	model.Import
	// Counts are entries per state, plus "all", "selected" and "selected:<state>".
	Counts map[string]int `json:"counts"`
	// Busy is true while the import is being matched or run.
	Busy bool `json:"busy"`
}

// ImportEntryView is an entry without its chapter list (see the counts).
type ImportEntryView struct {
	model.ImportEntry
	ChapterCount int `json:"chapterCount"`
	ReadCount    int `json:"readCount"`
}

type ImportEntriesPage struct {
	Items    []ImportEntryView `json:"items"`
	Total    int               `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"pageSize"`
}

// ImportEntryFilter selects entries for bulk changes.
type ImportEntryFilter struct {
	State    string `json:"state,omitempty" enum:",pending,ready,review,extension,library,imported,failed"`
	Selected *bool  `json:"selected,omitempty"`
	Query    string `json:"q,omitempty"`
}

type ImportEntriesPatch struct {
	IDs    []int64            `json:"ids,omitempty"`
	Filter *ImportEntryFilter `json:"filter,omitempty" doc:"Change every entry matching this filter instead of ids"`
	imports.EntryPatch
}

func importError(err error) error {
	switch {
	case errors.Is(err, imports.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, imports.ErrBusy):
		return huma.Error409Conflict(err.Error())
	}
	var pe *imports.ParseError
	if errors.As(err, &pe) {
		return huma.Error400BadRequest(err.Error())
	}
	return toHTTPError(err)
}

func (s *Server) importResource(ctx context.Context, imp *model.Import) (*ImportResource, error) {
	counts, err := s.app.Imports.Counts(ctx, imp.ID)
	if err != nil {
		return nil, err
	}
	return &ImportResource{Import: *imp, Counts: counts, Busy: s.app.Imports.Busy(imp.ID)}, nil
}

func (s *Server) registerImports() {
	tags := []string{"Import"}
	huma.Register(s.api, huma.Operation{OperationID: "imports-create", Method: http.MethodPost, Path: "/api/v1/imports", Tags: tags,
		Summary:      "Upload a Mihon/Tachiyomi/Suwayomi (.tachibk, .proto.gz) or Aidoku (.aib) backup; its manga are matched in the background",
		MaxBodyBytes: 128 << 20, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct {
			FileName string `query:"fileName"`
			RawBody  []byte `contentType:"application/octet-stream"`
		}) (*struct{ Body *ImportResource }, error) {
			imp, err := s.app.Imports.Create(ctx, in.FileName, in.RawBody)
			if err != nil {
				return nil, importError(err)
			}
			if _, err := s.app.Queue.Push(ctx, "MapImport", map[string]any{"importId": imp.ID}, "upload"); err != nil {
				return nil, toHTTPError(err)
			}
			res, err := s.importResource(ctx, imp)
			return &struct{ Body *ImportResource }{res}, toHTTPError(err)
		})

	huma.Register(s.api, huma.Operation{OperationID: "imports-list", Method: http.MethodGet, Path: "/api/v1/imports", Tags: tags},
		func(ctx context.Context, in *struct{}) (*struct{ Body []ImportResource }, error) {
			list, err := s.app.Imports.List(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := []ImportResource{}
			for i := range list {
				r, err := s.importResource(ctx, &list[i])
				if err != nil {
					return nil, toHTTPError(err)
				}
				out = append(out, *r)
			}
			return &struct{ Body []ImportResource }{out}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "imports-get", Method: http.MethodGet, Path: "/api/v1/imports/{id}", Tags: tags},
		func(ctx context.Context, in *IDPath) (*struct{ Body *ImportResource }, error) {
			imp, err := s.app.Imports.Get(ctx, in.ID)
			if err != nil {
				return nil, importError(err)
			}
			res, err := s.importResource(ctx, imp)
			return &struct{ Body *ImportResource }{res}, toHTTPError(err)
		})

	huma.Register(s.api, huma.Operation{OperationID: "imports-entries", Method: http.MethodGet, Path: "/api/v1/imports/{id}/entries", Tags: tags},
		func(ctx context.Context, in *struct {
			ID       int64  `path:"id"`
			State    string `query:"state" enum:",pending,ready,review,extension,library,imported,failed"`
			Selected string `query:"selected" enum:",true,false"`
			Query    string `query:"q"`
			Page     int    `query:"page" default:"1"`
			PageSize int    `query:"pageSize" default:"100"`
		}) (*struct{ Body ImportEntriesPage }, error) {
			f := imports.EntryFilter{State: in.State, Query: in.Query}
			if in.Selected != "" {
				sel := in.Selected == "true"
				f.Selected = &sel
			}
			pageSize := min(max(in.PageSize, 1), 500)
			items, total, err := s.app.Imports.Entries(ctx, in.ID, f, in.Page, pageSize)
			if err != nil {
				return nil, importError(err)
			}
			views := make([]ImportEntryView, 0, len(items))
			for _, e := range items {
				v := ImportEntryView{ImportEntry: e, ChapterCount: len(e.Data.Chapters), ReadCount: e.Data.ReadCount()}
				v.Data.Chapters = nil // can be thousands per manga
				views = append(views, v)
			}
			return &struct{ Body ImportEntriesPage }{ImportEntriesPage{Items: views, Total: total, Page: max(in.Page, 1), PageSize: pageSize}}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "imports-entry-cover", Method: http.MethodGet, Path: "/api/v1/imports/{id}/entries/{entryId}/cover", Tags: tags,
		Summary: "An entry's cover: the matched manga's thumbnail, else the backup's cover link (resized and cached)"},
		func(ctx context.Context, in *struct {
			ID      int64  `path:"id"`
			EntryID int64  `path:"entryId"`
			V       string `query:"v" doc:"Cache buster"`
		}) (*imageOutput, error) {
			var e model.ImportEntry
			if err := s.app.DB.NewSelect().Model(&e).Where("id = ? AND import_id = ?", in.EntryID, in.ID).Scan(ctx); err != nil {
				return nil, huma.Error404NotFound("entry not found")
			}
			if src := e.Source; src != nil && e.State != model.EntryExtension {
				if th, _, err := modules.GetAs[source.Thumbnails](s.app.Modules, src.ModuleID); err == nil {
					// same cache entry as the catalog thumbnail endpoint
					key := strconv.FormatInt(src.ModuleID, 10) + "|" + src.SourceID + "|" + src.URL
					data, ct, err := s.cachedImage(ctx, "thumbs", key, 7*24*time.Hour, func(ctx context.Context) (io.ReadCloser, string, error) {
						return th.Thumbnail(ctx, source.MangaRef{SourceID: src.SourceID, URL: src.URL, TitleHint: src.Title})
					})
					if err == nil {
						return imageReply(ctx, data, ct, cachePrivate(24*time.Hour)), nil
					}
				}
			}
			u, err := url.Parse(e.Data.ThumbnailURL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return nil, huma.Error404NotFound("no cover")
			}
			data, ct, err := s.cachedImage(ctx, "thumbs", "url|"+u.String(), 30*24*time.Hour, func(ctx context.Context) (io.ReadCloser, string, error) {
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
				req.Header.Set("Referer", u.Scheme+"://"+u.Host+"/") // sites refuse foreign referers
				req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; mangarr)")
				resp, err := backupCovers.Do(req)
				if err != nil {
					return nil, "", err
				}
				if resp.StatusCode != http.StatusOK {
					resp.Body.Close()
					return nil, "", errors.New(resp.Status)
				}
				return resp.Body, resp.Header.Get("Content-Type"), nil
			})
			if err != nil {
				return nil, huma.Error404NotFound("no cover: " + err.Error())
			}
			return imageReply(ctx, data, ct, cachePrivate(24*time.Hour)), nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "imports-entries-update", Method: http.MethodPatch, Path: "/api/v1/imports/{id}/entries", Tags: tags,
		Summary: "Select, accept or re-map entries (by ids or filter)"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body ImportEntriesPatch
		}) (*struct {
			Body struct {
				Changed int `json:"changed"`
			}
		}, error) {
			f := imports.EntryFilter{IDs: in.Body.IDs}
			if in.Body.Filter != nil {
				f = imports.EntryFilter{State: in.Body.Filter.State, Selected: in.Body.Filter.Selected, Query: in.Body.Filter.Query}
			} else if len(in.Body.IDs) == 0 {
				return nil, huma.Error400BadRequest("ids or filter required")
			}
			n, err := s.app.Imports.UpdateEntries(ctx, in.ID, f, in.Body.EntryPatch)
			if err != nil {
				return nil, importError(err)
			}
			out := &struct {
				Body struct {
					Changed int `json:"changed"`
				}
			}{}
			out.Body.Changed = n
			return out, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "imports-options", Method: http.MethodPut, Path: "/api/v1/imports/{id}/options", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body model.ImportOptions
		}) (*struct{ Body *ImportResource }, error) {
			imp, err := s.app.Imports.SetOptions(ctx, in.ID, in.Body)
			if err != nil {
				return nil, importError(err)
			}
			res, err := s.importResource(ctx, imp)
			return &struct{ Body *ImportResource }{res}, toHTTPError(err)
		})

	command := func(name string) func(ctx context.Context, id int64, body map[string]any) (*struct{ Body *model.Command }, error) {
		return func(ctx context.Context, id int64, body map[string]any) (*struct{ Body *model.Command }, error) {
			if _, err := s.app.Imports.Get(ctx, id); err != nil {
				return nil, importError(err)
			}
			if s.app.Imports.Busy(id) {
				return nil, importError(imports.ErrBusy)
			}
			body["importId"] = id
			c, err := s.app.Queue.Push(ctx, name, body, "manual")
			return &struct{ Body *model.Command }{c}, toHTTPError(err)
		}
	}
	mapCmd, installCmd, runCmd := command("MapImport"), command("InstallImportExtensions"), command("RunImport")

	huma.Register(s.api, huma.Operation{OperationID: "imports-remap", Method: http.MethodPost, Path: "/api/v1/imports/{id}/remap", Tags: tags,
		Summary: "Match entries again (all that aren't imported when no ids are given)"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				IDs []int64 `json:"ids,omitempty"`
			}
		}) (*struct{ Body *model.Command }, error) {
			ids := in.Body.IDs
			if len(ids) == 0 {
				all, _, err := s.app.Imports.Entries(ctx, in.ID, imports.EntryFilter{}, 0, 0)
				if err != nil {
					return nil, importError(err)
				}
				for _, e := range all {
					if e.State != model.EntryImported {
						ids = append(ids, e.ID)
					}
				}
			}
			return mapCmd(ctx, in.ID, map[string]any{"entryIds": ids})
		})

	huma.Register(s.api, huma.Operation{OperationID: "imports-install-extensions", Method: http.MethodPost, Path: "/api/v1/imports/{id}/install-extensions", Tags: tags,
		Summary: "Install the extensions entries need, then match those entries again"},
		func(ctx context.Context, in *IDPath) (*struct{ Body *model.Command }, error) {
			return installCmd(ctx, in.ID, map[string]any{})
		})

	huma.Register(s.api, huma.Operation{OperationID: "imports-run", Method: http.MethodPost, Path: "/api/v1/imports/{id}/run", Tags: tags,
		Summary: "Add the selected entries to the library"},
		func(ctx context.Context, in *IDPath) (*struct{ Body *model.Command }, error) {
			imp, err := s.app.Imports.Get(ctx, in.ID)
			if err != nil {
				return nil, importError(err)
			}
			if imp.Options.RootFolderID == 0 {
				return nil, huma.Error400BadRequest("choose a root folder first")
			}
			return runCmd(ctx, in.ID, map[string]any{})
		})

	huma.Register(s.api, huma.Operation{OperationID: "imports-delete", Method: http.MethodDelete, Path: "/api/v1/imports/{id}", Tags: tags,
		Summary: "Delete an import (series it added stay)"},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			return nil, importError(s.app.Imports.Delete(ctx, in.ID))
		})
}
