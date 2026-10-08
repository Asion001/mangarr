// Package auth handles users, groups, web sessions and the admin API key.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/uptrace/bun"
	"golang.org/x/crypto/bcrypt"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

const CookieName = "mangarr_session"
const sessionTTL = 30 * 24 * time.Hour

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrDisabled           = errors.New("this account is disabled")
)

type Service struct {
	db       *db.DB
	settings *settings.Store
	disabled bool
	// Limiter counts failed logins (web, reading apps, invites).
	Limiter *Limiter

	mu    sync.Mutex
	cache map[string]cached // session id -> principal

	// workers caches worker keys (see workers.go).
	workers workers
}

type cached struct {
	p        *access.Principal
	loaded   time.Time
	lastSeen time.Time
}

// cacheTTL is how long a session's principal is reused before reloading
// (user and group changes clear the cache at once).
const cacheTTL = time.Minute

func NewService(d *db.DB, s *settings.Store, disabled bool) *Service {
	return &Service{db: d, settings: s, disabled: disabled, Limiter: NewLimiter(), cache: map[string]cached{}}
}

func (s *Service) Disabled() bool { return s.disabled }

// Invalidate drops cached principals (after user, group or session changes).
func (s *Service) Invalidate() {
	s.mu.Lock()
	s.cache = map[string]cached{}
	s.mu.Unlock()
}

func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	n, err := s.db.NewSelect().Model((*model.User)(nil)).Count(ctx)
	return n == 0, err
}

// NewUser is what CreateUser needs.
type NewUser struct {
	Username    string
	Password    string // empty for single sign-on users
	DisplayName string
	GroupID     int64 // 0: the Users group (or Admins for the first user)
	CreatedBy   int64
	OIDCSubject string
}

// CreateUser creates a user and their reader (their own progress).
func (s *Service) CreateUser(ctx context.Context, in NewUser) (*model.User, error) {
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" || strings.ContainsAny(in.Username, " \t:/") {
		return nil, fmt.Errorf("choose a username without spaces, colons or slashes")
	}
	if in.OIDCSubject == "" && len(in.Password) < 8 {
		return nil, fmt.Errorf("the password must be at least 8 characters")
	}
	if n, _ := s.db.NewSelect().Model((*model.User)(nil)).Where("LOWER(username) = LOWER(?)", in.Username).Count(ctx); n > 0 {
		return nil, fmt.Errorf("the username %q is taken", in.Username)
	}
	hash := ""
	if in.Password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		hash = string(h)
	}
	first, err := s.NeedsSetup(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := s.EnsureGroups(ctx)
	if err != nil {
		return nil, err
	}
	if in.GroupID == 0 {
		in.GroupID = groups[model.GroupUsers]
		if first {
			in.GroupID = groups[model.GroupAdmins]
		}
	}
	now := time.Now().UTC()
	u := &model.User{Username: in.Username, PasswordHash: hash, DisplayName: strings.TrimSpace(in.DisplayName), GroupID: in.GroupID,
		CreatedBy: in.CreatedBy, OIDCSubject: in.OIDCSubject, CreatedAt: now}
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		r, err := newReader(ctx, tx, u, first || in.GroupID == groups[model.GroupAdmins])
		if err != nil {
			return err
		}
		u.ReaderID = r.ID
		_, err = tx.NewInsert().Model(u).Exec(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	return u, nil
}

// newReader creates the reader for a user's progress, named after them
// ("ann", else "ann (2)" when a reader of that name exists).
func newReader(ctx context.Context, tx bun.IDB, u *model.User, countsForCleanup bool) (*model.Reader, error) {
	base := u.DisplayName
	if base == "" {
		base = u.Username
	}
	name := base
	for i := 2; ; i++ {
		n, err := tx.NewSelect().Model((*model.Reader)(nil)).Where("name = ?", name).Count(ctx)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			break
		}
		name = fmt.Sprintf("%s (%d)", base, i)
	}
	r := &model.Reader{Name: name, CountForCleanup: countsForCleanup, CreatedAt: time.Now().UTC()}
	_, err := tx.NewInsert().Model(r).Exec(ctx)
	return r, err
}

