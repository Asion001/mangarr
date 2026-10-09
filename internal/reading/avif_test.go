package reading

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestReadY4M(t *testing.T) {
	// 4x2, 4:2:0, 8 bits: luma 10..80, chroma 2x1
	var b bytes.Buffer
	b.WriteString("YUV4MPEG2 W4 H2 F25:1 Ip A1:1 C420jpeg\nFRAME\n")
	b.Write([]byte{10, 20, 30, 40, 50, 60, 70, 80, 128, 128, 100, 160})
	img, err := readY4M(bufio.NewReader(&b))
	if err != nil {
		t.Fatal(err)
	}
	ycc, ok := img.(*image.YCbCr)
	if !ok || ycc.Bounds().Dx() != 4 || ycc.Bounds().Dy() != 2 || ycc.Y[5] != 60 || ycc.Cr[1] != 160 || ycc.SubsampleRatio != image.YCbCrSubsampleRatio420 {
		t.Fatalf("decoded %#v", img)
	}

	// 10 bits, monochrome: samples are scaled to 8
	b.Reset()
	b.WriteString("YUV4MPEG2 W2 H1 Cmonop10\nFRAME\n")
	b.Write([]byte{0xff, 0x03, 0x00, 0x02}) // 1023, 512
	img, err = readY4M(bufio.NewReader(&b))
	if err != nil {
		t.Fatal(err)
	}
	if g := img.(*image.Gray); g.Pix[0] != 255 || g.Pix[1] != 128 {
		t.Fatalf("10-bit samples: %v", g.Pix)
	}

	for _, bad := range []string{"", "P5\n", "YUV4MPEG2 W0 H2\nFRAME\n", "YUV4MPEG2 W2 H2 C420jpeg\nFRAME\n\x01"} {
		if _, err := readY4M(bufio.NewReader(bytes.NewBufferString(bad))); err == nil {
			t.Errorf("%q should not decode", bad)
		}
	}
}

// With libavif's tools installed, an AVIF page decodes through avifdec and
// keeps its picture: the dark box on a white page is where it was.
func TestDecodeAVIF(t *testing.T) {
	enc, err := exec.LookPath("avifenc")
	if err != nil || avifdec() == "" {
		t.Skip("avifenc and avifdec are not installed")
	}
	src := image.NewGray(image.Rect(0, 0, 200, 300))
	for i := range src.Pix {
		src.Pix[i] = 255
	}
	for y := 60; y < 240; y++ {
		for x := 40; x < 160; x++ {
			src.SetGray(x, y, color.Gray{Y: 20})
		}
	}
	dir := t.TempDir()
	var pngData bytes.Buffer
	_ = png.Encode(&pngData, src)
	in, out := filepath.Join(dir, "p.png"), filepath.Join(dir, "p.avif")
	if err := os.WriteFile(in, pngData.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg, err := exec.Command(enc, "-q", "90", in, out).CombinedOutput(); err != nil {
		t.Fatalf("avifenc: %v %s", err, msg)
	}
	data, _ := os.ReadFile(out)
	img, err := decodeAVIF(data)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 200 || img.Bounds().Dy() != 300 {
		t.Fatalf("size %v", img.Bounds())
	}
	bd := ContentBox(img)
	if bd.X < 30 || bd.X > 40 || bd.Y < 50 || bd.Y > 60 || bd.W < 120 || bd.H < 180 {
		t.Fatalf("content box %s", fmt.Sprint(bd))
	}
}
