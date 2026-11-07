package app_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/dbtest"
)

// A reading app signed in with a password is signed out when the password
// changes: its session token and the remembered password stop working.
func TestKomgaPasswordSessionsEndOnPasswordChange(t *testing.T) {
	e := newTestApp(t, dbtest.DSNs(t)["sqlite"])
	u, err := e.App.Auth.CreateUser(e.Ctx, auth.NewUser{Username: "ann", Password: "secret-pass"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(e.App.Komga.Handler())
	defer srv.Close()
	get := func(set func(*http.Request)) *http.Response {
		req, _ := http.NewRequest("GET", srv.URL+"/api/v2/users/me", nil)
		set(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	resp := get(func(r *http.Request) { r.SetBasicAuth("ann", "secret-pass") })
	token := resp.Header.Get("X-Auth-Token")
	if resp.StatusCode != 200 || token == "" {
		t.Fatalf("login: %d %q", resp.StatusCode, token)
	}
	if resp := get(func(r *http.Request) { r.Header.Set("X-Auth-Token", token) }); resp.StatusCode != 200 {
		t.Fatalf("token: %d", resp.StatusCode)
	}
	if err := e.App.Auth.SetPassword(e.Ctx, u.ID, "new-secret-pass", ""); err != nil {
		t.Fatal(err)
	}
	if resp := get(func(r *http.Request) { r.Header.Set("X-Auth-Token", token) }); resp.StatusCode != 401 {
		t.Fatalf("old token after the change: %d", resp.StatusCode)
	}
	if resp := get(func(r *http.Request) { r.SetBasicAuth("ann", "secret-pass") }); resp.StatusCode != 401 {
		t.Fatalf("old password after the change: %d", resp.StatusCode)
	}
	if resp := get(func(r *http.Request) { r.SetBasicAuth("ann", "new-secret-pass") }); resp.StatusCode != 200 {
		t.Fatalf("new password: %d", resp.StatusCode)
	}
}
