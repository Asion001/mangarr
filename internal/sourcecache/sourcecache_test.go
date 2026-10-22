package sourcecache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLRUAndTTL(t *testing.T) {
	c := New(1000)
	now := time.Unix(0, 0)
	c.now = func() time.Time { return now }
	c.Set("a", 1, 400, time.Minute)
	c.Set("b", 2, 400, time.Minute)
	if _, ok := c.Get("a"); !ok { // a becomes most recent
		t.Fatal("a missing")
	}
	c.Set("c", 3, 400, time.Minute) // evicts b (least recent)
	if _, ok := c.Get("b"); ok {
		t.Fatal("b should be evicted")
	}
	if n, b, _ := c.Stats(); n != 2 || b != 800 {
		t.Fatalf("stats %d %d", n, b)
	}
	now = now.Add(2 * time.Minute)
	if _, ok := c.Get("a"); ok {
		t.Fatal("a should be expired")
	}
	c.Set("huge", 1, 5000, time.Minute)
	if _, ok := c.Get("huge"); ok {
		t.Fatal("entries larger than the cache are not stored")
	}
	c.Set("x1", 1, 10, time.Minute)
	c.Set("y1", 1, 10, time.Minute)
	c.DeletePrefix("x")
	if _, ok := c.Get("x1"); ok {
		t.Fatal("prefix delete")
	}
}

func TestDoCoalescesAndSkipsErrors(t *testing.T) {
	c := New(1 << 20)
	var calls atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			v, _, err := Do(c, "k", time.Minute, func() (string, error) {
				calls.Add(1)
				time.Sleep(50 * time.Millisecond)
				return "v", nil
			})
			if err != nil || v != "v" {
				t.Errorf("got %q %v", v, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("want 1 upstream call, got %d", calls.Load())
	}
	if _, cached, _ := Do(c, "k", time.Minute, func() (string, error) { return "x", nil }); !cached {
		t.Fatal("second call should be cached")
	}
	_, _, err := Do(c, "e", time.Minute, func() (int, error) { return 0, errors.New("boom") })
	if err == nil {
		t.Fatal("expected error")
	}
	if v, _, _ := Do(c, "e", time.Minute, func() (int, error) { return 7, nil }); v != 7 {
		t.Fatal("errors must not be cached")
	}
}

// TestDoStale: an expired value is served at once while it is refreshed in
// the background, and only until keep runs out.
func TestDoStale(t *testing.T) {
	c := New(1 << 20)
	var mu sync.Mutex
	now := time.Unix(0, 0)
	c.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	var calls atomic.Int32
	refreshed := make(chan struct{}, 4)
	fetch := func(ctx context.Context) (int32, error) {
		n := calls.Add(1)
		refreshed <- struct{}{}
		return n, nil
	}
	ctx := context.Background()
	if v, cached, err := DoStale(ctx, c, "k", time.Minute, time.Hour, fetch); err != nil || cached || v != 1 {
		t.Fatalf("first: %v %v %v", v, cached, err)
	}
	<-refreshed
	if v, cached, _ := DoStale(ctx, c, "k", time.Minute, time.Hour, fetch); !cached || v != 1 || calls.Load() != 1 {
		t.Fatalf("fresh: %v %v after %d calls", v, cached, calls.Load())
	}
	advance(2 * time.Minute)
	if v, cached, _ := DoStale(ctx, c, "k", time.Minute, time.Hour, fetch); !cached || v != 1 {
		t.Fatalf("stale: %v %v", v, cached)
	}
	select {
	case <-refreshed:
	case <-time.After(5 * time.Second):
		t.Fatal("no background refresh")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if v, ok := c.Get("k"); ok && v.(int32) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the refreshed value wasn't stored")
		}
		time.Sleep(5 * time.Millisecond)
	}
	advance(2 * time.Hour)
	if v, cached, _ := DoStale(ctx, c, "k", time.Minute, time.Hour, fetch); cached || v != 3 {
		t.Fatalf("past keep: %v %v", v, cached)
	}
}
