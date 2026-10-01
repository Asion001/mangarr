//go:build windows

package workerupdate

import (
	"os"
	"os/exec"
)

// Restart starts exe with args and ends this process: Windows has no exec,
// so the new program runs as a new process in the same console.
func Restart(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
