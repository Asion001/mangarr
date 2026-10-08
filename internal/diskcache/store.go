package diskcache

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/singleflight"
	"time"

	"github.com/Asion001/mangarr/internal/thumbs"
)

// Format is the version of the cached image format; caches written by older
// versions are compacted once (see Store.NeedsCompact).
const Format = "2"

// Normalized are the buckets whose images are resized to JPEG thumbnails.
var Normalized = map[string]bool{"thumbs": true, "covers": true}

// Fetch returns an image body and its content type.
type Fetch func(ctx context.Context) (io.ReadCloser, string, error)

// Store reads and writes the image cache under Root and keeps it under a
// size cap: every write adds to a running total, and going over the cap
// trims the oldest files down to 90% of it in the background.
type Store struct {
	Root string
	// MaxBytes returns the current cap (0 = unlimited).
	MaxBytes func() int64
	Log      *slog.Logger

	size     atomic.Int64
	trimming atomic.Bool
	mu       sync.Mutex // serializes Compact, Clear and Trim
	fetching singleflight.Group
	counts   sync.Map // bucket -> *hitCounter
}

type hitCounter struct{ hits, misses atomic.Int64 }

// HitCount is how often a bucket had what was asked for.
type HitCount struct{ Hits, Misses int64 }

func (s *Store) count(bucket string, hit bool) {
	v, _ := s.counts.LoadOrStore(bucket, &hitCounter{})
	if c := v.(*hitCounter); hit {
		c.hits.Add(1)
	} else {
		c.misses.Add(1)
	}
}

// HitCounts reports hits and misses per bucket since start.
func (s *Store) HitCounts() map[string]HitCount {
	out := map[string]HitCount{}
	s.counts.Range(func(k, v any) bool {
		c := v.(*hitCounter)
		out[k.(string)] = HitCount{Hits: c.hits.Load(), Misses: c.misses.Load()}
		return true
	})
	return out
}

// NewStore opens the cache at root and measures it.
func NewStore(root string, maxBytes func() int64, log *slog.Logger) *Store {
	s := &Store{Root: root, MaxBytes: maxBytes, Log: log}
	s.Measure()
	return s
}

// Measure recomputes the running total from disk.
func (s *Store) Measure() int64 {
	var total int64
	for _, b := range Buckets {
		walk(filepath.Join(s.Root, b), func(f file) { total += f.size })
	}
	s.size.Store(total)
	return total
}

// Size is the running total in bytes.
func (s *Store) Size() int64 { return s.size.Load() }

func (s *Store) path(bucket, key string) string {
	sum := sha1.Sum([]byte(key))
	name := hex.EncodeToString(sum[:])
	return filepath.Join(s.Root, bucket, name[:2], name)
}

// Get returns the cached image for key, fetching it when missing or older
// than ttl. When fetching fails, a stale copy is returned (stale=true).
func (s *Store) Get(ctx context.Context, bucket, key string, ttl time.Duration, fetch Fetch) (data []byte, ct string, stale bool, err error) {
	p := s.path(bucket, key)
	readCached := func() ([]byte, string, bool) {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, "", false
		}
		ct, _ := os.ReadFile(p + ".type")
		return data, string(ct), true
	}
	st, statErr := os.Stat(p)
	if statErr == nil && time.Since(st.ModTime()) < ttl {
		if data, ct, ok := readCached(); ok {
			s.count(bucket, true)
			return data, ct, false, nil
		}
	}
	s.count(bucket, false)
	// one fetch per key: a burst of readers on a cold page (a chapter being
	// streamed to several devices) must not hit the source once each
	type result struct {
		data []byte
		ct   string
	}
	v, err, _ := s.fetching.Do(bucket+"|"+key, func() (any, error) {
		data, ct, err := fetchImage(ctx, fetch)
		if err != nil {
			return nil, err
		}
		if Normalized[bucket] {
			if out, nct, _ := thumbs.Normalize(data, thumbs.MaxWidth, thumbs.Quality); nct != "" {
				data, ct = out, nct
			}
		}
		s.write(p, data, ct)
		return result{data, ct}, nil
	})
	if err != nil {
		if statErr == nil {
			if data, ct, ok := readCached(); ok {
				return data, ct, true, nil
			}
		}
		return nil, "", false, err
	}
	r := v.(result)
	return r.data, r.ct, false, nil
}

