package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

// Account is the signed-in user as the UI sees it.
type Account struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName,omitempty"`
	GroupID     int64  `json:"groupId"`
	Group       string `json:"group"`
	// Permissions this account has (every one for admins).
	Permissions []string `json:"permissions"`
	// ReaderID is the account's own progress.
	ReaderID int64 `json:"readerId"`
	// Kind: user, apikey (the admin API key) or anonymous (logins disabled).
	Kind string `json:"kind" enum:"user,apikey,anonymous"`
}

type AuthStatus struct {
	NeedsSetup    bool     `json:"needsSetup"`
	Authenticated bool     `json:"authenticated"`
	User          string   `json:"user,omitempty"`
	Account       *Account `json:"account,omitempty"`
	AuthDisabled  bool     `json:"authDisabled"`
	// SSO is set when signing in with a provider is on.
	SSO *SSOLogin `json:"sso,omitempty"`
	// PasswordLogin: passwords still work for everyone (off: administrators only).
	PasswordLogin bool `json:"passwordLogin"`
	// Appearance is the instance's look, needed before signing in.
	Appearance PublicAppearance `json:"appearance"`
}

// PublicAppearance is what the sign-in page and the UI need of the
// instance's look: its name, accent, sign-in message and user defaults.
type PublicAppearance struct {
	InstanceName string `json:"instanceName"`
	settings.Appearance
}

// SSOLogin is the login page's single sign-on button.
type SSOLogin struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

func accountOf(p *access.Principal) *Account {
	if p == nil {
		return nil
	}
	return &Account{ID: p.UserID, Username: p.Username, DisplayName: p.DisplayName, GroupID: p.GroupID, Group: p.GroupName,
		Permissions: p.Permissions(), ReaderID: p.ReaderID, Kind: p.Kind}
}

type Credentials struct {
	Username string `json:"username" minLength:"1"`
	Password string `json:"password" minLength:"1"`
}

type loginOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      AuthStatus
}

// SessionView is a web session of the current user.
type SessionView struct {
	model.Session
	ID      string `json:"id"`
	Current bool   `json:"current"`
}

