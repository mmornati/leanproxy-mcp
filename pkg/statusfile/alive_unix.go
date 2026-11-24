//go:build !windows

package statusfile

import (
	"errors"
	"syscall"
)

// processAlive reports whether a process with this pid exists. EPERM means
// it exists but belongs to someone else.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
