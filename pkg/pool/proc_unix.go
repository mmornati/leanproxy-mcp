//go:build !windows

package pool

import (
	"os"
	"os/exec"
	"syscall"
)

// configureProcAttr puts the child in its own process group so that stop
// can signal the whole tree (npx/uvx/docker wrappers and whatever they
// spawned), not just the direct child.
func configureProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// processGroupID returns the process group of a child started with
// configureProcAttr: with Setpgid the child leads a group whose ID is its
// PID.
func processGroupID(proc *os.Process) int {
	if proc == nil {
		return 0
	}
	return proc.Pid
}

// processAlive reports whether proc is still running (signal 0 probe).
func processAlive(proc *os.Process) bool {
	return proc.Signal(syscall.Signal(0)) == nil
}

// terminateProcessTree asks the child's whole process group to exit
// (SIGTERM to -pgid), falling back to the direct child.
func terminateProcessTree(proc *os.Process, pgid int) error {
	if pgid > 0 {
		if err := syscall.Kill(-pgid, syscall.SIGTERM); err == nil {
			return nil
		}
	}
	if proc == nil {
		return nil
	}
	return proc.Signal(syscall.SIGTERM)
}

// killProcessTree force-kills the child's whole process group (SIGKILL to
// -pgid) and the direct child.
func killProcessTree(proc *os.Process, pgid int) {
	if pgid > 0 {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
	if proc != nil {
		_ = proc.Kill()
	}
}

// processGroupAlive reports whether any process is left in group pgid
// (e.g. a grandchild that outlived the direct child).
func processGroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	return syscall.Kill(-pgid, 0) == nil
}
