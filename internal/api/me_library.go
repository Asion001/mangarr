package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/library"
	"github.com/Asion001/mangarr/internal/readsync"
)

func init() { register((*Server).registerMyLibraryAccounts) }

// MyLibraryAccount is a library server you can link your own account on.
type MyLibraryAccount struct {
	ModuleID       int64           `json:"moduleId"`
	Name           string          `json:"name"`
	Implementation string          `json:"implementation"`
	Fields         []modules.Field `json:"fields"`
	// Linked is set when your account there is linked.
	Linked *LinkedAccount `json:"linked,omitempty"`
}

// LinkedAccount is your account on a library server.
type LinkedAccount struct {
	ExternalUser string     `json:"externalUser"`
	LastSyncAt   *time.Time `json:"lastSyncAt,omitempty"`
	LastError    string     `json:"lastError,omitempty"`
	// Live is the state of the server's live progress connection (Komga);
	// unset for servers checked on a timer.
	Live *readsync.WatchStatus `json:"live,omitempty"`
}

// myUserID is the signed-in user (whose progress it is).
func myUserID(ctx context.Context) (int64, error) {
	p, err := me(ctx)
	if err != nil {
		return 0, err
	}
	if p.UserID == 0 {
		return 0, huma.Error400BadRequest("sign in with an account first")
	}
	return p.UserID, nil
}

func (s *Server) registerMyLibraryAccounts() {
	tags := []string{"Account"}

	huma.Register(s.api, huma.Operation{OperationID: "me-library-accounts", Method: http.MethodGet, Path: "/api/v1/me/library-accounts", Tags: tags,
		Summary: "Library servers (Komga, Kavita) where you can link your own account to sync your progress"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []MyLibraryAccount }, error) {
			uid, err := myUserID(ctx)
			if err != nil {
				return nil, err
			}
			var accs []model.LibraryAccount
			if err := s.app.DB.NewSelect().Model(&accs).Where("user_id = ?", uid).Scan(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			out := []MyLibraryAccount{}
			live := s.app.Watcher.Status()
			for _, l := range s.app.Modules.Active(modules.KindLibrary) {
				if l.Impl == nil || accountFieldsOf == nil {
					continue
				}
				fields := accountFieldsOf(l.Impl)
				if len(fields) == 0 {
					continue // no progress sync there
				}
				a := MyLibraryAccount{ModuleID: l.Def.ID, Name: l.Def.Name, Implementation: l.Def.Implementation, Fields: fields}
				for _, acc := range accs {
					if acc.ModuleID == l.Def.ID {
						a.Linked = &LinkedAccount{ExternalUser: acc.ExternalUser, LastSyncAt: acc.LastSyncAt, LastError: acc.LastError}
						if _, _, err := modules.GetAs[library.ProgressWatcher](s.app.Modules, acc.ModuleID); err == nil {
							st := live[acc.ID]
							a.Linked.Live = &st
						}
					}
				}
				out = append(out, a)
			}
			return &struct{ Body []MyLibraryAccount }{out}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "me-library-account-save", Method: http.MethodPost, Path: "/api/v1/me/library-accounts", Tags: tags,
		Summary: "Link your account on a library server (the credentials are tested first)"},
		func(ctx context.Context, in *struct{ Body AccountInput }) (*struct{}, error) {
			uid, err := myUserID(ctx)
			if err != nil {
				return nil, err
			}
			if l, ok := s.app.Modules.Get(in.Body.ModuleID); !ok || l.Def.Kind != string(modules.KindLibrary) || !l.Def.Enabled {
				return nil, huma.Error404NotFound("library server not found")
			}
			_, err = s.saveLibraryAccount(ctx, uid, in.Body)
			return nil, err
		})

	huma.Register(s.api, huma.Operation{OperationID: "me-library-account-delete", Method: http.MethodDelete, Path: "/api/v1/me/library-accounts/{moduleId}", Tags: tags},
		func(ctx context.Context, in *struct {
			ModuleID int64 `path:"moduleId"`
		}) (*struct{}, error) {
			uid, err := myUserID(ctx)
			if err != nil {
				return nil, err
			}
			return nil, s.deleteLibraryAccount(ctx, uid, "module_id = ?", in.ModuleID)
		})
}
