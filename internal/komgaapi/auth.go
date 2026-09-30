package komgaapi

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/reading"
)

// Session lifetime of tokens issued to apps.
const sessionTTL = 30 * 24 * time.Hour

// SessionCookie is the cookie apps keep the session in. Mihon's Komga
// tracker sends no credentials and relies on the cookie the extension got.
const SessionCookie = "KOMGA-SESSION"

// Principal is who a request is from.
type Principal struct {
	// KeyID is the reading key used (0 for a password login).
	KeyID int64
	// Device names the key's device ("KMReader iPad"; "password login" without a key).
	Device string
	// Client is the app, from its User-Agent.
	Client string
	// User is the account (with its reader and the series it may see).
	User *access.Principal
	// Languages is the key's language order (see model.ReadingKey).
	Languages []string
}

type principalKey struct{}

// PrincipalFrom returns the request's principal.
func PrincipalFrom(ctx context.Context) Principal {
	p, _ := ctx.Value(principalKey{}).(Principal)
	return p
}

// keys caches reading keys by hash.
type keys struct {
	mu       sync.Mutex
	loaded   bool
	byHash   map[string]model.ReadingKey
	lastSeen map[int64]time.Time
	// lastClient is the app that last used each key
	lastClient map[int64]string
}

// HashKey returns the stored form of a reading key.
func HashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// NewKey returns a random reading key.
func NewKey() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return "mgr_" + hex.EncodeToString(b)
}

// InvalidateKeys reloads reading keys (after creating or deleting one) and
// accounts (after user or group changes) on next use.
func (s *Service) InvalidateKeys() {
	s.keys.mu.Lock()
	s.keys.loaded = false
	s.keys.mu.Unlock()
	s.users.mu.Lock()
	s.users.m = nil
	s.users.mu.Unlock()
}

// loadKeys fills the key cache; the caller holds s.keys.mu.
func (s *Service) loadKeys(ctx context.Context) bool {
	k := &s.keys
	if k.loaded {
		return true
	}
	var list []model.ReadingKey
	if err := s.deps.DB.NewSelect().Model(&list).Scan(ctx); err != nil {
		return false
	}
	k.byHash = make(map[string]model.ReadingKey, len(list))
	for _, rk := range list {
		k.byHash[rk.KeyHash] = rk
	}
	k.loaded = true
	return true
}

func (s *Service) lookupKey(ctx context.Context, key string) (model.ReadingKey, bool) {
	s.keys.mu.Lock()
	defer s.keys.mu.Unlock()
	if !s.loadKeys(ctx) {
		return model.ReadingKey{}, false
	}
	rk, ok := s.keys.byHash[HashKey(key)]
	return rk, ok
}

// keyByID returns a key by id.
func (s *Service) keyByID(ctx context.Context, id int64) (model.ReadingKey, bool) {
	s.keys.mu.Lock()
	defer s.keys.mu.Unlock()
	if !s.loadKeys(ctx) {
		return model.ReadingKey{}, false
	}
	for _, rk := range s.keys.byHash {
		if rk.ID == id {
			return rk, true
		}
	}
	return model.ReadingKey{}, false
}

// CreateKey stores a new reading key for a device and returns it (the key
// itself is only available now).
func (s *Service) CreateKey(ctx context.Context, userID int64, comment, client string) (string, *model.ReadingKey, error) {
	key := NewKey()
	now := time.Now().UTC()
	md5Key := md5.Sum([]byte(key))
	rk := &model.ReadingKey{KeyHash: HashKey(key), KOReaderHash: HashKey(hex.EncodeToString(md5Key[:])), Prefix: key[:8], UserID: userID, Comment: strings.TrimSpace(comment), LastClient: client, Languages: []string{}, CreatedAt: now}
	if rk.Comment == "" {
		rk.Comment = "reading app"
	}
	if _, err := s.deps.DB.NewInsert().Model(rk).Exec(ctx); err != nil {
		return "", nil, err
	}
	s.InvalidateKeys()
	return key, rk, nil
}

// touchKey records a key's last use and app.
func (s *Service) touchKey(rk model.ReadingKey, client string) {
	k := &s.keys
	k.mu.Lock()
	if k.lastSeen == nil {
		k.lastSeen, k.lastClient = map[int64]time.Time{}, map[int64]string{}
	}
	// at most once a minute, unless another app uses the key
	if time.Since(k.lastSeen[rk.ID]) < time.Minute && k.lastClient[rk.ID] == client {
		k.mu.Unlock()
		return
	}
	k.lastSeen[rk.ID], k.lastClient[rk.ID] = time.Now(), client
	k.mu.Unlock()
	go func() {
		now := time.Now().UTC()
		_, _ = s.deps.DB.NewUpdate().Model((*model.ReadingKey)(nil)).Set("last_used_at = ?", now).Set("last_client = ?", client).
			Where("id = ?", rk.ID).Exec(context.Background())
	}()
}

