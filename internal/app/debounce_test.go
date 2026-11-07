package app

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestDebouncerRunsOnceAfterSettling: calls for a key close together run
// the work once, after the last one; other keys run on their own.
func TestDebouncerRunsOnceAfterSettling(t *testing.T) {
	d := NewDebouncer[string](30 * time.Millisecond)
	var a, b atomic.Int32
	for range 5 {
		d.Do("a", func() { a.Add(1) })
		time.Sleep(5 * time.Millisecond)
	}
	d.Do("b", func() { b.Add(1) })
	deadline := time.Now().Add(2 * time.Second)
	for (a.Load() == 0 || b.Load() == 0) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(60 * time.Millisecond)
	if a.Load() != 1 || b.Load() != 1 {
		t.Fatalf("runs: a %d, b %d", a.Load(), b.Load())
	}
	d.mu.Lock()
	left := len(d.pending)
	d.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d timers left pending", left)
	}
}
