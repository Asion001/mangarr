package model

// What an upscale route looks at.
const (
	// RouteScale matches pages by the scale they need to reach the
	// profile's minimum width.
	RouteScale = "scale"
	// RouteWidth matches pages narrower than a width, before upscaling.
	RouteWidth = "width"
)

// Route targets besides a worker's id.
const (
	// RouteAnyUpscaler leaves the machine to the priority order.
	RouteAnyUpscaler int64 = 0
	// RouteThisServer is the built-in upscaler of this server.
	RouteThisServer int64 = -1
)

// UpscaleRoute sends the pages it matches to a chosen upscaler and model,
// ahead of the priority order in System → Workers. The routes a page
// matches list its upscalers in order: it goes to the first of them that
// is online and has room, and to the others only when none of them is
// online. A page no route matches goes by priority as before.
type UpscaleRoute struct {
	// Match is RouteScale or RouteWidth.
	Match string `json:"match" enum:"scale,width"`
	// Scales the route takes (RouteScale).
	Scales []int `json:"scales,omitempty"`
	// BelowWidth takes pages narrower than this many pixels (RouteWidth).
	BelowWidth int `json:"belowWidth,omitempty"`
	// Target is a worker's id, RouteThisServer or RouteAnyUpscaler.
	Target int64 `json:"target"`
	// Model replaces the profile's model and the machine's own; empty keeps
	// them.
	Model string `json:"model,omitempty"`
	// Wait keeps the pages for the target while it is offline; without it
	// they go by priority when it is.
	Wait bool `json:"wait"`
}

// Matches reports whether a page of this width (before upscaling) that
// needs this scale falls under the route.
func (r UpscaleRoute) Matches(scale, width int) bool {
	switch r.Match {
	case RouteScale:
		for _, s := range r.Scales {
			if s == scale {
				return true
			}
		}
	case RouteWidth:
		return r.BelowWidth > 0 && width > 0 && width < r.BelowWidth
	}
	return false
}

// RoutesFor is the indexes of every route a page falls under, in order.
func RoutesFor(routes []UpscaleRoute, scale, width int) []int {
	var out []int
	for i, r := range routes {
		if r.Matches(scale, width) {
			out = append(out, i)
		}
	}
	return out
}
