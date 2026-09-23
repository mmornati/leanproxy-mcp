package responsecache

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

func TestCache_SetGet(t *testing.T) {
	c := New(time.Minute, DefaultMaxBytes, DefaultMaxEntryBytes)
	if !c.Set("k", []byte("v")) {
		t.Fatal("Set() = false, want true")
	}
	got, ok := c.Get("k")
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if !bytes.Equal(got, []byte("v")) {
		t.Errorf("Get() = %q, want %q", got, "v")
	}
}

func TestCache_MissOnUnknownKey(t *testing.T) {
	c := New(time.Minute, DefaultMaxBytes, DefaultMaxEntryBytes)
	if _, ok := c.Get("missing"); ok {
		t.Error("expected a miss for an unknown key")
	}
	stats := c.Stats()
	if stats.Misses != 1 {
		t.Errorf("Misses = %d, want 1", stats.Misses)
	}
}

func TestCache_EntryLargerThanMaxEntryBytesNeverStored(t *testing.T) {
	c := New(time.Minute, 1024, 8)
	if c.Set("k", []byte("this value is way bigger than 8 bytes")) {
		t.Fatal("Set() = true for an oversized entry, want false")
	}
	if _, ok := c.Get("k"); ok {
		t.Error("oversized entry must never be retrievable")
	}
}

func TestCache_TTLExpiry(t *testing.T) {
	c := New(10*time.Millisecond, DefaultMaxBytes, DefaultMaxEntryBytes)
	c.Set("k", []byte("v"))
	time.Sleep(30 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Error("expected the entry to have expired")
	}
}

func TestCache_LRUEvictionByBytes(t *testing.T) {
	// Each entry is ~10 bytes; a 25-byte budget keeps roughly 2 entries.
	c := New(time.Minute, 25, DefaultMaxEntryBytes)
	c.Set("a", []byte("0123456789"))
	c.Set("b", []byte("0123456789"))
	c.Set("c", []byte("0123456789")) // should evict "a" (least recently used)

	if _, ok := c.Get("a"); ok {
		t.Error("expected the least-recently-used entry to have been evicted")
	}
	if _, ok := c.Get("b"); !ok {
		t.Error("expected \"b\" to still be cached")
	}
	if _, ok := c.Get("c"); !ok {
		t.Error("expected \"c\" to still be cached")
	}

	stats := c.Stats()
	if stats.Bytes > 25 {
		t.Errorf("Bytes = %d, want <= 25", stats.Bytes)
	}
	if stats.Evictions == 0 {
		t.Error("expected at least one eviction")
	}
}

func TestCache_LRUOrderRespectsRecentGet(t *testing.T) {
	c := New(time.Minute, 25, DefaultMaxEntryBytes)
	c.Set("a", []byte("0123456789"))
	c.Set("b", []byte("0123456789"))
	c.Get("a") // touch "a" so "b" becomes the LRU victim
	c.Set("c", []byte("0123456789"))

	if _, ok := c.Get("b"); ok {
		t.Error("expected \"b\" to have been evicted after \"a\" was touched")
	}
	if _, ok := c.Get("a"); !ok {
		t.Error("expected \"a\" to still be cached (recently used)")
	}
}

// Acceptance criterion: inserting 200 x 1 MB responses with max_bytes: 64MiB
// keeps cache bytes at or below 64 MiB, per the cache's own accounting.
func TestCache_BoundedMemoryUnderLoad(t *testing.T) {
	const maxBytes = 64 * 1024 * 1024
	c := New(time.Minute, maxBytes, 1024*1024)

	payload := bytes.Repeat([]byte("x"), 1024*1024) // 1 MiB
	for i := 0; i < 200; i++ {
		c.Set(fmt.Sprintf("key-%d", i), payload)
	}

	stats := c.Stats()
	if stats.Bytes > maxBytes {
		t.Fatalf("Bytes = %d, want <= %d", stats.Bytes, maxBytes)
	}
	if stats.Evictions == 0 {
		t.Error("expected evictions once the byte budget was exceeded")
	}
}

func TestCache_SetOverwritesExisting(t *testing.T) {
	c := New(time.Minute, DefaultMaxBytes, DefaultMaxEntryBytes)
	c.Set("k", []byte("v1"))
	c.Set("k", []byte("v2"))
	got, ok := c.Get("k")
	if !ok || !bytes.Equal(got, []byte("v2")) {
		t.Errorf("Get() = %q, ok=%v, want %q, true", got, ok, "v2")
	}
	if c.Stats().Entries != 1 {
		t.Errorf("Entries = %d, want 1", c.Stats().Entries)
	}
}

func TestCache_GetReturnsACopy(t *testing.T) {
	c := New(time.Minute, DefaultMaxBytes, DefaultMaxEntryBytes)
	c.Set("k", []byte("v"))
	got, _ := c.Get("k")
	got[0] = 'X'
	again, _ := c.Get("k")
	if !bytes.Equal(again, []byte("v")) {
		t.Error("mutating a Get() result must not affect the stored entry")
	}
}
