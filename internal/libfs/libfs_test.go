package libfs_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/libfs"
	"github.com/Asion001/mangarr/internal/libfs/libfstest"
)

// realS3 is a bucket from MANGARR_TEST_S3
// (http://key:secret@host:port/bucket), for checking against a real
// service; the fake doesn't check signatures.
func realS3(t *testing.T) *libfs.S3 {
	raw := os.Getenv("MANGARR_TEST_S3")
	if raw == "" {
		t.Skip("MANGARR_TEST_S3 not set")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := u.User.Password()
	cfg := libfs.S3Config{Endpoint: u.Scheme + "://" + u.Host, Bucket: strings.Trim(u.Path, "/"), AccessKey: u.User.Username(), SecretKey: secret,
		PathStyle: true, Prefix: "t" + strings.ReplaceAll(t.Name(), "/", "-"), TempDir: t.TempDir()}
	fs, err := libfs.NewS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	libfs.CreateBucket(context.Background(), fs)
	return fs
}

func TestS3Fake(t *testing.T) {
	_, s3 := libfstest.Use(t)
	if err := s3.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	suite(t, s3)
}

func TestS3Real(t *testing.T) {
	s3 := realS3(t)
	if err := s3.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	libfs.Use(s3)
	t.Cleanup(func() { libfs.Use(nil) })
	suite(t, s3)

	// a signed link works without credentials, until it expires
	root := filepath.Join(t.TempDir(), "signed")
	libfs.Mount(root)
	name := filepath.Join(root, "Title (2020)", "Ch. 1 [ä] #1.cbz")
	if err := libfs.WriteFile(name, []byte("cbz bytes"), 0o664); err != nil {
		t.Fatal(err)
	}
	link, ok := libfs.DownloadURL(name, time.Minute, "Ch. 1 [ä] #1.cbz")
	if !ok {
		t.Fatal("no signed link")
	}
	resp, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "cbz bytes" {
		t.Fatalf("signed link: %d %q", resp.StatusCode, body)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Logf("the service ignored the download name (%q)", cd)
	}
	bad := strings.Replace(link, "X-Amz-Signature=", "X-Amz-Signature=0", 1)
	if resp, err := http.Get(bad); err == nil {
		resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Fatal("a tampered link worked")
		}
	}
}

func TestLocal(t *testing.T) {
	libfs.Use(nil)
	suite(t, libfs.Local{})
}

func suite(t *testing.T, backend libfs.FS) {
	root := filepath.Join(t.TempDir(), "lib")
	libfs.Mount(root)
	if err := libfs.MkdirAll(root, 0o775); err != nil {
		t.Fatal(err)
	}
	if got := libfs.For(filepath.Join(root, "x")); got != backend {
		t.Fatalf("backend for a library path: %T", got)
	}
	if st, err := libfs.Stat(root); err != nil || !st.IsDir() {
		t.Fatalf("empty root: %v %v", st, err)
	}
	if err := libfs.Writable(root); err != nil {
		t.Fatal(err)
	}

	// a CBZ with a stored page, written through Create
	series := filepath.Join(root, "Some Series (2020)", "Vol 1")
	if err := libfs.MkdirAll(series, 0o775); err != nil {
		t.Fatal(err)
	}
	page := bytes.Repeat([]byte("page-data-"), 200_000) // 2 MB: spans blocks
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "0001.jpg", Method: zip.Store})
	_, _ = w.Write(page)
	_ = zw.Close()
	cbz := filepath.Join(series, "Ch. 1 [ä+ö] #1 & (x).cbz")
	if err := libfs.WriteFile(cbz, buf.Bytes(), 0o664); err != nil {
		t.Fatal(err)
	}
	if _, err := libfs.Stat(cbz + ".partial"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("partial left behind: %v", err)
	}
	st, err := libfs.Stat(cbz)
	if err != nil || st.Size() != int64(buf.Len()) || st.IsDir() {
		t.Fatalf("stat: %v %v", st, err)
	}

	f, err := libfs.Open(cbz)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	off, _ := zr.File[0].DataOffset()
	rc, _ := zr.File[0].Open()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, page) {
		t.Fatal("page read through the zip differs")
	}
	all, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(all, buf.Bytes()) {
		t.Fatalf("sequential read: %d bytes, %v", len(all), err)
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 10)
	if _, err := io.ReadFull(f, head); err != nil || string(head) != "page-data-" {
		t.Fatalf("read after seek: %q %v", head, err)
	}
	f.Close()

	rr, err := libfs.OpenRange(cbz, off+10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(rr); string(b) != "page-data-" {
		t.Fatalf("range: %q", b)
	}
	rr.Close()

	// listing
	if err := libfs.WriteFile(filepath.Join(root, "Some Series (2020)", "cover.jpg"), []byte("img"), 0o664); err != nil {
		t.Fatal(err)
	}
	entries, err := libfs.ReadDir(filepath.Join(root, "Some Series (2020)"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() {
			n += "/"
		}
		names = append(names, n)
	}
	if strings.Join(names, ",") != "Vol 1/,cover.jpg" {
		t.Fatalf("readdir: %v", names)
	}
	var walked []string
	_ = libfs.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			walked = append(walked, rel)
		}
		return nil
	})
	if len(walked) != 2 {
		t.Fatalf("walk: %v", walked)
	}

	// renaming a folder moves everything in it
	moved := filepath.Join(root, "Renamed")
	if err := libfs.Rename(filepath.Join(root, "Some Series (2020)"), moved); err != nil {
		t.Fatal(err)
	}
	if libfs.Exists(cbz) {
		t.Fatal("old file still there")
	}
	movedCBZ := filepath.Join(moved, "Vol 1", filepath.Base(cbz))
	if b, err := libfs.ReadFile(movedCBZ); err != nil || !bytes.Equal(b, buf.Bytes()) {
		t.Fatalf("moved file: %v", err)
	}

	// from and to the local disk
	localDir := t.TempDir()
	out := filepath.Join(localDir, "out.cbz")
	if err := libfs.Rename(movedCBZ, out); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(out); err != nil || !bytes.Equal(b, buf.Bytes()) {
		t.Fatalf("moved out: %v", err)
	}
	if err := libfs.Rename(out, movedCBZ); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("moved in, but the local file is still there")
	}

	if err := libfs.Remove(moved); err == nil {
		t.Fatal("removed a folder with files in it")
	}
	if err := libfs.RemoveAll(moved); err != nil {
		t.Fatal(err)
	}
	// some services (SeaweedFS) list emptied folders for a while; what
	// matters is that no file is left
	files := 0
	_ = libfs.WalkDir(moved, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files++
		}
		return nil
	})
	if libfs.Exists(movedCBZ) || files > 0 {
		t.Fatal("RemoveAll left files")
	}
	if _, err := libfs.Open(filepath.Join(root, "nope.cbz")); !errors.Is(err, fs.ErrNotExist) || !os.IsNotExist(err) {
		t.Fatalf("missing file: %v", err)
	}
}

func TestBucketMissing(t *testing.T) {
	srv := libfstest.NewServer(t)
	cfg := srv.Config(t)
	cfg.Bucket = "other"
	s3, err := libfs.NewS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	err = s3.Probe(context.Background())
	var e *libfs.S3Error
	if !errors.As(err, &e) || e.Status != http.StatusNotFound {
		t.Fatalf("probe of a missing bucket: %v", err)
	}
}
