// Package upscaler is the mangarr-upscaler worker: a small HTTP service that
// upscales batches of pages with the ncnn/Vulkan CLIs (waifu2x, Real-CUGAN,
// Real-ESRGAN). It runs next to mangarr (Intel/AMD iGPU through /dev/dri) or
// on any machine with a GPU.
package upscaler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Engine describes one model of one ncnn tool.
type Engine struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Tool        string `json:"-"` // folder under ToolsDir
	Binary      string `json:"-"`
	ModelDir    string `json:"-"`
	ModelName   string `json:"-"` // realesrgan -n
	Scales      []int  `json:"scales"`
	NoiseLevels []int  `json:"noiseLevels,omitempty"`
}

// Catalog lists every model the worker knows. Availability depends on which
// tools are installed.
var Catalog = []Engine{
	{Name: "waifu2x-cunet", Description: "waifu2x CUnet – clean line art, good for black & white manga; removes JPEG noise",
		Tool: "waifu2x", Binary: "waifu2x-ncnn-vulkan", ModelDir: "models-cunet", Scales: []int{2, 4, 8}, NoiseLevels: []int{-1, 0, 1, 2, 3}},
	{Name: "waifu2x-anime", Description: "waifu2x UpConv7 anime style – fast, color pages",
		Tool: "waifu2x", Binary: "waifu2x-ncnn-vulkan", ModelDir: "models-upconv_7_anime_style_art_rgb", Scales: []int{2, 4, 8}, NoiseLevels: []int{-1, 0, 1, 2, 3}},
	{Name: "realcugan", Description: "Real-CUGAN SE – sharp anime/manga art",
		Tool: "realcugan", Binary: "realcugan-ncnn-vulkan", ModelDir: "models-se", Scales: []int{2, 3, 4}, NoiseLevels: []int{-1, 0, 1, 2, 3}},
	{Name: "realesr-animevideov3", Description: "Real-ESRGAN AnimeVideo v3 – fast, good for color webtoons",
		Tool: "realesrgan", Binary: "realesrgan-ncnn-vulkan", ModelDir: "models", ModelName: "realesr-animevideov3", Scales: []int{2, 3, 4}},
	{Name: "realesrgan-x4plus-anime", Description: "Real-ESRGAN x4plus anime – slow, strongest restoration",
		Tool: "realesrgan", Binary: "realesrgan-ncnn-vulkan", ModelDir: "models", ModelName: "realesrgan-x4plus-anime", Scales: []int{4}},
}

func findEngine(name string) (Engine, bool) {
	for _, e := range Catalog {
		if e.Name == name {
			return e, true
		}
	}
	return Engine{}, false
}

// Runner executes an engine on a directory of images (outputs PNG files
// named after the inputs).
type Runner interface {
	Available(e Engine) bool
	Run(ctx context.Context, e Engine, inDir, outDir string, scale, noise int) error
}

// DeviceRunner runs one batch pinned to the selected Vulkan device.
type DeviceRunner interface {
	RunDevice(ctx context.Context, e Engine, inDir, outDir string, scale, noise int, device string) error
}

// CLIRunner runs the real ncnn binaries from ToolsDir/<tool>/.
type CLIRunner struct {
	ToolsDir string
	GPU      string // "" = auto
	Threads  string // e.g. "1:2:2"
	Tile     int
	// Log (optional) reports retries with smaller tiles.
	Log *slog.Logger
}

func (r CLIRunner) bin(e Engine) string { return filepath.Join(r.ToolsDir, e.Tool, e.Binary+exeSuffix) }

// exeSuffix is what the ncnn release zips name their binaries with here:
// waifu2x-ncnn-vulkan.exe on Windows, no suffix elsewhere.
var exeSuffix = func() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}()

func (r CLIRunner) Available(e Engine) bool {
	st, err := os.Stat(r.bin(e))
	if err != nil || st.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(r.ToolsDir, e.Tool, e.ModelDir))
	return err == nil
}

// ErrOutOfMemory is returned when the tool ran out of (GPU or system)
// memory, even with the smallest tile size.
var ErrOutOfMemory = errors.New("out of memory")

// RetryTiles are the tile sizes tried, in order, after the tool runs out of
// memory (larger configured tiles first fall back to these).
var RetryTiles = []int{256, 128}

// Run upscales every image in inDir into outDir. When the tool runs out of
// memory (killed by the kernel, or a Vulkan allocation failure) it is run
// again with smaller tiles.
func (r CLIRunner) Run(ctx context.Context, e Engine, inDir, outDir string, scale, noise int) error {
	return r.RunDevice(ctx, e, inDir, outDir, scale, noise, r.GPU)
}

func (r CLIRunner) RunDevice(ctx context.Context, e Engine, inDir, outDir string, scale, noise int, device string) error {
	tiles := []int{r.Tile}
	for _, t := range RetryTiles {
		if r.Tile <= 0 || t < r.Tile {
			tiles = append(tiles, t)
		}
	}
	var err error
	for i, tile := range tiles {
		err = r.run(ctx, e, inDir, outDir, scale, noise, tile, device)
		if err == nil || !errors.Is(err, ErrOutOfMemory) || i == len(tiles)-1 {
			if err == nil && i > 0 && r.Log != nil {
				r.Log.Warn("upscaler ran out of memory with larger tiles; set this tile size to avoid retries", "tile", tile, "tool", e.Binary)
			}
			return err
		}
		if r.Log != nil {
			r.Log.Warn("upscaler ran out of memory, retrying with smaller tiles", "tool", e.Binary, "tile", tile, "next", tiles[i+1])
		}
	}
	return err
}

