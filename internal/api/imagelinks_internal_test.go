package api

import (
	"testing"
	"time"
)

func TestImageLinkTokens(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	tok := signImage(key, fileSubject(7, "abc"), imagePeriod(now))
	if !verifyImage(key, fileSubject(7, "abc"), tok, now) {
		t.Fatal("a fresh token must verify")
	}
	if !verifyImage(key, fileSubject(7, "abc"), tok, now.Add(imageLinkPeriod)) {
		t.Fatal("a token from the last period still works")
	}
	if verifyImage(key, fileSubject(7, "abc"), tok, now.Add(2*imageLinkPeriod)) {
		t.Fatal("a token two periods old must not work")
	}
	if verifyImage(key, fileSubject(7, "abd"), tok, now) || verifyImage(key, fileSubject(8, "abc"), tok, now) {
		t.Fatal("a token is for one file's bytes only")
	}
	if verifyImage([]byte("another key, another key, another"), fileSubject(7, "abc"), tok, now) {
		t.Fatal("a token made with another key must not work")
	}
	future := signImage(key, fileSubject(7, "abc"), imagePeriod(now)+1)
	if verifyImage(key, fileSubject(7, "abc"), future, now) {
		t.Fatal("a token from a later period must not work")
	}
	for _, bad := range []string{"", ".", "x.y", tok + "x"} {
		if verifyImage(key, fileSubject(7, "abc"), bad, now) {
			t.Fatalf("%q must not verify", bad)
		}
	}
}
