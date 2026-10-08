package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/model"
)

func init() { register((*Server).registerUsers) }

// UserView is a user for the admin pages.
type UserView struct {
	model.User
	Group      string `json:"group"`
	ReaderName string `json:"readerName"`
	Sessions   int    `json:"sessions"`
	Devices    int    `json:"devices"`
}

// GroupView is a group with its member count.
type GroupView struct {
	model.Group
	Members int `json:"members"`
}

// InviteView is an invite for the admin pages.
type InviteView struct {
	model.Invite
	Group string `json:"group"`
	// Active: it can still be used.
	Active bool `json:"active"`
}

// NewInvite is an invite as created: Token is only returned this once.
type NewInvite struct {
	InviteView
	Token string `json:"token"`
}

// InviteInfo is what the invite page shows before signing up.
type InviteInfo struct {
	Group string `json:"group"`
	Note  string `json:"note,omitempty"`
	// Instance is the server's name.
	Instance  string     `json:"instance"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

type groupInput struct {
	Name                string   `json:"name" minLength:"1"`
	Permissions         []string `json:"permissions,omitempty"`
	IncludeTags         []int64  `json:"includeTags,omitempty" doc:"Only series with any of these tags (empty = all)"`
	ExcludeTags         []int64  `json:"excludeTags,omitempty" doc:"Never series with these tags"`
	RootFolders         []int64  `json:"rootFolders,omitempty" doc:"Only series in these root folders (empty = all)"`
	AutoApproveRequests bool     `json:"autoApproveRequests,omitempty"`
	MaxRating           string   `json:"maxRating,omitempty" enum:",all,teen,mature" doc:"Highest content rating members see (empty: no limit)"`
	BlockedGenres       []string `json:"blockedGenres,omitempty" doc:"Genres or tags members never see"`
}

func (g groupInput) validate() error {
	for _, p := range g.Permissions {
		if !access.Valid(p) {
			return errors.New("unknown permission " + p)
		}
	}
	return nil
}

func nonNilIDs(v []int64) []int64 {
	if v == nil {
		return []int64{}
	}
	return v
}

func (s *Server) registerUsers() {
	tags := []string{"Users"}
	db := s.app.DB

	huma.Register(s.api, huma.Operation{OperationID: "permissions-list", Method: http.MethodGet, Path: "/api/v1/permissions", Tags: tags,
		Summary: "The permissions groups can have"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []access.Permission }, error) {
			return &struct{ Body []access.Permission }{access.All}, nil
		})

	// users
	huma.Register(s.api, huma.Operation{OperationID: "users-list", Method: http.MethodGet, Path: "/api/v1/users", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []UserView }, error) {
			var users []model.User
			if err := db.NewSelect().Model(&users).Order("username").Scan(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			var groups []model.Group
			_ = db.NewSelect().Model(&groups).Scan(ctx)
			var readers []model.Reader
			_ = db.NewSelect().Model(&readers).Scan(ctx)
			gname, rname := map[int64]string{}, map[int64]string{}
			for _, g := range groups {
				gname[g.ID] = g.Name
			}
			for _, r := range readers {
				rname[r.ID] = r.Name
			}
			out := make([]UserView, len(users))
			for i, u := range users {
				out[i] = UserView{User: u, Group: gname[u.GroupID], ReaderName: rname[u.ReaderID]}
				out[i].Sessions, _ = db.NewSelect().Model((*model.Session)(nil)).Where("user_id = ? AND expires_at > ?", u.ID, time.Now().UTC()).Count(ctx)
				out[i].Devices, _ = db.NewSelect().Model((*model.ReadingKey)(nil)).Where("user_id = ?", u.ID).Count(ctx)
			}
			return &struct{ Body []UserView }{out}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "users-create", Method: http.MethodPost, Path: "/api/v1/users", Tags: tags,
		Summary: "Create a user (with their own reader for progress)"},
		func(ctx context.Context, in *struct {
			Body struct {
				Username    string `json:"username" minLength:"1"`
				Password    string `json:"password" minLength:"8"`
				DisplayName string `json:"displayName,omitempty"`
				GroupID     int64  `json:"groupId,omitempty" doc:"0 = the Users group"`
			}
		}) (*struct{ Body model.User }, error) {
			u, err := s.app.Auth.CreateUser(ctx, auth.NewUser{Username: in.Body.Username, Password: in.Body.Password,
				DisplayName: in.Body.DisplayName, GroupID: in.Body.GroupID, CreatedBy: access.From(ctx).UserID})
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			s.app.Bus.Changed("users", "created", u.ID)
			return &struct{ Body model.User }{*u}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "users-update", Method: http.MethodPut, Path: "/api/v1/users/{id}", Tags: tags,
		Summary: "Change a user's name, group, reader, or disable them"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				DisplayName string `json:"displayName,omitempty"`
				GroupID     int64  `json:"groupId"`
				ReaderID    int64  `json:"readerId,omitempty" doc:"The reader holding their progress"`
				Disabled    bool   `json:"disabled,omitempty"`
			}
		}) (*struct{}, error) {
			var u model.User
			if err := db.NewSelect().Model(&u).Where("id = ?", in.ID).Scan(ctx); err != nil {
				return nil, huma.Error404NotFound("no such user")
			}
			var g model.Group
			if err := db.NewSelect().Model(&g).Where("id = ?", in.Body.GroupID).Scan(ctx); err != nil {
				return nil, huma.Error400BadRequest("no such group")
			}
			if in.Body.ReaderID > 0 {
				if n, _ := db.NewSelect().Model((*model.Reader)(nil)).Where("id = ?", in.Body.ReaderID).Count(ctx); n == 0 {
					return nil, huma.Error400BadRequest("no such reader")
				}
				if n, _ := db.NewSelect().Model((*model.User)(nil)).Where("reader_id = ? AND id <> ?", in.Body.ReaderID, u.ID).Count(ctx); n > 0 {
					return nil, huma.Error400BadRequest("another user already has that reader")
				}
			}
			me := access.From(ctx)
			if in.Body.Disabled && u.ID == me.UserID {
				return nil, huma.Error400BadRequest("you can't disable yourself")
			}
			if err := s.keepAnAdmin(ctx, &u, g.Builtin == model.GroupAdmins && !in.Body.Disabled); err != nil {
				return nil, err
			}
			u.DisplayName, u.GroupID, u.Disabled = strings.TrimSpace(in.Body.DisplayName), g.ID, in.Body.Disabled
			if in.Body.ReaderID > 0 {
				u.ReaderID = in.Body.ReaderID
			}
			if _, err := db.NewUpdate().Model(&u).Column("display_name", "group_id", "disabled", "reader_id").WherePK().Exec(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			if u.Disabled {
				_ = s.app.Auth.RevokeSessions(ctx, u.ID, "")
			}
			s.app.Auth.Invalidate()
			s.app.Bus.Changed("users", "updated", u.ID)
			return nil, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "users-password", Method: http.MethodPost, Path: "/api/v1/users/{id}/password", Tags: tags,
		Summary: "Set a user's password (signs them out everywhere)"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Password string `json:"password" minLength:"8"`
			}
		}) (*struct{}, error) {
			if err := s.app.Auth.SetPassword(ctx, in.ID, in.Body.Password, access.From(ctx).SessionID); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			return nil, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "users-signout", Method: http.MethodPost, Path: "/api/v1/users/{id}/signout", Tags: tags,
		Summary: "Sign a user out of every web session"},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			keep := ""
			if me := access.From(ctx); me.UserID == in.ID {
				keep = me.SessionID
			}
			return nil, toHTTPError(s.app.Auth.RevokeSessions(ctx, in.ID, keep))
		})
	huma.Register(s.api, huma.Operation{OperationID: "users-delete", Method: http.MethodDelete, Path: "/api/v1/users/{id}", Tags: tags,
		Summary: "Delete a user (their reader and its progress stay)"},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			var u model.User
			if err := db.NewSelect().Model(&u).Where("id = ?", in.ID).Scan(ctx); err != nil {
				return nil, huma.Error404NotFound("no such user")
			}
			if u.ID == access.From(ctx).UserID {
				return nil, huma.Error400BadRequest("you can't delete yourself")
			}
			if err := s.keepAnAdmin(ctx, &u, false); err != nil {
				return nil, err
			}
			if _, err := db.NewDelete().Model(&u).WherePK().Exec(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			s.app.Auth.Invalidate()
			s.app.Komga.InvalidateKeys()
			s.app.Bus.Changed("users", "deleted", u.ID)
			return nil, nil
		})

	// groups
	huma.Register(s.api, huma.Operation{OperationID: "groups-list", Method: http.MethodGet, Path: "/api/v1/groups", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []GroupView }, error) {
			var groups []model.Group
			if err := db.NewSelect().Model(&groups).Order("id").Scan(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			out := make([]GroupView, len(groups))
			for i, g := range groups {
				out[i] = GroupView{Group: g}
				out[i].Members, _ = db.NewSelect().Model((*model.User)(nil)).Where("group_id = ?", g.ID).Count(ctx)
			}
			return &struct{ Body []GroupView }{out}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "groups-create", Method: http.MethodPost, Path: "/api/v1/groups", Tags: tags},
		func(ctx context.Context, in *struct{ Body groupInput }) (*struct{ Body model.Group }, error) {
			if err := in.Body.validate(); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			g := model.Group{Name: strings.TrimSpace(in.Body.Name), Permissions: in.Body.Permissions, IncludeTags: nonNilIDs(in.Body.IncludeTags),
				ExcludeTags: nonNilIDs(in.Body.ExcludeTags), RootFolders: nonNilIDs(in.Body.RootFolders), AutoApproveRequests: in.Body.AutoApproveRequests,
				MaxRating: in.Body.MaxRating, BlockedGenres: cleanGenres(in.Body.BlockedGenres), CreatedAt: time.Now().UTC()}
			if g.Permissions == nil {
				g.Permissions = []string{}
			}
			if _, err := db.NewInsert().Model(&g).Exec(ctx); err != nil {
				return nil, huma.Error409Conflict("a group with that name exists")
			}
			s.app.Bus.Changed("users", "groups", g.ID)
			return &struct{ Body model.Group }{g}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "groups-update", Method: http.MethodPut, Path: "/api/v1/groups/{id}", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body groupInput
		}) (*struct{}, error) {
			if err := in.Body.validate(); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			var g model.Group
			if err := db.NewSelect().Model(&g).Where("id = ?", in.ID).Scan(ctx); err != nil {
				return nil, huma.Error404NotFound("no such group")
			}
			g.Name, g.Permissions, g.AutoApproveRequests = strings.TrimSpace(in.Body.Name), in.Body.Permissions, in.Body.AutoApproveRequests
			g.IncludeTags, g.ExcludeTags, g.RootFolders = nonNilIDs(in.Body.IncludeTags), nonNilIDs(in.Body.ExcludeTags), nonNilIDs(in.Body.RootFolders)
			g.MaxRating, g.BlockedGenres = in.Body.MaxRating, cleanGenres(in.Body.BlockedGenres)
			if g.Permissions == nil {
				g.Permissions = []string{}
			}
			if g.Builtin == model.GroupAdmins {
				// admins always have everything and see everything
				g.Permissions, g.IncludeTags, g.ExcludeTags, g.RootFolders = []string{access.Admin}, []int64{}, []int64{}, []int64{}
				g.MaxRating, g.BlockedGenres = "", []string{}
			}
			if _, err := db.NewUpdate().Model(&g).WherePK().Exec(ctx); err != nil {
				return nil, huma.Error409Conflict("a group with that name exists")
			}
			s.app.Auth.Invalidate()
			s.app.Bus.Changed("users", "groups", g.ID)
			return nil, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "groups-delete", Method: http.MethodDelete, Path: "/api/v1/groups/{id}", Tags: tags,
		Summary: "Delete a group (its members move to Users)"},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			var g model.Group
			if err := db.NewSelect().Model(&g).Where("id = ?", in.ID).Scan(ctx); err != nil {
				return nil, huma.Error404NotFound("no such group")
			}
			if g.Builtin != "" {
				return nil, huma.Error400BadRequest("built-in groups can't be deleted")
			}
			ids, err := s.app.Auth.EnsureGroups(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			if _, err := db.NewUpdate().Model((*model.User)(nil)).Set("group_id = ?", ids[model.GroupUsers]).Where("group_id = ?", g.ID).Exec(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			if _, err := db.NewDelete().Model(&g).WherePK().Exec(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			s.app.Auth.Invalidate()
			s.app.Bus.Changed("users", "groups", g.ID)
			return nil, nil
		})

	// invites
	huma.Register(s.api, huma.Operation{OperationID: "invites-list", Method: http.MethodGet, Path: "/api/v1/invites", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []InviteView }, error) {
			var list []model.Invite
			if err := db.NewSelect().Model(&list).OrderExpr("id DESC").Scan(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			out := make([]InviteView, len(list))
			for i, inv := range list {
				out[i] = s.inviteView(ctx, inv)
			}
			return &struct{ Body []InviteView }{out}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "invites-create", Method: http.MethodPost, Path: "/api/v1/invites", Tags: tags,
		Summary: "Create an invite link; the token is only returned now"},
		func(ctx context.Context, in *struct {
			Body struct {
				GroupID    int64  `json:"groupId" doc:"0 = the Users group"`
				Note       string `json:"note,omitempty" doc:"Who it's for"`
				MaxUses    int    `json:"maxUses,omitempty" doc:"How many accounts it can create (default 1)"`
				ExpireDays int    `json:"expireDays,omitempty" doc:"Days until it expires (0 = never)"`
			}
		}) (*struct{ Body NewInvite }, error) {
			gid := in.Body.GroupID
			if gid == 0 {
				ids, err := s.app.Auth.EnsureGroups(ctx)
				if err != nil {
					return nil, toHTTPError(err)
				}
				gid = ids[model.GroupUsers]
			}
			var exp *time.Time
			if in.Body.ExpireDays > 0 {
				t := time.Now().UTC().Add(time.Duration(in.Body.ExpireDays) * 24 * time.Hour)
				exp = &t
			}
			token, inv, err := s.app.Auth.CreateInvite(ctx, gid, strings.TrimSpace(in.Body.Note), in.Body.MaxUses, exp, access.From(ctx).UserID)
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			s.app.Bus.Changed("users", "invites", inv.ID)
			return &struct{ Body NewInvite }{NewInvite{InviteView: s.inviteView(ctx, *inv), Token: token}}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "invites-delete", Method: http.MethodDelete, Path: "/api/v1/invites/{id}", Tags: tags},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			_, err := db.NewDelete().Model((*model.Invite)(nil)).Where("id = ?", in.ID).Exec(ctx)
			s.app.Bus.Changed("users", "invites", in.ID)
			return nil, toHTTPError(err)
		})

	// the invite page (no login)
	huma.Register(s.api, huma.Operation{OperationID: "invite-get", Method: http.MethodGet, Path: "/api/v1/invites/redeem/{token}", Tags: tags,
		Security: []map[string][]string{}, Summary: "What an invite link is for"},
		func(ctx context.Context, in *struct {
			Token string `path:"token"`
		}) (*struct{ Body InviteInfo }, error) {
			c := access.ClientFrom(ctx)
			if _, locked := s.app.Auth.Limiter.Locked("ip:" + c.IP); locked {
				return nil, huma.Error429TooManyRequests("too many attempts; try again later")
			}
			inv, err := s.app.Auth.Invite(ctx, in.Token)
			if err != nil {
				s.app.Auth.Limiter.Fail("ip:" + c.IP)
				return nil, huma.Error404NotFound(err.Error())
			}
			g, _ := s.app.Settings.General(ctx)
			v := s.inviteView(ctx, *inv)
			return &struct{ Body InviteInfo }{InviteInfo{Group: v.Group, Note: inv.Note, Instance: g.InstanceName, ExpiresAt: inv.ExpiresAt}}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "invite-redeem", Method: http.MethodPost, Path: "/api/v1/invites/redeem/{token}", Tags: tags,
		Security: []map[string][]string{}, Summary: "Create your account with an invite link (signs you in)"},
		func(ctx context.Context, in *struct {
			Token string `path:"token"`
			Body  struct {
				Username    string `json:"username" minLength:"1"`
				Password    string `json:"password" minLength:"8"`
				DisplayName string `json:"displayName,omitempty"`
			}
		}) (*loginOutput, error) {
			c := access.ClientFrom(ctx)
			if _, locked := s.app.Auth.Limiter.Locked("ip:" + c.IP); locked {
				return nil, huma.Error429TooManyRequests("too many attempts; try again later")
			}
			if cfg, err := s.app.Settings.SSO(ctx); err == nil && cfg.Enabled && !cfg.PasswordLogin {
				return nil, huma.Error400BadRequest("redeem the invite with single sign-on")
			}
			u, err := s.app.Auth.Redeem(ctx, in.Token, auth.NewUser{Username: in.Body.Username, Password: in.Body.Password, DisplayName: in.Body.DisplayName})
			switch {
			case errors.Is(err, auth.ErrInviteInvalid):
				s.app.Auth.Limiter.Fail("ip:" + c.IP)
				return nil, huma.Error404NotFound(err.Error())
			case err != nil:
				return nil, huma.Error400BadRequest(err.Error())
			}
			s.app.Log.Info("account created with an invite", "username", u.Username, "ip", c.IP)
			s.app.Bus.Changed("users", "created", u.ID)
			return s.startSession(ctx, u.ID)
		})
}

func (s *Server) inviteView(ctx context.Context, inv model.Invite) InviteView {
	v := InviteView{Invite: inv, Active: inv.Uses < inv.MaxUses && (inv.ExpiresAt == nil || time.Now().Before(*inv.ExpiresAt))}
	var g model.Group
	if err := s.app.DB.NewSelect().Model(&g).Where("id = ?", inv.GroupID).Scan(ctx); err == nil {
		v.Group = g.Name
	}
	return v
}

// keepAnAdmin refuses changes that would leave no enabled administrator
// (stillAdmin: u remains an enabled admin after the change).
func (s *Server) keepAnAdmin(ctx context.Context, u *model.User, stillAdmin bool) error {
	if stillAdmin || u.Disabled {
		return nil
	}
	var g model.Group
	if err := s.app.DB.NewSelect().Model(&g).Where("id = ?", u.GroupID).Scan(ctx); err != nil || g.Builtin != model.GroupAdmins {
		return nil // wasn't an admin
	}
	n, err := s.app.Auth.Admins(ctx, nil)
	if err != nil {
		return toHTTPError(err)
	}
	if n <= 1 {
		return huma.Error400BadRequest("keep at least one administrator")
	}
	return nil
}

// cleanGenres trims and de-duplicates hidden genres (never nil).
func cleanGenres(in []string) []string {
	out := []string{}
	for _, g := range in {
		g = strings.TrimSpace(g)
		if g != "" && !slices.ContainsFunc(out, func(x string) bool { return strings.EqualFold(x, g) }) {
			out = append(out, g)
		}
	}
	return out
}
