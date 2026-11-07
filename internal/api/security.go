package api

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Asion001/mangarr/internal/access"
)

// apiPolicy is the Content-Security-Policy of API answers: nothing they
// return is a page, so a file or image opened directly (a cover, a CBZ, a
// log) can't run script, load anything or be framed.
const apiPolicy = "default-src 'none'; frame-ancestors 'none'; sandbox"

// securityHeaders sets the headers every answer gets: browsers mustn't
// guess types (an image that sniffs as HTML stays an image), referrers stay
// on this site, and API answers get apiPolicy. The UI's own policy is set by
// staticHandler. docsPath (the interactive API docs) loads its own scripts.
func securityHeaders(docsPath string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "same-origin")
			if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != docsPath {
				h.Set("Content-Security-Policy", apiPolicy)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// inlineScript finds the inline scripts of index.html (the theme set before
// the app loads), which the policy allows by hash.
var inlineScript = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

// uiPolicy is the UI's Content-Security-Policy: its own scripts (and the
// inline ones of index, by hash) plus Spotify's player, images and sounds
// from anywhere (covers in search results come from the providers), and
// frameAncestors may frame it (MANGARR_FRAME_ANCESTORS).
func uiPolicy(index []byte, frameAncestors string) string {
	scripts := []string{"'self'", "https://sdk.scdn.co"}
	for _, m := range inlineScript.FindAllSubmatch(index, -1) {
		sum := sha256.Sum256(m[1])
		scripts = append(scripts, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	if frameAncestors == "" {
		frameAncestors = "'self'"
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + strings.Join(scripts, " "),
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob: https: http:",
		"media-src 'self' data: blob: https:",
		"font-src 'self' data:",
		"connect-src 'self' https: wss:",
		"frame-src https://sdk.scdn.co https://open.spotify.com",
		"worker-src 'self' blob:",
		"manifest-src 'self'",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors " + frameAncestors,
	}, "; ")
}

// crossSite reports whether an unsafe request riding on a browser session
// came from another site (cross-site request forgery). SameSite=Lax keeps
// the cookie off cross-site POSTs, but not off ones from a sibling
// subdomain (other self-hosted apps), so ask the browser where it came
// from: Sec-Fetch-Site, else Origin. Requests with neither (old clients,
// scripts) aren't from a page and pass.
func crossSite(r *http.Request, p *access.Principal) bool {
	if p == nil || p.SessionID == "" {
		return false // API keys and worker keys aren't sent by browsers on their own
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return false
	case "":
	default:
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return true
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h // only there when a trusted proxy set it
	}
	return !strings.EqualFold(u.Host, host)
}