// EnsureGroups creates the built-in groups when missing and returns their ids.
func (s *Service) EnsureGroups(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	for _, b := range []struct {
		key, name string
		perms     []string
	}{{model.GroupAdmins, "Admins", []string{access.Admin}}, {model.GroupUsers, "Users", access.UsersDefault}} {
		var g model.Group
		err := s.db.NewSelect().Model(&g).Where("builtin = ?", b.key).Limit(1).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			name := b.name
			if n, _ := s.db.NewSelect().Model((*model.Group)(nil)).Where("name = ?", name).Count(ctx); n > 0 {
				name += " (built-in)"
			}
			g = model.Group{Name: name, Builtin: b.key, Permissions: b.perms, IncludeTags: []int64{}, ExcludeTags: []int64{}, BlockedGenres: []string{},
				RootFolders: []int64{}, CreatedAt: time.Now().UTC()}
			if _, err := s.db.NewInsert().Model(&g).Exec(ctx); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		}
		out[b.key] = g.ID
	}
	return out, nil
}

// Upgrade brings accounts from before groups up to date: every user without
// a group becomes an admin (the only user there was), and every user gets a
// reader — preferred, when given, for the first user without one.
func (s *Service) Upgrade(ctx context.Context, preferredReader int64) error {
	groups, err := s.EnsureGroups(ctx)
	if err != nil {
		return err
	}
	if _, err := s.db.NewUpdate().Model((*model.User)(nil)).Set("group_id = ?", groups[model.GroupAdmins]).
		Where("group_id IS NULL").Exec(ctx); err != nil {
		return err
	}
	var users []model.User
	if err := s.db.NewSelect().Model(&users).Where("reader_id IS NULL").Order("id").Scan(ctx); err != nil {
		return err
	}
	for i := range users {
		u := &users[i]
		err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			rid := int64(0)
			if preferredReader > 0 {
				if n, _ := tx.NewSelect().Model((*model.User)(nil)).Where("reader_id = ?", preferredReader).Count(ctx); n == 0 {
					rid = preferredReader
				}
			}
			if rid == 0 {
				r, err := newReader(ctx, tx, u, u.GroupID == groups[model.GroupAdmins])
				if err != nil {
					return err
				}
				rid = r.ID
			} else if err := renameImportedReader(ctx, tx, rid, u); err != nil {
				return err
			}
			_, err := tx.NewUpdate().Model((*model.User)(nil)).Set("reader_id = ?", rid).Where("id = ?", u.ID).Exec(ctx)
			return err
		})
		if err != nil {
			return err
		}
	}
	// device keys from before accounts belong to the first admin
	var admin model.User
	if err := s.db.NewSelect().Model(&admin).Where("group_id = ?", groups[model.GroupAdmins]).Order("id").Limit(1).Scan(ctx); err == nil {
		if _, err := s.db.NewUpdate().Model((*model.ReadingKey)(nil)).Set("user_id = ?", admin.ID).Where("user_id IS NULL").Exec(ctx); err != nil {
			return err
		}
	}
	s.Invalidate()
	return nil
}

// renameImportedReader gives an unowned backup placeholder the account's
// identity when it is adopted. The progress rows stay on the same reader, so
// provenance and history are preserved. Non-placeholder and ambiguous names
// are deliberately left alone.
func renameImportedReader(ctx context.Context, tx bun.IDB, readerID int64, u *model.User) error {
	var r model.Reader
	if err := tx.NewSelect().Model(&r).Where("id = ?", readerID).Scan(ctx); err != nil {
		return err
	}
	if r.Name != "Mihon backup" && r.Name != "Aidoku backup" {
		return nil
	}
	name := strings.TrimSpace(u.DisplayName)
	if name == "" {
		name = u.Username
	}
	if name == "" {
		return nil
	}
	if n, err := tx.NewSelect().Model((*model.Reader)(nil)).Where("name = ? AND id <> ?", name, readerID).Count(ctx); err != nil {
		return err
	} else if n > 0 {
		return nil
	}
	_, err := tx.NewUpdate().Model((*model.Reader)(nil)).Set("name = ?", name).Where("id = ?", readerID).Exec(ctx)
	return err
}