// sessionKey derives the token signing key from the instance secret.
func (s *Service) sessionKey(ctx context.Context) []byte {
	g, _ := s.deps.Settings.General(ctx)
	sum := sha256.Sum256([]byte("komga-session:" + g.SessionSecret))
	return sum[:]
}

// issueToken signs a session for a key (keyID) or a password login (userID).
func (s *Service) issueToken(ctx context.Context, keyID, userID int64) string {
	payload := make([]byte, 24)
	binary.BigEndian.PutUint64(payload[:8], uint64(keyID))
	binary.BigEndian.PutUint64(payload[8:16], uint64(userID))
	binary.BigEndian.PutUint64(payload[16:], uint64(time.Now().Add(sessionTTL).Unix()))
	mac := hmac.New(sha256.New, s.sessionKey(ctx))
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

var errBadToken = errors.New("invalid session")

// verifyToken returns the key and user a token was issued for.
func (s *Service) verifyToken(ctx context.Context, tok string) (keyID, userID int64, exp time.Time, err error) {
	p, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return 0, 0, time.Time{}, errBadToken
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(p)
	mac, err2 := base64.RawURLEncoding.DecodeString(sig)
	if err1 != nil || err2 != nil || len(payload) != 24 {
		return 0, 0, time.Time{}, errBadToken
	}
	h := hmac.New(sha256.New, s.sessionKey(ctx))
	h.Write(payload)
	if !hmac.Equal(mac, h.Sum(nil)) {
		return 0, 0, time.Time{}, errBadToken
	}
	exp = time.Unix(int64(binary.BigEndian.Uint64(payload[16:])), 0)
	if time.Now().After(exp) {
		return 0, 0, time.Time{}, errBadToken
	}
	return int64(binary.BigEndian.Uint64(payload[:8])), int64(binary.BigEndian.Uint64(payload[8:16])), exp, nil
}

// basicCache remembers verified passwords briefly (bcrypt is slow).
type basicCache struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func (s *Service) verifyBasic(ctx context.Context, user, pass string) bool {
	sum := sha256.Sum256([]byte(user + "\x00" + pass))
	k := hex.EncodeToString(sum[:])
	c := &s.basic
	c.mu.Lock()
	if t, ok := c.seen[k]; ok && time.Since(t) < 10*time.Minute {
		c.mu.Unlock()
		return true
	}
	c.mu.Unlock()
	if _, err := s.deps.Auth.Verify(ctx, user, pass); err != nil {
		return false
	}
	c.mu.Lock()
	if c.seen == nil {
		c.seen = map[string]time.Time{}
	}
	c.seen[k] = time.Now()
	c.mu.Unlock()
	return true
}

// authenticate resolves the request's credentials. issue is true when a
// new session token should be handed out (key or password logins, and
// sessions close to expiry).
func (s *Service) authenticate(r *http.Request) (p Principal, issue bool, ok bool) {
	ctx := r.Context()
	p.Client = ClientName(r.UserAgent())
	withKey := func(rk model.ReadingKey) bool {
		u := s.keyUser(ctx, rk)
		if u == nil {
			return false // the key's user was disabled or deleted
		}
		s.touchKey(rk, p.Client)
		p.KeyID, p.Device, p.User, p.Languages = rk.ID, rk.Comment, u, rk.Languages
		return true
	}
	if key := r.Header.Get("X-API-Key"); key != "" {
		rk, found := s.lookupKey(ctx, key)
		if !found || !withKey(rk) {
			return p, false, false
		}
		// The extension authenticates with its API key, but Mihon's enhanced
		// tracker uses the shared cookie jar without that header. A cookie's
		// presence alone says nothing about its expiry or owning device.
		matches := func(token string) bool {
			keyID, userID, exp, err := s.verifyToken(ctx, token)
			return err == nil && keyID == rk.ID && userID == p.User.UserID && time.Until(exp) >= sessionTTL/2
		}
		cookie, err := r.Cookie(SessionCookie)
		renew := err != nil || !matches(cookie.Value)
		if token := r.Header.Get("X-Auth-Token"); token != "" && !matches(token) {
			renew = true
		}
		return p, renew, true
	}
	tok := r.Header.Get("X-Auth-Token")
	if tok == "" {
		if c, err := r.Cookie(SessionCookie); err == nil {
			tok = c.Value
		}
	}
	if tok != "" {
		if keyID, userID, exp, err := s.verifyToken(ctx, tok); err == nil {
			renew := time.Until(exp) < sessionTTL/2
			if keyID > 0 {
				if rk, found := s.keyByID(ctx, keyID); found && withKey(rk) {
					return p, renew, true
				}
			} else if u := s.userPrincipal(ctx, userID); u != nil {
				p.Device, p.User = "password login", u
				return p, renew, true
			}
		}
	}
	if user, pass, found := r.BasicAuth(); found {
		// any username with a device key as the password, for clients
		// that only do Basic (Paperback)
		if rk, isKey := s.lookupKey(ctx, pass); isKey {
			return p, true, withKey(rk)
		}
		keys := auth.LoginKeys(clientIP(r), user)
		if _, locked := s.deps.Auth.Limiter.Locked(keys...); locked {
			return p, false, false
		}
		if s.verifyBasic(ctx, user, pass) {
			s.deps.Auth.Limiter.Reset(keys[0])
			u, err := s.deps.Auth.PrincipalByName(ctx, user)
			if err != nil || u == nil {
				return p, false, false
			}
			p.Device, p.User = "password login", u
			return p, true, true
		}
		if s.deps.Auth.Limiter.Fail(keys...) {
			s.deps.Log.Warn("Komga API logins locked after repeated failures", "ip", clientIP(r), "username", user)
		}
	}
	return p, false, false
}

// keyUser is the account a device key acts as. Keys from before accounts
// (no user) act as the reader set for reading apps and see everything.
func (s *Service) keyUser(ctx context.Context, rk model.ReadingKey) *access.Principal {
	if rk.UserID > 0 {
		return s.userPrincipal(ctx, rk.UserID)
	}
	rid, err := s.deps.Reading.ReaderID(ctx)
	if err != nil {
		return nil
	}
	p := access.AdminPrincipal(access.KindAPIKey)
	p.ReaderID = rid
	return p
}

// userPrincipal loads a user's principal, cached briefly (every page turn
// authenticates).
func (s *Service) userPrincipal(ctx context.Context, userID int64) *access.Principal {
	s.users.mu.Lock()
	if e, ok := s.users.m[userID]; ok && time.Since(e.at) < 30*time.Second {
		s.users.mu.Unlock()
		return e.p
	}
	s.users.mu.Unlock()
	p, err := s.deps.Auth.UserPrincipal(ctx, userID)
	if err != nil {
		return nil
	}
	s.users.mu.Lock()
	if s.users.m == nil {
		s.users.m = map[int64]userEntry{}
	}
	s.users.m[userID] = userEntry{p: p, at: time.Now()}
	s.users.mu.Unlock()
	return p
}

type userEntry struct {
	p  *access.Principal
	at time.Time
}

type userCache struct {
	mu sync.Mutex
	m  map[int64]userEntry
}

// clientIP is the caller's address (RealIP resolved proxies).
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// setSession hands the session token to the app as a header (KMReader) and
// a cookie (Mihon's tracker).
func (s *Service) setSession(w http.ResponseWriter, r *http.Request, p Principal) {
	userID := int64(0)
	if p.User != nil {
		userID = p.User.UserID
	}
	tok := s.issueToken(r.Context(), p.KeyID, userID)
	w.Header().Set("X-Auth-Token", tok)
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Expires: time.Now().Add(sessionTTL), Secure: r.TLS != nil})
}

