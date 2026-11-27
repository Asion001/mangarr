package libfs

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// S3Config says how to reach an S3-compatible bucket.
type S3Config struct {
	// Endpoint is the service's base URL, e.g. https://s3.example.com; empty
	// means AWS in Region.
	Endpoint string
	Bucket   string
	Region   string
	// Prefix is put in front of every key, so one bucket can hold more.
	Prefix    string
	AccessKey string
	SecretKey string
	// PathStyle addresses the bucket as <endpoint>/<bucket> instead of
	// <bucket>.<endpoint>; most self-hosted services need it.
	PathStyle bool
	// TempDir holds files being uploaded (os.TempDir when empty).
	TempDir string
	// PublicEndpoint is the service's URL as readers' devices reach it, for
	// the signed links downloads are redirected to (Endpoint when empty).
	PublicEndpoint string
}

// s3Client speaks the small part of the S3 API the library needs, signed
// with AWS Signature Version 4.
type s3Client struct {
	cfg    S3Config
	base   *url.URL
	public *url.URL
	http   *http.Client
	prefix string
}

var errS3NotFound = fs.ErrNotExist

// S3Error is an error answer from the service.
type S3Error struct {
	Status  int
	Code    string
	Message string
	Op      string
	Key     string
}

func (e *S3Error) Error() string {
	msg := e.Code
	if e.Message != "" {
		msg += ": " + e.Message
	}
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("s3 %s %s: %d %s", e.Op, e.Key, e.Status, msg)
}

func newS3Client(cfg S3Config) (*s3Client, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("s3: no bucket")
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("s3: no access key or secret key")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	ep := cfg.Endpoint
	if ep == "" {
		ep = "https://s3." + cfg.Region + ".amazonaws.com"
	}
	if !strings.Contains(ep, "://") {
		ep = "https://" + ep
	}
	u, err := url.Parse(strings.TrimRight(ep, "/"))
	if err != nil {
		return nil, fmt.Errorf("s3 endpoint: %w", err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("s3 endpoint %q has no host", cfg.Endpoint)
	}
	public := u
	if cfg.PublicEndpoint != "" {
		pe := cfg.PublicEndpoint
		if !strings.Contains(pe, "://") {
			pe = "https://" + pe
		}
		if public, err = url.Parse(strings.TrimRight(pe, "/")); err != nil || public.Host == "" {
			return nil, fmt.Errorf("s3 public endpoint %q is not a URL", cfg.PublicEndpoint)
		}
	}
	prefix := strings.Trim(cfg.Prefix, "/")
	if prefix != "" {
		prefix += "/"
	}
	return &s3Client{cfg: cfg, base: u, public: public, prefix: prefix, http: &http.Client{
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			MaxIdleConnsPerHost: 32,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 15 * time.Second,
			// a stalled connection must not hang a page forever; bodies
			// themselves may take long (big uploads)
			ResponseHeaderTimeout: 2 * time.Minute,
		},
	}}, nil
}

// objectURL builds the address of key (empty: the bucket) and the path
// that is signed.
func (c *s3Client) objectURL(key string, query url.Values) *url.URL {
	return c.urlAt(c.base, key, query)
}

func (c *s3Client) urlAt(base *url.URL, key string, query url.Values) *url.URL {
	u := *base
	var segs []string
	if c.cfg.PathStyle {
		segs = append(segs, c.cfg.Bucket)
	} else {
		u.Host = c.cfg.Bucket + "." + u.Host
	}
	path := strings.TrimRight(u.Path, "/")
	raw := uriEncode(path, false)
	for _, s := range segs {
		path += "/" + s
		raw += "/" + uriEncode(s, true)
	}
	if key != "" || len(segs) == 0 {
		path += "/" + key
		raw += "/" + uriEncode(key, false)
	}
	u.Path, u.RawPath = path, raw
	u.RawQuery = canonicalQuery(query)
	return &u
}

// uriEncode escapes like S3 expects: everything but unreserved characters,
// and "/" too unless it separates key segments.
func uriEncode(s string, slash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case 'A' <= ch && ch <= 'Z', 'a' <= ch && ch <= 'z', '0' <= ch && ch <= '9',
			ch == '-', ch == '_', ch == '.', ch == '~':
			b.WriteByte(ch)
		case ch == '/' && !slash:
			b.WriteByte(ch)
		default:
			fmt.Fprintf(&b, "%%%02X", ch)
		}
	}
	return b.String()
}

func canonicalQuery(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}

