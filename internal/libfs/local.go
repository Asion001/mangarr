package libfs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// Local is the local disk.
type Local struct{}

func (Local) Local() bool                                  { return true }
func (Local) Open(name string) (File, error)               { return os.Open(name) }
func (Local) Stat(name string) (fs.FileInfo, error)        { return os.Stat(name) }
func (Local) Remove(name string) error                     { return os.Remove(name) }
func (Local) RemoveAll(name string) error                  { return os.RemoveAll(name) }
func (Local) MkdirAll(name string, mode fs.FileMode) error { return os.MkdirAll(name, mode) }
func (Local) ReadDir(name string) ([]fs.DirEntry, error)   { return os.ReadDir(name) }

func (Local) OpenRange(name string, off, n int64) (io.ReadCloser, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	return sectionCloser{io.NewSectionReader(f, off, n), f}, nil
}

type sectionCloser struct {
	*io.SectionReader
	c io.Closer
}

func (s sectionCloser) Close() error { return s.c.Close() }

// Rename renames, copying when from and to are on different devices.
func (Local) Rename(from, to string) error {
	err := os.Rename(from, to)
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	st, err := os.Stat(from)
	if err != nil {
		return err
	}
	if st.IsDir() {
		if err := copyLocalTree(from, to); err != nil {
			_ = os.RemoveAll(to)
			return err
		}
		return os.RemoveAll(from)
	}
	if err := CopyFile(from, to, st.Mode().Perm()); err != nil {
		return err
	}
	return os.Remove(from)
}

// copyLocalTree copies a folder keeping modes and modification times.
func copyLocalTree(src, dst string) error {
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
			if err := CopyFile(p, target, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chtimes(target, info.ModTime(), info.ModTime())
		}
		return nil
	})
}

// Create writes to "<name>.partial" in the same folder (an extension
// neither Komga nor Kavita scans), then fsyncs and renames it over name.
func (Local) Create(name string, mode fs.FileMode) (Writer, error) {
	tmp := name + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return nil, err
	}
	return &localWriter{f: f, tmp: tmp, dst: name, mode: mode}, nil
}

type localWriter struct {
	f        *os.File
	tmp, dst string
	mode     fs.FileMode
	n        int64
	finished bool
}

func (w *localWriter) Write(p []byte) (int, error) {
	n, err := w.f.Write(p)
	w.n += int64(n)
	return n, err
}

func (w *localWriter) Commit() (int64, error) {
	if w.finished {
		return 0, os.ErrClosed
	}
	w.finished = true
	if err := w.f.Sync(); err != nil {
		_ = w.f.Close()
		_ = os.Remove(w.tmp)
		return 0, err
	}
	if err := w.f.Close(); err != nil {
		_ = os.Remove(w.tmp)
		return 0, err
	}
	if err := os.Chmod(w.tmp, w.mode); err != nil {
		_ = os.Remove(w.tmp)
		return 0, err
	}
	if err := os.Rename(w.tmp, w.dst); err != nil {
		_ = os.Remove(w.tmp)
		return 0, err
	}
	if d, err := os.Open(filepath.Dir(w.dst)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return w.n, nil
}

func (w *localWriter) Abort() {
	if w.finished {
		return
	}
	w.finished = true
	_ = w.f.Close()
	_ = os.Remove(w.tmp)
}
