package model

import (
	"fmt"
	"testing"
)

func TestRoutesFor(t *testing.T) {
	routes := []UpscaleRoute{
		{Match: RouteScale, Scales: []int{4}},
		{Match: RouteWidth, BelowWidth: 600},
		{Match: RouteScale, Scales: []int{2, 3}},
	}
	for _, c := range []struct {
		scale, width int
		want         string
	}{
		{4, 300, "[0 1]"}, // every match, in order
		{2, 500, "[1 2]"},
		{2, 700, "[2]"},
		{3, 600, "[2]"}, // below, not at
		{5, 900, "[]"},
	} {
		if got := fmt.Sprint(RoutesFor(routes, c.scale, c.width)); got != c.want {
			t.Errorf("×%d at %dpx: routes %s, want %s", c.scale, c.width, got, c.want)
		}
	}
	if (UpscaleRoute{Match: RouteWidth}).Matches(2, 100) {
		t.Error("a width route without a width matched")
	}
}