const unsignedPayload = "UNSIGNED-PAYLOAD"

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// sign adds the Signature Version 4 authorization to req.
func (c *s3Client) sign(req *http.Request, payloadHash string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	names := []string{"host"}
	values := map[string]string{"host": req.URL.Host}
	for k, v := range req.Header {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-amz-") || lk == "content-type" || lk == "content-md5" || lk == "range" {
			names = append(names, lk)
			values[lk] = strings.TrimSpace(strings.Join(v, ","))
		}
	}
	sort.Strings(names)
	var ch strings.Builder
	for _, n := range names {
		ch.WriteString(n + ":" + values[n] + "\n")
	}
	signed := strings.Join(names, ";")
	canonical := strings.Join([]string{req.Method, req.URL.EscapedPath(), req.URL.RawQuery, ch.String(), signed, payloadHash}, "\n")
	scope := day + "/" + c.cfg.Region + "/s3/aws4_request"
	sum := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	sig := hex.EncodeToString(hmacSHA256(c.signingKey(day), toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.cfg.AccessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

func (c *s3Client) signingKey(day string) []byte {
	k := hmacSHA256([]byte("AWS4"+c.cfg.SecretKey), day)
	k = hmacSHA256(k, c.cfg.Region)
	k = hmacSHA256(k, "s3")
	return hmacSHA256(k, "aws4_request")
}

// presign returns a link to GET (or PUT) key that works without
// credentials until it expires; extra holds response-* overrides (e.g. the
// download name).
func (c *s3Client) presign(method, key string, ttl time.Duration, extra url.Values, now time.Time) string {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	scope := day + "/" + c.cfg.Region + "/s3/aws4_request"
	q := url.Values{}
	for k, v := range extra {
		q[k] = v
	}
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", c.cfg.AccessKey+"/"+scope)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", strconv.Itoa(int(ttl/time.Second)))
	q.Set("X-Amz-SignedHeaders", "host")
	u := c.urlAt(c.public, c.prefix+key, q)
	canonical := strings.Join([]string{method, u.EscapedPath(), u.RawQuery, "host:" + u.Host + "\n", "host", unsignedPayload}, "\n")
	sum := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	u.RawQuery += "&X-Amz-Signature=" + hex.EncodeToString(hmacSHA256(c.signingKey(day), toSign))
	return u.String()
}

// do sends one request, retrying throttling and server errors a few times
// when the body can be sent again.
func (c *s3Client) do(ctx context.Context, method, key string, query url.Values, header http.Header, body func() (io.ReadCloser, int64, error)) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 300 * time.Millisecond):
			}
		}
		u := c.objectURL(key, query)
		req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
		if err != nil {
			return nil, err
		}
		req.URL = u
		for k, v := range header {
			req.Header[k] = v
		}
		if body != nil {
			rc, n, err := body()
			if err != nil {
				return nil, err
			}
			req.Body, req.ContentLength = rc, n
			if n == 0 {
				req.Body = http.NoBody
			}
		}
		c.sign(req, unsignedPayload, time.Now())
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = readS3Error(resp, method, key)
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

func readS3Error(resp *http.Response, op, key string) error {
	defer resp.Body.Close()
	e := &S3Error{Status: resp.StatusCode, Op: op, Key: key}
	var body struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = xml.Unmarshal(data, &body)
	e.Code, e.Message = body.Code, body.Message
	if resp.StatusCode == http.StatusNotFound && (e.Code == "" || e.Code == "NoSuchKey") {
		return fmt.Errorf("%w: %s", errS3NotFound, key)
	}
	return e
}

// objectInfo is what HEAD and listings tell about an object.
type objectInfo struct {
	Key     string
	Size    int64
	ModTime time.Time
	ETag    string
}

const metaMtime = "X-Amz-Meta-Mtime"

func (c *s3Client) head(ctx context.Context, key string) (objectInfo, error) {
	resp, err := c.do(ctx, http.MethodHead, c.prefix+key, nil, nil, nil)
	if err != nil {
		return objectInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return objectInfo{}, fmt.Errorf("%w: %s", errS3NotFound, key)
	}
	if resp.StatusCode != http.StatusOK {
		return objectInfo{}, &S3Error{Status: resp.StatusCode, Op: "HEAD", Key: key}
	}
	info := objectInfo{Key: key, Size: resp.ContentLength, ETag: strings.Trim(resp.Header.Get("ETag"), `"`)}
	if v, err := strconv.ParseInt(resp.Header.Get(metaMtime), 10, 64); err == nil && v > 0 {
		info.ModTime = time.Unix(v, 0)
	} else if t, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		info.ModTime = t
	}
	return info, nil
}

