//go:build windows

package statusfile

import "os"

// processAlive reports whether a process with this pid exists.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
