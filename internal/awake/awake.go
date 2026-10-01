// Package awake keeps the computer from going to sleep while a desktop
// worker has work it may be given. The display may still turn off; only
// system sleep is held off.
package awake

import "sync"

// Keeper holds sleep off between Hold and Release. The zero value is ready.
type Keeper struct {
	mu   sync.Mutex
	stop func()
	err  error
}

// Hold keeps the computer awake until Release (a second Hold does nothing).
func (k *Keeper) Hold() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.stop != nil {
		return nil
	}
	stop, err := hold()
	k.err = err
	if err != nil {
		return err
	}
	k.stop = stop
	return nil
}

// Release lets the computer sleep again.
func (k *Keeper) Release() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.stop != nil {
		k.stop()
		k.stop = nil
	}
}

// Holding reports whether sleep is held off now.
func (k *Keeper) Holding() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.stop != nil
}