// get reads n bytes from off (n < 0: to the end).
func (c *s3Client) get(ctx context.Context, key string, off, n int64) (io.ReadCloser, error) {
	h := http.Header{}
	if off > 0 || n >= 0 {
		if n >= 0 {
			if n == 0 {
				return io.NopCloser(strings.NewReader("")), nil
			}
			h.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+n-1))
		} else {
			h.Set("Range", fmt.Sprintf("bytes=%d-", off))
		}
	}
	resp, err := c.do(ctx, http.MethodGet, c.prefix+key, nil, h, nil)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
		return resp.Body, nil
	case http.StatusRequestedRangeNotSatisfiable:
		resp.Body.Close()
		return io.NopCloser(strings.NewReader("")), nil
	}
	return nil, readS3Error(resp, "GET", key)
}

// put uploads size bytes from open as key.
func (c *s3Client) put(ctx context.Context, key string, open func() (io.ReadCloser, int64, error), mtime time.Time) error {
	h := http.Header{}
	h.Set("Content-Type", "application/octet-stream")
	if !mtime.IsZero() {
		h.Set(metaMtime, strconv.FormatInt(mtime.Unix(), 10))
	}
	resp, err := c.do(ctx, http.MethodPut, c.prefix+key, nil, h, open)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return readS3Error(resp, "PUT", key)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

func (c *s3Client) copyObject(ctx context.Context, from, to string) error {
	h := http.Header{}
	h.Set("X-Amz-Copy-Source", "/"+uriEncode(c.cfg.Bucket, true)+"/"+uriEncode(c.prefix+from, false))
	h.Set("X-Amz-Metadata-Directive", "COPY")
	resp, err := c.do(ctx, http.MethodPut, c.prefix+to, nil, h, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return readS3Error(resp, "COPY", from)
	}
	// a copy can fail after the 200 header; the body then holds an error
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if strings.Contains(string(data), "<Error>") {
		var body struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		}
		_ = xml.Unmarshal(data, &body)
		return &S3Error{Status: http.StatusInternalServerError, Code: body.Code, Message: body.Message, Op: "COPY", Key: from}
	}
	return nil
}

func (c *s3Client) del(ctx context.Context, key string) error {
	resp, err := c.do(ctx, http.MethodDelete, c.prefix+key, nil, nil, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return readS3Error(resp, "DELETE", key)
	}
	resp.Body.Close()
	return nil
}

type listPage struct {
	Objects  []objectInfo
	Prefixes []string
	Next     string
}

// list lists keys under prefix; with a delimiter, deeper keys are folded
// into Prefixes. Keys and prefixes come back without the client's prefix.
func (c *s3Client) list(ctx context.Context, prefix, delimiter, token string, max int) (listPage, error) {
	q := url.Values{"list-type": {"2"}, "prefix": {c.prefix + prefix}}
	if delimiter != "" {
		q.Set("delimiter", delimiter)
	}
	if token != "" {
		q.Set("continuation-token", token)
	}
	if max > 0 {
		q.Set("max-keys", strconv.Itoa(max))
	}
	resp, err := c.do(ctx, http.MethodGet, "", q, nil, nil)
	if err != nil {
		return listPage{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return listPage{}, readS3Error(resp, "LIST", prefix)
	}
	defer resp.Body.Close()
	var body struct {
		Contents []struct {
			Key          string `xml:"Key"`
			Size         int64  `xml:"Size"`
			LastModified string `xml:"LastModified"`
			ETag         string `xml:"ETag"`
		} `xml:"Contents"`
		CommonPrefixes []struct {
			Prefix string `xml:"Prefix"`
		} `xml:"CommonPrefixes"`
		IsTruncated           bool   `xml:"IsTruncated"`
		NextContinuationToken string `xml:"NextContinuationToken"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&body); err != nil {
		return listPage{}, fmt.Errorf("s3 list %s: %w", prefix, err)
	}
	var page listPage
	for _, o := range body.Contents {
		t, _ := time.Parse(time.RFC3339, o.LastModified)
		page.Objects = append(page.Objects, objectInfo{Key: strings.TrimPrefix(o.Key, c.prefix), Size: o.Size, ModTime: t, ETag: strings.Trim(o.ETag, `"`)})
	}
	for _, p := range body.CommonPrefixes {
		page.Prefixes = append(page.Prefixes, strings.TrimPrefix(p.Prefix, c.prefix))
	}
	if body.IsTruncated {
		page.Next = body.NextContinuationToken
	}
	return page, nil
}

// listAll calls fn for every object under prefix.
func (c *s3Client) listAll(ctx context.Context, prefix string, fn func(objectInfo) error) error {
	token := ""
	for {
		page, err := c.list(ctx, prefix, "", token, 1000)
		if err != nil {
			return err
		}
		for _, o := range page.Objects {
			if err := fn(o); err != nil {
				return err
			}
		}
		if page.Next == "" {
			return nil
		}
		token = page.Next
	}
}
