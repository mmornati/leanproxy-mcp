//go:build windows

package pool

import (
	"os"
	"os/exec"
)

// configureProcAttr is a no-op on Windows: there are no POSIX process
// groups, so stop only reaches the direct child (a Job Object would be
// needed to reach grandchildren).
func configureProcAttr(_ *exec.Cmd) {}

// processGroupID returns 0: process groups are not used on Windows.
func processGroupID(_ *os.Process) int { return 0 }

// processAlive always reports true on Windows, where os.Process.Signal only
// supports Kill; waitForExit detects a child that died right after spawn.
func processAlive(_ *os.Process) bool { return true }

// terminateProcessTree kills the direct child: Windows has no SIGTERM to
// deliver to a console-less child process.
func terminateProcessTree(proc *os.Process, _ int) error {
	if proc == nil {
		return nil
	}
	return proc.Kill()
}

// killProcessTree kills the direct child.
func killProcessTree(proc *os.Process, _ int) {
	if proc != nil {
		_ = proc.Kill()
	}
}

// processGroupAlive always reports false on Windows (no process groups).
func processGroupAlive(_ int) bool { return false }
