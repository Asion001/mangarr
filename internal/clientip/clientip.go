// Package clientip works out who a request comes from. Forwarding headers
// (X-Forwarded-For, X-Real-IP, X-Forwarded-Proto/Host) are only believed
// when the connection itself comes from a trusted reverse proxy: anyone
// else could write them to look like a new address on every try and get
// around the failed-login limit, or fake the address and scheme mangarr
// builds links from.
package clientip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// DefaultTrusted are the proxies trusted when MANGARR_TRUSTED_PROXIES is
// unset: loopback and private networks (a reverse proxy on the same machine,
// in Docker or on the LAN, or an overlay network like Tailscale).
var DefaultTrusted = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "fc00::/7"} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

// Parse reads a comma-separated list of addresses and CIDR ranges; "none"
// (or "off") trusts no proxy, "private" is DefaultTrusted.
func Parse(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		switch strings.ToLower(part) {
		case "":
			continue
		case "none", "off", "false":
			return []netip.Prefix{}, nil
		case "private", "default":
			out = append(out, DefaultTrusted...)
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(part)
		if err != nil {
			return nil, fmt.Errorf("%q is not an address or CIDR range", part)
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}

func trusted(list []netip.Prefix, ip string) bool {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range list {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// forwarding are the headers a client could use to pose as another
// address, scheme or host.
var forwarding = []string{"X-Forwarded-For", "X-Real-Ip", "True-Client-Ip", "X-Forwarded-Proto", "X-Forwarded-Host", "X-Forwarded-Scheme", "Forwarded"}

// Middleware sets r.RemoteAddr to the client's address. Behind a trusted
// proxy that is the last address in X-Forwarded-For that isn't a trusted
// proxy itself (else X-Real-IP); from anyone else the forwarding headers
// are removed, so nothing later can be fooled by them. A nil list is
// DefaultTrusted.
func Middleware(list []netip.Prefix) func(http.Handler) http.Handler {
	if list == nil {
		list = DefaultTrusted
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer := r.RemoteAddr
			if host, _, err := net.SplitHostPort(peer); err == nil {
				peer = host
			}
			if !trusted(list, peer) {
				for _, h := range forwarding {
					r.Header.Del(h)
				}
				next.ServeHTTP(w, r)
				return
			}
			if ip := fromForwarded(list, r.Header.Values("X-Forwarded-For")); ip != "" {
				r.RemoteAddr = ip
			} else if ip := strings.TrimSpace(r.Header.Get("X-Real-Ip")); net.ParseIP(ip) != nil {
				r.RemoteAddr = ip
			}
			next.ServeHTTP(w, r)
		})
	}
}

// fromForwarded walks X-Forwarded-For from the right (the hops nearest to
// mangarr) past trusted proxies; what's left of that was written by the
// client and proves nothing.
func fromForwarded(list []netip.Prefix, values []string) string {
	var hops []string
	for _, v := range values {
		for _, h := range strings.Split(v, ",") {
			if h = strings.TrimSpace(h); h != "" {
				hops = append(hops, h)
			}
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		if net.ParseIP(hops[i]) == nil {
			return ""
		}
		if !trusted(list, hops[i]) || i == 0 {
			return hops[i]
		}
	}
	return ""
}
