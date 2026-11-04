package api

import (
	"testing"
	"time"
)

func TestReportedAt(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	for _, c := range []struct {
		name string
		in   *time.Time
		want time.Time
		err  bool
	}{
		{"none is now", nil, time.Time{}, false},
		{"an hour ago", at(-time.Hour), now.Add(-time.Hour), false},
		{"clock a little ahead counts as now", at(30 * time.Second), now, false},
		{"the future", at(time.Hour), time.Time{}, true},
		{"too old is clamped", at(-90 * 24 * time.Hour), now.Add(-lateReportLimit), false},
	} {
		got, err := reportedAt(c.in, now)
		if (err != nil) != c.err || !got.Equal(c.want) {
			t.Errorf("%s: got %v, %v; want %v (error %v)", c.name, got, err, c.want, c.err)
		}
	}
}
