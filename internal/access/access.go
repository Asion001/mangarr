// Package access is who a request is from and what they may do: users
// belong to a group, groups have permissions and can limit the series
// their members see (Scope).
package access

import (
	"context"
	"slices"
	"strings"

	"github.com/Asion001/mangarr/internal/model"
)

// Permissions. Reading the library a user can see, their own progress and
// their account need none.
const (
	// Admin allows everything: settings, modules, system, users.
	Admin = "admin"
	// LibraryManage is every library permission below (add, edit, delete,
	// the queue), and handling requests.
	LibraryManage = "library.manage"
	// LibraryAdd: add titles and languages, nothing else.
	LibraryAdd = "library.add"
	// LibraryEdit: metadata, sources, monitoring, renames; sees the whole library.
	LibraryEdit = "library.edit"
	// LibraryDelete: delete titles and chapter files.
	LibraryDelete = "library.delete"
	// QueueManage: pause, reorder, retry and remove downloads; history and blocklist.
	QueueManage = "queue.manage"
	// RequestsManage: see, approve and fulfil requests.
	RequestsManage = "requests.manage"
	// RequestsCreate: ask for series.
	RequestsCreate = "requests.create"
	// ActivityView: watch the download and processing queues without
	// changing anything.
	ActivityView = "activity.view"
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
	{LibraryManage, "Manage the library", "All of the library permissions below, and handling requests"},
	{LibraryAdd, "Add titles", "Add titles and languages, nothing else"},
	{LibraryEdit, "Edit titles", "Metadata, sources, monitoring and renames"},
	{LibraryDelete, "Delete titles and files", "Removed files go to the recycle bin"},
	{QueueManage, "Manage the queue", "Pause, reorder, retry and remove downloads"},
	{ActivityView, "See downloads and processing", "Watch the queues, change nothing"},
	{RequestsManage, "Handle requests", "See, approve and decline everyone's requests"},
	{RequestsCreate, "Request titles", "Ask for titles, languages and downloads"},
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
	// MaxRating ("all", "teen", "mature"; empty: no limit) and BlockedGenres
	// hide titles by what they are, wherever they show.
	MaxRating     string   `json:"maxRating,omitempty"`
	BlockedGenres []string `json:"blockedGenres,omitempty"`
}

// Content ratings, lowest first.
const (
	RatingAll = iota
	RatingTeen
	RatingMature
	RatingAdult
)

var ratingNames = map[string]int{"all": RatingAll, "teen": RatingTeen, "mature": RatingMature}

// RatingOf rates a title from its age rating text, adult flag and genres
// (titles nothing says anything about are for all ages).
func RatingOf(ageRating string, adult bool, genres []string) int {
	r := strings.ToLower(ageRating)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(r, w) {
				return true
			}
			for _, g := range genres {
				if strings.EqualFold(strings.TrimSpace(g), w) {
					return true
				}
			}
		}
		return false
	}
	switch {
	case adult || has("18", "adult", "hentai", "erotica", "pornographic"):
		return RatingAdult
	case has("16", "17", "mature", "ecchi", "smut"):
		return RatingMature
	case has("13", "teen"):
		return RatingTeen
	}
	return RatingAll
}

// AllowsContent reports whether the scope's rating limit and hidden genres
// let a title with this rating, adult flag and genres or tags through.
func (s Scope) AllowsContent(ageRating string, adult bool, genres []string) bool {
	if max, ok := ratingNames[s.MaxRating]; ok && RatingOf(ageRating, adult, genres) > max {
		return false
	}
	for _, g := range genres {
		if slices.ContainsFunc(s.BlockedGenres, func(b string) bool { return strings.EqualFold(strings.TrimSpace(b), strings.TrimSpace(g)) }) {
			return false
		}
	}
	return true
}

// ContentLimited reports whether the scope hides titles by content.
func (s Scope) ContentLimited() bool {
	_, ok := ratingNames[s.MaxRating]
	return ok || len(s.BlockedGenres) > 0
}

// ScopeOf is a group's scope.
func ScopeOf(g *model.Group) Scope {
	if g == nil {
		return Scope{}
	}
	return Scope{IncludeTags: g.IncludeTags, ExcludeTags: g.ExcludeTags, RootFolders: g.RootFolders, MaxRating: g.MaxRating, BlockedGenres: g.BlockedGenres}
}

// Limited reports whether the scope hides anything.
func (s Scope) Limited() bool {
	return len(s.IncludeTags) > 0 || len(s.ExcludeTags) > 0 || len(s.RootFolders) > 0 || s.ContentLimited()
}

// Allows reports whether a series is in the scope.
func (s Scope) Allows(ser *model.Series) bool {
	if ser == nil {
		return false
	}
	if len(s.RootFolders) > 0 && !slices.Contains(s.RootFolders, ser.RootFolderID) {
		return false
	}
	if s.ContentLimited() {
		md := ser.Metadata
		if !s.AllowsContent(md.AgeRating, false, append(append([]string{}, md.Genres...), md.Tags...)) {
			return false
		}
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

// bundled are the permissions "Manage the library" includes.
var bundled = map[string]bool{LibraryAdd: true, LibraryEdit: true, LibraryDelete: true, QueueManage: true, ActivityView: true}

// Can reports whether p has perm (admins have every permission, "Manage
// the library" the library ones, and managing the queue includes seeing it).
func (p *Principal) Can(perm string) bool {
	if p == nil {
		return false
	}
	switch {
	case p.Perms[Admin] || p.Perms[perm]:
		return true
	case bundled[perm] && p.Perms[LibraryManage]:
		return true
	case perm == ActivityView && p.Perms[QueueManage]:
		return true
	}
	return false
}

// IsAdmin reports whether p is an administrator.
func (p *Principal) IsAdmin() bool { return p.Can(Admin) }

// Sees reports whether p may see a series (admins and people who edit the
// library see everything).
func (p *Principal) Sees(ser *model.Series) bool {
	if p == nil {
		return false
	}
	if p.Can(LibraryEdit) {
		return true
	}
	if ser != nil && ser.Preview {
		// previews are opened from search, outside any library scope, but
		// not past the group's content limits
		md := ser.Metadata
		return (p.Can(RequestsCreate) || p.Can(RequestsManage) || p.Can(LibraryAdd)) &&
			p.Scope.AllowsContent(md.AgeRating, false, append(append([]string{}, md.Genres...), md.Tags...)) || p.Can(LibraryAdd)
	}
	return p.Scope.Allows(ser)
}

// ContentScope is p's scope when it hides titles by content (nil: it
// doesn't, or p sees the whole library).
func (p *Principal) ContentScope() *Scope {
	if p == nil || p.Can(LibraryEdit) || !p.Scope.ContentLimited() {
		return nil
	}
	return &p.Scope
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
