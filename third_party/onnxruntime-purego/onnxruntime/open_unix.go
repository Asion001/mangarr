//go:build !windows

package onnxruntime

import "github.com/ebitengine/purego"

// openLibrary loads the ONNX Runtime shared library (mangarr: split out
// so Windows can load it too).
func openLibrary(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}
