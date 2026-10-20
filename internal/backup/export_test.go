package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/db"
)

// TestExportCarriesLibraryAndSources: an export holds the series folders'
// files and the data dir's source options, and they unpack into the paths
// the folders have on the new server.
func TestExportCarriesLibraryAndSources(t *testing.T) {
	ctx := context.Background()
	dataDir, root := t.TempDir(), t.TempDir()
	d, err := db.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := d.ExecContext(ctx, "INSERT INTO profiles (id, name, created_at, updated_at) VALUES (3, 'p', ?, ?)", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, "INSERT INTO root_folders (id, path, created_at) VALUES (7, ?, ?)", root, now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, "INSERT INTO series (title, sort_title, root_folder_id, path, profile_id, added_at, updated_at) VALUES ('A', 'a', 7, 'A', 3, ?, ?)", now, now); err != nil {
		t.Fatal(err)
	}
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "A", "Vol 1", "Ch 1.cbz"), "chapter one")
	write(filepath.Join(root, "A", "cover.jpg"), "cover")
	write(filepath.Join(root, "Not managed", "x.cbz"), "someone else's")
	write(filepath.Join(dataDir, "sources", "native-1.json"), `{"lang":"en"}`)

	svc := New(d, nil, dataDir)
	var buf bytes.Buffer
	if err := svc.Export(ctx, &buf, true); err != nil {
		t.Fatal(err)
	}
	newData := t.TempDir()
	target := New(d, nil, newData)
	saved, err := target.SaveFrom(ctx, &buf)
	if err != nil {
		t.Fatal(err)
	}
	f := saved.Verification.Folders
	if len(f) != 1 || f[0].ID != 7 || f[0].Path != root || f[0].Series != 1 || f[0].Files != 2 || f[0].Bytes != int64(len("chapter one")+len("cover")) {
		t.Fatalf("folders %+v", f)
	}
	p, _ := target.Path(saved.Name)
	moved := filepath.Join(t.TempDir(), "manga")
	if err := Unpack(ctx, p, newData, map[int64]string{7: moved}, true, nil); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{
		filepath.Join(moved, "A", "Vol 1", "Ch 1.cbz"):     "chapter one",
		filepath.Join(moved, "A", "cover.jpg"):             "cover",
		filepath.Join(newData, "sources", "native-1.json"): `{"lang":"en"}`,
	} {
		if got, err := os.ReadFile(file); err != nil || string(got) != want {
			t.Fatalf("%s: %q %v", file, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(moved, "Not managed")); !os.IsNotExist(err) {
		t.Fatalf("a folder mangarr doesn't manage was exported: %v", err)
	}
}

// TestUnpackStaysInsideTheFolder: entries that climb out of their folder
// or the data dir's known parts are skipped.
func TestUnpackStaysInsideTheFolder(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "evil.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range []string{"library/1/../../escape.txt", "library/1//abs.txt", "data/../escape.txt", "data/logs/x.txt", "library/1/ok.txt"} {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte("x"))
	}
	_ = zw.Close()
	_ = f.Close()
	root, data := filepath.Join(dir, "root"), filepath.Join(dir, "data")
	if err := Unpack(context.Background(), zipPath, data, map[int64]string{1: root}, true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "ok.txt")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, "escape.txt"), filepath.Join(root, "abs.txt"), filepath.Join(data, "logs", "x.txt")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s was written", p)
		}
	}
}
