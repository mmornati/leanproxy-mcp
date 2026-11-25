package logx

import (
	"os"
	"sync"
)

// DefaultMaxLogBytes is the size at which a log file is rotated.
const DefaultMaxLogBytes = 20 << 20

// rotateCheckBytes is how many bytes are written between two size checks.
const rotateCheckBytes = 256 << 10

// RotatingFile is an append-only log file that never grows past about
// maxBytes: when it does, it is renamed to "<path>.1" (replacing the
// previous one) and a new file is started. So a log takes at most about
// twice maxBytes on disk, however long the process runs.
//
// Several processes may log to the same path (one `server run --stdio`
// per MCP client): each notices when another rotated the file and
// follows it to the new one.
type RotatingFile struct {
	path     string
	maxBytes int64

	mu         sync.Mutex
	f          *os.File
	sinceCheck int64
}

// OpenRotating opens (creating if needed, mode 0600) the log file at path.
// A non-positive maxBytes means DefaultMaxLogBytes.
func OpenRotating(path string, maxBytes int64) (*RotatingFile, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxLogBytes
	}
	r := &RotatingFile{path: path, maxBytes: maxBytes}
	if err := r.open(); err != nil {
		return nil, err
	}
	// Rotate at once a file that is already too big (a log left by an
	// earlier version that never rotated).
	r.check()
	return r, nil
}

func (r *RotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- log file path from a CLI flag
	if err != nil {
		return err
	}
	if r.f != nil {
		_ = r.f.Close()
	}
	r.f = f
	return nil
}

// check rotates the file when it is too big, or reopens the path when
// another process rotated it. Errors are ignored: logging keeps going to
// the file already open.
func (r *RotatingFile) check() {
	r.sinceCheck = 0
	mine, err := r.f.Stat()
	if err != nil {
		return
	}
	onDisk, err := os.Stat(r.path)
	if err != nil || !os.SameFile(mine, onDisk) {
		// Rotated (or removed) by someone else: follow the path.
		_ = r.open()
		return
	}
	if mine.Size() < r.maxBytes {
		return
	}
	if err := os.Rename(r.path, r.path+".1"); err != nil {
		return
	}
	_ = r.open()
}

// Write appends p to the log file.
func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sinceCheck >= rotateCheckBytes {
		r.check()
	}
	n, err := r.f.Write(p)
	r.sinceCheck += int64(n)
	return n, err
}

// Close closes the file.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Close()
}
