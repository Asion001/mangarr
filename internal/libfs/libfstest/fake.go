// Package libfstest runs an in-memory S3-compatible server for tests, and
// can put the library on it.
package libfstest

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/libfs"
)

type object struct {
	data  []byte
	mod   time.Time
	mtime string
}

// Server is a fake S3 service holding one bucket in memory. It checks
// that requests are signed, not the signatures.
type Server struct {
	*httptest.Server
	Bucket string

	mu       sync.Mutex
	objects  map[string]object
	requests map[string]int
}

// Requests counts the requests made with method.
func (s *Server) Requests(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[method]
}

// NewServer starts a fake service; it stops with the test.
func NewServer(t testing.TB) *Server {
	s := &Server{Bucket: "test", objects: map[string]object{}, requests: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Config is how to reach the fake bucket.
func (s *Server) Config(t testing.TB) libfs.S3Config {
	return libfs.S3Config{Endpoint: s.URL, Bucket: s.Bucket, Region: "us-east-1", AccessKey: "test", SecretKey: "test", PathStyle: true, TempDir: t.TempDir()}
}

// Keys lists the stored keys.
func (s *Server) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for k := range s.objects {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Use puts the folders dirs on a new fake bucket for the rest of the test.
func Use(t testing.TB, dirs ...string) (*Server, *libfs.S3) {
	s := NewServer(t)
	fs, err := libfs.NewS3(s.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	libfs.Use(fs)
	t.Cleanup(func() { libfs.Use(nil) })
	for _, d := range dirs {
		libfs.Mount(d)
	}
	return s, fs
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") && r.URL.Query().Get("X-Amz-Signature") == "" {
		http.Error(w, "<Error><Code>AccessDenied</Code></Error>", http.StatusForbidden)
		return
	}
	s.mu.Lock()
	s.requests[r.Method]++
	s.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, "/")
	bucket, key, _ := strings.Cut(p, "/")
	if bucket != s.Bucket {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "<Error><Code>NoSuchBucket</Code></Error>")
		return
	}
	if key == "" {
		if r.Method == http.MethodGet {
			s.list(w, r.URL.Query())
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	switch r.Method {
	case http.MethodHead, http.MethodGet:
		s.mu.Lock()
		o, ok := s.objects[key]
		s.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			if r.Method == http.MethodGet {
				fmt.Fprint(w, "<Error><Code>NoSuchKey</Code></Error>")
			}
			return
		}
		w.Header().Set("Last-Modified", o.mod.UTC().Format(http.TimeFormat))
		w.Header().Set("ETag", `"x"`)
		if o.mtime != "" {
			w.Header().Set("X-Amz-Meta-Mtime", o.mtime)
		}
		data := o.data
		status := http.StatusOK
		if rg := r.Header.Get("Range"); rg != "" {
			from, to, _ := strings.Cut(strings.TrimPrefix(rg, "bytes="), "-")
			a, _ := strconv.ParseInt(from, 10, 64)
			b := int64(len(data)) - 1
			if to != "" {
				b, _ = strconv.ParseInt(to, 10, 64)
			}
			if b >= int64(len(data)) {
				b = int64(len(data)) - 1
			}
			if a >= int64(len(data)) {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			data, status = data[a:b+1], http.StatusPartialContent
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(status)
		if r.Method == http.MethodGet {
			_, _ = w.Write(data)
		}
	case http.MethodPut:
		o := object{mod: time.Now(), mtime: r.Header.Get("X-Amz-Meta-Mtime")}
		if src := r.Header.Get("X-Amz-Copy-Source"); src != "" {
			src, _ = url.PathUnescape(src)
			_, sk, _ := strings.Cut(strings.TrimPrefix(src, "/"), "/")
			s.mu.Lock()
			from, ok := s.objects[sk]
			s.mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, "<Error><Code>NoSuchKey</Code></Error>")
				return
			}
			o.data, o.mtime = from.data, from.mtime
			defer fmt.Fprint(w, "<CopyObjectResult></CopyObjectResult>")
		} else {
			data, err := io.ReadAll(r.Body)
			if err != nil || int64(len(data)) != r.ContentLength {
				http.Error(w, "short body", http.StatusBadRequest)
				return
			}
			o.data = data
		}
		s.mu.Lock()
		s.objects[key] = o
		s.mu.Unlock()
	case http.MethodDelete:
		s.mu.Lock()
		delete(s.objects, key)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) list(w http.ResponseWriter, q url.Values) {
	prefix, delim := q.Get("prefix"), q.Get("delimiter")
	max, _ := strconv.Atoi(q.Get("max-keys"))
	if max <= 0 {
		max = 1000
	}
	start := q.Get("continuation-token")
	s.mu.Lock()
	var keys []string
	for k := range s.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	type content struct {
		Key          string
		Size         int
		LastModified string
	}
	var out struct {
		XMLName               xml.Name `xml:"ListBucketResult"`
		Contents              []content
		CommonPrefixes        []struct{ Prefix string }
		IsTruncated           bool
		NextContinuationToken string `xml:",omitempty"`
	}
	seen := map[string]bool{}
	n := 0
	for _, k := range keys {
		if start != "" && k <= start {
			continue
		}
		if n >= max {
			out.IsTruncated = true
			break
		}
		if delim != "" {
			if i := strings.Index(k[len(prefix):], delim); i >= 0 {
				cp := k[:len(prefix)+i+1]
				if !seen[cp] {
					seen[cp] = true
					out.CommonPrefixes = append(out.CommonPrefixes, struct{ Prefix string }{cp})
					n++
				}
				out.NextContinuationToken = k
				continue
			}
		}
		o := s.objects[k]
		out.Contents = append(out.Contents, content{k, len(o.data), o.mod.UTC().Format(time.RFC3339)})
		out.NextContinuationToken = k
		n++
	}
	s.mu.Unlock()
	if !out.IsTruncated {
		out.NextContinuationToken = ""
	}
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(out)
}
