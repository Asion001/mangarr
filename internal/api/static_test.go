package api

import "testing"

func TestWithBase(t *testing.T) {
	for _, in := range []string{`<head><base href="/" /></head>`, `<head><base href="/"></head>`, `<head><base href="/"/></head>`} {
		if got := string(withBase([]byte(in), "/mangarr")); got != `<head><base href="/mangarr/" /></head>` {
			t.Errorf("withBase(%q) = %q", in, got)
		}
	}
}
