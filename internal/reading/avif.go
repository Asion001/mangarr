package reading

import (
	"bufio"

	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var avifdec = sync.OnceValue(func() string {
	bin, _ := exec.LookPath("avifdec")
	return bin
})

// decodeAVIF decodes an AVIF page through libavif's avifdec, when it is
// installed, by way of a raw YUV (y4m) file: no PNG to write and read back.
// Alpha is dropped; pages don't have any worth keeping.
func decodeAVIF(data []byte) (image.Image, error) {
	bin := avifdec()
	if bin == "" {
		return nil, errors.New("avifdec is not installed")
	}
	dir, err := os.MkdirTemp("", "mangarr-avif-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, out := filepath.Join(dir, "in.avif"), filepath.Join(dir, "out.y4m")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return nil, err
	}
	if msg, err := exec.Command(bin, "--ignore-icc", in, out).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("avifdec: %v: %s", err, strings.TrimSpace(string(msg)))
	}
	f, err := os.Open(out)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readY4M(bufio.NewReaderSize(f, 1<<20))
}

// readY4M reads the first frame of a YUV4MPEG2 stream: 4:2:0, 4:2:2, 4:4:4
// or monochrome, 8 bits or more (deeper samples are scaled to 8 bits).
func readY4M(r *bufio.Reader) (image.Image, error) {
	header, err := r.ReadString('\n')
	if err != nil || !strings.HasPrefix(header, "YUV4MPEG2 ") {
		return nil, errors.New("not a y4m stream")
	}
	w, h, colour := 0, 0, "420jpeg"
	for _, f := range strings.Fields(header)[1:] {
		switch f[0] {
		case 'W':
			w, _ = strconv.Atoi(f[1:])
		case 'H':
			h, _ = strconv.Atoi(f[1:])
		case 'C':
			colour = f[1:]
		}
	}
	if w <= 0 || h <= 0 || w > 1<<15 || h > 1<<16 {
		return nil, fmt.Errorf("y4m: bad size %dx%d", w, h)
	}
	if frame, err := r.ReadString('\n'); err != nil || !strings.HasPrefix(frame, "FRAME") {
		return nil, errors.New("y4m: no frame")
	}
	depth := 8
	if i := strings.Index(colour, "p"); i > 0 && i+1 < len(colour) {
		if d, err := strconv.Atoi(colour[i+1:]); err == nil {
			depth = d
		}
		colour = colour[:i]
	}
	plane := func(n int) ([]byte, error) {
		if depth == 8 {
			b := make([]byte, n)
			_, err := io.ReadFull(r, b)
			return b, err
		}
		raw := make([]byte, 2*n)
		if _, err := io.ReadFull(r, raw); err != nil {
			return nil, err
		}
		b := make([]byte, n)
		for i := range b {
			b[i] = byte((int(raw[2*i]) | int(raw[2*i+1])<<8) >> (depth - 8))
		}
		return b, nil
	}
	rect := image.Rect(0, 0, w, h)
	var ratio image.YCbCrSubsampleRatio
	cw, ch := w, h
	switch {
	case strings.HasPrefix(colour, "420"):
		ratio, cw, ch = image.YCbCrSubsampleRatio420, (w+1)/2, (h+1)/2
	case strings.HasPrefix(colour, "422"):
		ratio, cw = image.YCbCrSubsampleRatio422, (w+1)/2
	case strings.HasPrefix(colour, "444"):
		ratio = image.YCbCrSubsampleRatio444
	case strings.HasPrefix(colour, "mono"):
		y, err := plane(w * h)
		if err != nil {
			return nil, err
		}
		return &image.Gray{Pix: y, Stride: w, Rect: rect}, nil
	default:
		return nil, fmt.Errorf("y4m: unsupported colour space %q", colour)
	}
	y, err := plane(w * h)
	if err != nil {
		return nil, err
	}
	cb, err := plane(cw * ch)
	if err != nil {
		return nil, err
	}
	cr, err := plane(cw * ch)
	if err != nil {
		return nil, err
	}
	return &image.YCbCr{Y: y, Cb: cb, Cr: cr, YStride: w, CStride: cw, SubsampleRatio: ratio, Rect: rect}, nil
}
