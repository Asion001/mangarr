package sourcekit

import "testing"

func TestDLEGuardChallengeIsRecognized(t *testing.T) {
	err := &StatusError{Code: 404, URL: "https://example.test/_c?token=x", Body: "guard page body is truncated before its script"}
	if !err.Challenged() {
		t.Fatal("DLE guard should be handed to the configured browser challenge solver")
	}
	if (&StatusError{Code: 404, Body: "ordinary missing page"}).Challenged() {
		t.Fatal("an ordinary 404 is not a browser challenge")
	}
}
