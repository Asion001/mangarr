package api

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/fsutil"
	"github.com/Asion001/mangarr/internal/library"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

// LanguageFolder is where titles in a language go.
type LanguageFolder struct {
	Language string `json:"language"`
	Path     string `json:"path,omitempty"`
	// Exists is false when the folder is made on the first add.
	Exists bool   `json:"exists"`
	Error  string `json:"error,omitempty"`
}

type RootFolderResource struct {
	model.RootFolder
	Accessible bool   `json:"accessible"`
	FreeSpace  uint64 `json:"freeSpace"`
	Error      string `json:"error,omitempty"`
	SeriesCnt  int    `json:"seriesCount"`
}

// settingsDoc registers GET/PUT for one settings document.
func settingsDoc[T any](s *Server, name, key string, get func(context.Context) (T, error), after func(context.Context, T) error) {
	tags := []string{"Settings"}
	huma.Register(s.api, huma.Operation{OperationID: "settings-get-" + name, Method: http.MethodGet, Path: "/api/v1/settings/" + name, Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body T }, error) {
			v, err := get(ctx)
			return &struct{ Body T }{v}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "settings-put-" + name, Method: http.MethodPut, Path: "/api/v1/settings/" + name, Tags: tags},
		func(ctx context.Context, in *struct{ Body T }) (*struct{ Body T }, error) {
			if v, ok := any(in.Body).(interface{ Validate() error }); ok {
				if err := v.Validate(); err != nil {
					return nil, huma.Error400BadRequest(err.Error())
				}
			}
			if err := s.app.Settings.Set(ctx, key, in.Body); err != nil {
				return nil, toHTTPError(err)
			}
			// respond with the effective document (env-pinned fields win)
			v, err := get(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			if after != nil {
				if err := after(ctx, v); err != nil {
					return nil, toHTTPError(err)
				}
			}
			s.app.Bus.Changed("settings", "updated", 0)
			return &struct{ Body T }{v}, nil
		})
}

type GeneralSettingsResource struct {
	APIKey          string `json:"apiKey" readOnly:"true"`
	InstanceName    string `json:"instanceName"`
	PublicURL       string `json:"publicUrl"`
	BackupRetention int    `json:"backupRetention"`
	ImageCacheMaxMB int    `json:"imageCacheMaxMb"`
}

func generalResource(g settings.General) GeneralSettingsResource {
	return GeneralSettingsResource{g.APIKey, g.InstanceName, g.PublicURL, g.BackupRetention, g.ImageCacheMaxMB}
}

