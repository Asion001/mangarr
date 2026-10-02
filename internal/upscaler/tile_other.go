//go:build !windows && !darwin

package upscaler

// systemGPUMemory has nothing to go on here beyond nvidia-smi.
func systemGPUMemory() []gpuMem { return nil }
