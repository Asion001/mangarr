package api

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/komgaapi"
	"github.com/Asion001/mangarr/internal/library"
	"github.com/Asion001/mangarr/internal/model"
)

func init() { register((*Server).registerReading) }

// ReadingStatus is the Komga-compatible API's state for connect guides.
type ReadingStatus struct {
	komgaapi.Status
	// PublicURL is the address apps should use (empty: this server's host).
	PublicURL string `json:"publicUrl"`
}

// ReadingKeyView is a device key with its owner.
type ReadingKeyView struct {
	model.ReadingKey
	User string `json:"user,omitempty"`
}

// NewReadingKey is a key as created: Key is only returned this once.
type NewReadingKey struct {
	model.ReadingKey
	Key string `json:"key"`
}

type mihonBackupOutput struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	CacheControl       string `header:"Cache-Control"`
	Body               []byte
}

// ShelfItem is a series on the "Continue reading" shelf.
type ShelfItem struct {
	SeriesID int64  `json:"seriesId"`
	Title    string `json:"title"`
	CoverURL string `json:"coverUrl"`
	// Next is the chapter to read next; Page is where the reader left off
	// in it (0 = not started).
	Next       NextChapter `json:"next"`
	Page       int         `json:"page"`
	Read       int         `json:"read"`
	Total      int         `json:"total"`
	LastReadAt *time.Time  `json:"lastReadAt,omitempty"`
}

// Shelf is a reader's "Continue reading" shelf.
type Shelf struct {
	ReaderID int64       `json:"readerId"`
	Reader   string      `json:"reader"`
	Items    []ShelfItem `json:"items"`
}

