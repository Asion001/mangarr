//go:build unix

package fsutil

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// FreeSpace returns available bytes on the filesystem holding path.
func FreeSpace(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// Usage returns the size of the filesystem holding path, its available
// bytes, and an id that is the same for paths on one filesystem.
func Usage(path string) (total, free uint64, device string, err error) {
	var st unix.Statfs_t
	if err = unix.Statfs(path, &st); err != nil {
		return 0, 0, "", err
	}
	return uint64(st.Blocks) * uint64(st.Bsize), uint64(st.Bavail) * uint64(st.Bsize),
		fmt.Sprintf("%d:%d", st.Fsid.Val[0], st.Fsid.Val[1]), nil
}