func (s *Server) registerSettings() {
	tags := []string{"Settings"}

	// General settings hide the session secret and keep the API key read-only.
	huma.Register(s.api, huma.Operation{OperationID: "settings-get-general", Method: http.MethodGet, Path: "/api/v1/settings/general", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body GeneralSettingsResource }, error) {
			g, err := s.app.Settings.General(ctx)
			return &struct{ Body GeneralSettingsResource }{generalResource(g)}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "settings-put-general", Method: http.MethodPut, Path: "/api/v1/settings/general", Tags: tags},
		func(ctx context.Context, in *struct{ Body GeneralSettingsResource }) (*struct{ Body GeneralSettingsResource }, error) {
			g, err := s.app.Settings.General(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			g.InstanceName, g.PublicURL, g.BackupRetention = in.Body.InstanceName, strings.TrimRight(in.Body.PublicURL, "/"), in.Body.BackupRetention
			g.ImageCacheMaxMB = max(in.Body.ImageCacheMaxMB, 0)
			if err := s.app.Settings.Set(ctx, settings.KeyGeneral, g); err != nil {
				return nil, toHTTPError(err)
			}
			g, _ = s.app.Settings.General(ctx) // effective values (env-pinned fields win)
			return &struct{ Body GeneralSettingsResource }{generalResource(g)}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "settings-regenerate-apikey", Method: http.MethodPost, Path: "/api/v1/settings/general/apikey", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body GeneralSettingsResource }, error) {
			g, err := s.app.Settings.General(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			for _, l := range s.app.Settings.Locks(settings.KeyGeneral) {
				if l.Path == "apiKey" {
					return nil, huma.Error409Conflict("the API key is set by " + l.Env)
				}
			}
			g.APIKey = settings.RandomHex(16)
			if err := s.app.Settings.Set(ctx, settings.KeyGeneral, g); err != nil {
				return nil, toHTTPError(err)
			}
			return &struct{ Body GeneralSettingsResource }{generalResource(g)}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "settings-locks", Method: http.MethodGet, Path: "/api/v1/settings/locks", Tags: tags,
		Summary: "Settings fields pinned by environment variables, per document"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body map[string][]settings.Lock }, error) {
			out := map[string][]settings.Lock{}
			for _, d := range settings.Docs {
				l := s.app.Settings.Locks(d.Key)
				if l == nil {
					l = []settings.Lock{}
				}
				out[d.Name] = l
			}
			return &struct{ Body map[string][]settings.Lock }{out}, nil
		})

	settingsDoc(s, "media", settings.KeyMediaManagement, s.app.Settings.MediaManagement, nil)
	settingsDoc(s, "schedule", settings.KeySchedule, s.app.Settings.Schedule, func(ctx context.Context, v settings.Schedule) error {
		s.app.DLQueue.Wake()
		s.app.Bus.Changed("tasks", "updated", 0)
		return nil
	})
	settingsDoc(s, "sources", settings.KeySources, s.app.Settings.Sources, func(ctx context.Context, v settings.Sources) error {
		s.app.Catalogs.Bump() // hidden/default catalogs may have changed
		return nil
	})
	settingsDoc(s, "downloads", settings.KeyDownloads, s.app.Settings.Downloads, nil)
	settingsDoc(s, "cleanup", settings.KeyCleanup, s.app.Settings.Cleanup, nil)
	settingsDoc(s, "appearance", settings.KeyAppearance, s.app.Settings.Appearance, nil)
	settingsDoc(s, "reading", settings.KeyReading, s.app.Settings.Reading, func(ctx context.Context, v settings.Reading) error {
		s.app.Komga.Reconcile(ctx) // start or stop the Komga-compatible API
		s.app.Bus.Changed("reading", "updated", 0)
		return nil
	})
	s.registerReadSyncSettings()

	// ---- root folders
	rtags := []string{"Root folders"}
	huma.Register(s.api, huma.Operation{OperationID: "rootfolders-list", Method: http.MethodGet, Path: "/api/v1/rootfolders", Tags: rtags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []RootFolderResource }, error) {
			var rows []model.RootFolder
			if err := s.app.DB.NewSelect().Model(&rows).Order("path").Scan(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			out := make([]RootFolderResource, 0, len(rows))
			for _, r := range rows {
				res := RootFolderResource{RootFolder: r}
				if err := fsutil.Writable(r.Path); err != nil {
					res.Error = err.Error()
				} else {
					res.Accessible = true
					res.FreeSpace, _ = fsutil.FreeSpace(r.Path)
				}
				n, _ := s.app.DB.NewSelect().Model((*model.Series)(nil)).Where("root_folder_id = ? AND preview = ?", r.ID, false).Count(ctx)
				res.SeriesCnt = n
				out = append(out, res)
			}
			return &struct{ Body []RootFolderResource }{out}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "rootfolders-create", Method: http.MethodPost, Path: "/api/v1/rootfolders", Tags: rtags},
		func(ctx context.Context, in *struct {
			Body struct {
				Path     string `json:"path" minLength:"1"`
				Language string `json:"language"`
			}
		}) (*struct{ Body model.RootFolder }, error) {
			p := filepath.Clean(in.Body.Path)
			if !filepath.IsAbs(p) {
				return nil, huma.Error400BadRequest("path must be absolute")
			}
			if err := fsutil.Writable(p); err != nil {
				return nil, huma.Error400BadRequest("folder is not writable: " + err.Error())
			}
			lang := library.NormalizeLanguage(in.Body.Language)
			if lang == "" {
				return nil, huma.Error400BadRequest("a root folder needs a language")
			}
			if err := s.languageFree(ctx, lang, 0); err != nil {
				return nil, err
			}
			rf := model.RootFolder{Path: p, Language: lang, CreatedAt: time.Now().UTC()}
			if _, err := s.app.DB.NewInsert().Model(&rf).Exec(ctx); err != nil {
				return nil, huma.Error409Conflict("root folder already exists")
			}
			s.app.Bus.Changed("rootfolder", "created", rf.ID)
			return &struct{ Body model.RootFolder }{rf}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "rootfolders-for-language", Method: http.MethodGet, Path: "/api/v1/rootfolders/for-language", Tags: rtags,
		Summary: "Where titles in a language go", Description: "The language's root folder, or the one the library folder would get; error when there is neither."},
		func(ctx context.Context, in *struct {
			Lang string `query:"lang" required:"true"`
		}) (*struct{ Body LanguageFolder }, error) {
			out := LanguageFolder{Language: library.NormalizeLanguage(in.Lang)}
			path, exists, err := s.app.Library.PlanFolder(ctx, in.Lang)
			out.Path, out.Exists = path, exists
			if err != nil {
				out.Error = err.Error()
			}
			return &struct{ Body LanguageFolder }{out}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "rootfolders-language", Method: http.MethodPut, Path: "/api/v1/rootfolders/{id}/language", Tags: rtags,
		Summary: "Set the language a root folder holds"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Language string `json:"language" minLength:"1"`
			}
		}) (*struct{ Body model.RootFolder }, error) {
			rf, err := s.app.Library.RootFolder(ctx, in.ID)
			if err != nil {
				return nil, huma.Error404NotFound("root folder not found")
			}
			lang := library.NormalizeLanguage(in.Body.Language)
			if lang == "" {
				return nil, huma.Error400BadRequest("a root folder needs a language")
			}
			if err := s.languageFree(ctx, lang, rf.ID); err != nil {
				return nil, err
			}
			rf.Language = lang
			if _, err := s.app.DB.NewUpdate().Model(rf).Column("language").WherePK().Exec(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			s.app.Bus.Changed("rootfolder", "updated", rf.ID)
			return &struct{ Body model.RootFolder }{*rf}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "rootfolders-delete", Method: http.MethodDelete, Path: "/api/v1/rootfolders/{id}", Tags: rtags},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			var rf model.RootFolder
			if err := s.app.DB.NewSelect().Model(&rf).Where("id = ?", in.ID).Scan(ctx); err == nil && rf.ManagedBy != "" {
				return nil, huma.Error409Conflict("this root folder is set by MANGARR_ROOT_FOLDERS")
			}
			n, _ := s.app.DB.NewSelect().Model((*model.Series)(nil)).Where("root_folder_id = ? AND preview = ?", in.ID, false).Count(ctx)
			if n > 0 {
				return nil, huma.Error409Conflict("root folder still has series")
			}
			// previews only borrow the folder; they go with it
			_, _ = s.app.DB.NewDelete().Model((*model.Series)(nil)).Where("root_folder_id = ? AND preview = ?", in.ID, true).Exec(ctx)
			_, err := s.app.DB.NewDelete().Model((*model.RootFolder)(nil)).Where("id = ?", in.ID).Exec(ctx)
			s.app.Bus.Changed("rootfolder", "deleted", in.ID)
			return nil, toHTTPError(err)
		})

	// ---- tags
	ttags := []string{"Tags"}
	huma.Register(s.api, huma.Operation{OperationID: "tags-list", Method: http.MethodGet, Path: "/api/v1/tags", Tags: ttags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []model.Tag }, error) {
			var rows []model.Tag
			err := s.app.DB.NewSelect().Model(&rows).Order("label").Scan(ctx)
			if rows == nil {
				rows = []model.Tag{}
			}
			return &struct{ Body []model.Tag }{rows}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "tags-create", Method: http.MethodPost, Path: "/api/v1/tags", Tags: ttags},
		func(ctx context.Context, in *struct {
			Body struct {
				Label string `json:"label" minLength:"1"`
			}
		}) (*struct{ Body model.Tag }, error) {
			t := model.Tag{Label: strings.ToLower(strings.TrimSpace(in.Body.Label))}
			var existing model.Tag
			if err := s.app.DB.NewSelect().Model(&existing).Where("label = ?", t.Label).Scan(ctx); err == nil {
				return &struct{ Body model.Tag }{existing}, nil
			}
			_, err := s.app.DB.NewInsert().Model(&t).Exec(ctx)
			s.app.Bus.Changed("tag", "created", t.ID)
			return &struct{ Body model.Tag }{t}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "tags-delete", Method: http.MethodDelete, Path: "/api/v1/tags/{id}", Tags: ttags},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			_, err := s.app.DB.NewDelete().Model((*model.Tag)(nil)).Where("id = ?", in.ID).Exec(ctx)
			s.app.Bus.Changed("tag", "deleted", in.ID)
			return nil, toHTTPError(err)
		})

	// ---- profiles
	ptags := []string{"Profiles"}
	huma.Register(s.api, huma.Operation{OperationID: "profiles-list", Method: http.MethodGet, Path: "/api/v1/profiles", Tags: ptags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []model.Profile }, error) {
			var rows []model.Profile
			err := s.app.DB.NewSelect().Model(&rows).Order("id").Scan(ctx)
			return &struct{ Body []model.Profile }{rows}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "profiles-create", Method: http.MethodPost, Path: "/api/v1/profiles", Tags: ptags},
		func(ctx context.Context, in *struct{ Body model.Profile }) (*struct{ Body model.Profile }, error) {
			p := in.Body
			p.ID = 0
			now := time.Now().UTC()
			p.CreatedAt, p.UpdatedAt = now, now
			p.Config.ProcessChangedAt = &now
			if err := validateProfile(&p); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			err := s.app.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				if _, err := tx.NewInsert().Model(&p).Exec(ctx); err != nil {
					return err
				}
				return clearOtherDefaults(ctx, tx, &p)
			})
			if err != nil {
				return nil, huma.Error409Conflict(err.Error())
			}
			s.app.Bus.Changed("profile", "created", p.ID)
			return &struct{ Body model.Profile }{p}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "profiles-update", Method: http.MethodPut, Path: "/api/v1/profiles/{id}", Tags: ptags},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body model.Profile
		}) (*struct{ Body model.Profile }, error) {
			var stored model.Profile
			if err := s.app.DB.NewSelect().Model(&stored).Where("id = ?", in.ID).Scan(ctx); err != nil {
				return nil, huma.Error404NotFound("profile not found")
			}
			p := in.Body
			p.ID, p.CreatedAt, p.UpdatedAt = in.ID, stored.CreatedAt, time.Now().UTC()
			// remember when processing settings changed: by default only chapters
			// imported afterwards are processed. processExisting is a one-off
			// answer for the settings it was given with, so a later change
			// waits for the person to ask again instead of re-queuing everything.
			p.Config.ProcessChangedAt = stored.Config.ProcessChangedAt
			if p.Config.ProcessParams() != stored.Config.ProcessParams() {
				p.Config.ProcessChangedAt = &p.UpdatedAt
				p.Config.ProcessExisting = false
			}
			if err := validateProfile(&p); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			if stored.IsDefault && !p.IsDefault {
				return nil, huma.Error400BadRequest("mark another profile as default instead")
			}
			err := s.app.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				if _, err := tx.NewUpdate().Model(&p).WherePK().Exec(ctx); err != nil {
					return err
				}
				return clearOtherDefaults(ctx, tx, &p)
			})
			if err != nil {
				return nil, toHTTPError(err)
			}
			s.app.Bus.Changed("profile", "updated", p.ID)
			s.app.PushProcessBacklog("profile-updated")
			return &struct{ Body model.Profile }{p}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "profiles-process-estimate", Method: http.MethodGet, Path: "/api/v1/profiles/{id}/process-estimate", Tags: ptags,
		Summary: "How many existing chapters of this profile's series would be processed"},
		func(ctx context.Context, in *IDPath) (*struct{ Body ProcessEstimate }, error) {
			var p model.Profile
			if err := s.app.DB.NewSelect().Model(&p).Where("id = ?", in.ID).Scan(ctx); err != nil {
				return nil, huma.Error404NotFound("profile not found")
			}
			var est ProcessEstimate
			params := p.Config.ProcessParams()
			if params == "" {
				return &struct{ Body ProcessEstimate }{est}, nil
			}
			q := s.app.DB.NewSelect().Model((*model.ChapterFile)(nil)).
				ColumnExpr("COUNT(*) AS files, COALESCE(SUM(size), 0) AS bytes").
				Where("process_params <> ?", params).
				Where("series_id IN (SELECT id FROM series WHERE profile_id = ?)", p.ID)
			if !p.Config.ProcessExisting && p.Config.ProcessChangedAt != nil {
				// what the backlog leaves alone until asked; newer imports and
				// forced files are queued anyway
				q = q.Where("imported_at < ? AND process_params <> ?", p.Config.ProcessChangedAt.UTC(), model.ProcessForce)
			}
			err := q.Scan(ctx, &est)
			return &struct{ Body ProcessEstimate }{est}, toHTTPError(err)
		})

	huma.Register(s.api, huma.Operation{OperationID: "profiles-delete", Method: http.MethodDelete, Path: "/api/v1/profiles/{id}", Tags: ptags},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			n, _ := s.app.DB.NewSelect().Model((*model.Series)(nil)).Where("profile_id = ?", in.ID).Count(ctx)
			if n > 0 {
				return nil, huma.Error409Conflict("profile is used by series")
			}
			var p model.Profile
			if err := s.app.DB.NewSelect().Model(&p).Where("id = ?", in.ID).Scan(ctx); err == nil && p.IsDefault {
				return nil, huma.Error409Conflict("cannot delete the default profile")
			}
			_, err := s.app.DB.NewDelete().Model((*model.Profile)(nil)).Where("id = ?", in.ID).Exec(ctx)
			s.app.Bus.Changed("profile", "deleted", in.ID)
			return nil, toHTTPError(err)
		})
}

