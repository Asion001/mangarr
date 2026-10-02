package model

import "testing"

func TestRouteFor(t *testing.T) {
	routes := []UpscaleRoute{
		{Match: RouteScale, Scales: []int{4}},
		{Match: RouteWidth, BelowWidth: 600},
		{Match: RouteScale, Scales: []int{2, 3}},
	}
	for _, c := range []struct{ scale, width, want int }{
		{4, 300, 0}, // the first match wins
		{2, 500, 1},
		{2, 700, 2},
		{3, 600, 2}, // below, not at
		{5, 900, -1},
	} {
		if got := RouteFor(routes, c.scale, c.width); got != c.want {
			t.Errorf("×%d at %dpx: route %d, want %d", c.scale, c.width, got, c.want)
		}
	}
	if (UpscaleRoute{Match: RouteWidth}).Matches(2, 100) {
		t.Error("a width route without a width matched")
	}
}
