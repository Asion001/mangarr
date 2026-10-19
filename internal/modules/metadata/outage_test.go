package metadata

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func response(code int, header http.Header, body string) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: code, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

func TestStatusErrorIsOneLine(t *testing.T) {
	body := `{"title":"Error 522: Connection timed out","status":522,"retryable":true,"retry_after":120,"ray_id":"abc","detail":"long text"}`
	e := StatusError("example", response(522, nil, body))
	if got, want := e.Error(), "example: HTTP 522 (connection timed out)"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if e.RetryAfter != 2*time.Minute || !e.Retryable() {
		t.Fatalf("retry after %s, retryable %v", e.RetryAfter, e.Retryable())
	}
	h := http.Header{"Retry-After": {"30"}}
	if e := StatusError("example", response(503, h, "<html>")); e.RetryAfter != 30*time.Second || e.Error() != "example: HTTP 503 (Service Unavailable)" {
		t.Fatalf("header: %s, %q", e.RetryAfter, e.Error())
	}
	if e := StatusError("example", response(401, nil, "")); e.Retryable() {
		t.Fatal("401 is not retryable")
	}
}

func TestBackoffGrowsAndHonorsRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	b := &Backoff{now: func() time.Time { return now }}
	down := &ProviderError{Provider: "example", Status: 522}

	b.Record(down)
	var paused *PausedError
	if err := b.Check(); !errors.As(err, &paused) || paused.Until != now.Add(time.Minute) || !Retryable(err) {
		t.Fatalf("first failure: %v", err)
	}
	// a paused call doesn't count as another failure
	b.Record(b.Check())
	now = now.Add(time.Minute)
	if err := b.Check(); err != nil {
		t.Fatalf("pause should be over: %v", err)
	}
	b.Record(down)
	if err := b.Check(); !errors.As(err, &paused) || paused.Until != now.Add(2*time.Minute) {
		t.Fatalf("second failure: %v", err)
	}
	now = now.Add(2 * time.Minute)
	b.Record(&ProviderError{Provider: "example", Status: 503, RetryAfter: time.Hour})
	if err := b.Check(); !errors.As(err, &paused) || paused.Until != now.Add(time.Hour) {
		t.Fatalf("retry after: %v", err)
	}
	b.Record(nil)
	if err := b.Check(); err != nil {
		t.Fatalf("success should end the pause: %v", err)
	}
	b.Record(fmt.Errorf("wrapped: %w", ErrNotFound))
	if err := b.Check(); err != nil {
		t.Fatalf("not found is no outage: %v", err)
	}
}
