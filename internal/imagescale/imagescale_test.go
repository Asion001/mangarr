package imagescale

import (
	"image"
	"image/color"
	"math/rand"
	"runtime"
	"runtime/debug"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/image/draw"
)

func noise(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(1))
	// smooth shapes plus noise, so both kernels' ringing and flat areas show
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8((x*7+y*3)%256/2 + r.Intn(128))
			img.SetRGBA(x, y, color.RGBA{v, uint8(y % 256), uint8(x % 256), 255})
		}
	}
	return img
}

// TestMatchesOneCall: scaling in bands gives what one Scale call gives, to
// within the rounding of the 8-bit step between the two passes — and no
// seams where the bands meet.
func TestMatchesOneCall(t *testing.T) {
	for _, tc := range []struct{ sw, sh, w, h int }{
		{400, 1000, 150, 375}, // down, several bands both ways
		{300, 200, 100, 67},
		{200, 300, 100, 300}, // width only
	} {
		src := noise(tc.sw, tc.sh)
		want := image.NewRGBA(image.Rect(0, 0, tc.w, tc.h))
		draw.CatmullRom.Scale(want, want.Bounds(), src, src.Bounds(), draw.Src, nil)
		got := CatmullRom(src, tc.w, tc.h)
		if got.Bounds() != want.Bounds() {
			t.Fatalf("%v: bounds %v", tc, got.Bounds())
		}
		worst := 0
		for i := range want.Pix {
			d := int(want.Pix[i]) - int(got.Pix[i])
			if d < 0 {
				d = -d
			}
			worst = max(worst, d)
		}
		if worst > 2 {
			t.Fatalf("%v: a channel is off by %d", tc, worst)
		}
	}
}

// TestSubImage: a source that doesn't start at the origin scales the same.
func TestSubImage(t *testing.T) {
	full := noise(300, 300)
	sub := full.SubImage(image.Rect(50, 40, 250, 240)).(*image.RGBA)
	flat := image.NewRGBA(image.Rect(0, 0, 200, 200))
	draw.Draw(flat, flat.Bounds(), sub, sub.Bounds().Min, draw.Src)
	a, b := CatmullRom(sub, 80, 80), CatmullRom(flat, 80, 80)
	for i := range a.Pix {
		if a.Pix[i] != b.Pix[i] {
			t.Fatal("a sub-image scaled differently")
		}
	}
}

// TestMemoryStaysSmall: a tall strip takes little beyond the images
// themselves (58 MB in, 29 MB between the passes, 14 MB out). One Scale call
// here holds 600×12000 float64 quads on top of that, 230 MB.
func TestMemoryStaysSmall(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	src := image.NewRGBA(image.Rect(0, 0, 1200, 12000))
	runtime.GC()
	var peak atomic.Uint64
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		var m runtime.MemStats
		for {
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > peak.Load() {
				peak.Store(m.HeapAlloc)
			}
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	dst := CatmullRom(src, 600, 6000)
	close(stop)
	<-done
	if dst.Bounds().Dx() != 600 || dst.Bounds().Dy() != 6000 {
		t.Fatal(dst.Bounds())
	}
	if got := peak.Load() >> 20; got > 180 {
		t.Fatalf("the heap reached %d MB", got)
	}
}
