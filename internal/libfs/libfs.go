// Package libfs is where the library's files live: the root folders, the
// series folders with their CBZ archives and sidecars, and the recycle bin.
//
// By default that is the local disk, and every function here behaves like
// its counterpart in package os. With S3-compatible object storage
// configured, the folders mounted on it (see Mount) are kept as objects
// instead: a path such as /data/manga/Series/0001.cbz becomes the object key
// data/manga/Series/0001.cbz, so the paths stored in the database stay the
// same whichever backend holds the files. Everything else (the database,
// caches, download staging) stays on the local disk.
package libfs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// File is an open library file. Reads may be sequential (Read, Seek) or at
// any offset (ReadAt), so a CBZ can be opened as a zip without reading all
// of it.
type File interface {
	io.Reader
	io.ReaderAt
	io.Seeker
	io.Closer
	Stat() (fs.FileInfo, error)
}

// Writer writes a new file. Nothing is visible at the path until Commit
// succeeds; Abort (safe after Commit) throws the data away.
type Writer interface {
	io.Writer
	// Commit publishes the file and reports its size.
	Commit() (int64, error)
	Abort()
}

// FS is one storage backend. Paths are absolute, cleaned local-style paths.
type FS interface {
	Open(name string) (File, error)
	// OpenRange reads n bytes from off, in one request where that matters.
	OpenRange(name string, off, n int64) (io.ReadCloser, error)
	Stat(name string) (fs.FileInfo, error)
	Create(name string, mode fs.FileMode) (Writer, error)
	// Remove deletes a file or an empty folder.
	Remove(name string) error
	RemoveAll(name string) error
	// Rename moves a file or folder within this backend, replacing a file.
	Rename(from, to string) error
	MkdirAll(name string, mode fs.FileMode) error
	ReadDir(name string) ([]fs.DirEntry, error)
	// Local reports whether paths are real files on this machine.
	Local() bool
}

type mount struct {
	prefix string
	fs     FS
}

var state = struct {
	sync.RWMutex
	remote FS
	mounts []mount // longest prefix first
}{}

var local FS = Local{}

// Use makes fs the backend of the folders Mount names from now on; nil
// goes back to the local disk for everything.
func Use(remote FS) {
	state.Lock()
	defer state.Unlock()
	state.remote = remote
	state.mounts = nil
}

// Remote returns the object storage backend, or nil when everything is
// local.
func Remote() FS {
	state.RLock()
	defer state.RUnlock()
	return state.remote
}

// Mount keeps the folder dir (and everything in it) on the object storage
// set with Use. It does nothing while there is none.
func Mount(dir string) {
	dir = clean(dir)
	if dir == "" {
		return
	}
	state.Lock()
	defer state.Unlock()
	if state.remote == nil {
		return
	}
	for _, m := range state.mounts {
		if m.prefix == dir {
			return
		}
	}
	state.mounts = append(state.mounts, mount{dir, state.remote})
	sort.SliceStable(state.mounts, func(i, j int) bool { return len(state.mounts[i].prefix) > len(state.mounts[j].prefix) })
}

// Mounts lists the folders kept on object storage.
func Mounts() []string {
	state.RLock()
	defer state.RUnlock()
	out := make([]string, 0, len(state.mounts))
	for _, m := range state.mounts {
		out = append(out, m.prefix)
	}
	sort.Strings(out)
	return out
}

func clean(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return filepath.Clean(p)
}

// within reports whether p is dir or inside it.
func within(p, dir string) bool {
	if p == dir {
		return true
	}
	if dir == string(filepath.Separator) {
		return strings.HasPrefix(p, dir)
	}
	return strings.HasPrefix(p, dir+string(filepath.Separator))
}

// For returns the backend that holds path.
func For(path string) FS {
	state.RLock()
	defer state.RUnlock()
	if len(state.mounts) == 0 {
		return local
	}
	p := clean(path)
	for _, m := range state.mounts {
		if within(p, m.prefix) {
			return m.fs
		}
	}
	return local
}

// IsLocal reports whether path is a real file on this machine.
func IsLocal(path string) bool { return For(path).Local() }

func Open(name string) (File, error)        { return For(name).Open(name) }
func Stat(name string) (fs.FileInfo, error) { return For(name).Stat(name) }
func Remove(name string) error              { return For(name).Remove(name) }
func RemoveAll(name string) error           { return For(name).RemoveAll(name) }
func ReadDir(name string) ([]fs.DirEntry, error) {
	return For(name).ReadDir(name)
}
func MkdirAll(name string, mode fs.FileMode) error { return For(name).MkdirAll(name, mode) }

// OpenRange reads n bytes of name from off.
func OpenRange(name string, off, n int64) (io.ReadCloser, error) {
	return For(name).OpenRange(name, off, n)
}

// Create starts writing a new file (see Writer); the folder must exist on
// the local disk.
func Create(name string, mode fs.FileMode) (Writer, error) {
	if mode == 0 {
		mode = 0o664
	}
	return For(name).Create(name, mode)
}

