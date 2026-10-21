package upscaler

import "testing"

// Real-CUGAN ships the light denoise models only at 2x; asking for one at
// 3x or 4x crashes the tool, so those scales fall back to a model it has.
func TestRealCUGANNoiseAtScale(t *testing.T) {
	e, ok := findEngine("realcugan")
	if !ok {
		t.Fatal("no realcugan engine")
	}
	for _, c := range []struct{ scale, noise, want int }{
		{2, 1, 1}, {2, 2, 2}, {4, 1, 0}, {4, 2, 0}, {3, 1, 0},
		{4, 3, 3}, {4, 0, 0}, {4, -1, -1}, {3, 3, 3},
	} {
		if got := e.noiseAt(c.scale, c.noise); got != c.want {
			t.Errorf("%dx noise %d: got %d, want %d", c.scale, c.noise, got, c.want)
		}
	}
	w, _ := findEngine("waifu2x-cunet")
	if got := w.noiseAt(4, 1); got != 1 {
		t.Errorf("waifu2x 4x noise 1: got %d", got)
	}
}
