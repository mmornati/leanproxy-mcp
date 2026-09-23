package responsecache

import (
	"container/list"
	"sync"
	"sync/atomic"
	"time"
)

// entry is one cached response, keyed by the pre-redaction request hash.
type entry struct {
	key       string
	value     []byte
	size      int64
	expiresAt time.Time
}

// Stats is a point-in-time snapshot of cache counters, suitable for the
// metrics endpoint.
type Stats struct {
	Hits      uint64 `json:"hits"`
	Misses    uint64 `json:"misses"`
	Evictions uint64 `json:"evictions"`
	Bytes     int64  `json:"bytes"`
	Entries   int    `json:"entries"`
}

// Cache is a bounded-by-bytes LRU with a per-entry TTL. It stores raw bytes
// (a marshaled, already-redacted JSON-RPC response) under an opaque SHA-256
// key (see Key) — never the original request or arguments. Safe for
// concurrent use.
type Cache struct {
	mu       sync.Mutex
	ttl      time.Duration
	maxBytes int64
	maxEntry int64
	bytes    int64
	ll       *list.List // front = most recently used
	items    map[string]*list.Element
	now      func() time.Time

	hits, misses, evictions atomic.Uint64
}

// New builds a Cache. maxBytes/maxEntryBytes <= 0 fall back to the package
// defaults. ttl <= 0 disables expiry (entries only leave via LRU eviction).
func New(ttl time.Duration, maxBytes, maxEntryBytes int64) *Cache {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if maxEntryBytes <= 0 {
		maxEntryBytes = DefaultMaxEntryBytes
	}
	return &Cache{
		ttl:      ttl,
		maxBytes: maxBytes,
		maxEntry: maxEntryBytes,
		ll:       list.New(),
		items:    make(map[string]*list.Element),
		now:      time.Now,
	}
}

// Get returns a copy of the cached value for key, or (nil, false) on a miss
// or an expired entry (which is evicted on the way out).
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		c.misses.Add(1)
		return nil, false
	}
	e, _ := el.Value.(*entry)
	if c.ttl > 0 && c.now().After(e.expiresAt) {
		c.removeElement(el)
		c.misses.Add(1)
		return nil, false
	}
	c.ll.MoveToFront(el)
	c.hits.Add(1)
	out := make([]byte, len(e.value))
	copy(out, e.value)
	return out, true
}

// Set stores value under key, evicting the least-recently-used entries until
// total bytes are back at or below maxBytes. A value larger than
// maxEntryBytes is never stored (Set returns false); this is intentional so
// one large response cannot alone blow the whole budget.
func (c *Cache) Set(key string, value []byte) bool {
	size := int64(len(value))
	if size > c.maxEntry {
		return false
	}

	stored := make([]byte, len(value))
	copy(stored, value)

	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.items[key]; ok {
		c.removeElement(el)
	}

	e := &entry{key: key, value: stored, size: size}
	if c.ttl > 0 {
		e.expiresAt = c.now().Add(c.ttl)
	}
	el := c.ll.PushFront(e)
	c.items[key] = el
	c.bytes += size

	for c.bytes > c.maxBytes {
		back := c.ll.Back()
		if back == nil {
			break
		}
		c.removeElement(back)
		c.evictions.Add(1)
	}
	return true
}

// removeElement removes el from both the list and the index and adjusts the
// byte total. Callers must hold c.mu.
func (c *Cache) removeElement(el *list.Element) {
	e, _ := el.Value.(*entry)
	c.ll.Remove(el)
	delete(c.items, e.key)
	c.bytes -= e.size
}

// Stats returns a snapshot of the cache's counters.
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	n := c.ll.Len()
	b := c.bytes
	c.mu.Unlock()
	return Stats{
		Hits:      c.hits.Load(),
		Misses:    c.misses.Load(),
		Evictions: c.evictions.Load(),
		Bytes:     b,
		Entries:   n,
	}
}
