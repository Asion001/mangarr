// Package backup creates zip backups of the database (SQLite) and settings.
package backup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbcopy"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/version"
)

const MaxUploadSize = 4 << 30

type Backup struct {
	Name         string        `json:"name"`
	Type         string        `json:"type"` // manual | scheduled
	Size         int64         `json:"size"`
	Created      time.Time     `json:"created"`
	Restorable   bool          `json:"restorable"`
	Verification *Verification `json:"verification,omitempty"`
}

type Verification struct {
	VerifiedAt     time.Time `json:"verifiedAt"`
	SourceVersion  string    `json:"sourceVersion"`
	SourceDatabase string    `json:"sourceDatabase"`
	Checksum       string    `json:"checksum"`
	// Size is the archive's size when it was verified; List trusts the
	// record only while it still matches, instead of re-hashing every zip.
	Size  int64          `json:"size"`
	Rows  map[string]int `json:"rows"`
	Total int            `json:"total"`
}

type Service struct {
	db       *db.DB
	settings *settings.Store
	dir      string
}

func New(d *db.DB, st *settings.Store, dataDir string) *Service {
	return &Service{db: d, settings: st, dir: filepath.Join(dataDir, "backups")}
}

func (s *Service) Dir() string { return s.dir }

// Create writes a new backup and applies retention to scheduled backups.
func (s *Service) Create(ctx context.Context, typ string) (*Backup, error) {
	if err := os.MkdirAll(s.dir, 0o775); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("mangarr_%s_%s.zip", typ, time.Now().UTC().Format("2006.01.02_15.04.05"))
	path := filepath.Join(s.dir, name)
	tmp := path + ".partial"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, err
	}
	zw := zip.NewWriter(f)
	fail := func(err error) (*Backup, error) {
		_ = zw.Close()
		_ = f.Close()
		_ = os.Remove(tmp)
		return nil, err
	}
	manifest := map[string]any{"version": version.Version, "database": string(s.db.Kind), "created": time.Now().UTC()}
	if s.db.Kind == db.SQLite {
		dbTmp := filepath.Join(s.dir, ".backup-db.tmp")
		if err := s.db.Backup(ctx, dbTmp); err != nil {
			return fail(err)
		}
		err := addFile(zw, "mangarr.db", dbTmp)
		_ = os.Remove(dbTmp)
		if err != nil {
			return fail(err)
		}
	} else {
		// the whole database as a SQLite file: the same backup format on
		// both databases, restorable into either
		dbTmp := filepath.Join(s.dir, ".backup-db.tmp")
		cleanup := func() {
			for _, suffix := range []string{"", "-wal", "-shm"} {
				_ = os.Remove(dbTmp + suffix)
			}
		}
		cleanup()
		lite, err := db.Open(ctx, "sqlite://"+dbTmp)
		if err != nil {
			return fail(err)
		}
		_, err = dbcopy.Copy(ctx, s.db, lite, false, nil)
		_ = lite.Close()
		if err == nil {
			err = addFile(zw, "mangarr.db", dbTmp)
		}
		cleanup()
		if err != nil {
			return fail(err)
		}
	}
	if err := addJSON(zw, "manifest.json", manifest); err != nil {
		return fail(err)
	}
	if err := zw.Close(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	verification, err := s.Verify(ctx, name)
	if err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("created backup failed verification: %w", err)
	}
	if typ == "scheduled" {
		g, _ := s.settings.General(ctx)
		s.prune(g.BackupRetention)
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return &Backup{Name: name, Type: typ, Size: st.Size(), Created: st.ModTime(), Restorable: true, Verification: verification}, nil
}

func addFile(zw *zip.Writer, name, path string) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, in)
	return err
}

