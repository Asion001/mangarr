package backup

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Asion001/mangarr/internal/db"
)

func TestVerifyAndSaveUpload(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	svc := New(d, nil, t.TempDir())
	b, err := svc.Create(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if !b.Restorable || b.Verification == nil || b.Verification.Checksum == "" {
		t.Fatalf("backup was not verified: %+v", b)
	}
	path, err := svc.Path(b.Name)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	uploaded, err := svc.SaveFrom(ctx, strings.NewReader(string(archive)))
	if err != nil {
		t.Fatal(err)
	}
	if !uploaded.Restorable || uploaded.Verification == nil || uploaded.Verification.Total < 0 {
		t.Fatalf("upload was not verified: %+v", uploaded)
	}
	list, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range list {
		if item.Name == uploaded.Name && (!item.Restorable || item.Verification == nil) {
			t.Fatalf("verification missing from list: %+v", item)
		}
	}
}

func TestSaveFromRejectsCorruptTruncatedAndOversized(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	svc := New(d, nil, t.TempDir())
	valid, err := svc.Create(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	path, err := svc.Path(valid.Name)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	truncated := data[:len(data)/2]
	for name, body := range map[string][]byte{"corrupt": []byte("not a zip"), "truncated": truncated} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.SaveFrom(ctx, strings.NewReader(string(body))); err == nil {
				t.Fatal("invalid archive accepted")
			}
		})
	}
	if _, err := svc.saveFromLimit(ctx, strings.NewReader(strings.Repeat("x", 33)), 32); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("oversized archive error = %v", err)
	}
}

func TestSaveFromStreamsToBoundedReads(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	svc := New(d, nil, t.TempDir())
	body := &chunkReader{remaining: 2 << 20}
	if _, err := svc.SaveFrom(ctx, body); err == nil {
		t.Fatal("invalid streamed archive accepted")
	}
	if body.maxRead > 32<<10 || body.total != 2<<20 {
		t.Fatalf("upload reads: total=%d max chunk=%d", body.total, body.maxRead)
	}
}

type chunkReader struct{ remaining, total, maxRead int }

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if len(p) > 32<<10 {
		p = p[:32<<10]
	}
	n := len(p)
	if n > r.remaining {
		n = r.remaining
	}
	for i := range p[:n] {
		p[i] = 'x'
	}
	r.remaining -= n
	r.total += n
	if n > r.maxRead {
		r.maxRead = n
	}
	return n, nil
}

func TestVerifyRejectsMissingManifest(t *testing.T) {
	d, err := db.Open(context.Background(), "sqlite://"+filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc := New(d, nil, t.TempDir())
	if err := os.MkdirAll(svc.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(svc.dir, "missing.zip"))
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	w, err := z.Create("other")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprint(w, "data")
	_ = z.Close()
	_ = f.Close()
	if _, err := svc.Verify(context.Background(), "missing.zip"); err == nil {
		t.Fatal("archive without manifest accepted")
	}
}

// TestBackupCarriesSourceSettings: a backup keeps the native sources' site
// options from the data dir, and restoring puts them back.
func TestBackupCarriesSourceSettings(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "sources"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "sources", "native-3.json"), []byte(`{"lang":"en"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := New(d, nil, dataDir)
	b, err := svc.Create(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	path, err := svc.Path(b.Name)
	if err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	if err := UnpackData(path, other); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(other, "sources", "native-3.json"))
	if err != nil || string(got) != `{"lang":"en"}` {
		t.Fatalf("restored source settings: %q, %v", got, err)
	}
}
