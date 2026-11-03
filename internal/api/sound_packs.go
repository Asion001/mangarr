package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/pagemoods"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/soundtrack"
)

func init() { register((*Server).registerSoundPacks) }

// SoundPacks is what's installed and how much room it takes.
type SoundPacks struct {
	Packs []soundtrack.PackView `json:"packs"`
	Usage soundtrack.Usage      `json:"usage"`
}

// CatalogPack is a pack the catalog offers and whether it's installed.
type CatalogPack struct {
	soundtrack.CatalogEntry
	Installed string `json:"installed,omitempty" doc:"The installed version, when installed"`
}

func packError(err error) error {
	switch {
	case errors.Is(err, soundtrack.ErrPackNotFound):
		return huma.Error404NotFound(err.Error())
	}
	return soundtrackError(err)
}

// soundtrackFile is the export's name.
func soundtrackFile(title string) string {
	name := folderSafe(title)
	if name == "" {
		name = "soundtrack"
	}
	return name + ".soundtrack.json"
}

func folderSafe(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r == '/' || r == '\\' || r == '"' || r == ':' || r < 32:
			out = append(out, '_')
		default:
			out = append(out, r)
		}
	}
	return string(out)
}

func (s *Server) soundPacks(ctx context.Context) (*SoundPacks, error) {
	packs, usage, err := s.app.Packs.List(ctx)
	if err != nil {
		return nil, err
	}
	return &SoundPacks{Packs: packs, Usage: usage}, nil
}

