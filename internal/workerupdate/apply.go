package workerupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The files kept next to the program while an update settles:
//
//	mangarr-worker.old     the program it replaced, until the new one connects
//	mangarr-worker.update  which update that was, and how often it started
//	mangarr-worker.skip    a version that was rolled back, not tried again
const (
	oldSuffix    = ".old"
	newSuffix    = ".new"
	markerSuffix = ".update"
	skipSuffix   = ".skip"
)

// maxStarts is how often an updated program may start without connecting
// before the one it replaced is put back.
const maxStarts = 3

// maxZip bounds a download: a worker zip is a few tens of MB.
const maxZip = 1 << 30

// Apply downloads the offered zip, checks it against its published
// checksum, takes the program out of it and puts it in place of exe, which
// runs version current. exe itself is kept as exe.old until Confirm.
// Nothing is changed when any step fails.
func Apply(ctx context.Context, client *http.Client, o Offer, exe, current string) error {
	if o.URL == "" || o.Checksum == "" {
		return errors.New("no worker zip is published for this platform")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	want, err := fetchChecksum(ctx, client, o.Checksum)
	if err != nil {
		return fmt.Errorf("the checksum: %w", err)
	}
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".mangarr-worker-*.zip")
	if err != nil {
		return fmt.Errorf("the program's folder isn't writable: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	got, err := download(ctx, client, o.URL, tmp)
	if err != nil {
		return fmt.Errorf("the download: %w", err)
	}
	if got != want {
		return fmt.Errorf("the download doesn't match its checksum (%s, expected %s)", got, want)
	}
	next := exe + newSuffix
	if err := extract(tmp, filepath.Base(exe), next); err != nil {
		os.Remove(next)
		return err
	}
	if err := smokeTest(ctx, next, o.Version); err != nil {
		os.Remove(next)
		return err
	}
	return swap(exe, next, Marker{From: current, To: o.Version})
}

func fetchChecksum(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return "", err
	}
	// "<hex>  <name>", as sha256sum writes it, or the hex alone
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "", errors.New("it is empty")
	}
	sum := strings.ToLower(fields[0])
	if b, err := hex.DecodeString(sum); err != nil || len(b) != sha256.Size {
		return "", fmt.Errorf("%q is not a SHA-256", fields[0])
	}
	return sum, nil
}

func download(ctx context.Context, client *http.Client, url string, to *os.File) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", url, resp.Status)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(to, h), io.LimitReader(resp.Body, maxZip+1))
	if err != nil {
		return "", err
	}
	if n > maxZip {
		return "", errors.New("it is far larger than a worker zip")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extract writes the zip's program (the file named like the running one)
// to dst.
func extract(f *os.File, name, dst string) error {
	st, err := f.Stat()
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		return fmt.Errorf("the download isn't a zip: %w", err)
	}
	want := strings.TrimSuffix(name, ".exe")
	for _, zf := range zr.File {
		base := strings.TrimSuffix(filepath.Base(filepath.FromSlash(zf.Name)), ".exe")
		if zf.FileInfo().IsDir() || base != want || strings.Count(zf.Name, "/") > 1 {
			continue
		}
		in, err := zf.Open()
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, io.LimitReader(in, maxZip)); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
	return fmt.Errorf("the zip has no %s in it", name)
}

// smokeTest runs the new program's `version` and checks it says what it
// was offered as: a program for another platform, or a broken one, never
// replaces the one that works.
func smokeTest(ctx context.Context, path, version string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return fmt.Errorf("the new program doesn't run here: %w", err)
	}
	if got := strings.Fields(string(out)); len(got) == 0 || got[0] != version {
		return fmt.Errorf("the new program says it is %q, not %s", strings.TrimSpace(string(out)), version)
	}
	return nil
}

// swap puts next in place of exe, keeping exe as exe.old. A running
// program can be renamed on every platform (Windows too), just not
// overwritten.
func swap(exe, next string, m Marker) error {
	old := exe + oldSuffix
	if err := os.Remove(old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("the previous backup: %w", err)
	}
	if err := os.Rename(exe, old); err != nil {
		os.Remove(next)
		return err
	}
	if err := os.Rename(next, exe); err != nil {
		_ = os.Rename(old, exe)
		os.Remove(next)
		return err
	}
	if err := writeMarker(exe, m); err != nil {
		// without the marker a bad update couldn't be undone: undo it now
		_ = os.Rename(exe, next)
		_ = os.Rename(old, exe)
		os.Remove(next)
		return err
	}
	return nil
}

// Marker records an update that hasn't connected yet.
type Marker struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Starts int    `json:"starts"`
}

func writeMarker(exe string, m Marker) error {
	data, _ := json.Marshal(m)
	return os.WriteFile(exe+markerSuffix, data, 0o644)
}

// Settle runs when the program starts. After an update it counts the
// start; when the new program has started maxStarts times without
// connecting to its server, it puts the old program back, remembers not to
// take that version again and reports rolledBack, and the caller restarts
// into the old program.
func Settle(exe string) (rolledBack bool, m Marker, err error) {
	data, err := os.ReadFile(exe + markerSuffix)
	if errors.Is(err, fs.ErrNotExist) {
		return false, m, nil
	}
	if err != nil {
		return false, m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		os.Remove(exe + markerSuffix)
		return false, m, nil
	}
	m.Starts++
	if m.Starts <= maxStarts {
		return false, m, writeMarker(exe, m)
	}
	old := exe + oldSuffix
	if _, err := os.Stat(old); err != nil {
		os.Remove(exe + markerSuffix)
		return false, m, fmt.Errorf("the program before the update is gone: %w", err)
	}
	bad := exe + ".failed"
	os.Remove(bad)
	if err := os.Rename(exe, bad); err != nil {
		return false, m, err
	}
	if err := os.Rename(old, exe); err != nil {
		_ = os.Rename(bad, exe)
		return false, m, err
	}
	os.Remove(bad) // may fail on Windows while it runs; harmless
	os.Remove(exe + markerSuffix)
	_ = os.WriteFile(exe+skipSuffix, []byte(m.To+"\n"), 0o644)
	return true, m, nil
}

// Confirm is called once the program has connected: the update worked, so
// what it replaced and the record of it go.
func Confirm(exe string) {
	if _, err := os.Stat(exe + markerSuffix); err != nil {
		return
	}
	os.Remove(exe + markerSuffix)
	os.Remove(exe + oldSuffix)
	os.Remove(exe + ".failed")
}

// Skipped is the version that was rolled back on this machine, which is not
// taken again ("" when there is none).
func Skipped(exe string) string {
	data, err := os.ReadFile(exe + skipSuffix)
	if err != nil {
		return ""
	}
	return string(bytes.TrimSpace(data))
}
