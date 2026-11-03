//go:build windows

package onnxruntime

import "syscall"

// openLibrary loads onnxruntime.dll (mangarr: purego has no Dlopen on
// Windows; a DLL handle works with purego.RegisterLibFunc).
func openLibrary(path string) (uintptr, error) {
	h, err := syscall.LoadLibrary(path)
	return uintptr(h), err
}
