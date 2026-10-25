package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/app"
	"github.com/Asion001/mangarr/internal/config"
	"github.com/Asion001/mangarr/internal/logging"
	_ "github.com/Asion001/mangarr/internal/modules/all"
)

func newServer(t *testing.T, authDisabled bool) (*httptest.Server, *app.App) {
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir, DB: "sqlite://" + filepath.Join(dir, "t.db"), AuthDisabled: authDisabled}
	_, ring := logging.Setup("error", io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a, err := app.New(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), ring)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	srv := httptest.NewServer(api.New(a)) // panics on schema conflicts
	t.Cleanup(srv.Close)
	return srv, a
}

func TestOpenAPIAndAuth(t *testing.T) {
	srv, a := newServer(t, false)
	resp, err := http.Get(srv.URL + "/api/v1/series")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without credentials, got %d", resp.StatusCode)
	}
	g, _ := a.Settings.General(context.Background())
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/openapi.json", nil)
	req.Header.Set("X-Api-Key", g.APIKey)
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("openapi: %v %v", err, resp.StatusCode)
	}
	var doc struct {
		Paths map[string]any `json:"paths"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/v1/series", "/api/v1/sources/search", "/api/v1/cleanup/preview", "/api/v1/users"} {
		if _, ok := doc.Paths[p]; !ok {
			t.Errorf("missing path %s", p)
		}
	}
	// setup flow
	resp, _ = http.Post(srv.URL+"/api/v1/auth/setup", "application/json", strings.NewReader(`{"username":"admin","password":"secret123"}`))
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Set-Cookie"), "mangarr_session=") {
		t.Fatalf("setup: %d %v", resp.StatusCode, resp.Header)
	}
	resp, _ = http.Post(srv.URL+"/api/v1/auth/setup", "application/json", strings.NewReader(`{"username":"x","password":"secret123"}`))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second setup must fail, got %d", resp.StatusCode)
	}
}
