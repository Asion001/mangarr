package imagedeliver_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/Asion001/mangarr/internal/diskcache"
	"github.com/Asion001/mangarr/internal/imagecheck"
	"github.com/Asion001/mangarr/internal/imagedeliver"
)

// page is a JPEG of the given size.
func page(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 255), uint8(y % 255), 120, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestVariant: a wide page is copied down to the asked-for size, cached, and
// a page that is already small enough is left alone.
func TestVariant(t *testing.T) {
	d := imagedeliver.New(diskcache.NewStore(t.TempDir(), nil, nil), nil)
	ctx := context.Background()
	big := page(t, 2400, 1600)
	loads := 0
	load := func(context.Context) ([]byte, error) { loads++; return big, nil }

	data, ct, ok := d.Variant(ctx, `"page-1"`, 1080, load)
	if !ok || ct != "image/jpeg" {
		t.Fatalf("variant: ok=%v ct=%q", ok, ct)
	}
	info, err := imagecheck.Detect(data)
	if err != nil || info.Width != 1080 {
		t.Fatalf("copy is %+v (%v)", info, err)
	}
	if len(data) >= len(big) {
		t.Fatalf("the copy (%d) isn't smaller than the page (%d)", len(data), len(big))
	}
	if _, _, ok := d.Variant(ctx, `"page-1"`, 1080, load); !ok || loads != 1 {
		t.Fatalf("the copy wasn't cached: %d loads", loads)
	}

	// a page already narrower than the screen is sent as it is
	small := page(t, 800, 1200)
	if _, _, ok := d.Variant(ctx, `"page-2"`, 1080, func(context.Context) ([]byte, error) { return small, nil }); ok {
		t.Fatal("a small page was copied for no reason")
	}

	// nor is one only a little wider: shrinking it costs more time than the
	// bytes it would save
	near := page(t, 1300, 400)
	if _, _, ok := d.Variant(ctx, `"page-3"`, 1080, func(context.Context) ([]byte, error) { return near, nil }); ok {
		t.Fatal("a page only a little wider than the screen was copied")
	}
}

// TestWidth rounds up to a size we keep, and asks for nothing when the client
// wants the full page.
func TestWidth(t *testing.T) {
	for want, expect := range map[int]int{0: 0, 1: 320, 320: 320, 400: 720, 1080: 1080, 1500: 2160, 4000: 0} {
		if got := imagedeliver.Width(want); got != expect {
			t.Fatalf("Width(%d) = %d, want %d", want, got, expect)
		}
	}
}
