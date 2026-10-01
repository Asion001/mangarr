package awake

import (
	"os"
	"os/exec"
	"strconv"
)

// hold takes a systemd sleep inhibitor for as long as a placeholder command
// runs; the command (tail --pid) ends by itself if this process dies.
func hold() (func(), error) {
	return run(exec.Command("systemd-inhibit", "--what=sleep:idle", "--who=mangarr-worker",
		"--why=working for mangarr", "--mode=block", "tail", "--pid="+strconv.Itoa(os.Getpid()), "-f", "/dev/null"))
}
