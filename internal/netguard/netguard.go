// Package netguard makes HTTP clients for URLs that come from user data
// (e.g. cover links in an uploaded backup) that can't reach the local
// network, loopback or cloud metadata addresses.
package netguard

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"sync/atomic"
	"syscall"
	"time"
)

// ErrBlocked is returned for connections to non-public addresses.
var ErrBlocked = errors.New("address not allowed")

// blocked are the special-purpose ranges (IANA registries) that are never
// someone's public web server: private networks, CGNAT (Tailscale and other
// overlay networks), loopback, link-local and cloud metadata, benchmarking,
// documentation, multicast and reserved space, and the IPv6 forms that
// embed IPv4 addresses (NAT64, 6to4, Teredo) so they can't smuggle one in.
var blocked = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
		"192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/32", "2001:db8::/32", "2002::/16",
		"fc00::/7", "fe80::/10", "ff00::/8",
	} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

// allowPrivate lets tests reach their own httptest servers.
var allowPrivate atomic.Bool

// AllowPrivateForTests turns the guard off until the returned func runs.
// Only tests call it.
func AllowPrivateForTests() (restore func()) {
	old := allowPrivate.Swap(true)
	return func() { allowPrivate.Store(old) }
}

// Public reports whether ip is a public unicast address.
func Public(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	return PublicAddr(addr)
}

// PublicAddr is Public for a netip.Addr.
func PublicAddr(addr netip.Addr) bool {
	addr = addr.Unmap() // ::ffff:10.0.0.1 is 10.0.0.1
	if !addr.IsValid() || addr.Zone() != "" {
		return false
	}
	for _, p := range blocked {
		if p.Contains(addr) {
			return false
		}
	}
	return addr.IsGlobalUnicast()
}

// Client returns an HTTP client that only connects to public addresses
// (checked at connect time, so DNS answers can't point it elsewhere).
func Client(timeout time.Duration) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, Control: func(network, address string, _ syscall.RawConn) error {
		if allowPrivate.Load() {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(host); ip == nil || !Public(ip) {
			return ErrBlocked
		}
		return nil
	}}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = d.DialContext
	tr.Proxy = nil
	return &http.Client{Timeout: timeout, Transport: tr}
}
