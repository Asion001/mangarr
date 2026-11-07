// Package sso signs people in with an OpenID Connect provider: the
// authorization code flow with PKCE, a nonce, and state kept in a signed
// cookie. Accounts are found by the provider's subject, optionally linked by
// username, created on first sign-in or through an invite, and their group
// follows the provider's groups.
package sso

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

var (
	ErrDisabled   = errors.New("single sign-on is off")
	ErrNoAccount  = errors.New("there's no account for you here yet: ask for an invite")
	ErrNotAllowed = errors.New("your account at the provider isn't in a group allowed here")
	ErrState      = errors.New("the sign-in expired or didn't start here; try again")
	ErrSetup      = errors.New("create the first account with a password before using single sign-on")
)

// StateCookie carries the sign-in state between the redirect and the callback.
const StateCookie = "mangarr_oidc"

const stateTTL = 10 * time.Minute

type Service struct {
	Settings *settings.Store
	Auth     *auth.Service
	DB       *db.DB
	HTTP     *http.Client
	Log      *slog.Logger

	mu       sync.Mutex
	provider *oidc.Provider
	issuer   string
	fetched  time.Time
}

// state is what the callback needs from the start of the sign-in.
type state struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Invite   string `json:"i,omitempty"`
	Next     string `json:"x,omitempty"`
	Expires  int64  `json:"e"`
}

func (s *Service) config(ctx context.Context) (settings.SSO, error) {
	cfg, err := s.Settings.SSO(ctx)
	if err != nil {
		return cfg, err
	}
	if !cfg.Enabled || cfg.Issuer == "" || cfg.ClientID == "" {
		return cfg, ErrDisabled
	}
	return cfg, nil
}

func (s *Service) client(ctx context.Context) context.Context {
	if s.HTTP == nil {
		return ctx
	}
	return oidc.ClientContext(ctx, s.HTTP)
}

// Provider fetches the provider's discovery document (cached for an hour).
func (s *Service) Provider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.provider != nil && s.issuer == issuer && time.Since(s.fetched) < time.Hour {
		return s.provider, nil
	}
	p, err := oidc.NewProvider(s.client(ctx), strings.TrimRight(issuer, "/"))
	if err != nil {
		return nil, fmt.Errorf("the provider's discovery document: %w", err)
	}
	s.provider, s.issuer, s.fetched = p, issuer, time.Now()
	return p, nil
}

func oauthConfig(cfg settings.SSO, p *oidc.Provider, redirectURL string) *oauth2.Config {
	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}
	if !contains(scopes, oidc.ScopeOpenID) {
		scopes = append([]string{oidc.ScopeOpenID}, scopes...)
	}
	return &oauth2.Config{ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, Endpoint: p.Endpoint(), RedirectURL: redirectURL, Scopes: scopes}
}

// Begin starts a sign-in: the provider's URL to send the browser to and the
// cookie to keep until it comes back. invite redeems an invite with the
// provider's account; next is where to go afterwards.
func (s *Service) Begin(ctx context.Context, redirectURL, invite, next string) (string, string, error) {
	cfg, err := s.config(ctx)
	if err != nil {
		return "", "", err
	}
	p, err := s.Provider(ctx, cfg.Issuer)
	if err != nil {
		return "", "", err
	}
	st := state{State: random(), Nonce: random(), Verifier: oauth2.GenerateVerifier(), Invite: invite, Next: safeNext(next),
		Expires: time.Now().Add(stateTTL).Unix()}
	cookie, err := s.seal(ctx, st)
	if err != nil {
		return "", "", err
	}
	u := oauthConfig(cfg, p, redirectURL).AuthCodeURL(st.State, oidc.Nonce(st.Nonce), oauth2.S256ChallengeOption(st.Verifier))
	return u, cookie, nil
}

