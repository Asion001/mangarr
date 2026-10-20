package backup

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbcopy"
	"github.com/Asion001/mangarr/internal/version"
)

// An archive holds mangarr.db, manifest.json, the data dir's own files
// under data/ and, in an export for another server, the library files under
// library/<folder id>/, each at its path inside that folder.
const (
	dataPrefix    = "data/"
	libraryPrefix = "library/"
)

// dataDirs are the parts of the data dir that hold settings rather than
// caches, so they travel with the database.
var dataDirs = []string{"sources"}

// Folder is a library folder in a backup.
type Folder struct {
	ID     int64  `json:"id"`
	Path   string `json:"path"`
	Series int    `json:"series"`
	// Files and Bytes count the library files in the archive for this
	// folder (an export made with library files).
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

func listFolders(ctx context.Context, d *db.DB) ([]Folder, error) {
	out := []Folder{}
	err := d.NewSelect().Table("root_folders").Column("id", "path").
		ColumnExpr("(SELECT COUNT(*) FROM series WHERE series.root_folder_id = root_folders.id) AS series").
		Order("id").Scan(ctx, &out)
	return out, err
}

// countLibrary adds up the archive's library files per folder.
func countLibrary(files []*zip.File, folders []Folder) {
	byID := map[int64]*Folder{}
	for i := range folders {
		byID[folders[i].ID] = &folders[i]
	}
	for _, f := range files {
		id, _, ok := libraryEntry(f.Name)
		if !ok || f.FileInfo().IsDir() {
			continue
		}
		if fo := byID[id]; fo != nil {
			fo.Files++
			fo.Bytes += int64(f.UncompressedSize64)
		}
	}
}

// libraryEntry splits library/<id>/<rel> and rejects anything that could
// leave the folder it is unpacked into.
func libraryEntry(name string) (int64, string, bool) {
	rest, ok := strings.CutPrefix(name, libraryPrefix)
	if !ok {
		return 0, "", false
	}
	idPart, rel, ok := strings.Cut(rest, "/")
	if !ok {
		return 0, "", false
	}
	id, err := strconv.ParseInt(idPart, 10, 64)
	if err != nil || !safeRel(rel) {
		return 0, "", false
	}
	return id, rel, true
}

func safeRel(rel string) bool {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.Contains(rel, "\\") {
		return false
	}
	clean := path.Clean(rel)
	return clean == rel && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

// Export writes an archive to w for moving mangarr to another server: a
// backup and, with library set, every series folder's files. Nothing is
// staged on disk but the database snapshot.
func (s *Service) Export(ctx context.Context, w io.Writer, library bool) error {
	if err := os.MkdirAll(s.dir, 0o775); err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	if err := s.writeArchive(ctx, zw, library); err != nil {
		_ = zw.Close()
		return err
	}
	return zw.Close()
}

// ExportName is the file name an export is downloaded as.
func ExportName() string {
	return fmt.Sprintf("mangarr_export_%s.zip", time.Now().UTC().Format("2006.01.02_15.04.05"))
}

func (s *Service) writeArchive(ctx context.Context, zw *zip.Writer, library bool) error {
	folders, err := listFolders(ctx, s.db)
	if err != nil {
		return err
	}
	manifest := map[string]any{"version": version.Version, "database": string(s.db.Kind), "created": time.Now().UTC(), "folders": folders, "library": library}
	if err := addJSON(zw, "manifest.json", manifest); err != nil {
		return err
	}
	if err := s.addDatabase(ctx, zw); err != nil {
		return err
	}
	if err := s.addDataFiles(zw); err != nil {
		return err
	}
	if library {
		return s.addLibrary(ctx, zw, folders)
	}
	return nil
}

func (s *Service) addDatabase(ctx context.Context, zw *zip.Writer) error {
	// a name of its own, so a scheduled backup and an export can run together
	dbTmp := filepath.Join(s.dir, fmt.Sprintf(".backup-%d.db", time.Now().UnixNano()))
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbTmp + suffix)
		}
	}()
	if s.db.Kind == db.SQLite {
		if err := s.db.Backup(ctx, dbTmp); err != nil {
			return err
		}
	} else {
		// the whole database as a SQLite file: the same backup format on
		// both databases, restorable into either
		lite, err := db.Open(ctx, "sqlite://"+dbTmp)
		if err != nil {
			return err
		}
		_, err = dbcopy.Copy(ctx, s.db, lite, false, nil)
		_ = lite.Close()
		if err != nil {
			return err
		}
	}
	return addFile(zw, "mangarr.db", dbTmp)
}

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

// addLibrary stores each series folder's files as they are: CBZ files and
// images are compressed already.
func (s *Service) addLibrary(ctx context.Context, zw *zip.Writer, folders []Folder) error {
	roots := map[int64]string{}
	for _, f := range folders {
		roots[f.ID] = f.Path
	}
	var series []struct {
		RootFolderID int64  `bun:"root_folder_id"`
		Path         string `bun:"path"`
	}
	if err := s.db.NewSelect().Table("series").Column("root_folder_id", "path").Order("id").Scan(ctx, &series); err != nil {
		return err
	}
	for _, se := range series {
		root, ok := roots[se.RootFolderID]
		if !ok || se.Path == "" {
			continue
		}
		dir := filepath.Join(root, se.Path)
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !d.Type().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			return addStored(zw, fmt.Sprintf("%s%d/%s", libraryPrefix, se.RootFolderID, filepath.ToSlash(rel)), p)
		})
		if err != nil {
			return fmt.Errorf("library folder %s: %w", dir, err)
		}
	}
	return nil
}

func addStored(zw *zip.Writer, name, p string) error {
	in, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil // removed while the export ran
	}
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store, Modified: st.ModTime()})
	if err != nil {
		return err
	}
	_, err = io.Copy(w, in)
	return err
}

// Unpack writes an archive's data dir files into dataDir and, with library
// set, its library files into the folders: folder id → the path the folder
// has on this server. A file that is already there with the same size is
// kept. progress is told how many library files are done.
func Unpack(ctx context.Context, zipPath, dataDir string, folders map[int64]string, library bool, progress func(done, total int)) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	total := 0
	if library {
		for _, f := range zr.File {
			if id, _, ok := libraryEntry(f.Name); ok && folders[id] != "" && !f.FileInfo().IsDir() {
				total++
			}
		}
	}
	done := 0
	for _, f := range zr.File {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if f.FileInfo().IsDir() {
			continue
		}
		var dst string
		if rel, ok := strings.CutPrefix(f.Name, dataPrefix); ok {
			dir, _, _ := strings.Cut(rel, "/")
			if !safeRel(rel) || !isDataDir(dir) || dataDir == "" {
				continue
			}
			dst = filepath.Join(dataDir, filepath.FromSlash(rel))
		} else if id, rel, ok := libraryEntry(f.Name); ok && library && folders[id] != "" {
			dst = filepath.Join(folders[id], filepath.FromSlash(rel))
			done++
		} else {
			continue
		}
		if err := unpackFile(f, dst); err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
		if progress != nil && strings.HasPrefix(f.Name, libraryPrefix) {
			progress(done, total)
		}
	}
	return nil
}

func isDataDir(dir string) bool {
	for _, d := range dataDirs {
		if d == dir {
			return true
		}
	}
	return false
}

func unpackFile(f *zip.File, dst string) error {
	if st, err := os.Stat(dst); err == nil && st.Mode().IsRegular() && st.Size() == int64(f.UncompressedSize64) {
		return nil
	}
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
	if _, err := io.Copy(out, rc); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Chtimes(tmp, f.Modified, f.Modified)
	return os.Rename(tmp, dst)
}
