package clientip

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func seen(t *testing.T, remote string, headers map[string]string) (addr, proto string) {
	t.Helper()
	h := Middleware(DefaultTrusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		addr, proto = r.RemoteAddr, r.Header.Get("X-Forwarded-Proto")
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remote
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	h.ServeHTTP(httptest.NewRecorder(), r)
	return addr, proto
}

func TestUntrustedPeerCantPoseAsAnother(t *testing.T) {
	addr, proto := seen(t, "203.0.113.7:5555", map[string]string{"X-Forwarded-For": "1.2.3.4", "X-Real-IP": "5.6.7.8", "X-Forwarded-Proto": "https"})
	if addr != "203.0.113.7:5555" || proto != "" {
		t.Fatalf("addr %q proto %q", addr, proto)
	}
}

func TestTrustedProxy(t *testing.T) {
	// the client wrote 9.9.9.9 itself; the proxy appended the address it saw
	addr, proto := seen(t, "172.18.0.2:4000", map[string]string{"X-Forwarded-For": "9.9.9.9, 198.51.100.20", "X-Forwarded-Proto": "https"})
	if addr != "198.51.100.20" || proto != "https" {
		t.Fatalf("addr %q proto %q", addr, proto)
	}
	if addr, _ := seen(t, "127.0.0.1:1", map[string]string{"X-Real-IP": "198.51.100.9"}); addr != "198.51.100.9" {
		t.Fatalf("X-Real-IP: %q", addr)
	}
	// a LAN client through the proxy is that LAN address
	if addr, _ := seen(t, "127.0.0.1:1", map[string]string{"X-Forwarded-For": "192.168.1.5"}); addr != "192.168.1.5" {
		t.Fatalf("LAN client: %q", addr)
	}
}

func TestParse(t *testing.T) {
	if p, err := Parse("none"); err != nil || len(p) != 0 {
		t.Fatalf("none: %v %v", p, err)
	}
	if p, err := Parse("10.1.2.3, 2001:db8::/32"); err != nil || len(p) != 2 || p[0].Bits() != 32 {
		t.Fatalf("list: %v %v", p, err)
	}
	if _, err := Parse("nope"); err == nil {
		t.Fatal("expected an error")
	}
}
