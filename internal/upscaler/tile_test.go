package upscaler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestTileForCarriesTheToolsTableOn(t *testing.T) {
	esrgan, _ := findEngine("realesr-animevideov3")
	cugan, _ := findEngine("realcugan")
	cunet, _ := findEngine("waifu2x-cunet")
	for _, c := range []struct {
		e                Engine
		scale, mib, jobs int
		want             int
	}{
		{esrgan, 4, 12288, 2, 416}, // 12 GB: twice the tool's 200
		{esrgan, 4, 2048, 2, 0},    // small GPU: the tool's own choice
		{esrgan, 4, 49152, 2, 800}, // capped
		{cugan, 2, 12288, 2, 704},  // the budget is split between the jobs
		{cugan, 2, 12288, 1, 800},  //
		{cugan, 3, 12288, 2, 448},  //
		{cunet, 2, 12288, 2, 512},  //
		{cunet, 2, 4096, 2, 0},     //
		{Engine{Tool: "other"}, 2, 12288, 2, 0},
	} {
		if got := tileFor(c.e, c.scale, c.mib, c.jobs); got != c.want {
			t.Errorf("%s x%d %d MiB %d jobs: tile %d, want %d", c.e.Name, c.scale, c.mib, c.jobs, got, c.want)
		}
	}
}

func TestProcJobs(t *testing.T) {
	for in, want := range map[string]int{"": 2, "1:2:2": 2, "1:4:2": 4, "2:3,3:2": 3, "junk": 2, "1:0:1": 2} {
		if got := procJobs(in); got != want {
			t.Errorf("procJobs(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestDeviceMemory(t *testing.T) {
	mems := []gpuMem{{"Intel(R) UHD Graphics 770", 128}, {"NVIDIA GeForce RTX 3060", 12288}}
	names := []string{"Intel(R) UHD Graphics 770", "NVIDIA GeForce RTX 3060"}
	if got := deviceMemory(mems, names, "auto"); got != 12288 {
		t.Errorf("auto: %d, want the discrete GPU", got)
	}
	if got := deviceMemory(mems, names, "0"); got != 128 {
		t.Errorf("0: %d", got)
	}
	if got := deviceMemory(mems, names, "5"); got != 0 {
		t.Errorf("unknown index: %d", got)
	}
	if got := deviceMemory([]gpuMem{{"", 8192}}, []string{"Apple M2"}, "auto"); got != 8192 {
		t.Errorf("shared memory: %d", got)
	}
	if got := deviceMemory(mems, nil, "auto"); got != 0 {
		t.Errorf("auto without device names: %d", got)
	}
}

// autoEngine is the fake tool posing as Real-ESRGAN, whose own top tile is
// 200, with 12 GB on its second device (llvmpipe).
func autoEngine(t *testing.T, script string) (CLIRunner, Engine, string) {
	r, e, log := fakeTool(t, script)
	if err := os.Rename(filepath.Join(r.ToolsDir, e.Tool), filepath.Join(r.ToolsDir, "realesrgan")); err != nil {
		t.Fatal(err)
	}
	e.Tool, e.Name = "realesrgan", "auto-"+t.Name()
	old, oldCatalog := detectGPUMemory, Catalog
	reset := func() { gpuMemOnce, gpuMems = sync.Once{}, nil; devNames.Delete(r.ToolsDir) }
	reset()
	Catalog = []Engine{e}
	detectGPUMemory = func() []gpuMem { return []gpuMem{{"llvmpipe", 12288}} }
	t.Cleanup(func() { detectGPUMemory, Catalog = old, oldCatalog; reset() })
	return r, e, log
}

func TestRunnerPicksTileFromGPUMemory(t *testing.T) {
	r, e, log := autoEngine(t, "exit 0")
	if err := r.RunDevice(context.Background(), e, t.TempDir(), t.TempDir(), 4, 0, "1"); err != nil {
		t.Fatal(err)
	}
	if err := r.RunDevice(context.Background(), e, t.TempDir(), t.TempDir(), 4, 0, "0"); err != nil {
		t.Fatal(err)
	}
	// the first call lists the devices; the Intel one has no known memory
	c := calls(t, log)
	if len(c) != 3 || !strings.Contains(c[1], "-t 416") || strings.Contains(c[2], "-t ") {
		t.Fatalf("calls %q", c)
	}
}

func TestRunnerRemembersTheTileThatFit(t *testing.T) {
	r, e, log := autoEngine(t, `case "$*" in *"-t 416"*) kill -9 $$;; esac`)
	for range 2 {
		if err := r.RunDevice(context.Background(), e, t.TempDir(), t.TempDir(), 4, 0, "1"); err != nil {
			t.Fatal(err)
		}
	}
	c := calls(t, log)
	if len(c) != 4 || !strings.Contains(c[1], "-t 416") || !strings.Contains(c[2], "-t 256") || !strings.Contains(c[3], "-t 256") {
		t.Fatalf("calls %q", c)
	}
}

func TestRunnerConfiguredTileWins(t *testing.T) {
	r, e, log := autoEngine(t, "exit 0")
	r.Tile = 300
	if err := r.RunDevice(context.Background(), e, t.TempDir(), t.TempDir(), 4, 0, "1"); err != nil {
		t.Fatal(err)
	}
	if c := calls(t, log); len(c) != 1 || !strings.Contains(c[0], "-t 300") {
		t.Fatalf("calls %q", c)
	}
}
