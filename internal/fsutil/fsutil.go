// Package fsutil has small filesystem helpers. Paths in the library may be
// on object storage (see libfs); the helpers that take them go through it.
package fsutil

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Asion001/mangarr/internal/libfs"
)

// Writable checks that dir exists and a file can be created in it.
func Writable(dir string) error { return libfs.Writable(dir) }

func local(paths ...string) bool {
	for _, p := range paths {
		if !libfs.IsLocal(p) {
			return false
		}
	}
	return true
}

// ParseMode parses an octal mode string ("0664"), falling back to def.
func ParseMode(s string, def os.FileMode) os.FileMode {
	if s == "" {
		return def
	}
	v, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return def
	}
	return os.FileMode(v)
}

// LinkOrCopy hardlinks src to dst, falling back to a copy (e.g. across devices).
func LinkOrCopy(src, dst string) error {
	if !local(src, dst) {
		return libfs.CopyFile(src, dst, 0)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o775); err != nil {
		return err
	}
	_ = os.Remove(dst)
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	return CopyFile(src, dst)
}

// CopyFile copies src to dst (fsynced).
func CopyFile(src, dst string) error {
	if !local(src, dst) {
		return libfs.CopyFile(src, dst, 0)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// Move renames src (a file or a directory) to dst, copying across devices
// when needed.
func Move(src, dst string) error {
	if !local(src, dst) {
		if err := libfs.MkdirAll(filepath.Dir(dst), 0o775); err != nil {
			return err
		}
		return libfs.Rename(src, dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o775); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if st.IsDir() {
		if err := CopyTree(src, dst); err != nil {
			_ = os.RemoveAll(dst)
			return err
		}
		return os.RemoveAll(src)
	}
	if err := CopyFile(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

// CopyTree copies the directory src to dst (which must not exist), keeping
// file modes and modification times.
func CopyTree(src, dst string) error {
	if !local(src, dst) {
		return libfs.CopyTree(src, dst)
	}
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode().IsRegular():
			if err := CopyFile(p, target); err != nil {
				return err
			}
			_ = os.Chmod(target, info.Mode().Perm())
			return os.Chtimes(target, info.ModTime(), info.ModTime())
		}
		return nil // skip symlinks and special files
	})
}

// ErrNoDisk is FreeSpace's and Usage's answer for a folder on object
// storage, which has no disk to fill.
var ErrNoDisk = errors.New("on object storage: no disk space to measure")
