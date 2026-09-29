package reading

import (
	"image"
	"image/color"
	"testing"
)

func TestContentBox(t *testing.T) {
	// a 400x600 page: white margins of 40 (sides) and 60 (top/bottom) around dark content
	img := image.NewRGBA(image.Rect(0, 0, 400, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 400; x++ {
			c := color.RGBA{255, 255, 255, 255}
			if x >= 40 && x < 360 && y >= 60 && y < 540 {
				c = color.RGBA{uint8(x % 200), uint8(y % 200), 90, 255}
			}
			img.Set(x, y, c)
		}
	}
	b := ContentBox(img)
	if b.Width != 400 || b.Height != 600 {
		t.Fatalf("size %+v", b)
	}
	// content 40..360 x 60..540, plus a 1% margin
	if b.X != 36 || b.Y != 54 || b.W != 328 || b.H != 492 {
		t.Fatalf("box %+v", b)
	}
	// a blank page isn't cropped
	blank := image.NewRGBA(image.Rect(0, 0, 400, 600))
	for i := range blank.Pix {
		blank.Pix[i] = 255
	}
	if b := ContentBox(blank); b.X != 0 || b.W != 400 || b.H != 600 {
		t.Fatalf("blank %+v", b)
	}
	// black borders too
	for y := 0; y < 600; y++ {
		for x := 0; x < 400; x++ {
			if x < 40 || x >= 360 {
				img.Set(x, y, color.Black)
			}
		}
	}
	if b := ContentBox(img); b.X < 30 || b.W > 340 {
		t.Fatalf("black sides %+v", b)
	}
	// a white margin around a black frame: only the margin goes
	framed := image.NewRGBA(image.Rect(0, 0, 400, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 400; x++ {
			c := color.RGBA{255, 255, 255, 255}
			if x >= 40 && x < 360 && y >= 60 && y < 540 {
				c = color.RGBA{20, 20, 20, 255}
				if x >= 60 && x < 340 && y >= 80 && y < 520 {
					c = color.RGBA{120, 160, 200, 255}
				}
			}
			framed.Set(x, y, c)
		}
	}
	if b := ContentBox(framed); b.X != 36 || b.Y != 54 || b.W != 328 || b.H != 492 {
		t.Fatalf("framed %+v", b)
	}
}

func TestContentBoxNoisyGreyMarginsAndPageNumber(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 400, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 400; x++ {
			inside := x >= 45 && x < 355 && y >= 70 && y < 530
			if inside {
				img.Set(x, y, color.RGBA{R: uint8(30 + x%170), G: uint8(40 + y%150), B: 110, A: 255})
				continue
			}
			// Off-white scan paper with enough variation to resemble JPEG noise.
			v := uint8(198 + (x*7+y*11)%22)
			img.Set(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	// A small printed page number must not pin the entire top margin in place.
	for y := 12; y < 28; y++ {
		for x := 184; x < 212; x++ {
			img.Set(x, y, color.RGBA{R: 55, G: 55, B: 55, A: 255})
		}
	}
	b := ContentBox(img)
	if b.X > 45 || b.Y > 70 || b.X < 35 || b.Y < 58 || b.W >= 380 || b.H >= 570 {
		t.Fatalf("noisy off-white margins were not cropped: %+v", b)
	}
}

func TestContentBoxDarkGreyMarginsWithSpecks(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 360, 540))
	for y := 0; y < 540; y++ {
		for x := 0; x < 360; x++ {
			inside := x >= 36 && x < 324 && y >= 54 && y < 486
			if inside {
				img.Set(x, y, color.RGBA{R: uint8(95 + x%120), G: uint8(80 + y%130), B: 160, A: 255})
				continue
			}
			v := uint8(45 + (x*5+y*3)%25)
			if (x*13+y*17)%41 == 0 { // sparse scanner dust
				v = 190
			}
			img.Set(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	b := ContentBox(img)
	if b.X > 36 || b.Y > 54 || b.X < 28 || b.Y < 45 || b.W >= 340 || b.H >= 515 {
		t.Fatalf("dark grey margins were not cropped: %+v", b)
	}
}

func TestContentBoxKeepsLetteringNearTheEdge(t *testing.T) {
	// Page 24 of a real chapter: art ends at x=300, but a caption's thin
	// letter strokes run on to x=373. Every column through them is still
	// over 92% paper, which used to count as margin and cut the caption off.
	img := image.NewRGBA(image.Rect(0, 0, 400, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 400; x++ {
			c := color.RGBA{255, 255, 255, 255}
			if x >= 40 && x < 300 && y >= 60 && y < 540 {
				c = color.RGBA{uint8(x % 200), uint8(y % 200), 90, 255}
			}
			img.Set(x, y, c)
		}
	}
	for x := 310; x < 373; x += 12 {
		for y := 200; y < 240; y++ {
			for d := 0; d < 3; d++ {
				img.Set(x+d, y, color.Black)
			}
		}
	}
	b := ContentBox(img)
	if b.X+b.W < 373 {
		t.Fatalf("lettering cut off at x=%d: %+v", b.X+b.W, b)
	}
	if b.X < 30 || b.Y < 50 || b.Y+b.H > 550 {
		t.Fatalf("margins not cropped: %+v", b)
	}
}
