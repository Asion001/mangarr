package upscaler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTool writes a shell script standing in for an ncnn binary; calls are
// appended to calls.log. script sees the arguments in "$@".
func fakeTool(t *testing.T, script string) (CLIRunner, Engine, string) {
	t.Helper()
	dir := t.TempDir()
	tool := filepath.Join(dir, "fake")
	_ = os.MkdirAll(filepath.Join(tool, "models"), 0o755)
	log := filepath.Join(dir, "calls.log")
	body := "#!/bin/sh\necho \"$@\" >> " + log + "\n" +
		"echo '[0 Intel(R) Graphics (ADL-N)]  queueC=0[1] queueT=0[1]'\n" +
		"echo '[1 llvmpipe (LLVM 19.1.7, 256 bits)]  fp16-p/s/u/a=1/1/1/1'\n" + script + "\n"
	if err := os.WriteFile(filepath.Join(tool, "tool"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return CLIRunner{ToolsDir: dir}, Engine{Name: "fake", Tool: "fake", Binary: "tool", ModelDir: "models", Scales: []int{2}}, log
}

func calls(t *testing.T, log string) []string {
	b, _ := os.ReadFile(log)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestRunnerRetriesSmallerTilesAfterOOMKill(t *testing.T) {
	r, e, log := fakeTool(t, `case "$*" in *"-t 256"*) exit 0;; *) kill -9 $$;; esac`)
	if err := r.Run(context.Background(), e, t.TempDir(), t.TempDir(), 2, 1); err != nil {
		t.Fatal(err)
	}
	c := calls(t, log)
	if len(c) != 2 || strings.Contains(c[0], "-t ") || !strings.Contains(c[1], "-t 256") {
		t.Fatalf("calls %q", c)
	}
}

func TestRunnerAlwaysKilled(t *testing.T) {
	r, e, log := fakeTool(t, `kill -9 $$`)
	err := r.Run(context.Background(), e, t.TempDir(), t.TempDir(), 2, 1)
	if !errors.Is(err, ErrOutOfMemory) || !strings.Contains(err.Error(), "killed by the system") || !strings.Contains(err.Error(), "tile 128") {
		t.Fatalf("err = %v", err)
	}
	if c := calls(t, log); len(c) != 3 {
		t.Fatalf("calls %q", c)
	}
}

func TestRunnerGPUOutOfMemoryMessage(t *testing.T) {
	r, e, log := fakeTool(t, `case "$*" in *"-t 128"*) exit 0;; *) echo 'vkAllocateMemory failed -2'; exit 255;; esac`)
	r.Tile = 400
	if err := r.Run(context.Background(), e, t.TempDir(), t.TempDir(), 2, 1); err != nil {
		t.Fatal(err)
	}
	if c := calls(t, log); len(c) != 3 || !strings.Contains(c[0], "-t 400") {
		t.Fatalf("calls %q", c)
	}
}

func TestRunnerTimeoutIsNotRetried(t *testing.T) {
	r, e, log := fakeTool(t, `exec sleep 5`)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := r.Run(ctx, e, t.TempDir(), t.TempDir(), 2, 1)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
	if c := calls(t, log); len(c) != 1 {
		t.Fatalf("calls %q", c)
	}
}

func TestRunnerErrorWithoutDeviceList(t *testing.T) {
	r, e, _ := fakeTool(t, `echo 'find_blob_index_by_name: model not found'; exit 1`)
	err := r.Run(context.Background(), e, t.TempDir(), t.TempDir(), 2, 1)
	if err == nil || errors.Is(err, ErrOutOfMemory) || !strings.Contains(err.Error(), "model not found") || strings.Contains(err.Error(), "Intel") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunnerFindsWindowsBinaries(t *testing.T) {
	dir := t.TempDir()
	e := Engine{Name: "fake", Tool: "fake", Binary: "tool", ModelDir: "models"}
	_ = os.MkdirAll(filepath.Join(dir, "fake", "models"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "fake", "tool.exe"), nil, 0o755)
	old := exeSuffix
	defer func() { exeSuffix = old }()
	exeSuffix = ".exe"
	if !(CLIRunner{ToolsDir: dir}).Available(e) {
		t.Fatal("tool.exe was not found")
	}
	exeSuffix = ""
	if (CLIRunner{ToolsDir: dir}).Available(e) {
		t.Fatal("tool was found without its .exe")
	}
}