func (s *Server) registerAuth() {
	tags := []string{"Auth"}
	huma.Register(s.api, huma.Operation{OperationID: "auth-status", Method: http.MethodGet, Path: "/api/v1/auth/status", Tags: tags, Security: []map[string][]string{}},
		func(ctx context.Context, _ *struct{}) (*struct{ Body AuthStatus }, error) {
			needs, err := s.app.Auth.NeedsSetup(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			p := access.From(ctx)
			st := AuthStatus{NeedsSetup: needs && !s.app.Auth.Disabled(), Authenticated: p != nil, AuthDisabled: s.app.Auth.Disabled(), Account: accountOf(p),
				PasswordLogin: true}
			if cfg, err := s.app.Settings.SSO(ctx); err == nil && cfg.Enabled && cfg.Issuer != "" && cfg.ClientID != "" {
				st.SSO = &SSOLogin{Label: cfg.ButtonLabel, URL: s.app.Cfg.URLBase + "/api/v1/auth/oidc/login"}
				if st.SSO.Label == "" {
					st.SSO.Label = "Sign in with SSO"
				}
				st.PasswordLogin = cfg.PasswordLogin
			}
			if p != nil {
				st.User = p.Username
			}
			gen, _ := s.app.Settings.General(ctx)
			look, _ := s.app.Settings.Appearance(ctx)
			st.Appearance = PublicAppearance{InstanceName: gen.InstanceName, Appearance: look}
			return &struct{ Body AuthStatus }{st}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "auth-setup", Method: http.MethodPost, Path: "/api/v1/auth/setup", Tags: tags, Security: []map[string][]string{},
		Summary: "Create the first user, an administrator (only allowed when no user exists)"},
		func(ctx context.Context, in *struct{ Body Credentials }) (*loginOutput, error) {
			needs, err := s.app.Auth.NeedsSetup(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			if !needs {
				return nil, huma.Error409Conflict("setup already completed")
			}
			u, err := s.app.Auth.CreateUser(ctx, auth.NewUser{Username: in.Body.Username, Password: in.Body.Password})
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			return s.startSession(ctx, u.ID)
		})

	huma.Register(s.api, huma.Operation{OperationID: "auth-login", Method: http.MethodPost, Path: "/api/v1/auth/login", Tags: tags, Security: []map[string][]string{}},
		func(ctx context.Context, in *struct{ Body Credentials }) (*loginOutput, error) {
			c := access.ClientFrom(ctx)
			u, cookie, err := s.app.Auth.Login(ctx, in.Body.Username, in.Body.Password, c)
			var locked *auth.LockedError
			switch {
			case errors.As(err, &locked):
				s.app.Log.Warn("login locked after repeated failures", "ip", c.IP, "username", in.Body.Username)
				return nil, huma.Error429TooManyRequests(err.Error())
			case errors.Is(err, auth.ErrDisabled):
				return nil, huma.Error403Forbidden(err.Error())
			case err != nil:
				if errors.Is(err, auth.ErrInvalidCredentials) {
					s.app.Log.Info("failed login", "ip", c.IP, "username", in.Body.Username)
				}
				return nil, huma.Error401Unauthorized(err.Error())
			}
			if err := s.passwordAllowed(ctx, u); err != nil {
				_ = s.app.Auth.EndSession(ctx, cookie.Value)
				return nil, err
			}
			return s.sessionOutput(ctx, u.ID, cookie)
		})

	huma.Register(s.api, huma.Operation{OperationID: "auth-logout", Method: http.MethodPost, Path: "/api/v1/auth/logout", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct {
			SetCookie http.Cookie `header:"Set-Cookie"`
		}, error) {
			if p := access.From(ctx); p != nil && p.SessionID != "" {
				_ = s.app.Auth.EndSession(ctx, p.SessionID)
			}
			return &struct {
				SetCookie http.Cookie `header:"Set-Cookie"`
			}{http.Cookie{Name: auth.CookieName, Value: "", Path: "/", MaxAge: -1}}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "auth-password", Method: http.MethodPost, Path: "/api/v1/auth/password", Tags: tags,
		Summary: "Change your password (other sessions are signed out)"},
		func(ctx context.Context, in *struct {
			Body struct {
				Current  string `json:"current,omitempty" doc:"Your current password"`
				Password string `json:"password" minLength:"8"`
			}
		}) (*struct{}, error) {
			p := access.From(ctx)
			if p.Kind != access.KindUser {
				return nil, huma.Error400BadRequest("log in as a user to change the password")
			}
			if _, err := s.app.Auth.Verify(ctx, p.Username, in.Body.Current); err != nil {
				return nil, huma.Error400BadRequest("the current password is wrong")
			}
			return nil, toHTTPError(s.app.Auth.SetPassword(ctx, p.UserID, in.Body.Password, p.SessionID))
		})

	huma.Register(s.api, huma.Operation{OperationID: "me-sessions", Method: http.MethodGet, Path: "/api/v1/me/sessions", Tags: tags,
		Summary: "Your web sessions"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []SessionView }, error) {
			p := access.From(ctx)
			list, err := s.app.Auth.Sessions(ctx, p.UserID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := make([]SessionView, len(list))
			for i, x := range list {
				out[i] = SessionView{Session: x, ID: sessionRef(x.ID), Current: x.ID == p.SessionID}
			}
			return &struct{ Body []SessionView }{out}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-sessions-revoke", Method: http.MethodDelete, Path: "/api/v1/me/sessions/{id}", Tags: tags,
		Summary: "Sign out one of your sessions"},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{}, error) {
			p := access.From(ctx)
			list, err := s.app.Auth.Sessions(ctx, p.UserID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			for _, x := range list {
				if sessionRef(x.ID) == in.ID {
					return nil, toHTTPError(s.app.Auth.EndSession(ctx, x.ID))
				}
			}
			return nil, huma.Error404NotFound("no such session")
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-sessions-revoke-others", Method: http.MethodPost, Path: "/api/v1/me/sessions/revoke-others", Tags: tags,
		Summary: "Sign out everywhere else"},
		func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			p := access.From(ctx)
			return nil, toHTTPError(s.app.Auth.RevokeSessions(ctx, p.UserID, p.SessionID))
		})
}

// sessionRef is how a session is referred to outside the cookie (a prefix:
// listing sessions must not hand out usable ids).
func sessionRef(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func (s *Server) startSession(ctx context.Context, userID int64) (*loginOutput, error) {
	c, err := s.app.Auth.StartSession(ctx, userID, access.ClientFrom(ctx))
	if err != nil {
		return nil, toHTTPError(err)
	}
	return s.sessionOutput(ctx, userID, c)
}

func (s *Server) sessionOutput(ctx context.Context, userID int64, c *http.Cookie) (*loginOutput, error) {
	c.Secure = access.ClientFrom(ctx).Secure
	p, err := s.app.Auth.UserPrincipal(ctx, userID)
	if err != nil {
		return nil, toHTTPError(err)
	}
	return &loginOutput{SetCookie: *c, Body: AuthStatus{Authenticated: true, User: p.Username, Account: accountOf(p)}}, nil
}

// passwordAllowed refuses password logins when single sign-on replaced
// them. Administrators keep theirs, to get back in when the provider is
// down.
func (s *Server) passwordAllowed(ctx context.Context, u *model.User) error {
	cfg, err := s.app.Settings.SSO(ctx)
	if err != nil || !cfg.Enabled || cfg.PasswordLogin {
		return nil
	}
	p, err := s.app.Auth.UserPrincipal(ctx, u.ID)
	if err != nil {
		return toHTTPError(err)
	}
	if p == nil || !p.IsAdmin() {
		return huma.Error403Forbidden("sign in with single sign-on")
	}
	return nil
}
