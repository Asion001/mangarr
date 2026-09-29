// Package access is who a request is from and what they may do: users
// belong to a group, groups have permissions and can limit the series
// their members see (Scope).
package access

import (
	"context"
	"slices"

	"github.com/Asion001/mangarr/internal/model"
)

// Permissions. Reading the library a user can see, their own progress and
// their account need none.
const (
	// Admin allows everything: settings, modules, system, users.
	Admin = "admin"
	// LibraryManage: add, edit and delete series; queue, sources, history, wanted.
	LibraryManage = "library.manage"
	// RequestsManage: see, approve and fulfil requests.
	RequestsManage = "requests.manage"
	// RequestsCreate: ask for series.
	RequestsCreate = "requests.create"
	// Apps: the Komga-compatible API and device keys.
	Apps = "apps"
	// Download: CBZ files and offline downloads.
	Download = "download"
)

// Permission describes a permission for the UI.
type Permission struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// All lists the permissions, most powerful first.
var All = []Permission{
	{Admin, "Administrator", "Everything, including settings, modules, users and the system"},
	{LibraryManage, "Manage the library", "Add, edit and delete series; queue, sources, history and wanted"},
	{RequestsManage, "Handle requests", "See, approve and fulfil series requests"},
	{RequestsCreate, "Request series", "Ask for series to be added"},
	{Apps, "Reading apps", "Use Mihon, KMReader, Paperback… through the Komga-compatible API"},
	{Download, "Download files", "Download chapters as CBZ files, also for offline reading"},
}

// Valid reports whether p is a known permission.
func Valid(p string) bool {
	return slices.ContainsFunc(All, func(x Permission) bool { return x.Key == p })
}

// UsersDefault are the permissions of the built-in Users group.
var UsersDefault = []string{RequestsCreate, Apps, Download}

// Scope limits the series a group sees (empty fields: no limit).
type Scope struct {
	IncludeTags []int64 `json:"includeTags,omitempty"`
	ExcludeTags []int64 `json:"excludeTags,omitempty"`
	RootFolders []int64 `json:"rootFolders,omitempty"`
}

// ScopeOf is a group's scope.
func ScopeOf(g *model.Group) Scope {
	if g == nil {
		return Scope{}
	}
	return Scope{IncludeTags: g.IncludeTags, ExcludeTags: g.ExcludeTags, RootFolders: g.RootFolders}
}

// Limited reports whether the scope hides anything.
func (s Scope) Limited() bool {
	return len(s.IncludeTags) > 0 || len(s.ExcludeTags) > 0 || len(s.RootFolders) > 0
}

// Allows reports whether a series is in the scope.
func (s Scope) Allows(ser *model.Series) bool {
	if ser == nil {
		return false
	}
	if len(s.RootFolders) > 0 && !slices.Contains(s.RootFolders, ser.RootFolderID) {
		return false
	}
	for _, t := range ser.Tags {
		if slices.Contains(s.ExcludeTags, t) {
			return false
		}
	}
	if len(s.IncludeTags) > 0 {
		return slices.ContainsFunc(ser.Tags, func(t int64) bool { return slices.Contains(s.IncludeTags, t) })
	}
	return true
}

// Principal kinds.
const (
	KindUser      = "user"
	KindAPIKey    = "apikey"    // the admin API key
	KindAnonymous = "anonymous" // MANGARR_AUTH_DISABLED (an auth proxy in front)
	KindWorker    = "worker"    // a machine holding a worker key
)

// Principal is who a request is from.
type Principal struct {
	Kind        string
	UserID      int64
	Username    string
	DisplayName string
	// ReaderID is the user's own progress.
	ReaderID  int64
	GroupID   int64
	GroupName string
	Perms     map[string]bool
	Scope     Scope
	// SessionID is the web session used (empty for API keys).
	SessionID string
	// WorkerID is the worker a key belongs to (only with KindWorker).
	WorkerID int64
}

// AdminPrincipal is the admin API key or a disabled login.
func AdminPrincipal(kind string) *Principal {
	return &Principal{Kind: kind, Username: kind, Perms: map[string]bool{Admin: true}}
}

// Can reports whether p has perm (admins have every permission).
func (p *Principal) Can(perm string) bool {
	if p == nil {
		return false
	}
	return p.Perms[Admin] || p.Perms[perm]
}

// IsAdmin reports whether p is an administrator.
func (p *Principal) IsAdmin() bool { return p.Can(Admin) }

// Sees reports whether p may see a series (admins and library managers see
// everything).
func (p *Principal) Sees(ser *model.Series) bool {
	if p == nil {
		return false
	}
	if p.Can(LibraryManage) {
		return true
	}
	if ser != nil && ser.Preview {
		// previews are opened from search, outside any library scope
		return p.Can(RequestsCreate) || p.Can(RequestsManage)
	}
	return p.Scope.Allows(ser)
}

// Permissions lists p's permissions (every one for admins).
func (p *Principal) Permissions() []string {
	out := []string{}
	for _, x := range All {
		if p.Can(x.Key) {
			out = append(out, x.Key)
		}
	}
	return out
}

type ctxKey struct{}

// With stores the principal in ctx.
func With(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// From returns the request's principal (nil when not logged in).
func From(ctx context.Context) *Principal {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	return p
}

// Client is where a request comes from (for sessions and login limits).
type Client struct {
	IP        string
	UserAgent string
	// Secure: the request came over HTTPS (for cookie flags).
	Secure bool
	// Host is the address the browser asked for (for redirect URLs).
	Host string
}

type clientKey struct{}

// WithClient stores the request's client in ctx.
func WithClient(ctx context.Context, c Client) context.Context {
	return context.WithValue(ctx, clientKey{}, c)
}

// ClientFrom returns the request's client.
func ClientFrom(ctx context.Context) Client {
	c, _ := ctx.Value(clientKey{}).(Client)
	return c
}