// PrincipalByName builds the principal of a user by username (nil when
// unknown or disabled).
func (s *Service) PrincipalByName(ctx context.Context, username string) (*access.Principal, error) {
	var u model.User
	if err := s.db.NewSelect().Model(&u).Column("id").Where("LOWER(username) = LOWER(?)", strings.TrimSpace(username)).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return s.UserPrincipal(ctx, u.ID)
}

// SetPassword changes a user's password and signs out their other sessions
// (keep: the session to leave signed in, if any).
func (s *Service) SetPassword(ctx context.Context, userID int64, password, keep string) error {
	if len(password) < 8 {
		return fmt.Errorf("the password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if _, err := s.db.NewUpdate().Model((*model.User)(nil)).Set("password_hash = ?", string(hash)).Where("id = ?", userID).Exec(ctx); err != nil {
		return err
	}
	return s.RevokeSessions(ctx, userID, keep)
}

// Verify checks a username and password (constant time for unknown users).
func (s *Service) Verify(ctx context.Context, username, password string) (*model.User, error) {
	var u model.User
	err := s.db.NewSelect().Model(&u).Where("LOWER(username) = LOWER(?)", strings.TrimSpace(username)).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && u.PasswordHash == "") {
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinva"), []byte(password))
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, ErrInvalidCredentials
	}
	if u.Disabled {
		return nil, ErrDisabled
	}
	return &u, nil
}

// Login checks credentials with the failed-login limit and starts a session.
func (s *Service) Login(ctx context.Context, username, password string, c access.Client) (*model.User, *http.Cookie, error) {
	keys := LoginKeys(c.IP, username)
	if until, locked := s.Limiter.Locked(keys...); locked {
		return nil, nil, &LockedError{Until: until}
	}
	u, err := s.Verify(ctx, username, password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			s.Limiter.Fail(keys...)
		}
		return nil, nil, err
	}
	s.Limiter.Reset(keys[0])
	cookie, err := s.StartSession(ctx, u.ID, c)
	return u, cookie, err
}

// StartSession creates a session for a user and returns its cookie.
func (s *Service) StartSession(ctx context.Context, userID int64, c access.Client) (*http.Cookie, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	sess := &model.Session{ID: hex.EncodeToString(b), UserID: userID, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(sessionTTL),
		IP: c.IP, UserAgent: truncate(c.UserAgent, 300)}
	if _, err := s.db.NewInsert().Model(sess).Exec(ctx); err != nil {
		return nil, err
	}
	_, _ = s.db.NewUpdate().Model((*model.User)(nil)).Set("last_login_at = ?", now).Where("id = ?", userID).Exec(ctx)
	return &http.Cookie{Name: CookieName, Value: sess.ID, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Expires: sess.ExpiresAt}, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// EndSession signs a session out.
func (s *Service) EndSession(ctx context.Context, id string) error {
	_, err := s.db.NewDelete().Model((*model.Session)(nil)).Where("id = ?", id).Exec(ctx)
	s.mu.Lock()
	delete(s.cache, id)
	s.mu.Unlock()
	return err
}

// RevokeSessions signs a user out everywhere except the session keep.
func (s *Service) RevokeSessions(ctx context.Context, userID int64, keep string) error {
	_, err := s.db.NewDelete().Model((*model.Session)(nil)).Where("user_id = ? AND id <> ?", userID, keep).Exec(ctx)
	s.Invalidate()
	return err
}

// Sessions lists a user's sessions, newest first.
func (s *Service) Sessions(ctx context.Context, userID int64) ([]model.Session, error) {
	out := []model.Session{}
	err := s.db.NewSelect().Model(&out).Where("user_id = ? AND expires_at > ?", userID, time.Now().UTC()).
		OrderExpr("last_seen_at DESC").Scan(ctx)
	return out, err
}

// Authenticate returns the principal for a request, or nil when it has no
// valid credentials.
func (s *Service) Authenticate(r *http.Request) *access.Principal {
	ctx := r.Context()
	key := r.Header.Get("X-Api-Key")
	if key == "" {
		key = r.URL.Query().Get("apikey")
	}
	if key == "" {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			key = strings.TrimPrefix(h, "Bearer ")
		}
	}
	// A worker key names a machine, and it does so even where logins are
	// switched off for people (an auth proxy in front): the worker protocol
	// has to know which worker it is talking to.
	if strings.HasPrefix(key, WorkerKeyPrefix) {
		w, ok := s.WorkerByKey(ctx, key)
		if !ok || !w.Enabled {
			return nil
		}
		s.touchWorker(w.ID, access.ClientFrom(ctx).IP)
		return WorkerPrincipal(w)
	}
	if s.disabled {
		return access.AdminPrincipal(access.KindAnonymous)
	}
	if key != "" {
		g, err := s.settings.General(ctx)
		if err == nil && g.APIKey != "" && subtle.ConstantTimeCompare([]byte(key), []byte(g.APIKey)) == 1 {
			return access.AdminPrincipal(access.KindAPIKey)
		}
		return nil
	}
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	return s.sessionPrincipal(ctx, c.Value)
}

