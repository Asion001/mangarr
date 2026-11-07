package thumbs

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"os"
	"testing"
)

func noisy(w, h int, alpha bool) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(7))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := uint8(255)
			if alpha && x < w/4 {
				a = 0
			}
			img.SetNRGBA(x, y, color.NRGBA{uint8(r.Intn(256)), uint8(y), uint8(x), a})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func dims(t *testing.T, data []byte) (int, int, string) {
	t.Helper()
	cfg, f, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Width, cfg.Height, f
}

func TestNormalizeLargePNG(t *testing.T) {
	in := noisy(1600, 2400, false)
	out, ct, changed := Normalize(in, MaxWidth, Quality)
	w, h, f := dims(t, out)
	if !changed || ct != "image/jpeg" || f != "jpeg" || w != 768 || h != 1152 || len(out) >= len(in) {
		t.Fatalf("got %s %dx%d %d bytes (from %d)", ct, w, h, len(out), len(in))
	}
}

func TestNormalizeFlattensTransparency(t *testing.T) {
	out, _, changed := Normalize(noisy(1000, 1500, true), MaxWidth, Quality)
	if !changed {
		t.Fatal("not converted")
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(5, 5).RGBA()
	if r>>8 < 240 || g>>8 < 240 || b>>8 < 240 {
		t.Fatalf("transparent corner should be white, got %d %d %d", r>>8, g>>8, b>>8)
	}
}

func TestNormalizeKeepsSmallJPEG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 300, 450))
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, nil)
	out, ct, changed := Normalize(buf.Bytes(), MaxWidth, Quality)
	if changed || ct != "image/jpeg" || !bytes.Equal(out, buf.Bytes()) {
		t.Fatal("small JPEG should be kept")
	}
}

func TestNormalizeTallImage(t *testing.T) {
	out, _, _ := Normalize(noisy(800, 4000, false), MaxWidth, Quality)
	w, h, _ := dims(t, out)
	if h > 2*MaxWidth || w > MaxWidth {
		t.Fatalf("got %dx%d", w, h)
	}
}

func TestNormalizeWebPAndAVIF(t *testing.T) {
	for _, p := range []string{"testdata/cover.webp", "../imagecheck/testdata/color.avif"} {
		in, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out, ct, _ := Normalize(in, MaxWidth, Quality)
		w, _, f := dims(t, out)
		if ct != "image/jpeg" || f != "jpeg" || w > MaxWidth {
			t.Fatalf("%s: got %s %s %d wide", p, ct, f, w)
		}
	}
}

func TestNormalizeCorrupt(t *testing.T) {
	in := []byte("definitely not an image")
	out, ct, changed := Normalize(in, MaxWidth, Quality)
	if changed || ct != "" || !bytes.Equal(out, in) {
		t.Fatal("corrupt input must pass through")
	}
}

func TestSafeType(t *testing.T) {
	for _, c := range []struct {
		data []byte
		want string
	}{
		{[]byte("\xFF\xD8\xFF\xE0\x00\x10JFIF"), "image/jpeg"},
		{[]byte("\x89PNG\r\n\x1a\n\x00\x00"), "image/png"},
		{[]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), "image/webp"},
		{[]byte("\x00\x00\x00\x1cftypavif\x00\x00"), "image/avif"},
		{[]byte("<script>alert(1)</script>"), ""},
		{[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), ""},
		{[]byte("%PDF-1.4"), ""},
	} {
		if got := SafeType(c.data); got != c.want {
			t.Errorf("SafeType(%q) = %q, want %q", c.data, got, c.want)
		}
	}
}
