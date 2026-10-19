package fsutil

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// FreeSpace returns available bytes on the filesystem holding path. Only
// the Windows worker build needs this to compile; the server runs on Unix.
func FreeSpace(path string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, nil, nil); err != nil {
		return 0, err
	}
	return free, nil
}

// Usage returns the size of the volume holding path, its available bytes,
// and the path's volume as its id.
func Usage(path string) (total, free uint64, device string, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, "", err
	}
	if err = windows.GetDiskFreeSpaceEx(p, &free, &total, nil); err != nil {
		return 0, 0, "", err
	}
	return total, free, filepath.VolumeName(path), nil
}