func (r CLIRunner) run(ctx context.Context, e Engine, inDir, outDir string, scale, noise, tile int, device string) error {
	args := []string{"-i", inDir, "-o", outDir, "-s", strconv.Itoa(scale), "-f", "png",
		"-m", filepath.Join(r.ToolsDir, e.Tool, e.ModelDir)}
	if e.ModelName != "" {
		args = append(args, "-n", e.ModelName)
	} else if len(e.NoiseLevels) > 0 {
		args = append(args, "-n", strconv.Itoa(noise))
	}
	if device != "" && device != "auto" {
		args = append(args, "-g", device)
	}
	if r.Threads != "" {
		args = append(args, "-j", r.Threads)
	}
	if tile > 0 {
		args = append(args, "-t", strconv.Itoa(tile))
	}
	// the tool's memory and ours share one limit in a container: hand back
	// what the last batch left behind before the tool starts taking its own
	debug.FreeOSMemory()
	cmd := exec.CommandContext(ctx, r.bin(e), args...)
	cmd.Dir = filepath.Join(r.ToolsDir, e.Tool)
	cmd.WaitDelay = 2 * time.Second // don't hang on pipes held open after a kill
	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	tileNote := "automatic tiles"
	if tile > 0 {
		tileNote = fmt.Sprintf("tile %d", tile)
	}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("%s timed out after %s: %w", e.Binary, time.Since(start).Round(time.Second), ctx.Err())
	case ctx.Err() != nil:
		return ctx.Err()
	case killed(err):
		// SIGKILL without a timeout or cancel: the kernel's out-of-memory killer
		return fmt.Errorf("%s was killed by the system after %s with %s, most likely out of memory (lower the tile size or use a lighter model): %w",
			e.Binary, time.Since(start).Round(time.Second), tileNote, ErrOutOfMemory)
	case outOfMemory(out):
		return fmt.Errorf("%s ran out of GPU memory with %s: %s: %w", e.Binary, tileNote, errorTail(out), ErrOutOfMemory)
	}
	return fmt.Errorf("%s failed: %w: %s", e.Binary, err, errorTail(out))
}

// killed reports whether the process died from SIGKILL.
func killed(err error) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	return ok && ws.Signaled() && ws.Signal() == syscall.SIGKILL
}

func outOfMemory(out []byte) bool {
	s := strings.ToLower(string(out))
	return strings.Contains(s, "vkallocatememory failed") || strings.Contains(s, "out_of_device_memory") ||
		strings.Contains(s, "out_of_host_memory") || strings.Contains(s, "out of memory")
}

// deviceLine matches the device list ncnn prints on every run
// ("[0 Intel(R) Graphics (ADL-N)]  queueC=0[1] ...").
var deviceLine = regexp.MustCompile(`^\[\d+ [^\]]*\]`)

// errorTail keeps the tool's output that isn't the device list.
func errorTail(out []byte) string {
	var keep []string
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !deviceLine.MatchString(l) {
			keep = append(keep, l)
		}
	}
	tail := strings.Join(keep, "; ")
	if tail == "" {
		return "no error output"
	}
	if len(tail) > 1500 {
		tail = "…" + tail[len(tail)-1500:]
	}
	return tail
}

// deviceIndex reads a device line's number and name.
var deviceIndex = regexp.MustCompile(`^\[(\d+) ([^\]]*)\]`)

// Devices lists the GPUs the ncnn tools see, in the order -g numbers them.
// It runs the first installed tool on an empty folder, which prints the
// devices and stops; this works where vulkaninfo isn't installed (Windows,
// macOS).
func (r CLIRunner) Devices(ctx context.Context) []string {
	for _, e := range Catalog {
		if !r.Available(e) {
			continue
		}
		dir, err := os.MkdirTemp("", "mangarr-devices-*")
		if err != nil {
			return nil
		}
		defer os.RemoveAll(dir)
		in, out := filepath.Join(dir, "in"), filepath.Join(dir, "out")
		_ = os.Mkdir(in, 0o755)
		_ = os.Mkdir(out, 0o755)
		args := []string{"-i", in, "-o", out, "-m", filepath.Join(r.ToolsDir, e.Tool, e.ModelDir), "-s", strconv.Itoa(e.Scales[0])}
		if e.ModelName != "" {
			args = append(args, "-n", e.ModelName)
		}
		cmd := exec.CommandContext(ctx, r.bin(e), args...)
		cmd.Dir = filepath.Join(r.ToolsDir, e.Tool)
		cmd.WaitDelay = 2 * time.Second
		b, _ := cmd.CombinedOutput()
		var names []string
		for _, l := range strings.Split(string(b), "\n") {
			m := deviceIndex.FindStringSubmatch(strings.TrimSpace(l))
			if m == nil {
				continue
			}
			i, _ := strconv.Atoi(m[1])
			for len(names) <= i {
				names = append(names, "")
			}
			names[i] = m[2]
		}
		return names
	}
	return nil
}

// ToolsAvailable reports whether any upscaler tool is installed in dir.
func ToolsAvailable(dir string) bool {
	r := CLIRunner{ToolsDir: dir}
	for _, e := range Catalog {
		if r.Available(e) {
			return true
		}
	}
	return false
}
