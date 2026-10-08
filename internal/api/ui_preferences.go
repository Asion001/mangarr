package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/danielgtaylor/huma/v2"
)

func init() { register((*Server).registerUIPreferences) }

func canEditUI(p *access.Principal) bool {
	return p != nil && (p.IsAdmin() || p.Can(access.LibraryAdd) || p.Can(access.LibraryEdit) || p.Can(access.LibraryDelete) || p.Can(access.QueueManage) || p.Can(access.RequestsManage))
}

func (s *Server) registerUIPreferences() {
	type output struct{ Body model.UIPreferences }
	huma.Register(s.api, huma.Operation{OperationID: "me-ui-preferences", Method: http.MethodGet, Path: "/api/v1/me/ui-preferences", Tags: []string{"Account"}},
		func(ctx context.Context, _ *struct{}) (*output, error) {
			p := access.From(ctx)
			v := model.UIPreferences{Locale: "auto", Mode: "reading"}
			// someone who never chose gets the instance's default language
			if look, err := s.app.Settings.Appearance(ctx); err == nil && look.Locale != "" {
				v.Locale = look.Locale
			}
			if p.Kind == access.KindUser {
				err := s.app.DB.NewSelect().Model(&v).Where("user_id = ?", p.UserID).Scan(ctx)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return nil, toHTTPError(err)
				}
			}
			if !canEditUI(p) {
				v.Mode = "reading"
			}
			return &output{v}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-ui-preferences-save", Method: http.MethodPut, Path: "/api/v1/me/ui-preferences", Tags: []string{"Account"}},
		func(ctx context.Context, in *struct {
			Body struct {
				Locale string `json:"locale" enum:"auto,en,ru,uk,ua" required:"true"`
				Mode   string `json:"mode" enum:"reading,editing" required:"true"`
				// Options replaces the stored options; left out, they are kept.
				Options *model.UIOptions `json:"options,omitempty"`
			}
		}) (*output, error) {
			p := access.From(ctx)
			if p.Kind != access.KindUser {
				return nil, huma.Error400BadRequest("Accountless UI preferences are stored in this browser")
			}
			var old model.UIPreferences
			if err := s.app.DB.NewSelect().Model(&old).Where("user_id = ?", p.UserID).Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, toHTTPError(err)
			}
			v := model.UIPreferences{UserID: p.UserID, Locale: in.Body.Locale, Mode: in.Body.Mode, Options: old.Options, UpdatedAt: time.Now().UTC()}
			if in.Body.Options != nil {
				v.Options = *in.Body.Options
			}
			if v.Locale == "ua" {
				v.Locale = "uk"
			}
			if !canEditUI(p) {
				v.Mode = "reading"
			}
			_, err := s.app.DB.NewInsert().Model(&v).On("CONFLICT (user_id) DO UPDATE").
				Set("locale = EXCLUDED.locale").Set("mode = EXCLUDED.mode").Set("options = EXCLUDED.options").Set("updated_at = EXCLUDED.updated_at").Exec(ctx)
			return &output{v}, toHTTPError(err)
		})
}
