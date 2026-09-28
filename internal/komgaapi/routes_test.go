package komgaapi

import (
	"flag"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

var update = flag.Bool("update", false, "rewrite testdata/routes.txt")

func testService() *Service {
	return NewService(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, "")
}

// routeList walks protected routes, optionally including public web redirects.
func routeList(t *testing.T, includeWeb bool) []string {
	t.Helper()
	r := chi.NewRouter()
	testService().routes(r)
	if includeWeb {
		testService().webRoutes(r)
	}
	var out []string
	_ = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out = append(out, method+" "+strings.TrimSuffix(route, "/"))
		return nil
	})
	out = append(out, "GET /opds", "GET /opds/covers/chapters/{id}", "GET /opds/covers/series/{id}", "GET /opds/chapters/{id}", "GET /opds/chapters/{id}/epub", "GET /opds/chapters/{id}/xtch",
		"GET /opds/libraries", "GET /opds/libraries/{id}", "GET /opds/search", "GET /opds/search.xml", "GET /opds/series/{id}", "GET /opds/updated")
	sort.Strings(out)
	return out
}

// TestRoutesGolden keeps the route manifest reviewed: run with -update after
// adding routes.
func TestRoutesGolden(t *testing.T) {
	got := strings.Join(routeList(t, true), "\n") + "\n"
	if *update {
		if err := os.WriteFile("testdata/routes.txt", []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("testdata/routes.txt")
	if err != nil {
		t.Fatalf("%v (run go test -run TestRoutesGolden -update)", err)
	}
	if got != string(want) {
		t.Fatalf("routes changed; review and run with -update:\n%s", got)
	}
}

var param = regexp.MustCompile(`\{[^}]+\}`)

// TestProtectedRoutesNeedAuth: every route answers 401 (never 404) without
// credentials, with the Basic challenge OkHttp needs before it sends a password.
func TestProtectedRoutesNeedAuth(t *testing.T) {
	h := testService().Handler()
	for _, rt := range routeList(t, false) {
		method, path, _ := strings.Cut(rt, " ")
		req := httptest.NewRequest(method, param.ReplaceAllString(path, "1"), nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Basic") {
			t.Errorf("%s: %d %q", rt, rec.Code, rec.Header().Get("WWW-Authenticate"))
		}
	}
	// the server check is public
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/client-settings/global/list", nil))
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "{}" {
		t.Fatalf("client settings: %d %s", rec.Code, rec.Body.String())
	}
}
