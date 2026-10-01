package api_test

import (
	"net/http"
	"testing"

	"github.com/Asion001/mangarr/internal/api"
)

// TestAppearanceIsPublic: the sign-in page needs the instance's name, accent
// and message before anyone signs in.
func TestAppearanceIsPublic(t *testing.T) {
	srv, _ := newServer(t, true)
	if code := doJSON(t, http.MethodPut, srv.URL+"/api/v1/settings/appearance", `{"accent":"#3b82f6","loginMessage":"Ask for an invite","theme":"light","startPage":"discover","locale":"auto"}`, nil); code != 200 {
		t.Fatalf("save appearance: %d", code)
	}
	if code := doJSON(t, http.MethodPut, srv.URL+"/api/v1/settings/appearance", `{"accent":"blue","theme":"dark","startPage":"series","locale":"auto"}`, nil); code != 422 {
		t.Fatalf("invalid accent: %d", code)
	}
	var st api.AuthStatus
	if code := doJSON(t, http.MethodGet, srv.URL+"/api/v1/auth/status", "", &st); code != 200 {
		t.Fatalf("status: %d", code)
	}
	a := st.Appearance
	if a.InstanceName != "mangarr" || a.Accent != "#3b82f6" || a.LoginMessage != "Ask for an invite" || a.Theme != "light" || a.StartPage != "discover" {
		t.Fatalf("appearance: %+v", a)
	}
}
