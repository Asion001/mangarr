package netguard

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	_, err := Client(5 * time.Second).Get(srv.URL)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v", err)
	}
}

func TestPublic(t *testing.T) {
	for ip, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "192.168.1.1": false, "172.20.0.1": false, "169.254.169.254": false,
		"100.100.1.1": false, "0.0.0.1": false, "198.18.0.1": false, "::1": false, "fd00::1": false, "fe80::1": false,
		"::ffff:10.0.0.1": false, "64:ff9b::a00:1": false, "2002:a00:1::": false, "224.0.0.1": false, "255.255.255.255": false,
	} {
		if got := Public(net.ParseIP(ip)); got != want {
			t.Errorf("Public(%s) = %v, want %v", ip, got, want)
		}
	}
}
