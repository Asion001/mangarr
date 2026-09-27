package sourcekit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/publicsuffix"
)

// UserAgent is what sites see. Many refuse anything that looks automated.
const UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

// maxBody caps a single response (a page image is fetched elsewhere).
const maxBody = 32 << 20

// Client is the HTTP client sites use: a cookie jar, a browser user agent,
// and (when configured) a challenge solver for sites behind Cloudflare.
type Client struct {
	HTTP      *http.Client
	UserAgent string
	// Solver answers a challenge and returns cookies (nil when not set up).
	Solver *Solver
}

// NewClient builds a client. Pass nil to get one with sane defaults.
func NewClient(hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	if hc.Jar == nil {
		jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
		if err == nil {
			hc.Jar = jar
		}
	}
	return &Client{HTTP: hc, UserAgent: UserAgent}
}

// StatusError is an unhappy response. Its message names the status so the
// core's throttling sees "HTTP error 429" and backs the catalog off.
type StatusError struct {
	Code int
	URL  string
	Body string
}

func (e *StatusError) Error() string {
	msg := fmt.Sprintf("HTTP error %d for %s", e.Code, e.URL)
	if e.Body != "" {
		msg += ": " + e.Body
	}
	if e.Code == http.StatusForbidden && looksChallenged(e.Body) {
		msg += " (cloudflare)"
	}
	return msg
}

// Challenged reports whether the site asked for a browser challenge.
func (e *StatusError) Challenged() bool {
	if u, err := url.Parse(e.URL); err == nil && u.Path == "/_c" {
		return true
	}
	return looksChallenged(e.Body)
}

func looksChallenged(body string) bool {
	b := strings.ToLower(body)
	for _, s := range []string{"just a moment", "cf-chl", "cloudflare", "checking your browser", "attention required",
		`pow_nonce`, `x.open("post", "/_v"`, "для доступа к сайту необходимо включить javascript"} {
		if strings.Contains(b, s) {
			return true
		}
	}
	return false
}

// Request describes one request to a site. Body is kept as bytes, not a
// reader, so a request that has to be retried (through the challenge solver)
// can be sent again unchanged.
type Request struct {
	Method  string
	URL     string
	Headers map[string]string
	Query   url.Values
	Body    []byte
}

// Do performs a request, retrying once through the challenge solver when the
// site turns it away.
func (c *Client) Do(ctx context.Context, r Request) ([]byte, error) {
	data, err := c.do(ctx, r)
	var se *StatusError
	if err != nil && c.Solver != nil && asStatus(err, &se) && se.Challenged() {
		if solveErr := c.Solver.Solve(ctx, c, r.URL); solveErr != nil {
			return nil, fmt.Errorf("%w (the challenge solver said: %v)", err, solveErr)
		}
		return c.do(ctx, r)
	}
	return data, err
}

func asStatus(err error, out **StatusError) bool {
	for e := err; e != nil; {
		if se, ok := e.(*StatusError); ok {
			*out = se
			return true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}

func (c *Client) do(ctx context.Context, r Request) ([]byte, error) {
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	u := r.URL
	if len(r.Query) > 0 {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u += sep + r.Query.Encode()
	}
	var send io.Reader
	if len(r.Body) > 0 {
		send = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, send)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &StatusError{Code: resp.StatusCode, URL: resp.Request.URL.String(), Body: snippet(body)}
	}
	return body, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return strings.Join(strings.Fields(s), " ")
}

// JSON performs a request and decodes the answer.
func (c *Client) JSON(ctx context.Context, r Request, out any) error {
	if r.Headers == nil {
		r.Headers = map[string]string{}
	}
	if _, ok := r.Headers["Accept"]; !ok {
		r.Headers["Accept"] = "application/json"
	}
	data, err := c.Do(ctx, r)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s: %w", r.URL, err)
	}
	return nil
}

// Document performs a request and parses the answer as HTML, for sites
// without an API. Use CSS selectors on the result.
func (c *Client) Document(ctx context.Context, r Request) (*goquery.Document, error) {
	body, err := c.Do(ctx, r)
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.URL, err)
	}
	return doc, nil
}

// Abs turns a site-relative link into an absolute URL.
func Abs(base, href string) string {
	if href == "" {
		return ""
	}
	b, err := url.Parse(base)
	if err != nil {
		return href
	}
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return href
	}
	return b.ResolveReference(u).String()
}

// Path is the site-relative part of a URL, which is what mangarr stores as a
// manga's or chapter's identity (it survives a domain change).
func Path(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.RawQuery != "" {
		return u.Path + "?" + u.RawQuery
	}
	return u.Path
}