// ProcessEstimate counts chapter files not processed with a profile's settings.
type ProcessEstimate struct {
	Files int   `json:"files" bun:"files"`
	Bytes int64 `json:"bytes" bun:"bytes"`
}

// clearOtherDefaults keeps exactly one default profile.
func clearOtherDefaults(ctx context.Context, tx bun.Tx, p *model.Profile) error {
	if !p.IsDefault {
		return nil
	}
	_, err := tx.NewUpdate().Model((*model.Profile)(nil)).Set("is_default = ?", false).Where("id <> ?", p.ID).Exec(ctx)
	return err
}

func validateProfile(p *model.Profile) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return badRequest("name is required")
	}
	switch p.Config.Encode.Format {
	case "":
		p.Config.Encode.Format = "keep"
	case "keep", "avif", "jxl":
	default:
		return badRequest("encode format must be keep, avif or jxl")
	}
	if p.Config.Encode.Format != "avif" {
		p.Config.Encode.Progressive = false
	}
	if p.Config.Encode.Format != "jxl" {
		p.Config.Encode.Lossy = false
	}
	if p.Config.Encode.Preset == "" {
		p.Config.Encode.Preset = "balanced"
	}
	if p.Config.ProcessTiming == "" {
		p.Config.ProcessTiming = "background"
	}
	if p.Config.PreferredScanlators == nil {
		p.Config.PreferredScanlators = []string{}
	}
	if p.Config.BlockedScanlators == nil {
		p.Config.BlockedScanlators = []string{}
	}
	for _, re := range append(append([]string{}, p.Config.PreferredScanlators...), p.Config.BlockedScanlators...) {
		if _, err := compileScanlatorPattern(re); err != nil {
			return badRequest("invalid scanlator pattern " + re + ": " + err.Error())
		}
	}
	if p.Config.Pages.MaxWidth < 0 {
		p.Config.Pages.MaxWidth = 0
	}
	if p.Config.Pages.SplitRatio < 0 {
		p.Config.Pages.SplitRatio = 0
	}
	if p.Config.Pages.SegmentRatio < 0 {
		p.Config.Pages.SegmentRatio = 0
	}
	if p.Config.LowRes.Width < 0 {
		p.Config.LowRes.Width = 0
	}
	u := &p.Config.Upscale
	// Upscalers are installation-wide workers selected by availability and
	// priority. Profiles describe the desired output, not a specific machine.
	u.UpscalerID = 0
	if u.MinWidth <= 0 {
		u.MinWidth = 1400
	}
	if u.Format == "" {
		u.Format = "webp"
	}
	if u.Quality <= 0 || u.Quality > 100 {
		u.Quality = 90
	}
	return nil
}

func compileScanlatorPattern(p string) (*regexp.Regexp, error) { return regexp.Compile("(?i)" + p) }

// languageFree fails when another root folder already holds the language:
// each language has exactly one folder.
func (s *Server) languageFree(ctx context.Context, lang string, except int64) error {
	n, err := s.app.DB.NewSelect().Model((*model.RootFolder)(nil)).Where("LOWER(language) = ? AND id <> ?", lang, except).Count(ctx)
	if err != nil {
		return toHTTPError(err)
	}
	if n > 0 {
		return huma.Error409Conflict(fmt.Sprintf("another root folder already holds %q; each language has one folder", lang))
	}
	return nil
}