func (s *Store) write(p string, data []byte, ct string) {
	if err := os.MkdirAll(filepath.Dir(p), 0o775); err != nil {
		return
	}
	var old int64
	for _, f := range []string{p, p + ".type"} {
		if st, err := os.Stat(f); err == nil {
			old += st.Size()
		}
	}
	if writeAtomic(p+".type", []byte(ct)) != nil || writeAtomic(p, data) != nil {
		return
	}
	s.grow(int64(len(data)+len(ct)) - old)
}

// grow adds to the running total and trims when over the cap.
func (s *Store) grow(n int64) {
	total := s.size.Add(n)
	limit := int64(0)
	if s.MaxBytes != nil {
		limit = s.MaxBytes()
	}
	if limit <= 0 || total <= limit || !s.trimming.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.trimming.Store(false)
		n := s.Trim(0, limit*9/10)
		if s.Log != nil {
			s.Log.Info("image cache over its limit: removed the oldest files", "files", n, "size", s.Size(), "limit", limit)
		}
	}()
}

// Trim removes files older than maxAge, then the oldest until the cache is
// under maxBytes; it returns the files removed.
func (s *Store) Trim(maxAge time.Duration, maxBytes int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := Trim(s.Root, maxAge, maxBytes)
	s.Measure()
	return n
}

// Clear removes buckets (all when empty).
func (s *Store) Clear(buckets []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := Clear(s.Root, buckets)
	s.Measure()
	return err
}

// Stats reports each bucket.
func (s *Store) Stats() []BucketStats { return Stats(s.Root) }

// NeedsCompact reports whether the cache was written by an older version.
func (s *Store) NeedsCompact() bool {
	b, err := os.ReadFile(filepath.Join(s.Root, ".format"))
	if err == nil && strings.TrimSpace(string(b)) == Format {
		return false
	}
	for b := range Normalized {
		if _, err := os.Stat(filepath.Join(s.Root, b)); err == nil {
			return true
		}
	}
	// nothing to convert: mark the (empty) cache as current
	_ = os.MkdirAll(s.Root, 0o775)
	_ = writeAtomic(filepath.Join(s.Root, ".format"), []byte(Format))
	return false
}

// CompactResult reports what Compact did.
type CompactResult struct {
	Files     int   `json:"files"`
	Converted int   `json:"converted"`
	Before    int64 `json:"before"`
	After     int64 `json:"after"`
}

// Compact re-encodes the images of the normalized buckets in place.
func (s *Store) Compact(ctx context.Context, progress func(done int)) (CompactResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var res CompactResult
	for b := range Normalized {
		var files []file
		walk(filepath.Join(s.Root, b), func(f file) {
			if !strings.HasSuffix(f.path, ".type") && !strings.HasPrefix(filepath.Base(f.path), ".tmp-") {
				files = append(files, f)
			}
		})
		for _, f := range files {
			if err := ctx.Err(); err != nil {
				return res, err
			}
			res.Files++
			res.Before += f.size
			data, err := os.ReadFile(f.path)
			if err != nil {
				continue
			}
			out, ct, changed := thumbs.Normalize(data, thumbs.MaxWidth, thumbs.Quality)
			if changed {
				// keep the entry's age so the TTL and eviction order stay
				if writeAtomic(f.path, out) == nil {
					_ = writeAtomic(f.path+".type", []byte(ct))
					_ = os.Chtimes(f.path, f.mod, f.mod)
					_ = os.Chtimes(f.path+".type", f.mod, f.mod)
					res.Converted++
					data = out
				}
			}
			res.After += int64(len(data))
			if progress != nil && res.Files%50 == 0 {
				progress(res.Files)
			}
		}
	}
	s.Measure()
	err := writeAtomic(filepath.Join(s.Root, ".format"), []byte(Format))
	return res, err
}

func fetchImage(ctx context.Context, fetch Fetch) ([]byte, string, error) {
	body, ct, err := fetch(ctx)
	if err != nil {
		return nil, "", err
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, 20<<20))
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 {
		return nil, "", errors.New("empty image")
	}
	if ct == "" || ct == "application/octet-stream" {
		ct = http.DetectContentType(data)
	}
	return data, ct, nil
}

// writeAtomic writes via a temporary file so readers never see partial files.
func writeAtomic(p string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return err
	}
	_ = os.Chmod(f.Name(), 0o664)
	return os.Rename(f.Name(), p)
}
