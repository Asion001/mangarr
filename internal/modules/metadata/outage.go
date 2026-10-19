package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// ProviderError is a provider's failed response, summarized to one line: the
// body (often a Cloudflare error page or JSON) is dropped.
type ProviderError struct {
	Provider string
	Status   int
	// RetryAfter is the provider's hint (Retry-After header or a JSON
	// retry_after field), zero when it gave none.
	RetryAfter time.Duration
}

func (e *ProviderError) Error() string {
	msg := fmt.Sprintf("%s: HTTP %d", e.Provider, e.Status)
	if t := statusText(e.Status); t != "" {
		msg += " (" + t + ")"
	}
	return msg
}

// Retryable reports a failure on the provider's side that should pass.
func (e *ProviderError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500 || e.RetryAfter > 0
}

// cloudflareText names Cloudflare's own 52x codes, which net/http doesn't know.
var cloudflareText = map[int]string{
	520: "unknown error",
	521: "web server is down",
	522: "connection timed out",
	523: "origin is unreachable",
	524: "a timeout occurred",
	525: "SSL handshake failed",
	526: "invalid SSL certificate",
}

func statusText(code int) string {
	if t := http.StatusText(code); t != "" {
		return t
	}
	return cloudflareText[code]
}

// StatusError reads a non-2xx response into a ProviderError.
func StatusError(provider string, resp *http.Response) *ProviderError {
	e := &ProviderError{Provider: provider, Status: resp.StatusCode, RetryAfter: retryAfterHeader(resp.Header.Get("Retry-After"))}
	if e.RetryAfter == 0 {
		var body struct {
			RetryAfter json.Number `json:"retry_after"`
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if json.Unmarshal(raw, &body) == nil {
			if s, err := body.RetryAfter.Float64(); err == nil && s > 0 {
				e.RetryAfter = time.Duration(s * float64(time.Second))
			}
		}
	}
	return e
}

func retryAfterHeader(v string) time.Duration {
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// Retryable reports whether err is a failure that should pass by itself: the
// provider is down, overloaded or unreachable, not misconfigured.
func Retryable(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var r interface{ Retryable() bool }
	if errors.As(err, &r) {
		return r.Retryable()
	}
	var ne net.Error
	return errors.As(err, &ne)
}

// PausedError is returned while a provider is backed off after failures.
type PausedError struct {
	Err   error
	Until time.Time
}

func (e *PausedError) Error() string {
	return fmt.Sprintf("%v, next try at %s", e.Err, e.Until.Local().Format("15:04"))
}

func (e *PausedError) Unwrap() error   { return e.Err }
func (e *PausedError) Retryable() bool { return true }

// backoffSteps grow with each failure in a row while a provider stays down.
var backoffSteps = []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour}

// maxRetryAfter caps a provider's own hint.
const maxRetryAfter = 6 * time.Hour

// Backoff keeps a provider's callers from hammering it while it is down:
// after a retryable failure calls fail fast until a growing delay (or the
// provider's retry hint, if longer) has passed.
type Backoff struct {
	mu       sync.Mutex
	failures int
	until    time.Time
	last     error
	// now is replaced in tests.
	now func() time.Time
}

func (b *Backoff) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// Check returns a PausedError while the provider is backed off.
func (b *Backoff) Check() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.last != nil && b.clock().Before(b.until) {
		return &PausedError{Err: b.last, Until: b.until}
	}
	return nil
}

// Record notes a call's outcome. Anything but a retryable failure (success,
// not found, a bad request) ends the backoff.
func (b *Backoff) Record(err error) {
	if errors.Is(err, context.Canceled) {
		return // the caller gave up; says nothing about the provider
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err == nil || !Retryable(err) {
		b.failures, b.until, b.last = 0, time.Time{}, nil
		return
	}
	var paused *PausedError
	if errors.As(err, &paused) {
		return
	}
	b.failures++
	delay := backoffSteps[min(b.failures, len(backoffSteps))-1]
	var pe *ProviderError
	if errors.As(err, &pe) && pe.RetryAfter > delay {
		delay = min(pe.RetryAfter, maxRetryAfter)
	}
	b.last, b.until = err, b.clock().Add(delay)
}
