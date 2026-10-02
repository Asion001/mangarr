package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/version"
)

// A worker on an earlier CI build is offered the server's build on hello
// and lease, and marked in the list; one on the same version, or a server
// with updates switched off, offers nothing.
func TestWorkerUpdateOffer(t *testing.T) {
	oldV, oldB, oldC, oldU := version.Version, version.Build, version.Commit, version.UpdateURL
	version.Version, version.Build, version.Commit = "main", "120", "bbb"
	version.UpdateURL = "https://example.test/build-{build}/{zip}"
	t.Cleanup(func() { version.Version, version.Build, version.Commit, version.UpdateURL = oldV, oldB, oldC, oldU })

	srv, a := newServer(t, false)
	ctx := context.Background()
	g, _ := a.Settings.General(ctx)
	do := func(method, path, body, key string, out any) {
		t.Helper()
		r, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Api-Key", key)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			t.Fatalf("%s %s: %d", method, path, resp.StatusCode)
		}
		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				t.Fatal(err)
			}
		}
	}
	var made struct {
		Key string `json:"key"`
	}
	do(http.MethodPost, "/api/v1/workers", `{"name":"desk","roles":["download"]}`, g.APIKey, &made)

	type offered struct {
		Update *struct {
			Version string `json:"version"`
			URL     string `json:"url"`
		} `json:"update"`
	}
	var hello, lease offered
	do(http.MethodPost, "/api/v1/worker/hello", `{"version":"main","build":"118","commit":"aaa","platform":"windows/amd64","roles":["download"]}`, made.Key, &hello)
	if hello.Update == nil || hello.Update.Version != "main" || hello.Update.URL != "https://example.test/build-120/mangarr-worker-windows-amd64.zip" {
		t.Fatalf("hello offered %+v", hello.Update)
	}
	do(http.MethodPost, "/api/v1/worker/lease", `{"kinds":["download"]}`, made.Key, &lease)
	if lease.Update == nil || lease.Update.Version != "main" {
		t.Fatalf("lease offered %+v", lease.Update)
	}
	var list []struct {
		UpdateTo string `json:"updateTo"`
	}
	do(http.MethodGet, "/api/v1/workers", "", g.APIKey, &list)
	if len(list) != 1 || list[0].UpdateTo != "main build 120" {
		t.Fatalf("list %+v", list)
	}

	dl, _ := a.Settings.Downloads(ctx)
	dl.WorkerUpdates = false
	if err := a.Settings.Set(ctx, settings.KeyDownloads, dl); err != nil {
		t.Fatal(err)
	}
	lease.Update = nil
	do(http.MethodPost, "/api/v1/worker/lease", `{"kinds":["download"]}`, made.Key, &lease)
	if lease.Update != nil {
		t.Fatalf("offered with updates off: %+v", lease.Update)
	}

	hello.Update = nil
	do(http.MethodPost, "/api/v1/worker/hello", `{"version":"main","build":"120","commit":"bbb","platform":"windows/amd64","roles":["download"]}`, made.Key, &hello)
	if hello.Update != nil {
		t.Fatalf("offered the same version: %+v", hello.Update)
	}
}
