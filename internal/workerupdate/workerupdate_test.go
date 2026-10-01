package workerupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		target, current string
		want            bool
	}{
		{"v1.2.4", "v1.2.3", true},
		{"v1.10.0", "v1.9.9", true},
		{"v2.0.0", "v1.99.99", true},
		{"v1.2.3", "v1.2.3", false},
		{"v1.2.2", "v1.2.3", false}, // never backwards
		{"v1.3.0", "v1.3.0-rc.1", true},
		{"v1.3.0-rc.2", "v1.3.0-rc.1", true},
		{"v1.3.0-rc.10", "v1.3.0-rc.9", true},
		{"v1.3.0-rc.1", "v1.3.0", false},
		{"v1.3.0", "main", false},              // a branch build is left alone
		{"main", "v1.2.0", false},              // and offers nothing
		{"v1.3.0", "dev", false},               // a local build too
		{"v1.3.0-4-gabc1234", "v1.2.0", false}, // git describe past a tag
		{"v1.3.0", "v1.2.0-4-gabc1234", false},
		{"v1.3.0-dirty", "v1.2.0", false},
		{"1.3.0", "v1.2.0", false},
	} {
		if got := Newer(c.target, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.target, c.current, got, c.want)
		}
	}
}

func TestFor(t *testing.T) {
	o := For("v1.3.0", "v1.2.0", "windows/amd64")
	if o == nil {
		t.Fatal("no offer")
	}
	if o.URL != Releases+"/v1.3.0/mangarr-worker-windows-amd64.zip" || o.Checksum != o.URL+".sha256" {
		t.Errorf("zip %q, checksum %q", o.URL, o.Checksum)
	}
	if o.Image != "ghcr.io/asion001/mangarr:1.3.0" {
		t.Errorf("image %q", o.Image)
	}
	if o := For("v1.3.0", "v1.2.0", "linux/arm64"); o == nil || o.URL != "" {
		t.Errorf("a platform without a zip: %+v", o)
	}
	if o := For("v1.3.0", "v1.3.0", "linux/amd64"); o != nil {
		t.Errorf("same version: %+v", o)
	}
}

// publish serves a worker zip holding program and its checksum.
func publish(t *testing.T, program []byte, sum string) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range map[string][]byte{
		"mangarr-worker-test/mangarr-worker":                        program,
		"mangarr-worker-test/README.txt":                            []byte("hi"),
		"mangarr-worker-test/upscalers/waifu2x/waifu2x-ncnn-vulkan": []byte("x"),
	} {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write(data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if sum == "" {
		h := sha256.Sum256(buf.Bytes())
		sum = hex.EncodeToString(h[:])
	}
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".sha256"):
			_, _ = rw.Write([]byte(sum + "  w.zip\n"))
		default:
			_, _ = rw.Write(buf.Bytes())
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func script(version string) []byte { return []byte("#!/bin/sh\necho " + version + " build 1 abc\n") }

func installed(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in program is a shell script")
	}
	exe := filepath.Join(t.TempDir(), "mangarr-worker")
	if err := os.WriteFile(exe, script("v1.2.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func offerFrom(srv *httptest.Server) Offer {
	return Offer{Version: "v1.3.0", URL: srv.URL + "/w.zip", Checksum: srv.URL + "/w.zip.sha256"}
}

func TestApplyThenConfirm(t *testing.T) {
	exe := installed(t)
	srv := publish(t, script("v1.3.0"), "")
	if err := Apply(context.Background(), srv.Client(), offerFrom(srv), exe, "v1.2.0"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); !bytes.Equal(got, script("v1.3.0")) {
		t.Errorf("the program is %q", got)
	}
	if got, _ := os.ReadFile(exe + ".old"); !bytes.Equal(got, script("v1.2.0")) {
		t.Errorf("the backup is %q", got)
	}
	if back, m, err := Settle(exe); err != nil || back || m.Starts != 1 || m.To != "v1.3.0" || m.From != "v1.2.0" {
		t.Errorf("Settle = %v %+v %v", back, m, err)
	}
	Confirm(exe)
	for _, f := range []string{exe + ".old", exe + ".update"} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s is still there", filepath.Base(f))
		}
	}
	if back, _, _ := Settle(exe); back {
		t.Error("rolled back a confirmed update")
	}
}

// Anything wrong with the download leaves the program as it was.
func TestApplyRefuses(t *testing.T) {
	for name, srv := range map[string]func(*testing.T) *httptest.Server{
		"bad checksum":  func(t *testing.T) *httptest.Server { return publish(t, script("v1.3.0"), strings.Repeat("ab", 32)) },
		"wrong version": func(t *testing.T) *httptest.Server { return publish(t, script("v1.2.9"), "") },
		"not runnable":  func(t *testing.T) *httptest.Server { return publish(t, []byte("\x7fELFgarbage"), "") },
	} {
		t.Run(name, func(t *testing.T) {
			exe := installed(t)
			s := srv(t)
			if err := Apply(context.Background(), s.Client(), offerFrom(s), exe, "v1.2.0"); err == nil {
				t.Fatal("applied")
			}
			if got, _ := os.ReadFile(exe); !bytes.Equal(got, script("v1.2.0")) {
				t.Errorf("the program changed to %q", got)
			}
			entries, _ := os.ReadDir(filepath.Dir(exe))
			if len(entries) != 1 {
				var names []string
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Errorf("left behind: %v", names)
			}
		})
	}
}

// An update that never reaches its server is undone after a few starts,
// and that version is skipped from then on.
func TestSettleRollsBack(t *testing.T) {
	exe := installed(t)
	srv := publish(t, script("v1.3.0"), "")
	if err := Apply(context.Background(), srv.Client(), offerFrom(srv), exe, "v1.2.0"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= maxStarts; i++ {
		if back, _, err := Settle(exe); back || err != nil {
			t.Fatalf("start %d: rolled back %v, %v", i, back, err)
		}
	}
	back, _, err := Settle(exe)
	if !back || err != nil {
		t.Fatalf("Settle = %v, %v; want a rollback", back, err)
	}
	if got, _ := os.ReadFile(exe); !bytes.Equal(got, script("v1.2.0")) {
		t.Errorf("the program is %q after the rollback", got)
	}
	if Skipped(exe) != "v1.3.0" {
		t.Errorf("skipped %q", Skipped(exe))
	}
}
