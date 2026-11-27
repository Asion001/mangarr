package libfs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
)

// S3 keeps files as objects in an S3-compatible bucket. Folders are only
// key prefixes: they exist while something is in them, and MkdirAll does
// nothing.
type S3 struct {
	c   *s3Client
	tmp string

	mu    sync.Mutex
	stats map[string]statEntry
}

type statEntry struct {
	info objectInfo
	dir  bool
	err  error
	at   time.Time
}

// statTTL is how long a Stat answer is reused. The server is the only
// writer and drops its cached answers when it changes a key, so this only
// bounds how long a change made behind its back goes unnoticed.
const statTTL = 30 * time.Second

// NewS3 connects to a bucket (without checking it; see Probe).
func NewS3(cfg S3Config) (*S3, error) {
	c, err := newS3Client(cfg)
	if err != nil {
		return nil, err
	}
	tmp := cfg.TempDir
	if tmp == "" {
		tmp = os.TempDir()
	}
	return &S3{c: c, tmp: tmp, stats: map[string]statEntry{}}, nil
}

// Identity names the bucket and prefix, without credentials.
func (s *S3) Identity() string {
	ep := s.c.base.String()
	return "s3|" + ep + "|" + s.c.cfg.Bucket + "|" + strings.TrimSuffix(s.c.prefix, "/")
}

// Probe checks that the bucket can be listed, written and read.
func (s *S3) Probe(ctx context.Context) error {
	if _, err := s.c.list(ctx, "", "/", "", 1); err != nil {
		return err
	}
	key := ".mangarr-probe"
	data := []byte(time.Now().UTC().Format(time.RFC3339Nano))
	if err := s.c.put(ctx, key, bytesBody(data), time.Time{}); err != nil {
		return err
	}
	rc, err := s.c.get(ctx, key, 0, -1)
	if err != nil {
		return err
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return err
	}
	if string(got) != string(data) {
		return errors.New("s3 probe: read back something else than was written")
	}
	return s.c.del(ctx, key)
}

func bytesBody(b []byte) func() (io.ReadCloser, int64, error) {
	return func() (io.ReadCloser, int64, error) {
		return io.NopCloser(strings.NewReader(string(b))), int64(len(b)), nil
	}
}

// Key is the object key of a path: the cleaned absolute path with forward
// slashes and no leading slash (nor drive letter).
func Key(name string) string {
	p := clean(name)
	p = strings.TrimPrefix(p, filepath.VolumeName(p))
	return strings.Trim(filepath.ToSlash(p), "/")
}

func (s *S3) Local() bool { return false }

func pathErr(op, name string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
	}
	return &fs.PathError{Op: op, Path: name, Err: err}
}

func (s *S3) forget(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := key; ; {
		delete(s.stats, k)
		i := strings.LastIndex(k, "/")
		if i < 0 {
			break
		}
		k = k[:i]
	}
	delete(s.stats, "")
}

func (s *S3) forgetTree(prefix string) {
	s.mu.Lock()
	for k := range s.stats {
		if k == prefix || strings.HasPrefix(k, prefix+"/") {
			delete(s.stats, k)
		}
	}
	s.mu.Unlock()
	s.forget(prefix)
}

func (s *S3) stat(key string) (objectInfo, bool, error) {
	s.mu.Lock()
	if e, ok := s.stats[key]; ok && time.Since(e.at) < statTTL {
		s.mu.Unlock()
		return e.info, e.dir, e.err
	}
	s.mu.Unlock()
	info, dir, err := s.statRemote(key)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		s.mu.Lock()
		if len(s.stats) > 20000 {
			s.stats = map[string]statEntry{}
		}
		s.stats[key] = statEntry{info, dir, err, time.Now()}
		s.mu.Unlock()
	}
	return info, dir, err
}

