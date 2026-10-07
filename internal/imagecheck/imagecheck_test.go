package imagecheck

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func encode(t *testing.T, format string, w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(1, 1, color.White)
	var buf bytes.Buffer
	var err error
	if format == "png" {
		err = png.Encode(&buf, img)
	} else {
		err = jpeg.Encode(&buf, img, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDetect(t *testing.T) {
	info, err := Detect(encode(t, "png", 40, 60))
	if err != nil || info.Format != "png" || info.Width != 40 || info.Height != 60 {
		t.Fatalf("png: %+v %v", info, err)
	}
	info, err = Detect(encode(t, "jpeg", 30, 20))
	if err != nil || info.Format != "jpeg" || info.Width != 30 {
		t.Fatalf("jpeg: %+v %v", info, err)
	}
	if _, err := Detect([]byte("  <!DOCTYPE html><html><body>Cloudflare</body></html>")); err != ErrHTML {
		t.Fatalf("html: %v", err)
	}
	if _, err := Detect(nil); err != ErrEmpty {
		t.Fatalf("empty: %v", err)
	}
	truncated := encode(t, "png", 40, 60)[:20]
	if _, err := Detect(truncated); err == nil {
		t.Fatal("expected error for truncated png")
	}
}

func TestModernFormats(t *testing.T) {
	for file, want := range map[string]Info{
		"gray.avif":          {Format: "avif", Width: 300, Height: 450},
		"color.avif":         {Format: "avif", Width: 300, Height: 450},
		"page.jxl":           {Format: "jxl", Width: 300, Height: 450},
		"page-from-jpeg.jxl": {Format: "jxl", Width: 300, Height: 450}, // container with jbrd + jxlc
	} {
		data, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Fatal(err)
		}
		got, err := Detect(data)
		if err != nil || got != want {
			t.Errorf("%s: got %+v, %v; want %+v", file, got, err, want)
		}
	}
}

func TestTruncated(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 64, 64))
	for i := range img.Pix {
		img.Pix[i] = uint8(i * 7)
	}
	var j, p bytes.Buffer
	if err := jpeg.Encode(&j, img, nil); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&p, img); err != nil {
		t.Fatal(err)
	}
	webp := append([]byte("RIFF\x20\x00\x00\x00WEBPVP8 "), make([]byte, 24)...) // RIFF size 32: 40 bytes in all
	for _, c := range []struct {
		format string
		data   []byte
	}{{"jpeg", j.Bytes()}, {"png", p.Bytes()}, {"webp", webp}} {
		if Truncated(c.format, c.data) {
			t.Errorf("a whole %s is cut off", c.format)
		}
		if cut := c.data[:len(c.data)*3/4]; !Truncated(c.format, cut) {
			t.Errorf("a %s cut at 3/4 passes", c.format)
		}
		if _, err := Detect(c.data[:len(c.data)*3/4]); err != nil && c.format == "jpeg" {
			t.Errorf("a cut jpeg's header still reads: %v", err)
		}
	}
}
