package workerui

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testUI(t *testing.T, env map[string]string) (*UI, *Store) {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "w", "settings.json"), func(k string) string { return env[k] },
		map[string]string{"MANGARR_UPSCALER_TOOLS_DIR": "/next/to/exe"})
	if err != nil {
		t.Fatal(err)
	}
	logs := &Logs{}
	log := slog.New(logs.Handler(slog.NewTextHandler(io.Discard, nil)))
	return New(store, logs, log, "test"), store
}

func do(t *testing.T, h http.Handler, method, target, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Host = "127.0.0.1:8790"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestSettingsSaveKeepsKeyAndHonoursEnvironment(t *testing.T) {
	ui, store := testUI(t, map[string]string{"MANGARR_UPSCALER_GPU": "1"})
	h := ui.Handler()
	post := map[string]string{"X-Mangarr-Worker": "1"}
	// an unreachable server: the worker starts and waits for it
	rec := do(t, h, http.MethodPost, "/api/settings",
		`{"MANGARR_SERVER_URL":"http://127.0.0.1:1","MANGARR_WORKER_KEY":"mgw_secret","MANGARR_WORKER_ROLES":"download","MANGARR_UPSCALER_GPU":"0"}`, post)
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	defer ui.Stop()
	if store.Saved("MANGARR_WORKER_KEY") != "mgw_secret" || store.Saved("MANGARR_UPSCALER_GPU") != "" {
		t.Fatalf("saved key %q, gpu %q", store.Saved("MANGARR_WORKER_KEY"), store.Saved("MANGARR_UPSCALER_GPU"))
	}
	if store.Getenv("MANGARR_UPSCALER_GPU") != "1" || store.Getenv("MANGARR_UPSCALER_TOOLS_DIR") != "/next/to/exe" {
		t.Fatal("the environment and the defaults should fill what the file doesn't")
	}
	// the key never comes back, and a blank one keeps it
	var st state
	if err := json.Unmarshal(do(t, h, http.MethodGet, "/api/state", "", nil).Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Running {
		t.Fatal("the worker should be running")
	}
	for _, f := range st.Settings {
		if f.Name == "MANGARR_WORKER_KEY" && (f.Value != "" || !f.Set) {
			t.Fatalf("key field: %+v", f)
		}
		if f.Name == "MANGARR_UPSCALER_GPU" && !f.Locked {
			t.Fatalf("gpu field should be locked: %+v", f)
		}
	}
	if rec := do(t, h, http.MethodPost, "/api/settings", `{"MANGARR_WORKER_KEY":"","MANGARR_SERVER_URL":"http://127.0.0.1:2"}`, post); rec.Code != http.StatusOK {
		t.Fatalf("second save: %d %s", rec.Code, rec.Body)
	}
	if store.Saved("MANGARR_WORKER_KEY") != "mgw_secret" || store.Saved("MANGARR_SERVER_URL") != "http://127.0.0.1:2" {
		t.Fatal("a blank key should keep the saved one")
	}
	if fi, err := os.Stat(store.Path()); err != nil || (fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Fatalf("settings file: %v %v", fi.Mode(), err)
	}
	if do(t, h, http.MethodPost, "/api/stop", "", post).Code != http.StatusOK || ui.running() {
		t.Fatal("stop")
	}
}

func TestStartWithoutKeyExplainsWhy(t *testing.T) {
	ui, _ := testUI(t, nil)
	rec := do(t, ui.Handler(), http.MethodPost, "/api/start", "", map[string]string{"X-Mangarr-Worker": "1"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "MANGARR_SERVER_URL") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestGuardKeepsThePageLocal(t *testing.T) {
	ui, _ := testUI(t, nil)
	h := ui.Handler()
	r := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	r.Host = "evil.example:8790" // a DNS-rebinding page
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign host: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/stop", "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("post without the header: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/", "", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "mangarr worker") {
		t.Fatalf("page: %d", rec.Code)
	}
}

func TestLogsKeepTheTail(t *testing.T) {
	logs := &Logs{}
	log := slog.New(logs.Handler(slog.NewTextHandler(io.Discard, nil))).With("task", 7)
	for i := range logKept + 10 {
		log.Info("line", "n", i)
	}
	all := logs.Since(0)
	if len(all) != logKept || all[0].Seq != 11 || all[len(all)-1].Attrs != "task=7 n=509" {
		t.Fatalf("%d lines, first %d, last %+v", len(all), all[0].Seq, all[len(all)-1])
	}
	if got := logs.Since(all[len(all)-2].Seq); len(got) != 1 {
		t.Fatalf("since: %d", len(got))
	}
}