func (s *Service) sessionPrincipal(ctx context.Context, id string) *access.Principal {
	now := time.Now()
	s.mu.Lock()
	e, ok := s.cache[id]
	s.mu.Unlock()
	if ok && now.Sub(e.loaded) < cacheTTL {
		if now.Sub(e.lastSeen) > time.Minute {
			e.lastSeen = now
			s.mu.Lock()
			s.cache[id] = e
			s.mu.Unlock()
			go func() {
				_, _ = s.db.NewUpdate().Model((*model.Session)(nil)).Set("last_seen_at = ?", now.UTC()).Where("id = ?", id).Exec(context.Background())
			}()
		}
		return e.p
	}
	var sess model.Session
	if err := s.db.NewSelect().Model(&sess).Where("id = ?", id).Scan(ctx); err != nil || now.After(sess.ExpiresAt) {
		return nil
	}
	p, err := s.UserPrincipal(ctx, sess.UserID)
	if err != nil || p == nil {
		return nil
	}
	p.SessionID = id
	_, _ = s.db.NewUpdate().Model((*model.Session)(nil)).Set("last_seen_at = ?", now.UTC()).Where("id = ?", id).Exec(ctx)
	s.mu.Lock()
	s.cache[id] = cached{p: p, loaded: now, lastSeen: now}
	s.mu.Unlock()
	return p
}

// UserPrincipal builds a user's principal (nil when disabled or missing).
func (s *Service) UserPrincipal(ctx context.Context, userID int64) (*access.Principal, error) {
	var u model.User
	if err := s.db.NewSelect().Model(&u).Where("id = ?", userID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if u.Disabled {
		return nil, nil
	}
	var g model.Group
	_ = s.db.NewSelect().Model(&g).Where("id = ?", u.GroupID).Scan(ctx)
	p := &access.Principal{Kind: access.KindUser, UserID: u.ID, Username: u.Username, DisplayName: u.DisplayName, ReaderID: u.ReaderID,
		GroupID: g.ID, GroupName: g.Name, Perms: map[string]bool{}, Scope: access.ScopeOf(&g)}
	for _, perm := range g.Permissions {
		p.Perms[perm] = true
	}
	if g.Builtin == model.GroupAdmins {
		p.Perms[access.Admin] = true
	}
	if p.IsAdmin() {
		p.Scope = access.Scope{}
	}
	return p, nil
}

// PurgeSessions deletes expired sessions.
func (s *Service) PurgeSessions(ctx context.Context) {
	_, _ = s.db.NewDelete().Model((*model.Session)(nil)).Where("expires_at < ?", time.Now().UTC()).Exec(ctx)
}

// SetDisabled disables (or enables) a user; disabling signs them out.
func (s *Service) SetDisabled(ctx context.Context, userID int64, disabled bool) error {
	if _, err := s.db.NewUpdate().Model((*model.User)(nil)).Set("disabled = ?", disabled).Where("id = ?", userID).Exec(ctx); err != nil {
		return err
	}
	if disabled {
		return s.RevokeSessions(ctx, userID, "")
	}
	s.Invalidate()
	return nil
}
