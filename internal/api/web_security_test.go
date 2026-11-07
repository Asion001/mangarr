package api_test

import (
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/Asion001/mangarr/internal/config"
)

// session signs up the first admin and returns its cookie.
func session(t *testing.T, base string) *http.Cookie {
	t.Helper()
	resp, err := http.Post(base+"/api/v1/auth/setup", "application/json", strings.NewReader(`{"username":"admin","password":"secret123"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == "mangarr_session" {
			return c
		}
	}
	t.Fatalf("no session cookie: %d", resp.StatusCode)
	return nil
}

func TestCrossSiteRequestsRefused(t *testing.T) {
	srv, _ := newServer(t, false)
	c := session(t, srv.URL)
	post := func(headers map[string]string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/command", strings.NewReader(`{"name":"DiskScan"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(c)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(map[string]string{"Sec-Fetch-Site": "same-site"}); code != http.StatusForbidden {
		t.Errorf("a sibling site: %d", code)
	}
	if code := post(map[string]string{"Origin": "https://other.example"}); code != http.StatusForbidden {
		t.Errorf("another origin: %d", code)
	}
	if code := post(map[string]string{"Sec-Fetch-Site": "same-origin"}); code == http.StatusForbidden {
		t.Errorf("the UI itself: %d", code)
	}
	if code := post(map[string]string{"Origin": srv.URL}); code == http.StatusForbidden {
		t.Errorf("the UI's own origin: %d", code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	srv, _ := newServer(t, false)
	resp, err := http.Get(srv.URL + "/api/v1/auth/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("API headers: %v", resp.Header)
	}
}

// A client can't pose as a new address on every try to get around the
// failed-login limit.
func TestLoginLimitIgnoresForgedAddresses(t *testing.T) {
	// this test's client (loopback) is no proxy here
	srv, _ := newServerWith(t, func(c *config.Config) { c.TrustedProxies = []netip.Prefix{} })
	session(t, srv.URL)
	last := 0
	for i := 0; i < 40; i++ {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"wrong-pass"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "203.0.113."+strconv.Itoa(i+1))
		req.Header.Set("X-Real-IP", "198.51.100."+strconv.Itoa(i+1))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		last = resp.StatusCode
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("still answering %d after 40 failures", last)
	}
}
