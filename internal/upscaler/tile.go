package upscaler

import (
	"context"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// With no tile size the ncnn tools pick one from the GPU's memory, but their
// table stops early: Real-ESRGAN never goes past 200 and the others past 400,
// so a GPU with 8 GB or more spends much of its time on tile overlap and on
// syncing between tiles. autoTile carries the tools' own table on past its
// last step, by the same memory per pixel, when it can learn how much memory
// the GPU has.

// maxAutoTile caps the automatic size: past it pages are only a tile or two
// wide anyway, and bigger tiles measured slower again.
const maxAutoTile = 800

// tileStep is the tools' own top step for a model: tile at budget MiB, and
// whether that budget is shared by the parallel processing jobs.
type tileStep struct {
	tile, budget int
	perJob       bool
}

func topStep(e Engine, scale int) (tileStep, bool) {
	switch {
	case e.Tool == "realesrgan":
		return tileStep{200, 1900, false}, true
	case e.Tool == "realcugan" && scale == 2:
		return tileStep{400, 1300, true}, true
	case e.Tool == "realcugan" && scale == 3:
		return tileStep{400, 3300, true}, true
	case e.Tool == "realcugan" && scale == 4:
		return tileStep{400, 1690, true}, true
	case e.Tool == "waifu2x" && strings.Contains(e.ModelDir, "cunet"):
		return tileStep{400, 2600, true}, true
	case e.Tool == "waifu2x":
		return tileStep{400, 1900, true}, true
	}
	return tileStep{}, false
}

// tileFor is the tile size for a GPU with mib MiB of memory, or 0 when the
// tool's own choice is as large.
func tileFor(e Engine, scale, mib, jobs int) int {
	step, ok := topStep(e, scale)
	if !ok || mib <= 0 {
		return 0
	}
	// what ncnn assumes it may use when the driver doesn't say
	budget := mib * 7 / 10
	if step.perJob && jobs > 1 {
		budget /= jobs
	}
	if budget <= step.budget {
		return 0
	}
	t := int(float64(step.tile)*math.Sqrt(float64(budget)/float64(step.budget))) / 32 * 32
	t = min(t, maxAutoTile)
	if t <= step.tile {
		return 0
	}
	return t
}

// procJobs is how many images the tool processes at once on one GPU (the
// middle of -j load:proc:save; the tools default to 2).
func procJobs(threads string) int {
	parts := strings.Split(threads, ":")
	if len(parts) != 3 {
		return 2
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.Split(parts[1], ",")[0]))
	if err != nil || n < 1 {
		return 2
	}
	return n
}

// gpuMem is one GPU's dedicated memory as the system reports it. An empty
// name matches any device (one shared memory, as on Apple silicon).
type gpuMem struct {
	name string
	mib  int
}

var (
	gpuMemOnce sync.Once
	gpuMems    []gpuMem
	// detectGPUMemory is replaced in tests.
	detectGPUMemory = func() []gpuMem {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// nvidia-smi knows NVIDIA cards exactly; what the system says fills in the rest
		return append(nvidiaMemory(ctx), systemGPUMemory()...)
	}
	devNames sync.Map // tools dir → []string, the tools' device names by index
	// tileCaps remembers, per model, scale and device, the tile that worked
	// after the automatic one ran out of memory.
	tileCaps   sync.Map
	autoLogged sync.Map
)

func nvidiaMemory(ctx context.Context) []gpuMem {
	p, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return nil
	}
	out, err := exec.CommandContext(ctx, p, "--query-gpu=name,memory.total", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil
	}
	var mems []gpuMem
	for _, l := range strings.Split(string(out), "\n") {
		name, mib, ok := strings.Cut(l, ",")
		n, err := strconv.Atoi(strings.TrimSpace(mib))
		if ok && err == nil && n > 0 {
			mems = append(mems, gpuMem{strings.TrimSpace(name), n})
		}
	}
	return mems
}

// memoryOf finds a device's memory by the name the tools print.
func memoryOf(mems []gpuMem, name string) int {
	name = strings.ToLower(strings.TrimSpace(name))
	shared := 0
	for _, m := range mems {
		n := strings.ToLower(m.name)
		switch {
		case n == "":
			shared = m.mib
		case name != "" && (n == name || strings.Contains(name, n) || strings.Contains(n, name)):
			return m.mib
		}
	}
	return shared
}

// deviceMemory is the memory of the device a batch runs on: an index from
// the GPU setting, or for auto the one the tools pick by default (the first
// discrete GPU, which is the first with memory of its own).
func deviceMemory(mems []gpuMem, names []string, device string) int {
	if i, err := strconv.Atoi(device); err == nil {
		if i < len(names) {
			return memoryOf(mems, names[i])
		}
		return 0
	}
	if len(names) == 0 {
		if len(mems) == 1 {
			return mems[0].mib
		}
		return 0
	}
	best := 0
	for _, n := range names {
		m := memoryOf(mems, n)
		if m >= 2048 {
			return m
		}
		best = max(best, m)
	}
	return best
}

// autoTile is the tile size to use when none is set, or 0 to leave it to
// the tool.
func (r CLIRunner) autoTile(ctx context.Context, e Engine, scale int, device string) (tile, mib int) {
	if _, ok := topStep(e, scale); !ok {
		return 0, 0
	}
	gpuMemOnce.Do(func() { gpuMems = detectGPUMemory() })
	if len(gpuMems) == 0 {
		return 0, 0
	}
	var names []string
	if v, ok := devNames.Load(r.ToolsDir); ok {
		names = v.([]string)
	} else {
		names = r.Devices(ctx)
		devNames.Store(r.ToolsDir, names)
	}
	mib = deviceMemory(gpuMems, names, device)
	tile = tileFor(e, scale, mib, procJobs(r.Threads))
	if c, ok := tileCaps.Load(capKey(e, scale, device)); ok && tile > c.(int) {
		tile = c.(int)
	}
	return tile, mib
}

func capKey(e Engine, scale int, device string) string {
	return e.Name + "|" + strconv.Itoa(scale) + "|" + device
}