// requireAuth rejects requests without valid credentials with the 401
// challenge Komga clients expect (OkHttp only sends Basic after one).
func (s *Service) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, issue, ok := s.authenticate(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="mangarr"`)
			writeError(w, r, http.StatusUnauthorized, "Unauthorized")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && s.deps.Maintenance != nil && s.deps.Maintenance() {
			w.Header().Set("Retry-After", "10")
			writeError(w, r, http.StatusServiceUnavailable, "mangarr is moving its database; try again in a moment")
			return
		}
		if !p.User.Can(access.Apps) {
			writeError(w, r, http.StatusForbidden, "your account can't use reading apps; ask an administrator")
			return
		}
		if issue {
			s.setSession(w, r, p)
		}
		ctx := context.WithValue(r.Context(), principalKey{}, p)
		ctx = reading.WithLanguages(ctx, p.Languages)              // the reading service shows a title once with it
		next.ServeHTTP(w, r.WithContext(access.With(ctx, p.User))) // the reading service limits series by it
	})
}

// ClientName names the app from its User-Agent.
func ClientName(ua string) string {
	l := strings.ToLower(ua)
	switch {
	case ua == "":
		return "unknown app"
	case strings.Contains(l, "tachiyomikomga"):
		return "Mihon (Komga extension)"
	case strings.HasPrefix(l, "mihon"), strings.Contains(l, "tachiyomi"):
		return "Mihon"
	case strings.Contains(l, "kmreader"):
		return "KMReader"
	case strings.Contains(l, "paperback"):
		return "Paperback"
	case strings.Contains(l, "aidoku"):
		return "Aidoku"
	case strings.Contains(l, "mozilla"):
		return "web browser"
	}
	name, _, _ := strings.Cut(ua, "/")
	if len(name) > 40 {
		name = name[:40]
	}
	return name
}
