package governor

import (
	"container/list"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is returned for a result id that is unknown, expired, evicted
// or owned by another session. The cases are deliberately not told apart,
// so a client cannot probe for other sessions' ids.
var ErrNotFound = errors.New("result not found")

// ErrTooLarge is returned by Put for a result larger than the whole store.
var ErrTooLarge = errors.New("result larger than the spill store")

// Kind is how a spilled result is interpreted.
type Kind string

const (
	// KindText is plain text.
	KindText Kind = "text"
	// KindJSON is a JSON document (a text item holding valid JSON, or a
	// structuredContent value).
	KindJSON Kind = "json"
)

// idPrefix starts every result id.
const idPrefix = "r_"

// idPattern matches a result id: "r_" + 26 base32 characters (128 random
// bits).
var idPattern = regexp.MustCompile(`^r_[a-z2-7]{26}$`)

// ValidID reports whether id has the shape of a result id.
func ValidID(id string) bool { return idPattern.MatchString(id) }

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// newID returns an unguessable result id (crypto/rand, 128 bits).
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return idPrefix + strings.ToLower(idEncoding.EncodeToString(b[:])), nil
}

// Meta describes a spilled result.
type Meta struct {
	// ID is the result id (set by Put).
	ID string
	// Server and Tool identify the call that produced the result.
	Server, Tool string
	// Kind is how the data is interpreted.
	Kind Kind
	// Size is the length of the data in bytes (set by Put).
	Size int
	// Created and Expires are set by Put.
	Created, Expires time.Time
}

// Identity is "server.tool" (or just the tool when the server is unknown).
func (m Meta) Identity() string {
	if m.Server == "" {
		return m.Tool
	}
	return m.Server + "." + m.Tool
}

type entry struct {
	meta  Meta
	owner any
	data  []byte // memory mode
	file  string // disk mode
}

// Store keeps spilled results, each owned by one client session, bounded
// in bytes (LRU eviction) and in time (TTL). In disk mode the data lives in
// 0600 files under a per-process 0700 directory that Close removes; only
// the metadata stays in memory. Safe for concurrent use.
type Store struct {
	ttl      time.Duration
	maxBytes int64
	dir      string // disk mode when non-empty
	now      func() time.Time

	mu      sync.Mutex
	entries map[string]*list.Element
	lru     *list.List // front: most recently used
	bytes   int64
	closed  bool

	evictions, expirations uint64
}

// StoreStats are the store's counters.
type StoreStats struct {
	Entries     int    `json:"entries"`
	Bytes       int64  `json:"bytes"`
	Evictions   uint64 `json:"evictions"`
	Expirations uint64 `json:"expirations"`
	Disk        bool   `json:"disk"`
}

// NewMemoryStore returns a store that keeps results in memory.
func NewMemoryStore(ttl time.Duration, maxBytes int64) *Store {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Store{ttl: ttl, maxBytes: maxBytes, now: time.Now, entries: make(map[string]*list.Element), lru: list.New()}
}

// processDirPattern matches the per-process directories NewDiskStore
// creates under the parent directory.
var processDirPattern = regexp.MustCompile(`^[0-9]+-[a-z2-7]{8}$`)

// NewDiskStore returns a store that writes results to files in a new
// per-process directory under parent (created 0700). Per-process
// directories under parent left by a process that died more than ttl ago
// are removed.
func NewDiskStore(parent string, ttl time.Duration, maxBytes int64) (*Store, error) {
	s := NewMemoryStore(ttl, maxBytes)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, fmt.Errorf("create spill directory: %w", err)
	}
	pruneStaleDirs(parent, s.ttl, s.now())
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	dir := filepath.Join(parent, fmt.Sprintf("%d-%s", os.Getpid(), strings.ToLower(idEncoding.EncodeToString(b[:]))))
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create spill directory: %w", err)
	}
	s.dir = dir
	return s, nil
}

// pruneStaleDirs removes per-process spill directories not modified for
// longer than ttl (left by a process that did not shut down cleanly).
func pruneStaleDirs(parent string, ttl time.Duration, now time.Time) {
	items, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, it := range items {
		if !it.IsDir() || !processDirPattern.MatchString(it.Name()) {
			continue
		}
		info, err := it.Info()
		if err != nil || now.Sub(info.ModTime()) <= ttl {
			continue
		}
		_ = os.RemoveAll(filepath.Join(parent, it.Name()))
	}
}

// Dir is the per-process directory of a disk store ("" in memory mode).
func (s *Store) Dir() string { return s.dir }

// TTL is how long results stay retrievable.
func (s *Store) TTL() time.Duration { return s.ttl }