func (s *S3) statRemote(key string) (objectInfo, bool, error) {
	ctx := context.Background()
	if key == "" {
		return objectInfo{}, true, nil
	}
	info, err := s.c.head(ctx, key)
	if err == nil {
		return info, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return objectInfo{}, false, err
	}
	page, err := s.c.list(ctx, key+"/", "/", "", 1)
	if err != nil {
		return objectInfo{}, false, err
	}
	if len(page.Objects) > 0 || len(page.Prefixes) > 0 {
		return objectInfo{Key: key}, true, nil
	}
	if isMountRoot(key) {
		// an empty root folder still exists
		return objectInfo{Key: key}, true, nil
	}
	return objectInfo{}, false, fs.ErrNotExist
}

func isMountRoot(key string) bool {
	for _, m := range Mounts() {
		if Key(m) == key {
			return true
		}
	}
	return false
}

func (s *S3) Stat(name string) (fs.FileInfo, error) {
	key := Key(name)
	info, dir, err := s.stat(key)
	if err != nil {
		return nil, pathErr("stat", name, err)
	}
	return fileInfo{name: path.Base("/" + key), size: info.Size, mod: info.ModTime, dir: dir}, nil
}

func (s *S3) Open(name string) (File, error) {
	key := Key(name)
	info, dir, err := s.stat(key)
	if err != nil {
		return nil, pathErr("open", name, err)
	}
	if dir {
		return nil, pathErr("open", name, syscall.EISDIR)
	}
	return &s3File{s: s, key: key, info: info}, nil
}

func (s *S3) OpenRange(name string, off, n int64) (io.ReadCloser, error) {
	rc, err := s.c.get(context.Background(), Key(name), off, n)
	if err != nil {
		return nil, pathErr("open", name, err)
	}
	return rc, nil
}

func (s *S3) MkdirAll(string, fs.FileMode) error { return nil }

