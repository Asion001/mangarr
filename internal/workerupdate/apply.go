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
//	encoders.old           the encoders folder it replaced, likewise
const (
	oldSuffix    = ".old"
	newSuffix    = ".new"
	markerSuffix = ".update"
	skipSuffix   = ".skip"
)

// encoders is the folder of native encoders the zip carries next to the
// program (scripts/worker-zip.sh), replaced along with it. The upscalers
// are left as they are: they change rarely and are far larger.
const encoders = "encoders"

// maxStarts is how often an updated program may start without connecting
// before the one it replaced is put back.
const maxStarts = 3

// maxZip bounds a download: a worker zip is a few tens of MB.
const maxZip = 1 << 30

// Apply downloads the offered zip, checks it against its published
// checksum, takes the program and its encoders folder out of it and puts
// them in place of exe, which runs version current, and the encoders next
// to it. What they replace is kept (exe.old, encoders.old) until Confirm.
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
	st, err := tmp.Stat()
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(tmp, st.Size())
	if err != nil {
		return fmt.Errorf("the download isn't a zip: %w", err)
	}
	next := exe + newSuffix
	staged := filepath.Join(dir, encoders+newSuffix)
	cleanup := func() {
		os.Remove(next)
		os.RemoveAll(staged)
	}
	if err := extract(zr, filepath.Base(exe), next); err != nil {
		cleanup()
		return err
	}
	found, err := extractFolder(zr, encoders, staged)
	if err != nil {
		cleanup()
		return fmt.Errorf("the encoders: %w", err)
	}
	if !found {
		staged = "" // a zip without them leaves the folder as it is
	}
	if err := smokeTest(ctx, next, o.Version); err != nil {
		cleanup()
		return err
	}
	return swap(exe, next, staged, Marker{From: current, To: o.Version})
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
func extract(zr *zip.Reader, name, dst string) error {
	want := strings.TrimSuffix(name, ".exe")
	for _, zf := range zr.File {
		base := strings.TrimSuffix(filepath.Base(filepath.FromSlash(zf.Name)), ".exe")
		if zf.FileInfo().IsDir() || base != want || strings.Count(zf.Name, "/") > 1 {
			continue
		}
		return writeFile(zf, dst)
	}
	return fmt.Errorf("the zip has no %s in it", name)
}

// extractFolder writes the files of the zip's top-level folder (inside its
// one top folder, as the release lays it out) into dst, and reports whether
// it had any.
func extractFolder(zr *zip.Reader, folder, dst string) (bool, error) {
	if err := os.RemoveAll(dst); err != nil {
		return false, err
	}
	found := false
	for _, zf := range zr.File {
		parts := strings.Split(zf.Name, "/")
		if zf.FileInfo().IsDir() || len(parts) != 3 || parts[1] != folder || parts[2] == "" ||
			parts[2] == "." || parts[2] == ".." || strings.ContainsAny(parts[2], `\:`) {
			continue
		}
		if !found {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return false, err
			}
			found = true
		}
		if err := writeFile(zf, filepath.Join(dst, parts[2])); err != nil {
			return false, err
		}
	}
	return found, nil
}

// writeFile writes one zip entry to dst as a program anyone may run.
func writeFile(zf *zip.File, dst string) error {
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

// swap puts next in place of exe, keeping exe as exe.old, and the staged
// encoders folder (when there is one) in place of the one next to it. A
// running program can be renamed on every platform (Windows too), just not
// overwritten; no encoder runs now, as the worker has finished its tasks.
func swap(exe, next, staged string, m Marker) error {
	if staged != "" {
		defer os.RemoveAll(staged) // left only when something failed
		if err := swapFolder(staged, filepath.Join(filepath.Dir(exe), encoders)); err != nil {
			os.Remove(next)
			return fmt.Errorf("the encoders: %w", err)
		}
	}
	undoFolder := func() {
		if staged != "" {
			restoreFolder(filepath.Join(filepath.Dir(exe), encoders))
		}
	}
	old := exe + oldSuffix
	if err := os.Remove(old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("the previous backup: %w", err)
	}
	if err := os.Rename(exe, old); err != nil {
		os.Remove(next)
		undoFolder()
		return err
	}
	if err := os.Rename(next, exe); err != nil {
		_ = os.Rename(old, exe)
		os.Remove(next)
		undoFolder()
		return err
	}
	if err := writeMarker(exe, m); err != nil {
		// without the marker a bad update couldn't be undone: undo it now
		_ = os.Rename(exe, next)
		_ = os.Rename(old, exe)
		os.Remove(next)
		undoFolder()
		return err
	}
	return nil
}

// swapFolder puts staged in place of dir, keeping dir as dir.old.
func swapFolder(staged, dir string) error {
	old := dir + oldSuffix
	if err := os.RemoveAll(old); err != nil {
		return err
	}
	if err := os.Rename(dir, old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Rename(staged, dir); err != nil {
		_ = os.Rename(old, dir)
		return err
	}
	return nil
}

// restoreFolder puts dir.old back in place of dir; without a dir.old (the
// program before had none) the folder stays, which that program ignores.
func restoreFolder(dir string) {
	old := dir + oldSuffix
	if _, err := os.Stat(old); err != nil {
		return
	}
	failed := dir + ".failed"
	_ = os.RemoveAll(failed)
	if err := os.Rename(dir, failed); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err := os.Rename(old, dir); err != nil {
		_ = os.Rename(failed, dir)
		return
	}
	_ = os.RemoveAll(failed)
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
	restoreFolder(filepath.Join(filepath.Dir(exe), encoders))
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
	os.RemoveAll(filepath.Join(filepath.Dir(exe), encoders+oldSuffix))
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
