// Package cbz writes and reads CBZ archives. Writes are atomic: the archive
// is written to "<name>.cbz.partial" in the destination folder (an extension
// neither Komga nor Kavita scans), fsynced, then renamed over the final path.
package cbz

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const PartialSuffix = ".partial"

type Page struct {
	// Name inside the archive, e.g. "0001.jpg".
	Name string
	// Path of the page on disk (used when Data is nil).
	Path string
	Data []byte
}

type Result struct {
	Size   int64
	SHA256 string
}

// Write creates dst atomically with ComicInfo.xml (if non-nil) and pages.
func Write(dst string, pages []Page, comicInfo []byte, mode fs.FileMode, modTime time.Time) (Result, error) {
	if mode == 0 {
		mode = 0o664
	}
	if modTime.IsZero() {
		modTime = time.Now()
	}
	tmp := dst + PartialSuffix
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return Result{}, err
	}
	cleanup := func() { _ = f.Close(); _ = os.Remove(tmp) }

	h := sha256.New()
	w := zip.NewWriter(io.MultiWriter(f, h))
	add := func(name string, r io.Reader) error {
		fw, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store, Modified: modTime})
		if err != nil {
			return err
		}
		_, err = io.Copy(fw, r)
		return err
	}
	if comicInfo != nil {
		if err := add("ComicInfo.xml", strings.NewReader(string(comicInfo))); err != nil {
			cleanup()
			return Result{}, err
		}
	}
	sorted := append([]Page(nil), pages...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, p := range sorted {
		var err error
		if p.Data != nil {
			err = add(p.Name, strings.NewReader(string(p.Data)))
		} else {
			var pf *os.File
			pf, err = os.Open(p.Path)
			if err == nil {
				err = add(p.Name, pf)
				_ = pf.Close()
			}
		}
		if err != nil {
			cleanup()
			return Result{}, fmt.Errorf("add %s: %w", p.Name, err)
		}
	}
	if err := w.Close(); err != nil {
		cleanup()
		return Result{}, err
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return Result{}, err
	}
	st, err := f.Stat()
	if err != nil {
		cleanup()
		return Result{}, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return Result{}, err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return Result{}, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return Result{}, err
	}
	syncDir(filepath.Dir(dst))
	return Result{Size: st.Size(), SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}

// Read loads all pages (sorted) and ComicInfo.xml from a CBZ.
func Read(path string) (pages []Page, comicInfo []byte, err error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, nil, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, nil, err
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, nil, err
		}
		if strings.EqualFold(filepath.Base(f.Name), "ComicInfo.xml") {
			comicInfo = data
			continue
		}
		if isImageName(f.Name) {
			pages = append(pages, Page{Name: filepath.Base(f.Name), Data: data})
		}
	}
	sort.SliceStable(pages, func(i, j int) bool { return pages[i].Name < pages[j].Name })
	return pages, comicInfo, nil
}

func isImageName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".jxl", ".bmp":
		return true
	}
	return false
}

// PageName returns the archive name for page index i (0-based) and extension.
func PageName(i int, ext string) string { return fmt.Sprintf("%04d%s", i+1, ext) }

// Entry is a page image in a CBZ.
type Entry struct {
	// Name is the page's base name; Path its path inside the archive.
	Name string
	Path string
	Size int64
	// one past where a stored (uncompressed) entry's bytes start, so
	// OpenPage can read them without parsing the archive again; 0 when
	// compressed or unknown
	dataAt int64
}

// List lists the page images of a CBZ (sorted like Read) without reading them.
func List(path string) ([]Entry, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var out []Entry
	for _, f := range zr.File {
		if !f.FileInfo().IsDir() && isImageName(f.Name) {
			e := Entry{Name: filepath.Base(f.Name), Path: f.Name, Size: int64(f.UncompressedSize64)}
			if f.Method == zip.Store && f.CompressedSize64 == f.UncompressedSize64 {
				if off, err := f.DataOffset(); err == nil {
					e.dataAt = off + 1
				}
			}
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ReadEntry reads one file of a CBZ by its path inside the archive.
func ReadEntry(path, name string) ([]byte, error) {
	rc, _, err := OpenEntry(path, name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 128<<20))
}

// OpenEntry opens one file of a CBZ for reading and reports its size, so a
// page can be streamed to a reader instead of being held in memory. Closing
// the reader closes the archive.
func OpenEntry(path, name string) (io.ReadCloser, int64, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, 0, err
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			zr.Close()
			return nil, 0, err
		}
		return entryReader{rc, zr}, int64(f.UncompressedSize64), nil
	}
	zr.Close()
	return nil, 0, fs.ErrNotExist
}

// OpenPage opens an entry from Entries or List. Page images are usually
// stored uncompressed, and those are read straight from the file at their
// offset, without parsing the archive's directory again.
func OpenPage(path string, e Entry) (io.ReadCloser, int64, error) {
	if e.dataAt == 0 {
		return OpenEntry(path, e.Path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	return sectionReader{io.NewSectionReader(f, e.dataAt-1, e.Size), f}, e.Size, nil
}

type sectionReader struct {
	*io.SectionReader
	f *os.File
}

func (r sectionReader) Close() error { return r.f.Close() }

// entryReader closes the archive with the entry.
type entryReader struct {
	io.ReadCloser
	zr *zip.ReadCloser
}

func (e entryReader) Close() error {
	err := e.ReadCloser.Close()
	if zerr := e.zr.Close(); err == nil {
		err = zerr
	}
	return err
}

// listCache remembers the page list of recently read archives, so serving a
// page opens the file once instead of once to list it and once to read it.
var listCache = struct {
	sync.Mutex
	m map[string]cachedList
}{m: map[string]cachedList{}}

type cachedList struct {
	entries []Entry
	mod     time.Time
	size    int64
	used    time.Time
}

const listCacheMax = 256

// Entries is List with a cache keyed by the file's size and modification
// time, so a rewritten archive (an upgrade, a re-encode) is read again.
func Entries(path string) ([]Entry, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	listCache.Lock()
	if c, ok := listCache.m[path]; ok && c.mod.Equal(st.ModTime()) && c.size == st.Size() {
		c.used = time.Now()
		listCache.m[path] = c
		listCache.Unlock()
		return c.entries, nil
	}
	listCache.Unlock()

	entries, err := List(path)
	if err != nil {
		return nil, err
	}
	listCache.Lock()
	defer listCache.Unlock()
	if len(listCache.m) >= listCacheMax {
		oldest, at := "", time.Now()
		for k, c := range listCache.m {
			if c.used.Before(at) {
				oldest, at = k, c.used
			}
		}
		delete(listCache.m, oldest)
	}
	listCache.m[path] = cachedList{entries: entries, mod: st.ModTime(), size: st.Size(), used: time.Now()}
	return entries, nil
}
