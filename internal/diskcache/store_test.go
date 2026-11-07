package diskcache

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func bigPNG(seed int64) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 1200, 1800))
	r := rand.New(rand.NewSource(seed))
	for i := range img.Pix {
		img.Pix[i] = uint8(r.Intn(256))
	}
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func fetchBytes(data []byte) Fetch {
	return func(context.Context) (io.ReadCloser, string, error) {
		return io.NopCloser(bytes.NewReader(data)), "image/png", nil
	}
}

func TestStoreNormalizesThumbs(t *testing.T) {
	s := NewStore(t.TempDir(), nil, nil)
	in := bigPNG(1)
	data, ct, stale, err := s.Get(context.Background(), "thumbs", "k", time.Hour, fetchBytes(in))
	if err != nil || stale || ct != "image/jpeg" || len(data) >= len(in)/4 {
		t.Fatalf("got %s %d bytes (from %d), stale=%v err=%v", ct, len(data), len(in), stale, err)
	}
	// icons are kept as they are
	data, ct, _, _ = s.Get(context.Background(), "assets", "k", time.Hour, fetchBytes(in))
	if ct != "image/png" || len(data) != len(in) {
		t.Fatalf("assets changed: %s %d", ct, len(data))
	}
	if s.Size() != s.Measure() {
		t.Fatal("running size is off")
	}
	// served from cache, then stale when the source fails after the ttl
	failing := func(context.Context) (io.ReadCloser, string, error) { return nil, "", errors.New("down") }
	if _, _, _, err := s.Get(context.Background(), "thumbs", "k", time.Hour, failing); err != nil {
		t.Fatal(err)
	}
	if _, _, stale, err := s.Get(context.Background(), "thumbs", "k", 0, failing); err != nil || !stale {
		t.Fatalf("stale=%v err=%v", stale, err)
	}
}

func TestStoreTrimsOverCap(t *testing.T) {
	cap := int64(0)
	s := NewStore(t.TempDir(), func() int64 { return cap }, nil)
	one := bigPNG(2)
	for i := 0; i < 6; i++ {
		_, _, _, _ = s.Get(context.Background(), "assets", string(rune('a'+i)), time.Hour, fetchBytes(one))
		time.Sleep(10 * time.Millisecond) // distinct modification times
	}
	full := s.Size()
	cap = full / 2
	_, _, _, _ = s.Get(context.Background(), "assets", "trigger", time.Hour, fetchBytes(one))
	deadline := time.Now().Add(5 * time.Second)
	for s.trimming.Load() || s.Size() > cap {
		if time.Now().After(deadline) {
			t.Fatalf("size %d still over cap %d", s.Size(), cap)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.Size() > cap*9/10 || s.Size() != s.Measure() {
		t.Fatalf("size %d, want ≤ %d", s.Size(), cap*9/10)
	}
}

func TestStoreCompact(t *testing.T) {
	root := t.TempDir()
	// an old-format cache entry: a full-size PNG in thumbs
	dir := filepath.Join(root, "thumbs", "ab")
	_ = os.MkdirAll(dir, 0o755)
	in := bigPNG(3)
	_ = os.WriteFile(filepath.Join(dir, "abcdef"), in, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "abcdef.type"), []byte("image/png"), 0o644)
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(filepath.Join(dir, "abcdef"), old, old)
	s := NewStore(root, nil, nil)
	if !s.NeedsCompact() {
		t.Fatal("old cache should need compacting")
	}
	res, err := s.Compact(context.Background(), nil)
	if err != nil || res.Files != 1 || res.Converted != 1 || res.After >= res.Before {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	ct, _ := os.ReadFile(filepath.Join(dir, "abcdef.type"))
	st, _ := os.Stat(filepath.Join(dir, "abcdef"))
	if string(ct) != "image/jpeg" || st.ModTime().Sub(old).Abs() > time.Second {
		t.Fatalf("type %s, mtime %v", ct, st.ModTime())
	}
	if s.NeedsCompact() {
		t.Fatal("compacted cache still needs compacting")
	}
	// the marker survives trimming by age
	s.Trim(time.Hour, 0)
	if b, _ := os.ReadFile(filepath.Join(root, ".format")); strings.TrimSpace(string(b)) != Format {
		t.Fatal("format marker was trimmed")
	}
}

// pageBytes starts like a JPEG (the store only keeps images).
var pageBytes = []byte("\xFF\xD8\xFF\xE0\x00\x10JFIF\x00page-bytes")

// TestStoreRefusesNonImages: a remote server's word for a type counts for
// nothing, and HTML or SVG never gets in.
func TestStoreRefusesNonImages(t *testing.T) {
	s := NewStore(t.TempDir(), nil, nil)
	for _, body := range []string{"<html><script>alert(1)</script>", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`} {
		_, _, _, err := s.Get(context.Background(), "covers", body, time.Hour, func(context.Context) (io.ReadCloser, string, error) {
			return io.NopCloser(strings.NewReader(body)), "image/jpeg", nil
		})
		if err == nil {
			t.Errorf("%q was kept", body)
		}
	}
	// a remote server can't pass its answer off as a note of mangarr's own
	if _, _, _, err := s.Get(context.Background(), "covers", "marker", time.Hour, func(context.Context) (io.ReadCloser, string, error) {
		return io.NopCloser(strings.NewReader("<script>alert(1)</script>")), MarkerPrefix + "original", nil
	}); err == nil {
		t.Error("a marker type was taken from a cover fetch")
	}
	data, ct, _, err := s.Get(context.Background(), "pages", "x", time.Hour, func(context.Context) (io.ReadCloser, string, error) {
		return io.NopCloser(bytes.NewReader(pageBytes)), "text/html", nil
	})
	if err != nil || ct != "image/jpeg" || !bytes.Equal(data, pageBytes) {
		t.Fatalf("a JPEG called text/html: %v %q", err, ct)
	}
}

// TestStoreFetchesOncePerKey: a burst of readers on a cold page (a chapter
// streamed to several devices at once) makes one fetch, not one each.
func TestStoreFetchesOncePerKey(t *testing.T) {
	s := NewStore(t.TempDir(), nil, nil)
	var fetches atomic.Int32
	fetch := func(ctx context.Context) (io.ReadCloser, string, error) {
		fetches.Add(1)
		time.Sleep(20 * time.Millisecond) // long enough for the others to pile up
		return io.NopCloser(bytes.NewReader(pageBytes)), "image/jpeg", nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, ct, _, err := s.Get(context.Background(), "pages", "ch1|7", time.Hour, fetch)
			if err != nil || !bytes.Equal(data, pageBytes) || ct != "image/jpeg" {
				t.Errorf("get: %v %q %q", err, data, ct)
			}
		}()
	}
	wg.Wait()
	if n := fetches.Load(); n != 1 {
		t.Fatalf("fetched %d times", n)
	}
}
