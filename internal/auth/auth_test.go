package auth_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

func newService(t *testing.T) *auth.Service {
	d := dbtest.SQLite(t)
	st := settings.NewStore(d)
	if _, err := st.EnsureSecrets(context.Background()); err != nil {
		t.Fatal(err)
	}
	return auth.NewService(d, st, false)
}

func request(c *http.Cookie) *http.Request {
	r, _ := http.NewRequest("GET", "/api/v1/series", nil)
	if c != nil {
		r.AddCookie(c)
	}
	return r
}

func TestUsersAndSessions(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	admin, err := s.CreateUser(ctx, auth.NewUser{Username: "boss", Password: "boss-pass-1"})
	if err != nil {
		t.Fatal(err)
	}
	friend, err := s.CreateUser(ctx, auth.NewUser{Username: "ann", Password: "ann-pass-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, auth.NewUser{Username: "ANN", Password: "whatever-1"}); err == nil {
		t.Fatal("usernames are case-insensitive")
	}
	if !admin.CountForCleanup || friend.CountForCleanup {
		t.Fatalf("admins count for cleanup, others don't: %v %v", admin.CountForCleanup, friend.CountForCleanup)
	}

	// the first user is an admin, the next ones are Users
	c := access.Client{IP: "10.0.0.2", UserAgent: "Firefox"}
	_, adminCookie, err := s.Login(ctx, "boss", "boss-pass-1", c)
	if err != nil {
		t.Fatal(err)
	}
	if p := s.Authenticate(request(adminCookie)); p == nil || !p.IsAdmin() || p.UserID != admin.ID {
		t.Fatalf("admin principal %+v", p)
	}
	_, c1, err := s.Login(ctx, "Ann", "ann-pass-1", c) // any case
	if err != nil {
		t.Fatal(err)
	}
	_, c2, _ := s.Login(ctx, "ann", "ann-pass-1", access.Client{IP: "10.0.0.3"})
	p := s.Authenticate(request(c1))
	if p == nil || p.IsAdmin() || !p.Can(access.Apps) || p.Can(access.LibraryManage) || p.GroupName != "Users" {
		t.Fatalf("user principal %+v", p)
	}
	if s.Authenticate(request(&http.Cookie{Name: auth.CookieName, Value: "forged"})) != nil {
		t.Fatal("unknown session accepted")
	}
	if list, _ := s.Sessions(ctx, friend.ID); len(list) != 2 {
		t.Fatalf("sessions %d", len(list))
	}

	// a password change signs out the other sessions
	if err := s.SetPassword(ctx, friend.ID, "new-pass-12", p.SessionID); err != nil {
		t.Fatal(err)
	}
	if s.Authenticate(request(c2)) != nil {
		t.Fatal("other session still valid after the password change")
	}
	if s.Authenticate(request(c1)) == nil {
		t.Fatal("the current session was signed out")
	}
	if _, _, err := s.Login(ctx, "ann", "ann-pass-1", c); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("old password: %v", err)
	}

	// disabled users can't sign in and their sessions stop working
	_, _ = s.UserPrincipal(ctx, friend.ID)
	if err := s.SetDisabled(ctx, friend.ID, true); err != nil {
		t.Fatal(err)
	}
	if s.Authenticate(request(c1)) != nil {
		t.Fatal("disabled user's session still valid")
	}
	if _, _, err := s.Login(ctx, "ann", "new-pass-12", c); !errors.Is(err, auth.ErrDisabled) {
		t.Fatalf("disabled login: %v", err)
	}
}

func TestLoginLockout(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	if _, err := s.CreateUser(ctx, auth.NewUser{Username: "boss", Password: "boss-pass-1"}); err != nil {
		t.Fatal(err)
	}
	c := access.Client{IP: "203.0.113.9"}
	for i := 0; i < 10; i++ {
		if _, _, err := s.Login(ctx, "boss", "wrong", c); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	var locked *auth.LockedError
	if _, _, err := s.Login(ctx, "boss", "boss-pass-1", c); !errors.As(err, &locked) || time.Until(locked.Until) < 14*time.Minute {
		t.Fatalf("not locked: %v", err)
	}
	// another address isn't locked
	if _, _, err := s.Login(ctx, "boss", "boss-pass-1", access.Client{IP: "198.51.100.1"}); err != nil {
		t.Fatalf("other address: %v", err)
	}
	// trying many usernames from one address locks the address
	for i := 0; i < 30; i++ {
		_, _, _ = s.Login(ctx, "user"+string(rune('a'+i%26))+string(rune('a'+i/26)), "x", access.Client{IP: "192.0.2.7"})
	}
	if _, _, err := s.Login(ctx, "boss", "boss-pass-1", access.Client{IP: "192.0.2.7"}); !errors.As(err, &locked) {
		t.Fatalf("address not locked: %v", err)
	}
}

// TestUpgrade: users from before groups become admins.
func TestUpgrade(t *testing.T) {
	ctx := context.Background()
	d := dbtest.SQLite(t)
	st := settings.NewStore(d)
	_, _ = st.EnsureSecrets(ctx)
	now := time.Now().UTC()
	old := &model.User{Username: "sam", PasswordHash: "$2a$10$x", CreatedAt: now}
	if _, err := d.NewInsert().Model(old).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	s := auth.NewService(d, st, false)
	if err := s.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Upgrade(ctx); err != nil { // idempotent
		t.Fatal(err)
	}
	p, err := s.UserPrincipal(ctx, old.ID)
	if err != nil || p == nil || !p.IsAdmin() {
		t.Fatalf("upgraded %+v %v", p, err)
	}
	if n, _ := d.NewSelect().Model((*model.Group)(nil)).Count(ctx); n != 2 {
		t.Fatalf("groups %d", n)
	}
}

// TestSetupIgnoresProgressOnlyAccounts: an account nobody can sign in to
// (a former reader's progress) doesn't count as set up, and the first real
// account is still the admin.
func TestSetupIgnoresProgressOnlyAccounts(t *testing.T) {
	ctx := context.Background()
	d := dbtest.SQLite(t)
	st := settings.NewStore(d)
	_, _ = st.EnsureSecrets(ctx)
	s := auth.NewService(d, st, false)
	if _, err := d.NewInsert().Model(&model.User{Username: "reader-1", Disabled: true, CreatedAt: time.Now().UTC()}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if needs, err := s.NeedsSetup(ctx); err != nil || !needs {
		t.Fatalf("needs setup = %v, %v", needs, err)
	}
	u, err := s.CreateUser(ctx, auth.NewUser{Username: "boss", Password: "boss-pass-1"})
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := s.UserPrincipal(ctx, u.ID); p == nil || !p.IsAdmin() {
		t.Fatalf("first account %+v", p)
	}
	if needs, _ := s.NeedsSetup(ctx); needs {
		t.Fatal("still needs setup")
	}
}
