// Package sourcecache caches responses of source catalogs (search results,
// browse pages, manga details) in memory. Keys must include the catalogs
// generation (see internal/catalogs) so that disabling, hiding or removing a
// catalog never serves stale results; entries also expire after a TTL and the
// cache is bounded by an approximate byte size (LRU).
package sourcecache

import (
	"container/list"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Asion001/mangarr/internal/modules/source"
)

type entry struct {
	key     string
	val     any
	size    int64
	expires time.Time
}

// Cache is an LRU cache with TTLs and request coalescing.
type Cache struct {
	mu    sync.Mutex
	ll    *list.List
	items map[string]*list.Element
	bytes int64
	max   int64
	sf    singleflight.Group
	now   func() time.Time

	hits, misses atomic.Int64
}

// HitCounts reports how often a lookup found a live entry, since start.
func (c *Cache) HitCounts() (hits, misses int64) { return c.hits.Load(), c.misses.Load() }

func New(maxBytes int64) *Cache {
	return &Cache{ll: list.New(), items: map[string]*list.Element{}, max: maxBytes, now: time.Now}
}

// Get returns a live entry.
func (c *Cache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		c.misses.Add(1)
		return nil, false
	}
	e := el.Value.(*entry)
	if c.now().After(e.expires) {
		c.remove(el)
		c.misses.Add(1)
		return nil, false
	}
	c.ll.MoveToFront(el)
	c.hits.Add(1)
	return e.val, true
}

// Set stores val for ttl; size is its approximate memory cost.
func (c *Cache) Set(key string, val any, size int64, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.remove(el)
	}
	if size > c.max {
		return
	}
	e := &entry{key: key, val: val, size: size, expires: c.now().Add(ttl)}
	c.items[key] = c.ll.PushFront(e)
	c.bytes += size
	for c.bytes > c.max {
		c.remove(c.ll.Back())
	}
}

func (c *Cache) remove(el *list.Element) {
	e := el.Value.(*entry)
	c.ll.Remove(el)
	delete(c.items, e.key)
	c.bytes -= e.size
}

// DeletePrefix removes every entry whose key starts with prefix.
func (c *Cache) DeletePrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, el := range c.items {
		if strings.HasPrefix(k, prefix) {
			c.remove(el)
		}
	}
}

// Clear drops every entry.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ll.Init()
	c.items = map[string]*list.Element{}
	c.bytes = 0
}

// Stats reports the number of entries and their approximate size.
func (c *Cache) Stats() (entries int, bytes, max int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items), c.bytes, c.max
}

// Do returns the cached value for key or computes it with fn; concurrent
// callers for the same key share one fn call. Errors are not cached.
func Do[T any](c *Cache, key string, ttl time.Duration, fn func() (T, error)) (T, bool, error) {
	if v, ok := c.Get(key); ok {
		return v.(T), true, nil
	}
	v, err, _ := c.sf.Do(key, func() (any, error) {
		if v, ok := c.Get(key); ok { // filled while we waited
			return v, nil
		}
		v, err := fn()
		if err != nil {
			return nil, err
		}
		c.Set(key, v, sizeOf(v), ttl)
		return v, nil
	})
	if err != nil {
		var zero T
		return zero, false, err
	}
	return v.(T), false, nil
}

func sizeOf(v any) int64 {
	b, err := json.Marshal(v)
	if err != nil {
		return 1 << 10
	}
	return int64(len(b)) + 256
}

// Keys shared by the API and background jobs. gen is the catalogs generation.

func SearchKey(gen, moduleID int64, sourceID, query string, page int) string {
	return fmt.Sprintf("%d|search|%d|%s|%s|%d", gen, moduleID, sourceID, strings.ToLower(strings.TrimSpace(query)), page)
}

func BrowseKey(gen, moduleID int64, sourceID, kind string, page int) string {
	return fmt.Sprintf("%d|%s|%d|%s|%d", gen, kind, moduleID, sourceID, page)
}

func DetailsKey(gen, moduleID int64, sourceID, url string) string {
	return fmt.Sprintf("%d|manga|%d|%s|%s", gen, moduleID, sourceID, url)
}

// Details is a cached manga details response.
type Details struct {
	Details   *source.MangaDetails `json:"details"`
	Chapters  []source.Chapter     `json:"chapters"`
	FetchedAt time.Time            `json:"fetchedAt"`
}
