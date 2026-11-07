package sso

import "testing"

func TestLocalPath(t *testing.T) {
	for p, want := range map[string]bool{
		"/": true, "/series/3": true, "/account?tab=1": true,
		"": false, "series": false, "//evil.example": false, "/\\evil.example": false,
		"/\t/evil.example": false, "/\n/evil.example": false, "https://evil.example": false, "/a\\b": false,
	} {
		if got := LocalPath(p); got != want {
			t.Errorf("LocalPath(%q) = %v, want %v", p, got, want)
		}
	}
}