// Put stores data for owner and returns its metadata, with a new random
// id. Older results are evicted (least recently used first) to stay under
// the byte cap; a result larger than the cap is refused with ErrTooLarge.
func (s *Store) Put(owner any, meta Meta, data []byte) (Meta, error) {
	size := int64(len(data))
	if size > s.maxBytes {
		return Meta{}, ErrTooLarge
	}
	id, err := newID()
	if err != nil {
		return Meta{}, err
	}
	now := s.now()
	meta.ID = id
	meta.Size = len(data)
	meta.Created = now
	meta.Expires = now.Add(s.ttl)
	e := &entry{meta: meta, owner: owner}
	if s.dir != "" {
		e.file = filepath.Join(s.dir, id)
		if err := writeFile0600(e.file, data); err != nil {
			return Meta{}, err
		}
	} else {
		e.data = append([]byte(nil), data...)
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		removeFile(e)
		return Meta{}, errors.New("spill store closed")
	}
	drop := s.expireLocked(now, nil)
	for s.bytes+size > s.maxBytes {
		back := s.lru.Back()
		if back == nil {
			break
		}
		drop = append(drop, s.removeLocked(back))
		s.evictions++
	}
	s.entries[id] = s.lru.PushFront(e)
	s.bytes += size
	s.mu.Unlock()
	for _, d := range drop {
		removeFile(d)
	}
	return meta, nil
}

// Get returns the data and metadata of id for owner. It fails with
// ErrNotFound when the id is unknown, expired, evicted, or owned by
// another session.
func (s *Store) Get(owner any, id string) ([]byte, Meta, error) {
	s.mu.Lock()
	el, ok := s.entries[id]
	if !ok {
		s.mu.Unlock()
		return nil, Meta{}, ErrNotFound
	}
	e := el.Value.(*entry)
	if e.owner != owner {
		s.mu.Unlock()
		return nil, Meta{}, ErrNotFound
	}
	if !s.now().Before(e.meta.Expires) {
		s.removeLocked(el)
		s.expirations++
		s.mu.Unlock()
		removeFile(e)
		return nil, Meta{}, ErrNotFound
	}
	s.lru.MoveToFront(el)
	meta, data, file := e.meta, e.data, e.file
	s.mu.Unlock()
	if file == "" {
		return data, meta, nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, Meta{}, ErrNotFound
	}
	return b, meta, nil
}

// DropOwner removes every result of owner (its session ended).
func (s *Store) DropOwner(owner any) {
	s.mu.Lock()
	drop := make([]*entry, 0, s.lru.Len())
	for el := s.lru.Front(); el != nil; {
		next := el.Next()
		if el.Value.(*entry).owner == owner {
			drop = append(drop, s.removeLocked(el))
		}
		el = next
	}
	s.mu.Unlock()
	for _, d := range drop {
		removeFile(d)
	}
}

// Sweep removes expired results.
func (s *Store) Sweep() {
	s.mu.Lock()
	drop := s.expireLocked(s.now(), nil)
	s.mu.Unlock()
	for _, d := range drop {
		removeFile(d)
	}
}

// Close removes every result (and, in disk mode, the per-process
// directory). The store refuses new results afterwards.
func (s *Store) Close() {
	s.mu.Lock()
	s.closed = true
	s.entries = make(map[string]*list.Element)
	s.lru.Init()
	s.bytes = 0
	dir := s.dir
	s.mu.Unlock()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// Stats returns the store's counters.
func (s *Store) Stats() StoreStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return StoreStats{Entries: len(s.entries), Bytes: s.bytes, Evictions: s.evictions, Expirations: s.expirations, Disk: s.dir != ""}
}

// expireLocked unlinks every expired entry; their files are removed by
// the caller once the lock is released.
func (s *Store) expireLocked(now time.Time, drop []*entry) []*entry {
	for el := s.lru.Back(); el != nil; {
		prev := el.Prev()
		if !now.Before(el.Value.(*entry).meta.Expires) {
			drop = append(drop, s.removeLocked(el))
			s.expirations++
		}
		el = prev
	}
	return drop
}

func (s *Store) removeLocked(el *list.Element) *entry {
	e := el.Value.(*entry)
	s.lru.Remove(el)
	delete(s.entries, e.meta.ID)
	s.bytes -= int64(e.meta.Size)
	return e
}

func removeFile(e *entry) {
	if e != nil && e.file != "" {
		_ = os.Remove(e.file)
	}
}

// writeFile0600 creates path (which must not exist) with mode 0600.
func writeFile0600(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}
