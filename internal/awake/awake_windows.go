package awake

import (
	"errors"
	"runtime"

	"golang.org/x/sys/windows"
)

const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

var setThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

// hold sets a standing "system required" state on one locked thread and
// clears it on that same thread when stopped: Windows keeps the state per
// thread, so only its owner can take it back. While held it shows in
// `powercfg /requests` under this program; once stopped nothing is left.
func hold() (func(), error) {
	if err := setThreadExecutionState.Find(); err != nil {
		return nil, err
	}
	started := make(chan error, 1)
	done := make(chan struct{})
	cleared := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if r, _, err := setThreadExecutionState.Call(esContinuous | esSystemRequired); r == 0 {
			started <- errors.Join(errors.New("SetThreadExecutionState failed"), err)
			return
		}
		started <- nil
		<-done
		_, _, _ = setThreadExecutionState.Call(esContinuous)
		close(cleared)
	}()
	if err := <-started; err != nil {
		return nil, err
	}
	return func() {
		close(done)
		<-cleared
	}, nil
}
