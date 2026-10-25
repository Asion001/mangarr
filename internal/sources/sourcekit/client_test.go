package sourcekit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestDLEGuardChallengeIsRecognized(t *testing.T) {
	err := &StatusError{Code: 404, URL: "https://example.test/_c?token=x", Body: "guard page body is truncated before its script"}
	if !err.Challenged() {
		t.Fatal("DLE guard should be handed to the configured browser challenge solver")
	}
	if (&StatusError{Code: 404, Body: "ordinary missing page"}).Challenged() {
		t.Fatal("an ordinary 404 is not a browser challenge")
	}
}

// TestPrivateRequestKeepsNoCookies: a request signed in as a person sends
// the site's cookies but keeps none it sets.
func TestPrivateRequestKeepsNoCookies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: r.URL.Path[1:], Value: "1", Path: "/"})
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := NewClient(srv.Client())
	ctx := context.Background()
	if _, err := c.Do(ctx, Request{URL: srv.URL + "/shared"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(ctx, Request{URL: srv.URL + "/session", Private: true}); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(srv.URL)
	var names []string
	for _, ck := range c.HTTP.Jar.Cookies(u) {
		names = append(names, ck.Name)
	}
	if len(names) != 1 || names[0] != "shared" {
		t.Fatalf("cookies kept = %v", names)
	}
}
