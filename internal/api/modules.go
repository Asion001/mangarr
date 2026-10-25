package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
)

type ImplementationResource struct {
	Kind        string          `json:"kind"`
	Name        string          `json:"name"`
	DisplayName string          `json:"displayName"`
	Description string          `json:"description"`
	InfoURL     string          `json:"infoUrl,omitempty"`
	Fields      []modules.Field `json:"fields"`
	// Events lists subscribable events (notify kind only).
	Events []string `json:"events,omitempty"`
	// AccountFields lists per-reader credentials (library kind with progress support).
	AccountFields []modules.Field `json:"accountFields,omitempty"`
}

type ModuleResource struct {
	model.ProviderDefinition
	Error        string   `json:"error,omitempty"`
	Capabilities []string `json:"capabilities"`
	// EnvLock lists fields pinned by environment variables (managed instances only).
	EnvLock *modules.EnvLock `json:"envLock,omitempty"`
}

type ModuleInput struct {
	Kind           string         `json:"kind" enum:"source,metadata,library,notify,upscale,mediaserver"`
	Implementation string         `json:"implementation"`
	Name           string         `json:"name"`
	Enabled        bool           `json:"enabled"`
	Priority       int            `json:"priority"`
	Tags           []int64        `json:"tags,omitempty"`
	Events         []string       `json:"events,omitempty"`
	Settings       map[string]any `json:"settings"`
}

func (in ModuleInput) def(id int64) model.ProviderDefinition {
	return model.ProviderDefinition{ID: id, Kind: in.Kind, Implementation: in.Implementation, Name: in.Name,
		Enabled: in.Enabled, Priority: in.Priority, Tags: in.Tags, Events: in.Events, Settings: in.Settings}
}

// capabilitiesOf lists optional interfaces an instance implements.
var capabilityProbes []func(inst modules.Instance) string

func capabilitiesOf(inst modules.Instance) []string {
	out := []string{}
	if inst == nil {
		return out
	}
	for _, p := range capabilityProbes {
		if c := p(inst); c != "" {
			out = append(out, c)
		}
	}
	return out
}

func (s *Server) toModuleResource(l *modules.Loaded) ModuleResource {
	res := ModuleResource{ProviderDefinition: l.Def, Capabilities: capabilitiesOf(l.Raw), EnvLock: s.app.Modules.EnvLockFor(l.Def)}
	if l.Impl != nil {
		res.Settings = modules.MaskSecrets(l.Impl, l.Def.Settings)
	}
	if l.Err != nil {
		res.Error = l.Err.Error()
	}
	return res
}

// accountFieldsOf is set by the library feature to describe reader credentials.
var accountFieldsOf func(impl *modules.Implementation) []modules.Field

func (s *Server) registerModules() {
	tags := []string{"Modules"}
	huma.Register(s.api, huma.Operation{OperationID: "modules-schema", Method: http.MethodGet, Path: "/api/v1/modules/schema", Tags: tags,
		Summary: "List available implementations and their settings fields"},
		func(ctx context.Context, in *struct {
			Kind string `query:"kind"`
		}) (*struct{ Body []ImplementationResource }, error) {
			out := []ImplementationResource{}
			for _, k := range modules.Kinds {
				if in.Kind != "" && string(k) != in.Kind {
					continue
				}
				for _, impl := range modules.Implementations(k) {
					r := ImplementationResource{Kind: string(k), Name: impl.Name, DisplayName: impl.DisplayName,
						Description: impl.Description, InfoURL: impl.InfoURL, Fields: modules.FieldsOf(impl.Settings())}
					if k == modules.KindNotify {
						r.Events = events.NotificationEvents
					}
					if accountFieldsOf != nil {
						r.AccountFields = accountFieldsOf(impl)
					}
					out = append(out, r)
				}
			}
			return &struct{ Body []ImplementationResource }{out}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "modules-list", Method: http.MethodGet, Path: "/api/v1/modules", Tags: tags},
		func(ctx context.Context, in *struct {
			Kind string `query:"kind"`
		}) (*struct{ Body []ModuleResource }, error) {
			out := []ModuleResource{}
			for _, l := range s.app.Modules.All(modules.Kind(in.Kind)) {
				if l.Def.UserID != nil {
					continue // users' own targets are under their account
				}
				out = append(out, s.toModuleResource(l))
			}
			return &struct{ Body []ModuleResource }{out}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "modules-create", Method: http.MethodPost, Path: "/api/v1/modules", Tags: tags},
		func(ctx context.Context, in *struct{ Body ModuleInput }) (*struct{ Body ModuleResource }, error) {
			def := in.Body.def(0)
			if err := s.app.Modules.Create(ctx, &def); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			l, _ := s.app.Modules.Get(def.ID)
			return &struct{ Body ModuleResource }{s.toModuleResource(l)}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "modules-update", Method: http.MethodPut, Path: "/api/v1/modules/{id}", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body ModuleInput
		}) (*struct{ Body ModuleResource }, error) {
			def := in.Body.def(in.ID)
			if err := s.app.Modules.Update(ctx, &def); err != nil {
				if errors.Is(err, modules.ErrNotFound) {
					return nil, huma.Error404NotFound("module not found")
				}
				return nil, huma.Error400BadRequest(err.Error())
			}
			l, _ := s.app.Modules.Get(def.ID)
			return &struct{ Body ModuleResource }{s.toModuleResource(l)}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "modules-delete", Method: http.MethodDelete, Path: "/api/v1/modules/{id}", Tags: tags},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			if inUse, err := s.moduleInUse(ctx, in.ID); err != nil {
				return nil, toHTTPError(err)
			} else if inUse != "" {
				return nil, huma.Error409Conflict(inUse)
			}
			if err := s.app.Modules.Delete(ctx, in.ID); err != nil {
				if errors.Is(err, modules.ErrNotFound) {
					return nil, huma.Error404NotFound("module not found")
				}
				if errors.Is(err, modules.ErrManaged) {
					return nil, huma.Error409Conflict(err.Error())
				}
				return nil, toHTTPError(err)
			}
			return nil, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "modules-test", Method: http.MethodPost, Path: "/api/v1/modules/test", Tags: tags,
		Summary: "Test a module definition without saving it"},
		func(ctx context.Context, in *struct {
			Body struct {
				ID int64 `json:"id,omitempty"`
				ModuleInput
			}
		}) (*struct{}, error) {
			if err := s.app.Modules.TestDefinition(ctx, in.Body.def(in.Body.ID)); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			return nil, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "modules-test-saved", Method: http.MethodPost, Path: "/api/v1/modules/{id}/test", Tags: tags},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			l, ok := s.app.Modules.Get(in.ID)
			if !ok {
				return nil, huma.Error404NotFound("module not found")
			}
			if l.Err != nil {
				return nil, huma.Error400BadRequest(l.Err.Error())
			}
			if err := l.Instance.Test(ctx); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			return nil, nil
		})
}

// moduleInUse reports why a module can't be deleted (series linked, library accounts).
func (s *Server) moduleInUse(ctx context.Context, id int64) (string, error) {
	n, err := s.app.DB.NewSelect().Model((*model.SeriesSource)(nil)).Where("module_id = ?", id).Count(ctx)
	if err != nil {
		return "", err
	}
	if n > 0 {
		return "module is linked to series sources; reassign or remove them first", nil
	}
	return "", nil
}
