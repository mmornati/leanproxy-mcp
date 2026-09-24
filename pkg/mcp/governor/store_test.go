package governor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestStore_PutGetIsolatedPerOwner(t *testing.T) {
	s := NewMemoryStore(time.Minute, 1<<20)
	alice, bob := new(int), new(int)
	meta, err := s.Put(alice, Meta{Server: "fs", Tool: "read", Kind: KindText}, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if !ValidID(meta.ID) || meta.Size != 5 || meta.Identity() != "fs.read" {
		t.Fatalf("meta = %+v", meta)
	}
	data, got, err := s.Get(alice, meta.ID)
	if err != nil || string(data) != "hello" || got.ID != meta.ID {
		t.Fatalf("Get = %q %+v %v", data, got, err)
	}
	if _, _, err := s.Get(bob, meta.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another owner read the result: %v", err)
	}
	if _, _, err := s.Get(alice, "r_aaaaaaaaaaaaaaaaaaaaaaaaaa"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestStore_IDsAreUniqueAndUnguessable(t *testing.T) {
	s := NewMemoryStore(time.Minute, 1<<20)
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		m, err := s.Put(nil, Meta{}, []byte("x"))
		if err != nil {
			t.Fatal(err)
		}
		if seen[m.ID] || !ValidID(m.ID) {
			t.Fatalf("duplicate or malformed id %q", m.ID)
		}
		seen[m.ID] = true
	}
	for _, bad := range []string{"", "r_", "r_ABCDEFGHIJKLMNOPQRSTUVWXYZ", "../etc/passwd", "r_abcdefghijklmnopqrstuvwxy1"} {
		if ValidID(bad) {
			t.Errorf("ValidID(%q) = true", bad)
		}
	}
}

func TestStore_TTLExpiry(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	s := NewMemoryStore(time.Minute, 1<<20)
	s.now = clock.now
	m, _ := s.Put("o", Meta{}, []byte("data"))
	clock.advance(59 * time.Second)
	if _, _, err := s.Get("o", m.ID); err != nil {
		t.Fatalf("expired too early: %v", err)
	}
	clock.advance(2 * time.Second)
	if _, _, err := s.Get("o", m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("not expired: %v", err)
	}
	m2, _ := s.Put("o", Meta{}, []byte("more"))
	clock.advance(2 * time.Minute)
	s.Sweep()
	if st := s.Stats(); st.Entries != 0 || st.Bytes != 0 || st.Expirations != 2 {
		t.Fatalf("stats after sweep = %+v (id %s)", st, m2.ID)
	}
}

func TestStore_LRUByteCap(t *testing.T) {
	s := NewMemoryStore(time.Hour, 100)
	a, _ := s.Put("o", Meta{}, make([]byte, 40))
	b, _ := s.Put("o", Meta{}, make([]byte, 40))
	if _, _, err := s.Get("o", a.ID); err != nil { // a is now most recently used
		t.Fatal(err)
	}
	c, _ := s.Put("o", Meta{}, make([]byte, 40))
	if _, _, err := s.Get("o", b.ID); !errors.Is(err, ErrNotFound) {
		t.Error("least recently used entry was not evicted")
	}
	for _, id := range []string{a.ID, c.ID} {
		if _, _, err := s.Get("o", id); err != nil {
			t.Errorf("%s evicted: %v", id, err)
		}
	}
	if st := s.Stats(); st.Bytes != 80 || st.Evictions != 1 {
		t.Errorf("stats = %+v", st)
	}
	if _, err := s.Put("o", Meta{}, make([]byte, 101)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversized put: %v", err)
	}
}

func TestStore_DropOwner(t *testing.T) {
	s := NewMemoryStore(time.Hour, 1<<20)
	a, _ := s.Put("alice", Meta{}, []byte("a"))
	b, _ := s.Put("bob", Meta{}, []byte("b"))
	s.DropOwner("alice")
	if _, _, err := s.Get("alice", a.ID); !errors.Is(err, ErrNotFound) {
		t.Error("alice's result survived DropOwner")
	}
	if _, _, err := s.Get("bob", b.ID); err != nil {
		t.Error("bob's result was dropped")
	}
}

func TestStore_DiskModeFilesAre0600AndRemoved(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "results")
	s, err := NewDiskStore(parent, time.Minute, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(s.Dir())
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("spill dir mode = %v, %v", fi.Mode().Perm(), err)
	}
	m, err := s.Put("o", Meta{}, []byte("on disk"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir(), m.ID)
	fi, err = os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("spill file mode = %v, %v", fi.Mode().Perm(), err)
	}
	data, _, err := s.Get("o", m.ID)
	if err != nil || string(data) != "on disk" {
		t.Fatalf("Get = %q, %v", data, err)
	}
	s.DropOwner("o")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file survived DropOwner: %v", err)
	}
	m2, _ := s.Put("o", Meta{}, []byte("again"))
	s.Close()
	if _, err := os.Stat(s.Dir()); !os.IsNotExist(err) {
		t.Errorf("spill dir survived Close: %v", err)
	}
	if _, err := s.Put("o", Meta{}, []byte("late")); err == nil {
		t.Error("Put after Close succeeded")
	}
	_ = m2
}

func TestStore_DiskModePrunesStaleProcessDirs(t *testing.T) {
	parent := t.TempDir()
	stale := filepath.Join(parent, "12345-abcdefgh")
	fresh := filepath.Join(parent, "12346-abcdefgh")
	other := filepath.Join(parent, "keep-me")
	for _, d := range []string{stale, fresh, other} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, d := range []string{stale, other} {
		if err := os.Chtimes(d, old, old); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewDiskStore(parent, time.Hour, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale process dir not pruned")
	}
	for _, d := range []string{fresh, other} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s pruned: %v", d, err)
		}
	}
}

func TestStore_Concurrent(t *testing.T) {
	s := NewMemoryStore(time.Minute, 64<<10)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			owner := fmt.Sprint("owner", w)
			for i := 0; i < 200; i++ {
				m, err := s.Put(owner, Meta{}, make([]byte, 512))
				if err != nil {
					t.Error(err)
					return
				}
				_, _, _ = s.Get(owner, m.ID)
				if _, _, err := s.Get("intruder", m.ID); err == nil {
					t.Error("cross-owner read")
				}
				if i%50 == 0 {
					s.Sweep()
					_ = s.Stats()
				}
			}
			s.DropOwner(owner)
		}(w)
	}
	wg.Wait()
	if st := s.Stats(); st.Bytes > 64<<10 {
		t.Fatalf("byte cap exceeded: %+v", st)
	}
}
