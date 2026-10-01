//go:build darwin || linux

package awake

import (
	"errors"
	"os/exec"
	"time"
)

// run starts cmd as the holder; stopping kills it.
func run(cmd *exec.Cmd) (func(), error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	// a holder that can't take the lock (no systemd, say) exits at once
	select {
	case err := <-exited:
		if err == nil {
			err = errors.New("exited at once")
		}
		return nil, err
	case <-time.After(300 * time.Millisecond):
	}
	return func() {
		_ = cmd.Process.Kill()
		<-exited
	}, nil
}
