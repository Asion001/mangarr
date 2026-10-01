package awake

import "testing"

func TestHoldAndRelease(t *testing.T) {
	var k Keeper
	if err := k.Hold(); err != nil {
		t.Skipf("can't keep awake on this machine: %v", err)
	}
	if err := k.Hold(); err != nil || !k.Holding() {
		t.Fatalf("second hold: %v, holding %v", err, k.Holding())
	}
	k.Release()
	k.Release()
	if k.Holding() {
		t.Fatal("still holding after release")
	}
}
