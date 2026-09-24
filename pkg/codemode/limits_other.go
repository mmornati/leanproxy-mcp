//go:build codemode && !linux

package codemode

import (
	"os"
	"syscall"
)

// HardLimits reports whether this platform enforces the kernel-level
// limits on the sandbox process. The prototype only sets them on Linux:
// elsewhere the memory and CPU limits are the worker's own watchdogs
// (which cannot interrupt native code) plus the proxy's wall-clock kill.
const HardLimits = false

func applyOSLimits(Limits) error { return nil }

func notifyCPULimit(chan<- os.Signal) {}

func sysProcAttr() *syscall.SysProcAttr { return nil }

func workerExecutable() (string, error) { return os.Executable() }
