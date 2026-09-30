package upscaler

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Asion001/mangarr/internal/imagescale"
	_ "golang.org/x/image/webp"
)

type Config struct {
	TmpDir  string
	GPU     string // auto or comma-separated Vulkan device indices
	CWebP   string // path to cwebp; empty = look up in PATH
	Timeout time.Duration
	MaxBody int64
	Version string
}

type Server struct {
	cfg       Config
	runner    Runner
	log       *slog.Logger
	slots     chan string
	configErr error
	queued    atomic.Int32
}

func NewServer(cfg Config, runner Runner, log *slog.Logger) *Server {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Minute
	}
	if cfg.MaxBody <= 0 {
		cfg.MaxBody = 1 << 30
	}
	if cfg.TmpDir == "" {
		cfg.TmpDir = os.TempDir()
	}
	if cfg.CWebP == "" {
		if p, err := exec.LookPath("cwebp"); err == nil {
			cfg.CWebP = p
		}
	}
	devices, err := parseGPUs(cfg.GPU)
	if err != nil {
		devices = []string{""}
	}
	s := &Server{cfg: cfg, runner: runner, log: log, slots: make(chan string, len(devices)), configErr: err}
	for _, device := range devices {
		s.slots <- device
	}
	return s
}

type Info struct {
	Version string   `json:"version"`
	Devices []string `json:"devices"`
	Models  []Engine `json:"models"`
	Formats []string `json:"formats"`
	Queued  int      `json:"queued"`
}

func (s *Server) Info() Info {
	info := Info{Version: s.cfg.Version, Devices: devices(), Models: []Engine{}, Formats: []string{"png", "jpeg"}, Queued: int(s.queued.Load())}
	if s.cfg.CWebP != "" {
		info.Formats = append(info.Formats, "webp")
	}
	for _, e := range Catalog {
		if s.runner.Available(e) {
			info.Models = append(info.Models, e)
		}
	}
	return info
}

// Devices lists the Vulkan devices (via vulkaninfo, when installed).
func Devices() []string { return devices() }

// devices lists Vulkan devices via vulkaninfo when available.
func devices() []string {
	out := []string{}
	p, err := exec.LookPath("vulkaninfo")
	if err != nil {
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, p, "--summary").Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "deviceName") {
			if _, v, ok := strings.Cut(line, "="); ok {
				out = append(out, strings.TrimSpace(v))
			}
		}
	}
	return out
}

type Params struct {
	Model    string
	Scale    int
	Noise    int
	Format   string
	Quality  int
	MaxWidth int
}

func parseParams(r *http.Request) (Params, error) {
	q := r.URL.Query()
	atoi := func(k string, def int) int {
		if v, err := strconv.Atoi(q.Get(k)); err == nil {
			return v
		}
		return def
	}
	p := Params{Model: q.Get("model"), Scale: atoi("scale", 2), Noise: atoi("noise", 1), Format: strings.ToLower(q.Get("format")),
		Quality: atoi("quality", 90), MaxWidth: atoi("maxWidth", 0)}
	if p.Format == "" {
		p.Format = "webp"
	}
	if p.Format == "jpg" {
		p.Format = "jpeg"
	}
	if p.Format != "webp" && p.Format != "jpeg" && p.Format != "png" {
		return p, fmt.Errorf("unsupported format %q", p.Format)
	}
	if p.Quality <= 0 || p.Quality > 100 {
		p.Quality = 90
	}
	return p, nil
}

// Image is a page in memory.
type Image struct {
	Name string
	Data []byte
}

type badRequest struct{ error }

// Process upscales images with one engine run.
func (s *Server) Process(ctx context.Context, p Params, images []Image) ([]Image, error) {
	out, _, err := s.ProcessDevice(ctx, p, images)
	return out, err
}

func (s *Server) ProcessDevice(ctx context.Context, p Params, images []Image) ([]Image, string, error) {
	if s.configErr != nil {
		return nil, "", fmt.Errorf("invalid upscaler GPU setting: %w", s.configErr)
	}
	eng, ok := findEngine(p.Model)
	if !ok || !s.runner.Available(eng) {
		return nil, "", badRequest{fmt.Errorf("model not available: %s", p.Model)}
	}
	if !contains(eng.Scales, p.Scale) {
		return nil, "", badRequest{fmt.Errorf("model %s supports scales %v", eng.Name, eng.Scales)}
	}
	s.queued.Add(1)
	var device string
	select {
	case device = <-s.slots:
		s.queued.Add(-1)
	case <-ctx.Done():
		s.queued.Add(-1)
		return nil, "", ctx.Err()
	}
	defer func() { s.slots <- device }()
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	start := time.Now()
	out, err := s.process(ctx, eng, p, images, device)
	if err == nil {
		s.log.Info("upscaled batch", "model", p.Model, "gpu", device, "scale", p.Scale, "pages", len(out), "duration", time.Since(start).Round(time.Millisecond))
	}
	return out, device, err
}

func parseGPUs(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return []string{""}, nil
	}
	if value == "auto" {
		// One slot, and no -g: the tool picks its default device.
		return []string{"auto"}, nil
	}
	parts := strings.Split(value, ",")
	seen := map[string]bool{}
	for i, part := range parts {
		part = strings.TrimSpace(part)
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || seen[part] {
			return nil, fmt.Errorf("invalid GPU list %q", value)
		}
		seen[part] = true
		parts[i] = strconv.Itoa(n)
	}
	return parts, nil
}

