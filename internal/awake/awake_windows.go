package awake

import (
	"time"

	"golang.org/x/sys/windows"
)

const esSystemRequired = 0x00000001

var setThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

// hold tells Windows the system is in use every half minute, which resets
// its idle-sleep timer each time (no thread has to keep a state set).
func hold() (func(), error) {
	if err := setThreadExecutionState.Find(); err != nil {
		return nil, err
	}
	ping := func() { _, _, _ = setThreadExecutionState.Call(esSystemRequired) }
	ping()
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				ping()
			}
		}
	}()
	return func() { close(done) }, nil
}
