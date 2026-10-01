package worker

import (
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// cgroupLimitFiles hold the container's memory limit (cgroup v2, then v1).
var cgroupLimitFiles = []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"}

// SetMemoryLimit gives the Go heap a soft limit of half the container's
// memory, unless GOMEMLIMIT already sets one. The upscaling tools run
// inside the same limit, and without a soft limit the heap grows to twice
// what it holds before the collector runs — enough, next to an upscaler,
// for the kernel to kill the whole worker. It returns the limit it set.
func SetMemoryLimit(getenv func(string) string) int64 {
	if getenv("GOMEMLIMIT") != "" {
		return 0
	}
	for _, f := range cgroupLimitFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if limit := halfOf(string(data)); limit > 0 {
			debug.SetMemoryLimit(limit)
			return limit
		}
		return 0
	}
	return 0
}

// halfOf reads a cgroup memory limit and returns half of it, or 0 when
// there is none ("max", or v1's huge "unlimited" number).
func halfOf(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 || n >= 1<<50 {
		return 0
	}
	return n / 2
}
