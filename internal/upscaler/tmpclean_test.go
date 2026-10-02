package upscaler

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSweepTmp: work folders left behind by a restart are removed; ones in
// use, recently written, or not the upscaler's own stay.
func TestSweepTmp(t *testing.T) {
	tmp := t.TempDir()
	// the sweep NewServer starts runs on an empty folder, not this test's
	s := NewServer(Config{TmpDir: t.TempDir()}, fakeRunner{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	old := time.Now().Add(-2 * staleAfter)
	mk := func(name string, mtime time.Time) string {
		dir := filepath.Join(tmp, name)
		for _, d := range []string{filepath.Join(dir, "in"), filepath.Join(dir, "out")} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(d, mtime, mtime); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chtimes(dir, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	left := mk("upscale-1", old)
	busy := mk("upscale-2", old)
	s.active.Store(busy, struct{}{})
	fresh := mk("upscale-3", time.Now())
	// the engine still writing pages out keeps an old folder alive
	writing := mk("upscale-4", old)
	if err := os.Chtimes(filepath.Join(writing, "out"), time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	other := mk("chapter-5", old)

	s.sweepTmp(tmp, time.Now())

	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Errorf("left-behind folder still there: %v", err)
	}
	for _, dir := range []string{busy, fresh, writing, other} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s removed: %v", filepath.Base(dir), err)
		}
	}
}
