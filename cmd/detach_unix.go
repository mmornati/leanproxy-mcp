//go:build !windows

package cmd

import "syscall"

// detachedProcAttr starts a child in a session of its own: it does not
// get the signals sent to our process group and survives our exit.
func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