func (s *S3) ReadDir(name string) ([]fs.DirEntry, error) {
	key := Key(name)
	prefix := key + "/"
	if key == "" {
		prefix = ""
	}
	ctx := context.Background()
	var out []fs.DirEntry
	token := ""
	for {
		page, err := s.c.list(ctx, prefix, "/", token, 1000)
		if err != nil {
			return nil, pathErr("readdir", name, err)
		}
		for _, p := range page.Prefixes {
			n := strings.TrimSuffix(strings.TrimPrefix(p, prefix), "/")
			if n != "" {
				out = append(out, fs.FileInfoToDirEntry(fileInfo{name: n, dir: true}))
			}
		}
		for _, o := range page.Objects {
			n := strings.TrimPrefix(o.Key, prefix)
			if n == "" || strings.Contains(n, "/") {
				continue // a folder marker some tools write
			}
			out = append(out, fs.FileInfoToDirEntry(fileInfo{name: n, size: o.Size, mod: o.ModTime}))
		}
		if page.Next == "" {
			break
		}
		token = page.Next
	}
	if len(out) == 0 {
		if _, dir, err := s.stat(key); err != nil || !dir {
			if err == nil {
				err = syscall.ENOTDIR
			}
			return nil, pathErr("readdir", name, err)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func (s *S3) Remove(name string) error {
	key := Key(name)
	_, dir, err := s.stat(key)
	if err != nil {
		return pathErr("remove", name, err)
	}
	if dir {
		page, err := s.c.list(context.Background(), key+"/", "/", "", 1)
		if err != nil {
			return pathErr("remove", name, err)
		}
		if len(page.Objects) > 0 || len(page.Prefixes) > 0 {
			return pathErr("remove", name, syscall.ENOTEMPTY)
		}
		s.forget(key)
		return nil
	}
	defer s.forget(key)
	if err := s.c.del(context.Background(), key); err != nil {
		return pathErr("remove", name, err)
	}
	return nil
}

func (s *S3) RemoveAll(name string) error {
	key := Key(name)
	if key == "" {
		return pathErr("removeall", name, errors.New("refusing to empty the whole bucket"))
	}
	ctx := context.Background()
	defer s.forgetTree(key)
	if err := s.c.del(ctx, key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return pathErr("removeall", name, err)
	}
	var keys []string
	if err := s.c.listAll(ctx, key+"/", func(o objectInfo) error {
		keys = append(keys, o.Key)
		return nil
	}); err != nil {
		return pathErr("removeall", name, err)
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for _, k := range keys {
		g.Go(func() error { return s.c.del(gctx, k) })
	}
	if err := g.Wait(); err != nil {
		return pathErr("removeall", name, err)
	}
	return nil
}

func (s *S3) Rename(from, to string) error {
	src, dst := Key(from), Key(to)
	if src == dst {
		return nil
	}
	ctx := context.Background()
	_, dir, err := s.stat(src)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: fs.ErrNotExist}
	}
	defer s.forgetTree(src)
	defer s.forgetTree(dst)
	if !dir {
		if err := s.c.copyObject(ctx, src, dst); err != nil {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
		}
		if err := s.c.del(ctx, src); err != nil {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
		}
		return nil
	}
	if dst == src || strings.HasPrefix(dst, src+"/") {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.EINVAL}
	}
	var keys []string
	if err := s.c.listAll(ctx, src+"/", func(o objectInfo) error {
		keys = append(keys, o.Key)
		return nil
	}); err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	// copy everything first, so a failure leaves the source whole
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for _, k := range keys {
		g.Go(func() error { return s.c.copyObject(gctx, k, dst+strings.TrimPrefix(k, src)) })
	}
	if err := g.Wait(); err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	g, gctx = errgroup.WithContext(ctx)
	g.SetLimit(8)
	for _, k := range keys {
		g.Go(func() error { return s.c.del(gctx, k) })
	}
	if err := g.Wait(); err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}

// Copy copies a file within the bucket.
func (s *S3) Copy(from, to string) error {
	dst := Key(to)
	defer s.forget(dst)
	if err := s.c.copyObject(context.Background(), Key(from), dst); err != nil {
		return pathErr("copy", from, err)
	}
	return nil
}

// Create spools the file to a temporary local file and uploads it on
// Commit, so the object appears whole or not at all.
func (s *S3) Create(name string, _ fs.FileMode) (Writer, error) {
	if err := os.MkdirAll(s.tmp, 0o700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(s.tmp, "mangarr-upload-*")
	if err != nil {
		return nil, err
	}
	return &s3Writer{s: s, key: Key(name), name: name, f: f}, nil
}

// Upload sends a local file as name, keeping its modification time.
func (s *S3) Upload(ctx context.Context, local, name string) error {
	st, err := os.Stat(local)
	if err != nil {
		return err
	}
	key := Key(name)
	defer s.forget(key)
	return s.c.put(ctx, key, func() (io.ReadCloser, int64, error) {
		f, err := os.Open(local)
		return f, st.Size(), err
	}, st.ModTime())
}

// Head reports the size of the object at name.
func (s *S3) Head(ctx context.Context, name string) (int64, error) {
	info, err := s.c.head(ctx, Key(name))
	return info.Size, err
}

type s3Writer struct {
	s         *S3
	key, name string
	f         *os.File
	n         int64
	finished  bool
}

func (w *s3Writer) Write(p []byte) (int, error) {
	n, err := w.f.Write(p)
	w.n += int64(n)
	return n, err
}

func (w *s3Writer) Commit() (int64, error) {
	if w.finished {
		return 0, os.ErrClosed
	}
	w.finished = true
	tmp := w.f.Name()
	defer os.Remove(tmp)
	if err := w.f.Close(); err != nil {
		return 0, err
	}
	defer w.s.forget(w.key)
	err := w.s.c.put(context.Background(), w.key, func() (io.ReadCloser, int64, error) {
		f, err := os.Open(tmp)
		return f, w.n, err
	}, time.Now())
	if err != nil {
		return 0, pathErr("write", w.name, err)
	}
	return w.n, nil
}

func (w *s3Writer) Abort() {
	if w.finished {
		return
	}
	w.finished = true
	_ = w.f.Close()
	_ = os.Remove(w.f.Name())
}

type fileInfo struct {
	name string
	size int64
	mod  time.Time
	dir  bool
}

func (f fileInfo) Name() string       { return f.name }
func (f fileInfo) Size() int64        { return f.size }
func (f fileInfo) ModTime() time.Time { return f.mod }
func (f fileInfo) IsDir() bool        { return f.dir }
func (f fileInfo) Sys() any           { return nil }
func (f fileInfo) Mode() fs.FileMode {
	if f.dir {
		return fs.ModeDir | 0o775
	}
	return 0o664
}

// s3File reads an object: sequential reads stream one ranged GET from the
// current offset, and ReadAt fetches (and keeps a few) blocks, which is
// what reading a zip's directory needs.
type s3File struct {
	s    *S3
	key  string
	info objectInfo

	mu      sync.Mutex
	off     int64
	body    io.ReadCloser
	bodyOff int64
	blocks  []block
	closed  bool
}

type block struct {
	at   int64
	data []byte
}

const (
	blockSize = 512 << 10
	maxBlocks = 8
)

func (f *s3File) Stat() (fs.FileInfo, error) {
	return fileInfo{name: path.Base("/" + f.key), size: f.info.Size, mod: f.info.ModTime}, nil
}

func (f *s3File) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, os.ErrClosed
	}
	if f.off >= f.info.Size {
		return 0, io.EOF
	}
	if f.body == nil || f.bodyOff != f.off {
		if f.body != nil {
			f.body.Close()
		}
		rc, err := f.s.c.get(context.Background(), f.key, f.off, -1)
		if err != nil {
			f.body = nil
			return 0, err
		}
		f.body, f.bodyOff = rc, f.off
	}
	n, err := f.body.Read(p)
	f.off += int64(n)
	f.bodyOff += int64(n)
	if err == io.EOF && f.off < f.info.Size {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func (f *s3File) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += f.off
	case io.SeekEnd:
		offset += f.info.Size
	default:
		return 0, errors.New("seek: bad whence")
	}
	if offset < 0 {
		return 0, errors.New("seek: negative position")
	}
	f.off = offset
	return offset, nil
}

