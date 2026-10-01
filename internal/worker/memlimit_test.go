package worker

import "testing"

func TestHalfOf(t *testing.T) {
	for in, want := range map[string]int64{
		"4294967296\n":        2147483648,
		"max\n":               0,
		"9223372036854771712": 0, // cgroup v1 without a limit
		"":                    0,
	} {
		if got := halfOf(in); got != want {
			t.Errorf("halfOf(%q) = %d, want %d", in, got, want)
		}
	}
}