// Finish completes a sign-in from the provider's callback and returns the
// account, and where to go next.
func (s *Service) Finish(ctx context.Context, redirectURL string, q url.Values, cookie string) (*model.User, string, error) {
	st, err := s.open(ctx, cookie)
	if err != nil {
		return nil, "", err
	}
	if subtle.ConstantTimeCompare([]byte(st.State), []byte(q.Get("state"))) != 1 {
		return nil, "", ErrState
	}
	if e := q.Get("error"); e != "" {
		msg := e
		if d := q.Get("error_description"); d != "" {
			msg = d
		}
		return nil, st.Next, fmt.Errorf("the provider said: %s", msg)
	}
	cfg, err := s.config(ctx)
	if err != nil {
		return nil, st.Next, err
	}
	p, err := s.Provider(ctx, cfg.Issuer)
	if err != nil {
		return nil, st.Next, err
	}
	conf := oauthConfig(cfg, p, redirectURL)
	tok, err := conf.Exchange(s.client(ctx), q.Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		return nil, st.Next, fmt.Errorf("the provider didn't accept the sign-in: %w", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return nil, st.Next, errors.New("the provider sent no ID token")
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: cfg.ClientID}).Verify(s.client(ctx), raw)
	if err != nil {
		return nil, st.Next, fmt.Errorf("the provider's ID token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(st.Nonce)) != 1 {
		return nil, st.Next, ErrState
	}
	claims := map[string]any{}
	if err := idt.Claims(&claims); err != nil {
		return nil, st.Next, err
	}
	// some providers only put groups (or names) in the userinfo response
	if _, ok := claims[cfg.GroupsClaim]; !ok || claims[cfg.UsernameClaim] == nil {
		if ui, err := p.UserInfo(s.client(ctx), oauth2.StaticTokenSource(tok)); err == nil {
			extra := map[string]any{}
			if ui.Subject == idt.Subject && ui.Claims(&extra) == nil {
				for k, v := range extra {
					if _, ok := claims[k]; !ok {
						claims[k] = v
					}
				}
			}
		}
	}
	u, err := s.account(ctx, cfg, idt.Subject, claims, st.Invite)
	return u, st.Next, err
}

// account finds (or makes) the account for a provider identity and keeps
// its group in line with the provider's groups.
func (s *Service) account(ctx context.Context, cfg settings.SSO, subject string, claims map[string]any, invite string) (*model.User, error) {
	if needs, err := s.Auth.NeedsSetup(ctx); err != nil {
		return nil, err
	} else if needs {
		return nil, ErrSetup
	}
	mapped, err := s.mappedGroup(ctx, cfg, claimStrings(claims[cfg.GroupsClaim]))
	if err != nil {
		return nil, err
	}
	if cfg.OnlyMapped && len(cfg.Groups) > 0 && mapped == 0 {
		return nil, ErrNotAllowed
	}
	username := cleanUsername(firstString(claims, cfg.UsernameClaim, "preferred_username", "email", "nickname"))
	if username == "" {
		username = "user-" + shortHash(subject)
	}
	display, _ := claims["name"].(string)

	var u model.User
	err = s.DB.NewSelect().Model(&u).Where("oidc_subject = ?", subject).Scan(ctx)
	switch {
	case err == nil:
	case !errors.Is(err, sql.ErrNoRows):
		return nil, err
	case cfg.LinkByUsername && s.DB.NewSelect().Model(&u).Where("LOWER(username) = LOWER(?) AND oidc_subject = ''", username).Scan(ctx) == nil:
		if _, err := s.DB.NewUpdate().Model(&u).Set("oidc_subject = ?", subject).WherePK().Exec(ctx); err != nil {
			return nil, err
		}
		s.Log.Info("single sign-on linked to an existing account", "user", u.Username)
	case invite != "":
		nu, err := s.Auth.Redeem(ctx, invite, auth.NewUser{Username: s.freeUsername(ctx, username), DisplayName: display, OIDCSubject: subject})
		if err != nil {
			return nil, err
		}
		u = *nu
	case cfg.CreateUsers:
		nu, err := s.Auth.CreateUser(ctx, auth.NewUser{Username: s.freeUsername(ctx, username), DisplayName: display, GroupID: mapped, OIDCSubject: subject})
		if err != nil {
			return nil, err
		}
		u = *nu
		s.Log.Info("single sign-on created an account", "user", u.Username)
	default:
		return nil, ErrNoAccount
	}
	if u.Disabled {
		return nil, auth.ErrDisabled
	}
	if mapped != 0 && u.GroupID != mapped {
		if err := s.moveGroup(ctx, &u, mapped); err != nil {
			return nil, err
		}
	}
	if u.DisplayName == "" && display != "" {
		_, _ = s.DB.NewUpdate().Model((*model.User)(nil)).Set("display_name = ?", display).Where("id = ?", u.ID).Exec(ctx)
	}
	return &u, nil
}

// mappedGroup is the mangarr group for the provider's groups (0: none).
func (s *Service) mappedGroup(ctx context.Context, cfg settings.SSO, groups []string) (int64, error) {
	for _, m := range cfg.Groups {
		if m.GroupID == 0 || !contains(groups, m.Claim) {
			continue
		}
		n, err := s.DB.NewSelect().Model((*model.Group)(nil)).Where("id = ?", m.GroupID).Count(ctx)
		if err != nil {
			return 0, err
		}
		if n > 0 {
			return m.GroupID, nil
		}
	}
	return 0, nil
}

// moveGroup puts an account in the group the provider says, unless that
// would leave the install without an administrator.
func (s *Service) moveGroup(ctx context.Context, u *model.User, groupID int64) error {
	var from, to model.Group
	_ = s.DB.NewSelect().Model(&from).Where("id = ?", u.GroupID).Scan(ctx)
	if err := s.DB.NewSelect().Model(&to).Where("id = ?", groupID).Scan(ctx); err != nil {
		return err
	}
	if from.Builtin == model.GroupAdmins && to.Builtin != model.GroupAdmins {
		if n, err := s.Auth.Admins(ctx, nil); err != nil || n <= 1 {
			s.Log.Warn("single sign-on kept the last administrator's group", "user", u.Username, "provider group", to.Name)
			return err
		}
	}
	if _, err := s.DB.NewUpdate().Model((*model.User)(nil)).Set("group_id = ?", groupID).Where("id = ?", u.ID).Exec(ctx); err != nil {
		return err
	}
	u.GroupID = groupID
	s.Auth.Invalidate()
	return nil
}

// freeUsername adds a number to a username someone has ("ann-2").
func (s *Service) freeUsername(ctx context.Context, name string) string {
	for i := 1; i < 100; i++ {
		try := name
		if i > 1 {
			try = name + "-" + strconv.Itoa(i)
		}
		if n, _ := s.DB.NewSelect().Model((*model.User)(nil)).Where("LOWER(username) = LOWER(?)", try).Count(ctx); n == 0 {
			return try
		}
	}
	return name + "-" + random()[:6]
}

var usernameJunk = regexp.MustCompile(`[\s:/]+`)

// cleanUsername makes a claim a valid username (an email keeps its local part).
func cleanUsername(s string) string {
	if at := strings.IndexByte(s, '@'); at > 0 {
		s = s[:at]
	}
	s = usernameJunk.ReplaceAllString(strings.TrimSpace(s), "-")
	if len(s) > 64 {
		s = s[:64]
	}
	return strings.Trim(s, "-")
}

func firstString(claims map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := claims[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// claimStrings reads a claim that is a list of strings (or one string).
func claimStrings(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// safeNext keeps only local paths ("/series/3"), never another site.
func safeNext(next string) string {
	if !LocalPath(next) {
		return "/"
	}
	return next
}

// LocalPath reports whether p is a path on this site ("/series/3") that a
// browser can't read as another site: not "//evil", "/\\evil", and no
// control characters (browsers drop tabs and newlines, so "/\t/evil" would
// become "//evil").
func LocalPath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") || strings.ContainsRune(p, '\\') {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	u, err := url.Parse(p)
	return err == nil && u.Scheme == "" && u.Host == "" && u.User == nil
}

func random() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:4])
}

// seal signs the state for the cookie.
func (s *Service) seal(ctx context.Context, st state) (string, error) {
	key, err := s.key(ctx)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(st)
	payload := base64.RawURLEncoding.EncodeToString(b)
	m := hmac.New(sha256.New, key)
	m.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil)), nil
}

// open checks the cookie's signature and age.
func (s *Service) open(ctx context.Context, cookie string) (state, error) {
	var st state
	payload, sig, ok := strings.Cut(cookie, ".")
	if !ok {
		return st, ErrState
	}
	key, err := s.key(ctx)
	if err != nil {
		return st, err
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(payload))
	want := base64.RawURLEncoding.EncodeToString(m.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(sig), []byte(want)) != 1 {
		return st, ErrState
	}
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(b, &st) != nil || time.Now().Unix() > st.Expires {
		return st, ErrState
	}
	return st, nil
}

func (s *Service) key(ctx context.Context) ([]byte, error) {
	g, err := s.Settings.General(ctx)
	if err != nil {
		return nil, err
	}
	if g.SessionSecret == "" {
		return nil, errors.New("no session secret")
	}
	return []byte("mangarr-oidc:" + g.SessionSecret), nil
}
