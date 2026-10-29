package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/cleanup"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/library"
	"github.com/Asion001/mangarr/internal/reading"
)

func init() {
	register((*Server).registerProgressSync)
	register((*Server).registerCleanup)
	accountFieldsOf = func(impl *modules.Implementation) []modules.Field {
		if impl.Kind != modules.KindLibrary {
			return nil
		}
		// build a throwaway instance with defaults to ask for its account fields
		inst, err := impl.New(modules.Deps{}, impl.Settings())
		if err != nil {
			return nil
		}
		if pr, ok := inst.(library.ProgressReader); ok {
			return pr.AccountFields()
		}
		return nil
	}
}

// ProgressSync is an account's sync health: every app, device and server
// that reported its progress, the latest reports, and its device keys.
type ProgressSync struct {
	reading.SyncHealth
	Keys []model.ReadingKey `json:"keys"`
}

type AccountInput struct {
	ModuleID    int64             `json:"moduleId"`
	Credentials map[string]string `json:"credentials"`
}

func (s *Server) registerProgressSync() {
	huma.Register(s.api, huma.Operation{OperationID: "me-sync", Method: http.MethodGet, Path: "/api/v1/me/sync", Tags: []string{"Account"},
		Summary: "Sync health: apps, devices and servers that reported your progress, and recent reports"},
		func(ctx context.Context, in *struct {
			Limit int `query:"limit" default:"50" minimum:"1" maximum:"500"`
		}) (*struct{ Body ProgressSync }, error) {
			uid, err := myUserID(ctx)
			if err != nil {
				return nil, err
			}
			return s.progressSync(ctx, uid, in.Limit)
		})
	huma.Register(s.api, huma.Operation{OperationID: "users-sync", Method: http.MethodGet, Path: "/api/v1/users/{id}/sync", Tags: []string{"Users"},
		Summary: "Sync health of a user's progress"},
		func(ctx context.Context, in *struct {
			ID    int64 `path:"id"`
			Limit int   `query:"limit" default:"50" minimum:"1" maximum:"500"`
		}) (*struct{ Body ProgressSync }, error) {
			return s.progressSync(ctx, in.ID, in.Limit)
		})

}

func (s *Server) registerCleanup() {
	ctags := []string{"Cleanup"}
	huma.Register(s.api, huma.Operation{OperationID: "cleanup-preview", Method: http.MethodGet, Path: "/api/v1/cleanup/preview", Tags: ctags,
		Summary: "What a cleanup run would delete with the current settings"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body *cleanup.Plan }, error) {
			p, err := s.app.Cleaner.Plan(ctx)
			return &struct{ Body *cleanup.Plan }{p}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "cleanup-run", Method: http.MethodPost, Path: "/api/v1/cleanup/run", Tags: ctags,
		Summary: "Queue a cleanup. force=true deletes even when dry run is enabled."},
		func(ctx context.Context, in *struct {
			Force bool `query:"force"`
		}) (*struct{ Body *model.Command }, error) {
			c, err := s.app.Queue.Push(ctx, "Cleanup", map[string]any{"force": in.Force}, "manual")
			return &struct{ Body *model.Command }{c}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "chapter-restore", Method: http.MethodPost, Path: "/api/v1/chapters/{id}/restore", Tags: ctags,
		Summary: "Make a cleaned chapter wanted again and download it"},
		func(ctx context.Context, in *IDPath) (*struct{ Body *model.Chapter }, error) {
			ch, err := s.app.Cleaner.Restore(ctx, in.ID)
			if err != nil {
				return nil, huma.Error404NotFound(err.Error())
			}
			_, _ = s.app.Queue.Push(ctx, "SearchMissing", map[string]any{"seriesId": ch.SeriesID, "chapterIds": []int64{ch.ID}, "explicit": true}, "restore")
			return &struct{ Body *model.Chapter }{ch}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "chapters-delete", Method: http.MethodPost, Path: "/api/v1/chapters/delete", Tags: ctags,
		Summary: "Delete downloaded chapter files and mark them cleaned"},
		func(ctx context.Context, in *struct {
			Body struct {
				ChapterIDs []int64 `json:"chapterIds" minItems:"1" maxItems:"20000"`
			}
		}) (*struct{ Body *cleanup.RemoveResult }, error) {
			if len(in.Body.ChapterIDs) == 0 {
				return nil, huma.Error422UnprocessableEntity("choose at least one chapter")
			}
			result, err := s.app.Cleaner.RemoveChapters(ctx, in.Body.ChapterIDs)
			return &struct{ Body *cleanup.RemoveResult }{result}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-files-delete", Method: http.MethodPost, Path: "/api/v1/series/files/delete", Tags: ctags,
		Summary: "Delete the downloaded chapter files of whole series and mark them cleaned; the series stay"},
		func(ctx context.Context, in *struct {
			Body struct {
				SeriesIDs []int64 `json:"seriesIds" minItems:"1" maxItems:"10000"`
			}
		}) (*struct{ Body *cleanup.RemoveResult }, error) {
			result, err := s.app.Cleaner.RemoveSeriesFiles(ctx, in.Body.SeriesIDs)
			return &struct{ Body *cleanup.RemoveResult }{result}, toHTTPError(err)
		})
}

func (s *Server) progressSync(ctx context.Context, userID int64, limit int) (*struct{ Body ProgressSync }, error) {
	h, err := s.app.Reading.SyncHealth(ctx, userID, limit)
	if err != nil {
		return nil, toHTTPError(err)
	}
	out := ProgressSync{SyncHealth: h, Keys: []model.ReadingKey{}}
	q := s.app.DB.NewSelect().Model(&out.Keys).Where("user_id = ?", userID)
	if def, err := s.app.Reading.DefaultUserID(ctx); err == nil && def == userID {
		q = q.WhereOr("user_id IS NULL") // keys from before accounts
	}
	if err := q.Order("created_at").Scan(ctx); err != nil {
		return nil, toHTTPError(err)
	}
	return &struct{ Body ProgressSync }{out}, nil
}

// saveLibraryAccount tests credentials on a library server and links them
// to a user (replacing their account there).
func (s *Server) saveLibraryAccount(ctx context.Context, userID int64, in AccountInput) (*model.LibraryAccount, error) {
	user, err := s.app.ReadSync.TestAccount(ctx, in.ModuleID, in.Credentials)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	acc := model.LibraryAccount{UserID: userID, ModuleID: in.ModuleID, Credentials: in.Credentials, ExternalUser: user, CreatedAt: time.Now().UTC()}
	_, err = s.app.DB.NewInsert().Model(&acc).
		On("CONFLICT (user_id, module_id) DO UPDATE").
		Set("credentials = EXCLUDED.credentials").Set("external_user = EXCLUDED.external_user").Set("last_error = ''").
		Exec(ctx)
	if err != nil {
		return nil, toHTTPError(err)
	}
	s.app.Bus.Changed("progress", "accounts", userID)
	_, _ = s.app.Queue.Push(ctx, "SyncReadProgress", nil, "account-added")
	go s.app.Watcher.Refresh(context.Background())
	return &acc, nil
}

// deleteLibraryAccount unlinks a user's account (where: which one).
func (s *Server) deleteLibraryAccount(ctx context.Context, userID int64, where string, arg any) error {
	_, err := s.app.DB.NewDelete().Model((*model.LibraryAccount)(nil)).Where("user_id = ?", userID).Where(where, arg).Exec(ctx)
	s.app.Bus.Changed("progress", "accounts", userID)
	go s.app.Watcher.Refresh(context.Background())
	return toHTTPError(err)
}