func (f *s3File) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("readat: negative offset")
	}
	if off >= f.info.Size {
		return 0, io.EOF
	}
	want := int64(len(p))
	short := false
	if off+want > f.info.Size {
		want, short = f.info.Size-off, true
	}
	if want > 2*blockSize {
		rc, err := f.s.c.get(context.Background(), f.key, off, want)
		if err != nil {
			return 0, err
		}
		defer rc.Close()
		n, err := io.ReadFull(rc, p[:want])
		if err == nil && short {
			err = io.EOF
		}
		return n, err
	}
	n := 0
	for int64(n) < want {
		pos := off + int64(n)
		b, err := f.block(pos / blockSize * blockSize)
		if err != nil {
			return n, err
		}
		n += copy(p[n:want], b[pos-pos/blockSize*blockSize:])
	}
	if short {
		return n, io.EOF
	}
	return n, nil
}

func (f *s3File) block(at int64) ([]byte, error) {
	f.mu.Lock()
	for i, b := range f.blocks {
		if b.at == at {
			// most recently used last
			f.blocks = append(append(f.blocks[:i:i], f.blocks[i+1:]...), b)
			f.mu.Unlock()
			return b.data, nil
		}
	}
	f.mu.Unlock()
	n := int64(blockSize)
	if at+n > f.info.Size {
		n = f.info.Size - at
	}
	rc, err := f.s.c.get(context.Background(), f.key, at, n)
	if err != nil {
		return nil, err
	}
	data := make([]byte, n)
	_, err = io.ReadFull(rc, data)
	rc.Close()
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	if len(f.blocks) >= maxBlocks {
		f.blocks = f.blocks[1:]
	}
	f.blocks = append(f.blocks, block{at, data})
	f.mu.Unlock()
	return data, nil
}

func (f *s3File) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	f.blocks = nil
	if f.body != nil {
		err := f.body.Close()
		f.body = nil
		return err
	}
	return nil
}

// CreateBucket makes the bucket if it isn't there yet.
func CreateBucket(ctx context.Context, s *S3) error {
	resp, err := s.c.do(ctx, "PUT", "", nil, nil, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode == 200 || resp.StatusCode == 409 {
		resp.Body.Close()
		return nil
	}
	return readS3Error(resp, "CREATE", s.c.cfg.Bucket)
}