func (s *Server) registerReading() {
	tags := []string{"Reading apps"}
	huma.Register(s.api, huma.Operation{OperationID: "reading-shelf", Method: http.MethodGet, Path: "/api/v1/reading/shelf", Tags: tags,
		Summary: "Continue reading: the next chapter of each series the reader started, most recently read first"},
		func(ctx context.Context, in *struct {
			ReaderID int64 `query:"readerId" doc:"Reader (0 = the one reading apps act as)"`
			Limit    int   `query:"limit" default:"20" minimum:"1" maximum:"100"`
		}) (*struct{ Body Shelf }, error) {
			rid := in.ReaderID
			if p := access.From(ctx); p != nil && p.Kind == access.KindUser && (rid == 0 || !p.IsAdmin()) {
				rid = p.ReaderID // your own shelf (only admins look at others')
			}
			if rid == 0 {
				// (without readers there's no shelf; don't create one)
				if n, err := s.app.DB.NewSelect().Model((*model.Reader)(nil)).Count(ctx); err != nil || n == 0 {
					return &struct{ Body Shelf }{Shelf{Items: []ShelfItem{}}}, toHTTPError(err)
				}
				var err error
				if rid, err = s.app.Reading.ReaderID(ctx); err != nil {
					return nil, toHTTPError(err)
				}
			}
			var r model.Reader
			if err := s.app.DB.NewSelect().Model(&r).Where("id = ?", rid).Scan(ctx); err != nil {
				return nil, huma.Error404NotFound("reader not found")
			}
			next, err := s.app.Reading.OnDeck(ctx, rid)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := Shelf{ReaderID: rid, Reader: r.Name, Items: []ShelfItem{}}
			for _, n := range next[:min(in.Limit, len(next))] {
				ser, ch := n.Series.Series, n.Book.Chapter
				it := ShelfItem{SeriesID: ser.ID, Title: ser.Title,
					CoverURL: seriesCoverURL(ser),
					Next:     NextChapter{ChapterID: ch.ID, Number: ch.NumberKey, Title: ch.Title, Available: n.Book.File != nil},
					Read:     n.Series.Read, Total: n.Series.Books, LastReadAt: n.Series.LastRead}
				if n.Book.State != nil && !n.Book.State.Completed {
					it.Page = n.Book.State.Page
				}
				out.Items = append(out.Items, it)
			}
			return &struct{ Body Shelf }{out}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "reading-status", Method: http.MethodGet, Path: "/api/v1/reading/status", Tags: tags,
		Summary: "Whether the Komga-compatible API is on, and the address apps should use"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body ReadingStatus }, error) {
			rs, _ := s.app.Settings.Reading(ctx)
			return &struct{ Body ReadingStatus }{ReadingStatus{Status: s.app.Komga.Status(ctx), PublicURL: rs.PublicURL}}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "reading-keys", Method: http.MethodGet, Path: "/api/v1/reading/keys", Tags: tags,
		Summary: "Your reading apps' keys (one per device); admins can list everyone's"},
		func(ctx context.Context, in *struct {
			All bool `query:"all" doc:"Everyone's keys (admins)"`
		}) (*struct{ Body []ReadingKeyView }, error) {
			p := access.From(ctx)
			var keys []model.ReadingKey
			q := s.app.DB.NewSelect().Model(&keys).Order("id")
			if !(in.All && p.IsAdmin()) {
				q = q.Where("COALESCE(user_id, 0) = ?", p.UserID)
			}
			if err := q.Scan(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			var users []model.User
			_ = s.app.DB.NewSelect().Model(&users).Column("id", "username", "display_name").Scan(ctx)
			names := map[int64]string{}
			for _, u := range users {
				names[u.ID] = u.Username
				if u.DisplayName != "" {
					names[u.ID] = u.DisplayName
				}
			}
			out := make([]ReadingKeyView, len(keys))
			for i, k := range keys {
				out[i] = ReadingKeyView{ReadingKey: k, User: names[k.UserID]}
			}
			return &struct{ Body []ReadingKeyView }{out}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "reading-keys-create", Method: http.MethodPost, Path: "/api/v1/reading/keys", Tags: tags,
		Summary: "Create a key for one of your reading apps; the key is only shown in this response"},
		func(ctx context.Context, in *struct {
			Body struct {
				Comment string `json:"comment" doc:"Device name, e.g. \"Mihon phone\""`
			}
		}) (*struct{ Body NewReadingKey }, error) {
			key, rk, err := s.app.Komga.CreateKey(ctx, access.From(ctx).UserID, in.Body.Comment, "")
			if err != nil {
				return nil, toHTTPError(err)
			}
			s.app.Bus.Changed("reading", "key-created", rk.ID)
			return &struct{ Body NewReadingKey }{NewReadingKey{ReadingKey: *rk, Key: key}}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "reading-keys-update", Method: http.MethodPut, Path: "/api/v1/reading/keys/{id}", Tags: tags,
		Summary: "Set a device's language order: a title in several languages is then shown to it once (yours; admins any)"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Languages []string `json:"languages" doc:"Language codes, preferred first; empty lists every language edition on its own"`
			}
		}) (*struct{ Body model.ReadingKey }, error) {
			p := access.From(ctx)
			languages := []string{}
			for _, l := range in.Body.Languages {
				if l = library.NormalizeLanguage(l); l != "" && !slices.Contains(languages, l) {
					languages = append(languages, l)
				}
			}
			var rk model.ReadingKey
			q := s.app.DB.NewSelect().Model(&rk).Where("id = ?", in.ID)
			if !p.IsAdmin() {
				q = q.Where("COALESCE(user_id, 0) = ?", p.UserID)
			}
			if err := q.Scan(ctx); err != nil {
				return nil, huma.Error404NotFound("no such key")
			}
			rk.Languages = languages
			if _, err := s.app.DB.NewUpdate().Model(&rk).Column("languages").WherePK().Exec(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			s.app.Komga.InvalidateKeys()
			s.app.Bus.Changed("reading", "key-updated", in.ID)
			return &struct{ Body model.ReadingKey }{rk}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "reading-keys-delete", Method: http.MethodDelete, Path: "/api/v1/reading/keys/{id}", Tags: tags,
		Summary: "Revoke a device key (yours; admins any)"},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			p := access.From(ctx)
			q := s.app.DB.NewDelete().Model((*model.ReadingKey)(nil)).Where("id = ?", in.ID)
			if !p.IsAdmin() {
				q = q.Where("COALESCE(user_id, 0) = ?", p.UserID)
			}
			res, err := q.Exec(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, huma.Error404NotFound("no such key")
			}
			s.app.Komga.InvalidateKeys()
			s.app.Bus.Changed("reading", "key-deleted", in.ID)
			return nil, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "reading-mihon-backup", Method: http.MethodPost, Path: "/api/v1/reading/mihon-backup", Tags: tags,
		Summary: "Download the caller's visible library and progress as a Mihon backup with mangarr's Komga address and a new revocable device key"},
		func(ctx context.Context, in *struct {
			Body struct {
				Address string `json:"address" doc:"Public HTTP(S) address of mangarr's Komga-compatible API"`
			}
		}) (*mihonBackupOutput, error) {
			settings, err := s.app.Settings.Reading(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			if !settings.Enabled {
				return nil, huma.Error409Conflict("reading apps are turned off")
			}
			address := strings.TrimRight(strings.TrimSpace(in.Body.Address), "/")
			u, err := url.Parse(address)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return nil, huma.Error422UnprocessableEntity("address must be an HTTP(S) server address without credentials, query or fragment")
			}
			principal := access.From(ctx)
			readerID := principal.ReaderID
			if readerID == 0 {
				readerID, err = s.app.Reading.ReaderID(ctx)
				if err != nil {
					return nil, toHTTPError(err)
				}
			}
			comment := "Mihon backup " + time.Now().Format("2006-01-02")
			key, device, err := s.app.Komga.CreateKey(ctx, principal.UserID, comment, "Mihon backup export")
			if err != nil {
				return nil, toHTTPError(err)
			}
			data, err := s.app.Reading.MihonBackup(ctx, readerID, address, key)
			if err != nil {
				_, _ = s.app.DB.NewDelete().Model(device).WherePK().Exec(ctx)
				s.app.Komga.InvalidateKeys()
				return nil, toHTTPError(err)
			}
			s.app.Bus.Changed("reading", "key-created", device.ID)
			name := "mangarr-mihon-" + time.Now().Format("2006-01-02_15-04") + ".tachibk"
			return &mihonBackupOutput{ContentType: "application/octet-stream", ContentDisposition: `attachment; filename="` + name + `"`,
				CacheControl: "no-store", Body: data}, nil
		})
}