func (s *Server) registerSoundPacks() {
	tags := []string{"Soundtracks"}
	settingsDoc(s, "sound", settings.KeySound, s.app.Settings.Sound, nil)

	huma.Register(s.api, huma.Operation{OperationID: "audio-packs", Method: http.MethodGet, Path: "/api/v1/audio/packs", Tags: tags,
		Summary: "Installed sound packs and the room they take"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body *SoundPacks }, error) {
			out, err := s.soundPacks(ctx)
			return &struct{ Body *SoundPacks }{out}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "audio-catalog", Method: http.MethodGet, Path: "/api/v1/audio/catalog", Tags: tags,
		Summary: "Packs the sound catalog offers (Settings › Soundtracks)"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []CatalogPack }, error) {
			ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			entries, err := s.app.Packs.Catalog(ctx)
			if err != nil {
				if errors.Is(err, soundtrack.ErrInvalid) {
					return nil, huma.Error400BadRequest(err.Error())
				}
				return nil, huma.Error502BadGateway(err.Error())
			}
			packs, _, err := s.app.Packs.List(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := make([]CatalogPack, 0, len(entries))
			for _, e := range entries {
				c := CatalogPack{CatalogEntry: e}
				for _, p := range packs {
					if p.Slug == e.ID {
						c.Installed = p.Version
					}
				}
				out = append(out, c)
			}
			return &struct{ Body []CatalogPack }{out}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "audio-pack-install", Method: http.MethodPost, Path: "/api/v1/audio/packs/install", Tags: tags,
		Summary: "Install (or update) a pack from the catalog, in the background"},
		func(ctx context.Context, in *struct {
			Body struct {
				ID string `json:"id" minLength:"1"`
			}
		}) (*struct{ Body *model.Command }, error) {
			c, err := s.app.Queue.Push(ctx, "InstallSoundPack", map[string]any{"id": in.Body.ID}, "manual")
			return &struct{ Body *model.Command }{c}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "audio-pack-import-folder", Method: http.MethodPost, Path: "/api/v1/audio/packs/import-folder", Tags: tags,
		Summary: "Add the sounds in a folder on the server as a pack, in the background (music is skipped)"},
		func(ctx context.Context, in *struct {
			Body struct {
				Path string `json:"path" minLength:"1"`
			}
		}) (*struct{ Body *model.Command }, error) {
			if st, err := os.Stat(in.Body.Path); err != nil || !st.IsDir() || !filepath.IsAbs(in.Body.Path) {
				return nil, huma.Error400BadRequest("give the full path of a folder mangarr can read")
			}
			c, err := s.app.Queue.Push(ctx, "ImportSoundFolder", map[string]any{"path": in.Body.Path}, "manual")
			return &struct{ Body *model.Command }{c}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "audio-pack-delete", Method: http.MethodDelete, Path: "/api/v1/audio/packs/{id}", Tags: tags,
		Summary: "Remove a pack and its sounds (moods lose them)", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			if err := s.app.Packs.Delete(ctx, in.ID); err != nil {
				return nil, packError(err)
			}
			s.app.Bus.Changed("soundtrack", "updated", 0)
			return nil, nil
		})

	// a pack zip: streamed to a temporary file, then installed
	uploadOp := huma.Operation{OperationID: "audio-pack-upload", Method: http.MethodPost, Path: "/api/v1/audio/packs/upload", Tags: tags,
		Summary: "Install (or update) a pack from a zip holding pack.json and its sounds", MaxBodyBytes: -1,
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/zip": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}},
		Responses: map[string]*huma.Response{
			"200":     {Description: "Installed pack", Content: map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{Ref: "#/components/schemas/AudioPack"}}}},
			"default": {Description: "Error", Content: map[string]*huma.MediaType{"application/problem+json": {Schema: &huma.Schema{Ref: "#/components/schemas/ErrorModel"}}}},
		}}
	s.api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(model.AudioPack{}), true, "")
	s.api.OpenAPI().AddOperation(&uploadOp)
	s.api.Adapter().Handle(&uploadOp, s.api.Middlewares().Handler(func(hctx huma.Context) {
		ctx := hctx.Context()
		fail := func(status int, err error) { writeBackupAPIError(hctx, status, err) }
		if err := os.MkdirAll(s.app.Packs.Dir, 0o755); err != nil {
			fail(http.StatusInternalServerError, err)
			return
		}
		tmp, err := os.CreateTemp(s.app.Packs.Dir, ".upload-*.zip")
		if err != nil {
			fail(http.StatusInternalServerError, err)
			return
		}
		defer os.Remove(tmp.Name())
		_, err = io.Copy(tmp, io.LimitReader(hctx.BodyReader(), 2<<30))
		tmp.Close()
		if err != nil {
			fail(http.StatusBadRequest, err)
			return
		}
		pack, err := s.app.Packs.InstallZip(ctx, tmp.Name(), "upload")
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, soundtrack.ErrInvalid) {
				status = http.StatusBadRequest
			}
			fail(status, err)
			return
		}
		s.app.Bus.Changed("soundtrack", "updated", 0)
		writeBackupAPIJSON(hctx, http.StatusOK, pack)
	}))

	huma.Register(s.api, huma.Operation{OperationID: "audio-tracks", Method: http.MethodGet, Path: "/api/v1/audio/tracks", Tags: tags,
		Summary: "Installed sounds (ambience loops and effects)"},
		func(ctx context.Context, in *struct {
			Kind     string `query:"kind" enum:"ambience,sfx,"`
			Category string `query:"category"`
			Pack     int64  `query:"pack"`
			Q        string `query:"q" maxLength:"100"`
		}) (*struct{ Body []soundtrack.TrackView }, error) {
			out, err := s.app.Packs.Tracks(ctx, soundtrack.TrackFilter{Kind: in.Kind, Category: in.Category, PackID: in.Pack, Query: in.Q})
			return &struct{ Body []soundtrack.TrackView }{out}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "audio-track-stream", Method: http.MethodGet, Path: "/api/v1/audio/tracks/{id}/stream", Tags: tags,
		Summary:   "A sound's file (Range requests work; the ETag is its hash)",
		Responses: map[string]*huma.Response{"200": {Description: "The sound", Content: map[string]*huma.MediaType{"audio/*": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}}}},
		func(ctx context.Context, in *IDPath) (*huma.StreamResponse, error) {
			t, slug, err := s.app.Packs.Track(ctx, in.ID)
			if err != nil {
				return nil, packError(err)
			}
			f, err := os.Open(s.app.Packs.Path(slug, t))
			if err != nil {
				return nil, huma.Error404NotFound("the sound's file is missing: reinstall its pack")
			}
			return &huma.StreamResponse{Body: func(hctx huma.Context) {
				defer f.Close()
				r, w := humachi.Unwrap(hctx)
				w.Header().Set("Content-Type", soundtrack.AudioExts[t.Ext])
				w.Header().Set("ETag", `"`+t.SHA256+`"`)
				w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
				info, _ := f.Stat()
				var mod time.Time
				if info != nil {
					mod = info.ModTime()
				}
				http.ServeContent(w, r, "", mod, f)
			}}, nil
		})

	// one cue at a time (the reader's "Start a mood here")
	setCue := func(ctx context.Context, seriesID int64, userID *int64, c soundtrack.Cue) (*SeriesSoundtrack, error) {
		if _, err := s.visibleSeries(ctx, seriesID); err != nil {
			return nil, err
		}
		if _, err := soundtrack.SetCue(ctx, s.app.DB, seriesID, userID, c); err != nil {
			return nil, packError(err)
		}
		if userID == nil {
			s.app.Bus.Changed("soundtrack", "updated", seriesID)
		}
		return s.seriesSoundtrack(ctx, seriesID)
	}
	huma.Register(s.api, huma.Operation{OperationID: "series-soundtrack-cue", Method: http.MethodPut, Path: "/api/v1/series/{id}/soundtrack/cue", Tags: tags,
		Summary: "Add or replace one cue of the shared soundtrack (no mood removes it)"},
		func(ctx context.Context, in *struct {
			IDPath
			Body soundtrack.Cue
		}) (*struct{ Body *SeriesSoundtrack }, error) {
			out, err := setCue(ctx, in.ID, nil, in.Body)
			return &struct{ Body *SeriesSoundtrack }{out}, err
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-series-soundtrack-cue", Method: http.MethodPut, Path: "/api/v1/me/series/{id}/soundtrack/cue", Tags: tags,
		Summary: "Add or replace one of your own cues (no mood removes it)"},
		func(ctx context.Context, in *struct {
			IDPath
			Body soundtrack.Cue
		}) (*struct{ Body *SeriesSoundtrack }, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			out, err := setCue(ctx, in.ID, &p.UserID, in.Body)
			return &struct{ Body *SeriesSoundtrack }{out}, err
		})

	// automatic moods
	huma.Register(s.api, huma.Operation{OperationID: "series-soundtrack-detect", Method: http.MethodPost, Path: "/api/v1/series/{id}/soundtrack/detect", Tags: tags,
		Summary: "Find moods from the chapter titles now (automatic cues; cues people placed stay)"},
		func(ctx context.Context, in *IDPath) (*struct {
			Body struct {
				Detection  *soundtrack.Detection `json:"detection"`
				Soundtrack *SeriesSoundtrack     `json:"soundtrack"`
			}
		}, error) {
			if _, err := s.visibleSeries(ctx, in.ID); err != nil {
				return nil, err
			}
			d, err := soundtrack.DetectChapters(ctx, s.app.DB, s.app.Settings, in.ID)
			if err != nil {
				return nil, packError(err)
			}
			s.app.Bus.Changed("soundtrack", "updated", in.ID)
			out := &struct {
				Body struct {
					Detection  *soundtrack.Detection `json:"detection"`
					Soundtrack *SeriesSoundtrack     `json:"soundtrack"`
				}
			}{}
			out.Body.Detection = d
			out.Body.Soundtrack, err = s.seriesSoundtrack(ctx, in.ID)
			return out, err
		})
	huma.Register(s.api, huma.Operation{OperationID: "soundtracks-detect", Method: http.MethodPost, Path: "/api/v1/soundtracks/detect", Tags: tags,
		Summary: "Find moods from chapter titles for the whole library, in the background"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body *model.Command }, error) {
			c, err := s.app.Queue.Push(ctx, "DetectMoods", map[string]any{}, "manual")
			return &struct{ Body *model.Command }{c}, toHTTPError(err)
		})

	// moods in pages
	huma.Register(s.api, huma.Operation{OperationID: "page-moods-status", Method: http.MethodGet, Path: "/api/v1/audio/page-moods", Tags: tags,
		Summary: "Whether moods can be found in pages, and where (this server or workers)"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body pagemoods.Status }, error) {
			return &struct{ Body pagemoods.Status }{s.app.PageMoods.Status(ctx)}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-soundtrack-score", Method: http.MethodPost, Path: "/api/v1/series/{id}/soundtrack/score", Tags: tags,
		Summary: "Find moods in a title's pages, in the background"},
		func(ctx context.Context, in *IDPath) (*struct{ Body *model.Command }, error) {
			if _, err := s.visibleSeries(ctx, in.ID); err != nil {
				return nil, err
			}
			if st := s.app.PageMoods.Status(ctx); !st.Enabled {
				return nil, huma.Error409Conflict(pagemoods.ErrOff.Error())
			} else if !st.Local && !st.Workers {
				return nil, huma.Error409Conflict("page scoring unavailable: " + st.LocalReason + ", and no worker that can score pages is online")
			}
			c, err := s.app.Queue.Push(ctx, "ScorePages", map[string]any{"seriesId": in.ID}, "manual")
			return &struct{ Body *model.Command }{c}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-page-moods", Method: http.MethodGet, Path: "/api/v1/series/{id}/page-moods", Tags: tags,
		Summary: "The moods found in a title's pages, for reviewing them: its chapters, and one chapter's page scores"},
		func(ctx context.Context, in *struct {
			IDPath
			Chapter string `query:"chapter" doc:"The chapter number to detail (default: the first scored one)"`
		}) (*struct{ Body *pagemoods.Review }, error) {
			if _, err := s.visibleSeries(ctx, in.ID); err != nil {
				return nil, err
			}
			var number *float64
			if in.Chapter != "" {
				n, err := strconv.ParseFloat(in.Chapter, 64)
				if err != nil {
					return nil, huma.Error422UnprocessableEntity("chapter must be a number")
				}
				number = &n
			}
			out, err := s.app.PageMoods.Review(ctx, in.ID, number)
			return &struct{ Body *pagemoods.Review }{out}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-page-moods-dismiss", Method: http.MethodPost, Path: "/api/v1/series/{id}/page-moods/dismiss", Tags: tags,
		Summary: "Say a mood found in the pages doesn't start here: its cue goes, and finding moods again leaves the place be", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			IDPath
			Body struct {
				ChapterNumber float64 `json:"chapterNumber" minimum:"0"`
				Page          int     `json:"page,omitempty" minimum:"0" doc:"0 or 1: the chapter's start"`
			}
		}) (*struct{}, error) {
			if _, err := s.visibleSeries(ctx, in.ID); err != nil {
				return nil, err
			}
			if err := s.app.PageMoods.Dismiss(ctx, in.ID, in.Body.ChapterNumber, in.Body.Page); err != nil {
				return nil, toHTTPError(err)
			}
			s.app.Bus.Changed("soundtrack", "updated", in.ID)
			return nil, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "read-chapter-soundtrack-prepare", Method: http.MethodPost, Path: "/api/v1/read/chapters/{id}/soundtrack/prepare", Tags: []string{"Reader"},
		Summary: "Find moods in this chapter's pages and the next two, if that's on (the reader asks when music is on)", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			ch, ser, err := s.chapterSeries(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if st := s.app.PageMoods.Status(ctx); !st.Enabled || (!st.Local && !st.Workers) {
				return nil, nil
			}
			var ids []int64
			if err := s.app.DB.NewSelect().TableExpr("chapters").Column("id").Where("series_id = ? AND number_sort >= ? AND file_id IS NOT NULL", ser.ID, ch.NumberSort).
				OrderExpr("number_sort").Limit(3).Scan(ctx, &ids); err != nil {
				return nil, toHTTPError(err)
			}
			if len(ids) > 0 {
				_, _ = s.app.Queue.Push(ctx, "ScorePages", map[string]any{"seriesId": ser.ID, "chapterIds": ids}, "reader")
			}
			return nil, nil
		})

	// export and import
	huma.Register(s.api, huma.Operation{OperationID: "series-soundtrack-export", Method: http.MethodGet, Path: "/api/v1/series/{id}/soundtrack/export", Tags: tags,
		Summary: "A soundtrack as a file (cues by chapter number, sounds by hash)"},
		func(ctx context.Context, in *struct {
			IDPath
			Version string `query:"version" enum:"shared,personal" default:"shared"`
		}) (*struct {
			Disposition string `header:"Content-Disposition"`
			Body        *soundtrack.Export
		}, error) {
			ser, err := s.visibleSeries(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			var owner *int64
			if in.Version == "personal" {
				p, err := me(ctx)
				if err != nil {
					return nil, err
				}
				owner = &p.UserID
			}
			d, err := soundtrack.Get(ctx, s.app.DB, in.ID, owner)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out, err := soundtrack.MakeExport(ctx, s.app.DB, ser.Title, d)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &struct {
				Disposition string `header:"Content-Disposition"`
				Body        *soundtrack.Export
			}{`attachment; filename="` + soundtrackFile(ser.Title) + `"`, out}, nil
		})
	type importOut struct {
		Body struct {
			Result     *soundtrack.ImportResult `json:"result"`
			Soundtrack *SeriesSoundtrack        `json:"soundtrack"`
		}
	}
	doImport := func(ctx context.Context, seriesID int64, owner *int64, e soundtrack.Export) (*importOut, error) {
		if _, err := s.visibleSeries(ctx, seriesID); err != nil {
			return nil, err
		}
		// only admins (the shared soundtrack) make moods
		d, res, err := soundtrack.ReadExport(ctx, s.app.DB, e, owner == nil)
		if err != nil {
			return nil, packError(err)
		}
		if owner != nil {
			if old, err := soundtrack.Get(ctx, s.app.DB, seriesID, owner); err == nil && old != nil {
				d.Muted = old.Muted
			}
		}
		if err := soundtrack.Save(ctx, s.app.DB, seriesID, owner, *d); err != nil {
			return nil, packError(err)
		}
		out := &importOut{}
		out.Body.Result = res
		if out.Body.Soundtrack, err = s.seriesSoundtrack(ctx, seriesID); err != nil {
			return nil, err
		}
		if owner == nil {
			s.app.Bus.Changed("soundtrack", "updated", seriesID)
		}
		return out, nil
	}
	huma.Register(s.api, huma.Operation{OperationID: "series-soundtrack-import", Method: http.MethodPost, Path: "/api/v1/series/{id}/soundtrack/import", Tags: tags,
		Summary: "Replace the shared soundtrack with an exported one (moods it names that aren't here are made from installed sounds)"},
		func(ctx context.Context, in *struct {
			IDPath
			Body soundtrack.Export
		}) (*importOut, error) {
			return doImport(ctx, in.ID, nil, in.Body)
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-series-soundtrack-import", Method: http.MethodPost, Path: "/api/v1/me/series/{id}/soundtrack/import", Tags: tags,
		Summary: "Replace your own cues with an exported soundtrack (moods by name only)"},
		func(ctx context.Context, in *struct {
			IDPath
			Body soundtrack.Export
		}) (*importOut, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			return doImport(ctx, in.ID, &p.UserID, in.Body)
		})
}