// Exists reports whether name is there (file or folder).
func Exists(name string) bool {
	_, err := Stat(name)
	return err == nil
}

// ReadFile reads a whole file.
func ReadFile(name string) ([]byte, error) {
	f, err := Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// WriteFile replaces name with data atomically.
func WriteFile(name string, data []byte, mode fs.FileMode) error {
	w, err := Create(name, mode)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		w.Abort()
		return err
	}
	_, err = w.Commit()
	return err
}

// Rename moves a file or folder, also from one backend to the other (by
// copying, then deleting the source).
func Rename(from, to string) error {
	src, dst := For(from), For(to)
	if src == dst {
		return src.Rename(from, to)
	}
	st, err := src.Stat(from)
	if err != nil {
		return err
	}
	if st.IsDir() {
		if err := CopyTree(from, to); err != nil {
			return err
		}
		return src.RemoveAll(from)
	}
	if err := CopyFile(from, to, st.Mode().Perm()); err != nil {
		return err
	}
	return src.Remove(from)
}

// CopyFile copies one file, replacing dst atomically.
func CopyFile(from, to string, mode fs.FileMode) error {
	if s, ok := For(from).(*S3); ok && For(to) == FS(s) {
		return s.Copy(from, to) // without downloading it
	}
	in, err := Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	if mode == 0 {
		if st, err := in.Stat(); err == nil {
			mode = st.Mode().Perm()
		}
	}
	w, err := Create(to, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, in); err != nil {
		w.Abort()
		return err
	}
	_, err = w.Commit()
	return err
}

// CopyTree copies the folder from into to, file by file.
func CopyTree(from, to string) error {
	return WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		if d.IsDir() {
			return MkdirAll(target, 0o775)
		}
		if !d.Type().IsRegular() {
			return nil // symlinks and special files
		}
		return CopyFile(p, target, 0)
	})
}

// WalkDir walks the tree at root like filepath.WalkDir, in lexical order.
func WalkDir(root string, fn fs.WalkDirFunc) error {
	if For(root).Local() {
		return filepath.WalkDir(root, fn)
	}
	st, err := Stat(root)
	if err != nil {
		return fn(root, nil, err)
	}
	err = walk(root, fs.FileInfoToDirEntry(st), fn)
	if errors.Is(err, filepath.SkipDir) || errors.Is(err, filepath.SkipAll) {
		return nil
	}
	return err
}

func walk(p string, d fs.DirEntry, fn fs.WalkDirFunc) error {
	if err := fn(p, d, nil); err != nil || !d.IsDir() {
		if errors.Is(err, filepath.SkipDir) && d.IsDir() {
			return nil
		}
		return err
	}
	entries, err := ReadDir(p)
	if err != nil {
		if err := fn(p, d, err); err != nil {
			if errors.Is(err, filepath.SkipDir) {
				return nil
			}
			return err
		}
	}
	for _, e := range entries {
		if err := walk(filepath.Join(p, e.Name()), e, fn); err != nil {
			if errors.Is(err, filepath.SkipDir) {
				if e.IsDir() {
					continue
				}
				return nil // skip the rest of this folder
			}
			return err
		}
	}
	return nil
}

// Writable checks that dir exists (or, on object storage, can be written)
// and a file can be created in it.
func Writable(dir string) error {
	b := For(dir)
	if b.Local() {
		st, err := os.Stat(dir)
		if err != nil {
			return err
		}
		if !st.IsDir() {
			return errors.New("not a directory")
		}
		f, err := os.CreateTemp(dir, ".mangarr-write-test-*")
		if err != nil {
			return err
		}
		name := f.Name()
		_ = f.Close()
		return os.Remove(name)
	}
	probe := filepath.Join(dir, ".mangarr-write-test")
	if err := WriteFile(probe, []byte("ok"), 0o664); err != nil {
		return err
	}
	return Remove(probe)
}

// RemoveEmptyDirs deletes the empty folders under root (not root itself).
// Object storage has no empty folders, so there it does nothing.
func RemoveEmptyDirs(root string) {
	if !IsLocal(root) {
		return
	}
	var dirs []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && p != root {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i]) // only succeeds when empty
	}
}

// Lstat is Stat that does not follow a symbolic link on the local disk
// (object storage has none).
func Lstat(name string) (fs.FileInfo, error) {
	if b := For(name); !b.Local() {
		return b.Stat(name)
	}
	return os.Lstat(name)
}

// DownloadURL is a signed link to download path straight from object
// storage, so a whole chapter doesn't pass through the server; ok is false
// for a local file or with redirects turned off.
func DownloadURL(path string, ttl time.Duration, download string) (url string, ok bool) {
	s, isS3 := For(path).(*S3)
	if !isS3 || s.c.cfg.NoRedirect {
		return "", false
	}
	return s.SignedURL(path, ttl, download), true
}
