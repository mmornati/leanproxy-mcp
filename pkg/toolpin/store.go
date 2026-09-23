package toolpin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// FileVersion is the pin file format version.
const FileVersion = 1

// File is the pin file (pins.json).
type File struct {
	Version int                    `json:"version"`
	Servers map[string]*ServerPins `json:"servers"`
}

// ServerInfo is an upstream's serverInfo (name and version).
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ServerPins is what is pinned for one configured server.
type ServerPins struct {
	// ServerInfo is the approved serverInfo.
	ServerInfo *ServerInfo `json:"server_info,omitempty"`
	// PendingServerInfo is set when the server now reports another name
	// (server_identity_changed) until it is approved.
	PendingServerInfo *ServerInfo `json:"pending_server_info,omitempty"`
	FirstSeen         time.Time   `json:"first_seen"`
	UpdatedAt         time.Time   `json:"updated_at"`
	// Tools maps the upstream tool name to its pin.
	Tools map[string]*ToolPin `json:"tools"`
}

// Approval values of ToolPin.Approval.
const (
	// ApprovalTOFU: approved automatically the first time the server was
	// seen (trust on first use).
	ApprovalTOFU = "tofu"
	// ApprovalManual: approved with `tools pins approve`.
	ApprovalManual = "approved"
)

// ToolPin is the pin of one tool: the approved definition and, when the
// upstream now serves something else, the pending one.
type ToolPin struct {
	// Hash is the approved definition's hash ("" when the tool was never
	// approved).
	Hash string `json:"hash,omitempty"`
	// Definition is the approved definition, canonical JSON (see
	// Canonical), kept for diffs.
	Definition json.RawMessage `json:"definition,omitempty"`
	// Findings are the scanner findings of the approved definition.
	Findings   []Finding  `json:"findings,omitempty"`
	FirstSeen  time.Time  `json:"first_seen"`
	Approval   string     `json:"approval,omitempty"`
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
	// Pending is the definition the upstream serves now when it differs
	// from the approved one (or the tool was never approved).
	Pending *Observed `json:"pending,omitempty"`
	// RemovedAt is set while the upstream no longer lists the tool.
	RemovedAt *time.Time `json:"removed_at,omitempty"`
}

// Observed is a definition seen from the upstream but not approved.
type Observed struct {
	Hash       string          `json:"hash"`
	Definition json.RawMessage `json:"definition"`
	Findings   []Finding       `json:"findings,omitempty"`
	SeenAt     time.Time       `json:"seen_at"`
}

// Status is the pinning state of a tool.
type Status string

const (
	// StatusUnknown: the tool is not pinned (pinning off, or never seen).
	StatusUnknown Status = ""
	// StatusApproved: the upstream serves the approved definition.
	StatusApproved Status = "approved"
	// StatusChanged: the definition changed since it was approved.
	StatusChanged Status = "changed"
	// StatusNew: the tool was never approved (added after the server was
	// first pinned, or flagged by the scanner on first use).
	StatusNew Status = "new"
	// StatusRemoved: the upstream no longer lists the tool.
	StatusRemoved Status = "removed"
)

// Status returns the tool's pinning state.
func (t *ToolPin) Status() Status {
	switch {
	case t == nil:
		return StatusUnknown
	case t.RemovedAt != nil:
		return StatusRemoved
	case t.Pending != nil && t.Hash == "":
		return StatusNew
	case t.Pending != nil:
		return StatusChanged
	default:
		return StatusApproved
	}
}

// NeedsApproval reports whether the tool is pending (new or changed).
func (t *ToolPin) NeedsApproval() bool {
	s := t.Status()
	return s == StatusNew || s == StatusChanged
}

