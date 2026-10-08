package cbz

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// OpenPage reads stored pages at their offset and deflated ones through the
// archive, and both come back whole.
func TestOpenPage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.cbz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	pages := map[string][]byte{
		"0001.jpg": bytes.Repeat([]byte("stored page "), 500),
		"0002.png": bytes.Repeat([]byte("deflated page "), 500),
	}
	w := zip.NewWriter(f)
	for name, method := range map[string]uint16{"0001.jpg": zip.Store, "0002.png": zip.Deflate} {
		fw, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(pages[name])
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	entries, err := Entries(path)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries: %v %v", entries, err)
	}
	if entries[0].dataAt == 0 || entries[1].dataAt != 0 {
		t.Fatalf("stored page should have an offset, deflated not: %+v", entries)
	}
	for _, e := range entries {
		rc, size, err := OpenPage(path, e)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(rc)
		rc.Close()
		if size != int64(len(pages[e.Name])) || !bytes.Equal(got, pages[e.Name]) {
			t.Errorf("%s: got %d bytes (size %d), want %d", e.Name, len(got), size, len(pages[e.Name]))
		}
	}
}
