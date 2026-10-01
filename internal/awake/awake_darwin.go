package awake

import (
	"os"
	"os/exec"
	"strconv"
)

// hold runs caffeinate, which keeps idle sleep off while it runs and quits
// by itself if this process dies.
func hold() (func(), error) {
	return run(exec.Command("caffeinate", "-i", "-w", strconv.Itoa(os.Getpid())))
}