// SortedToolNames returns the pinned tool names in order.
func (sp *ServerPins) SortedToolNames() []string {
	names := make([]string, 0, len(sp.Tools))
	for n := range sp.Tools {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// SortedServerNames returns the pinned server names in order.
func (f *File) SortedServerNames() []string {
	names := make([]string, 0, len(f.Servers))
	for n := range f.Servers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func newFile() *File {
	return &File{Version: FileVersion, Servers: map[string]*ServerPins{}}
}

// Store reads and writes the pin file. Writes are atomic (temporary file
// in the same directory, fsync, rename) with mode 0600 in a 0700 directory.
// Every Update re-reads the file first, so changes made by another process
// (the CLI approving a tool, another proxy) are merged rather than
// overwritten; the remaining window between that read and the rename is
// last-writer-wins.
type Store struct {
	path string

	mu      sync.Mutex // serializes Update
	current atomic.Pointer[File]
	stamp   atomic.Pointer[fileStamp]
	existed atomic.Bool

	// recheck bounds how often Current looks at the file on disk.
	recheck   time.Duration
	lastCheck atomic.Int64
}

type fileStamp struct {
	mod  time.Time
	size int64
}

// DefaultRecheckInterval is how often Current checks the file for changes
// made by other processes.
const DefaultRecheckInterval = time.Second

// OpenStore loads path (a missing file is an empty pin set).
func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, recheck: DefaultRecheckInterval}
	f, stamp, err := s.read()
	if err != nil {
		return nil, err
	}
	s.current.Store(f)
	s.stamp.Store(stamp)
	s.lastCheck.Store(time.Now().UnixNano())
	return s, nil
}

// SetRecheckInterval changes how often Current re-reads a file changed on
// disk (0: on every call).
func (s *Store) SetRecheckInterval(d time.Duration) { s.recheck = d }

// Path is the pin file path.
func (s *Store) Path() string { return s.path }

// Existed reports whether the file existed when last read.
func (s *Store) Existed() bool { return s.existed.Load() }

// Current returns the pin set. The returned File is shared and must not be
// modified. At most once per recheck interval it checks whether the file
// changed on disk (another process wrote it) and reloads it.
func (s *Store) Current() *File {
	now := time.Now().UnixNano()
	last := s.lastCheck.Load()
	if now-last >= int64(s.recheck) && s.lastCheck.CompareAndSwap(last, now) {
		s.reloadIfChanged()
	}
	return s.current.Load()
}

func (s *Store) reloadIfChanged() {
	st := statFile(s.path)
	if old := s.stamp.Load(); old != nil && old.mod.Equal(st.mod) && old.size == st.size {
		return // unchanged (or still missing)
	}
	f, stamp, err := s.read()
	if err != nil {
		slog.Warn("toolpin: cannot reload the pin file, keeping the previous pins", "path", s.path, "error", err)
		return
	}
	s.current.Store(f)
	s.stamp.Store(stamp)
}

// Reload re-reads the file now.
func (s *Store) Reload() error {
	f, stamp, err := s.read()
	if err != nil {
		return err
	}
	s.current.Store(f)
	s.stamp.Store(stamp)
	return nil
}

// Update applies fn to a fresh copy of the pin file (re-read from disk)
// and writes the result atomically. When fn returns an error nothing is
// written. When the write fails the in-memory pins still take the new
// state (enforcement must not fall back to the old pins) and the error is
// returned.
func (s *Store) Update(fn func(f *File) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, _, err := s.read()
	if err != nil {
		// A corrupt file must not be silently replaced: keep it for the
		// operator and work on the in-memory copy.
		slog.Warn("toolpin: cannot read the pin file, updating the in-memory pins only", "path", s.path, "error", err)
		f = cloneFile(s.current.Load())
		if ferr := fn(f); ferr != nil {
			return ferr
		}
		s.current.Store(f)
		return err
	}
	if err := fn(f); err != nil {
		return err
	}
	s.current.Store(f)
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("toolpin: encode pins: %w", err)
	}
	data = append(data, '\n')
	if err := writeFileAtomic(s.path, data); err != nil {
		return err
	}
	s.existed.Store(true)
	s.stamp.Store(statFile(s.path))
	return nil
}

// read loads the file; a missing file is an empty pin set.
func (s *Store) read() (*File, *fileStamp, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		s.existed.Store(false)
		return newFile(), &fileStamp{size: -1}, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("toolpin: read %s: %w", s.path, err)
	}
	s.existed.Store(true)
	checkPermissions(s.path)
	f := newFile()
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, f); err != nil {
			return nil, nil, fmt.Errorf("toolpin: parse %s: %w", s.path, err)
		}
	}
	if f.Servers == nil {
		f.Servers = map[string]*ServerPins{}
	}
	for _, sp := range f.Servers {
		if sp != nil && sp.Tools == nil {
			sp.Tools = map[string]*ToolPin{}
		}
	}
	for name, sp := range f.Servers {
		if sp == nil {
			delete(f.Servers, name)
		}
	}
	if f.Version > FileVersion {
		return nil, nil, fmt.Errorf("toolpin: %s has format version %d, this build reads up to %d", s.path, f.Version, FileVersion)
	}
	f.Version = FileVersion
	return f, statFile(s.path), nil
}

func statFile(path string) *fileStamp {
	fi, err := os.Stat(path)
	if err != nil {
		return &fileStamp{size: -1}
	}
	return &fileStamp{mod: fi.ModTime(), size: fi.Size()}
}

// checkPermissions tightens a pin file readable or writable by others
// (Unix only).
func checkPermissions(path string) {
	if runtime.GOOS == "windows" {
		return
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm()&0o077 == 0 {
		return
	}
	slog.Warn("toolpin: pin file is accessible by other users, restricting it to 0600", "path", path, "mode", fi.Mode().Perm().String())
	if err := os.Chmod(path, 0o600); err != nil {
		slog.Warn("toolpin: cannot restrict the pin file permissions", "path", path, "error", err)
	}
}

// writeFileAtomic writes data to path through a temporary file in the same
// directory (created 0600), fsynced and renamed over path.
func writeFileAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("toolpin: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".pins-*.tmp")
	if err != nil {
		return fmt.Errorf("toolpin: create temporary pin file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("toolpin: write pins: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("toolpin: sync pins: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("toolpin: close pins: %w", err)
	}
	if err = os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("toolpin: chmod pins: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("toolpin: replace %s: %w", path, err)
	}
	if d, derr := os.Open(dir); derr == nil { // #nosec G304 -- dir of the configured pin file
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// cloneFile deep-copies f through JSON.
func cloneFile(f *File) *File {
	out := newFile()
	if f == nil {
		return out
	}
	data, err := json.Marshal(f)
	if err == nil {
		_ = json.Unmarshal(data, out)
	}
	if out.Servers == nil {
		out.Servers = map[string]*ServerPins{}
	}
	return out
}
