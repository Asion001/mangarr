package api_test

import (
	"context"
	"flag"
	"net/http"
	"net/http/cookiejar"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/auth"
)

var update = flag.Bool("update", false, "rewrite testdata/permissions.txt")

// TestPermissionTable: the list of what each operation needs is reviewed
// (go test ./internal/api -run TestPermissionTable -update after adding
// endpoints).
func TestPermissionTable(t *testing.T) {
	_, a := newServer(t, false)
	got := strings.Join(api.Permissions(a), "\n") + "\n"
	const file = "testdata/permissions.txt"
	if *update {
		_ = os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Fatalf("operation permissions changed; review and run with -update:\n%s", got)
	}
}

// login signs in and returns a client with the session cookie.
func login(t *testing.T, base, user, pass string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	resp, err := c.Post(base+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"`+user+`","password":"`+pass+`"}`))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("login %s: %v %d", user, err, resp.StatusCode)
	}
	return c
}

// TestUsersGroupCantManage: a member of the built-in Users group gets 403
// on every operation that needs admin or library.manage, and in on the
// ones everyone signed in may use.
func TestUsersGroupCantManage(t *testing.T) {
	srv, a := newServer(t, false)
	ctx := context.Background()
	if _, err := a.Auth.CreateUser(ctx, auth.NewUser{Username: "boss", Password: "boss-pass-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Auth.CreateUser(ctx, auth.NewUser{Username: "friend", Password: "friend-pass"}); err != nil {
		t.Fatal(err)
	}
	friend := login(t, srv.URL, "friend", "friend-pass")
	param := regexp.MustCompile(`\{[^}]+\}`)
	checked := 0
	for _, line := range api.Permissions(a) {
		// "op METHOD /path -> need"
		f := strings.Fields(line)
		method, path, need := f[1], f[2], strings.Join(f[4:], " ")
		path = param.ReplaceAllString(path, "1")
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		resp, err := friend.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		switch {
		case need == "admin" || need == "queue.manage" || need == "activity.view" || (strings.HasPrefix(need, "library.") && !strings.Contains(need, "|")):
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("%s: %d, want 403", line, resp.StatusCode)
			}
			checked++
		case need == "signedin" || need == "public":
			if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
				t.Errorf("%s: %d for a signed-in user", line, resp.StatusCode)
			}
		}
		if f[0] == "auth-logout" {
			friend = login(t, srv.URL, "friend", "friend-pass") // it just signed out
		}
	}
	if checked < 100 {
		t.Fatalf("only %d operations checked", checked)
	}
}
