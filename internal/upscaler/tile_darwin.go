package upscaler

import "golang.org/x/sys/unix"

// systemGPUMemory on a Mac: the GPU shares the system's memory, and Metal
// lets it use about half.
func systemGPUMemory() []gpuMem {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil || n == 0 {
		return nil
	}
	return []gpuMem{{"", int(n >> 21)}}
}
