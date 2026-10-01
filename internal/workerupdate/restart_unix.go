//go:build !windows

package workerupdate

import (
	"os"
	"syscall"
)

// Restart replaces this process with exe started with args, keeping its
// process id, so a service manager watching it sees nothing stop. It only
// returns when that fails.
func Restart(exe string, args []string) error {
	return syscall.Exec(exe, append([]string{exe}, args...), os.Environ())
}