func addJSON(zw *zip.Writer, name string, v any) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// List returns backups, newest first.
func (s *Service) List() ([]Backup, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Backup{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Backup{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".zip") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		typ := "manual"
		switch {
		case strings.Contains(e.Name(), "_scheduled_"):
			typ = "scheduled"
		case strings.Contains(e.Name(), "_uploaded_"):
			typ = "uploaded"
		}
		b := Backup{Name: e.Name(), Type: typ, Size: info.Size(), Created: info.ModTime()}
		if v, err := s.readVerification(e.Name()); err == nil {
			if v.Size == info.Size() && !info.ModTime().After(v.VerifiedAt) {
				b.Restorable, b.Verification = true, v
			}
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// SaveFrom stores and verifies an uploaded backup without keeping its body in memory.
func (s *Service) SaveFrom(ctx context.Context, src io.Reader) (*Backup, error) {
	return s.saveFromLimit(ctx, src, MaxUploadSize)
}

func (s *Service) saveFromLimit(ctx context.Context, src io.Reader, maxSize int64) (*Backup, error) {
	if err := os.MkdirAll(s.dir, 0o775); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(s.dir, ".upload-*.partial")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	n, err := io.Copy(tmp, io.LimitReader(src, maxSize+1))
	if err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if n > maxSize {
		_ = tmp.Close()
		return nil, fmt.Errorf("backup upload exceeds the %d byte limit", maxSize)
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	verification, err := s.verifyPath(ctx, tmpPath)
	if err != nil {
		return nil, err
	}
	name := fmt.Sprintf("mangarr_uploaded_%s.zip", time.Now().UTC().Format("2006.01.02_15.04.05"))
	path := filepath.Join(s.dir, name)
	if err := os.Rename(tmpPath, path); err != nil {
		return nil, err
	}
	if err := s.writeVerification(name, verification); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	st, err := os.Stat(path)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return &Backup{Name: name, Type: "uploaded", Size: st.Size(), Created: st.ModTime(), Restorable: true, Verification: verification}, nil
}

// Verify validates an existing archive and records the result alongside it.
func (s *Service) Verify(ctx context.Context, name string) (*Verification, error) {
	path, err := s.Path(name)
	if err != nil {
		return nil, err
	}
	v, err := s.verifyPath(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := s.writeVerification(name, v); err != nil {
		return nil, err
	}
	return v, nil
}

func (s *Service) verifyPath(ctx context.Context, path string) (*Verification, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("invalid backup ZIP: %w", err)
	}
	defer zr.Close()
	var manifest *zip.File
	var database *zip.File
	for _, f := range zr.File {
		switch f.Name {
		case "manifest.json":
			manifest = f
		case "mangarr.db":
			database = f
		}
	}
	if manifest == nil || database == nil {
		return nil, errors.New("backup must contain manifest.json and mangarr.db")
	}
	mr, err := manifest.Open()
	if err != nil {
		return nil, fmt.Errorf("read backup manifest: %w", err)
	}
	var info struct {
		Version  string `json:"version"`
		Database string `json:"database"`
	}
	err = json.NewDecoder(io.LimitReader(mr, 1<<20)).Decode(&info)
	_ = mr.Close()
	if err != nil || strings.TrimSpace(info.Version) == "" || strings.TrimSpace(info.Database) == "" {
		return nil, errors.New("backup manifest is missing a readable version or database")
	}
	// The extracted database can be as big as the library's, so it goes
	// next to the backups rather than in a possibly small system temp dir.
	dbPath := filepath.Join(s.dir, fmt.Sprintf(".verify-%d.db", time.Now().UnixNano()))
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	}()
	r, err := database.Open()
	if err != nil {
		return nil, fmt.Errorf("read backup database: %w", err)
	}
	out, err := os.OpenFile(dbPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = r.Close()
		return nil, err
	}
	n, copyErr := io.Copy(out, io.LimitReader(r, MaxUploadSize+1))
	closeErr := out.Close()
	rCloseErr := r.Close()
	if copyErr != nil {
		return nil, fmt.Errorf("extract backup database: %w", copyErr)
	}
	if n > MaxUploadSize {
		return nil, errors.New("backup database exceeds the verification size limit")
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if rCloseErr != nil {
		return nil, rCloseErr
	}
	checkDB, err := db.Open(ctx, "sqlite://"+dbPath)
	if err != nil {
		return nil, fmt.Errorf("backup database cannot be opened: %w", err)
	}
	defer checkDB.Close()
	var integrity string
	if err := checkDB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return nil, fmt.Errorf("backup database integrity check failed: %s", integrity)
	}
	if err := checkDB.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("backup database migrations failed: %w", err)
	}
	rows, err := dbcopy.CountRows(ctx, checkDB)
	if err != nil {
		return nil, fmt.Errorf("backup database is missing required tables: %w", err)
	}
	checksum, err := fileChecksum(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return &Verification{VerifiedAt: time.Now().UTC(), SourceVersion: info.Version, SourceDatabase: info.Database, Checksum: checksum, Size: st.Size(), Rows: rows.Rows, Total: rows.Total}, nil
}

func (s *Service) readVerification(name string) (*Verification, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, name+".verify.json"))
	if err != nil {
		return nil, err
	}
	var v Verification
	err = json.Unmarshal(data, &v)
	return &v, err
}

func (s *Service) writeVerification(name string, v *Verification) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, name+".verify.json.partial")
	defer os.Remove(tmp)
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.dir, name+".verify.json"))
}

func fileChecksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil)), nil
}

// Path returns the absolute path of a backup, rejecting path traversal.
func (s *Service) Path(name string) (string, error) {
	if name != filepath.Base(name) || !strings.HasSuffix(name, ".zip") {
		return "", errors.New("invalid backup name")
	}
	p := filepath.Join(s.dir, name)
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}

func (s *Service) Delete(name string) error {
	p, err := s.Path(name)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		return err
	}
	_ = os.Remove(p + ".verify.json")
	return nil
}

func (s *Service) prune(keep int) {
	if keep <= 0 {
		keep = 7
	}
	list, err := s.List()
	if err != nil {
		return
	}
	n := 0
	for _, b := range list {
		if b.Type != "scheduled" {
			continue
		}
		n++
		if n > keep {
			_ = os.Remove(filepath.Join(s.dir, b.Name))
		}
	}
}