// ParseGPUs validates a GPU setting, for use by module settings validators.
func ParseGPUs(value string) ([]string, error) { return parseGPUs(value) }

func unzipImages(body []byte) ([]Image, error) {
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("invalid zip: %w", err)
	}
	var out []Image
	for _, f := range zr.File {
		name := filepath.Base(f.Name)
		if f.FileInfo().IsDir() || name == "." || strings.HasPrefix(name, ".") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, Image{Name: name, Data: data})
	}
	return out, nil
}

func zipImages(images []Image) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, img := range images {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: img.Name, Method: zip.Store})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(img.Data); err != nil {
			return nil, err
		}
	}
	err := zw.Close()
	return buf.Bytes(), err
}

// process writes the images to disk, runs the engine and reads the results.
func (s *Server) process(ctx context.Context, eng Engine, p Params, images []Image, device string) ([]Image, error) {
	work, err := os.MkdirTemp(s.cfg.TmpDir, "upscale-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	in, outDir := filepath.Join(work, "in"), filepath.Join(work, "out")
	if err := os.MkdirAll(in, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	var names []string
	for _, img := range images {
		name := filepath.Base(img.Name)
		ext := strings.ToLower(filepath.Ext(name))
		base := strings.TrimSuffix(name, filepath.Ext(name))
		switch ext {
		case ".png", ".jpg", ".jpeg", ".webp":
			if err := os.WriteFile(filepath.Join(in, name), img.Data, 0o644); err != nil {
				return nil, err
			}
		default:
			// normalize other formats (e.g. gif, avif) to png for the engines
			decoded, _, err := image.Decode(bytes.NewReader(img.Data))
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			f, err := os.Create(filepath.Join(in, base+".png"))
			if err != nil {
				return nil, err
			}
			err = png.Encode(f, decoded)
			f.Close()
			if err != nil {
				return nil, err
			}
		}
		names = append(names, base)
	}
	if len(names) == 0 {
		return nil, badRequest{errors.New("no images in request")}
	}
	var runErr error
	if runner, ok := s.runner.(DeviceRunner); ok {
		runErr = runner.RunDevice(ctx, eng, in, outDir, p.Scale, p.Noise, device)
	} else {
		runErr = s.runner.Run(ctx, eng, in, outDir, p.Scale, p.Noise)
	}
	if err := runErr; err != nil {
		return nil, err
	}
	out := make([]Image, 0, len(names))
	for _, base := range names {
		data, ext, err := s.finish(ctx, filepath.Join(outDir, base+".png"), p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", base, err)
		}
		out = append(out, Image{Name: base + ext, Data: data})
	}
	return out, nil
}

// finish downsizes to MaxWidth and encodes to the requested format.
func (s *Server) finish(ctx context.Context, src string, p Params) ([]byte, string, error) {
	f, err := os.Open(src)
	if err != nil {
		return nil, "", fmt.Errorf("engine produced no output: %w", err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, "", err
	}
	if p.MaxWidth <= 0 || cfg.Width <= p.MaxWidth {
		// the engine already wrote this PNG: decoding an upscaled webtoon
		// strip (100+ megapixels) only to encode it again costs more memory
		// than a small server has
		switch {
		case p.Format == "png":
			data, err := os.ReadFile(src)
			return data, ".png", err
		case p.Format == "webp" && s.cfg.CWebP != "":
			return s.cwebp(ctx, src, src+".webp", p.Quality)
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, "", err
	}
	if b := img.Bounds(); p.MaxWidth > 0 && b.Dx() > p.MaxWidth {
		h := b.Dy() * p.MaxWidth / b.Dx()
		img = imagescale.CatmullRom(img, p.MaxWidth, h)
	}
	format := p.Format
	if format == "webp" && s.cfg.CWebP == "" {
		format = "jpeg"
	}
	var buf bytes.Buffer
	switch format {
	case "png":
		err = png.Encode(&buf, img)
		return buf.Bytes(), ".png", err
	case "jpeg":
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: p.Quality})
		return buf.Bytes(), ".jpg", err
	}
	// webp through cwebp (lossy, quality q)
	tmpPNG := src + ".resized.png"
	out := src + ".webp"
	pf, err := os.Create(tmpPNG)
	if err != nil {
		return nil, "", err
	}
	if err := png.Encode(pf, img); err != nil {
		pf.Close()
		return nil, "", err
	}
	pf.Close()
	return s.cwebp(ctx, tmpPNG, out, p.Quality)
}

// cwebp encodes the PNG src to lossy WebP at quality q.
func (s *Server) cwebp(ctx context.Context, src, out string, q int) ([]byte, string, error) {
	cmd := exec.CommandContext(ctx, s.cfg.CWebP, "-quiet", "-mt", "-q", strconv.Itoa(q), src, "-o", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return nil, "", fmt.Errorf("cwebp: %w: %s", err, b)
	}
	data, err := os.ReadFile(out)
	return data, ".webp", err
}

func contains(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
