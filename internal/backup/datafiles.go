package backup

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// dataPrefix is where an archive keeps the data dir's own files.
const dataPrefix = "data/"

// dataDirs are the parts of the data dir that hold settings rather than
// caches (each native source's site options), so they travel with the
// database.
var dataDirs = []string{"sources"}

func (s *Service) addDataFiles(zw *zip.Writer) error {
	if s.dataDir == "" {
		return nil
	}
	for _, dir := range dataDirs {
		entries, err := os.ReadDir(filepath.Join(s.dataDir, dir))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !e.Type().IsRegular() {
				continue
			}
			if err := addFile(zw, dataPrefix+dir+"/"+e.Name(), filepath.Join(s.dataDir, dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// UnpackData writes an archive's data dir files into dataDir, replacing
// what is there. Older backups have none.
func UnpackData(zipPath, dataDir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		rel, ok := strings.CutPrefix(f.Name, dataPrefix)
		if !ok || f.FileInfo().IsDir() {
			continue
		}
		dir, name, _ := strings.Cut(rel, "/")
		if !slices.Contains(dataDirs, dir) || name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			continue
		}
		if err := unpackData(f, filepath.Join(dataDir, dir, name)); err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	return nil
}

func unpackData(f *zip.File, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o775); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	tmp := dst + ".partial"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(rc, 64<<20)); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
