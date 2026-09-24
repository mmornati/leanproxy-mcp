//go:build codemode && linux

package codemode

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

// HardLimits reports whether this platform enforces the kernel-level
// limits (RLIMIT_CPU, RLIMIT_AS, ...) on the sandbox process.
const HardLimits = true

// applyOSLimits sets the sandbox process's own resource limits before any
// program code runs:
//
//   - RLIMIT_AS: the process's address space, set to what it already
//     maps plus twice the memory limit. The Go runtime reserves address
//     space before it uses it, so a huge allocation fails at the
//     reservation and the runtime aborts the process: an allocation in
//     native code (which the heap watchdog cannot interrupt) cannot take
//     the machine's memory. (RLIMIT_DATA does not work for Go: the kernel
//     checks it when address space grows, and the Go heap grows by
//     remapping space it reserved earlier.)
//   - RLIMIT_CPU: SIGXCPU at the soft limit (the worker interrupts the
//     program), SIGKILL from the kernel one second later.
//   - RLIMIT_FSIZE 0 and RLIMIT_CORE 0: no file can be written, no core
//     dump left behind.
//   - RLIMIT_NOFILE: no file descriptors beyond the few already open.
func applyOSLimits(l Limits) error {
	cpu := uint64(l.CPUTime.Seconds())
	if cpu < 1 {
		cpu = 1
	}
	// The heap watchdog interrupts at MaxMemoryMB of live objects; this
	// is the hard stop above it, with room for the heap's garbage and the
	// runtime's own reservations.
	mapped, err := mappedBytes()
	if err != nil {
		return err
	}
	as := mapped + 2*l.memoryBytes() + asOverhead
	for _, r := range []struct {
		name     string
		resource int
		cur, max uint64
	}{
		{"RLIMIT_AS", syscall.RLIMIT_AS, as, as},
		{"RLIMIT_CPU", syscall.RLIMIT_CPU, cpu, cpu + 1},
		{"RLIMIT_FSIZE", syscall.RLIMIT_FSIZE, 0, 0},
		{"RLIMIT_CORE", syscall.RLIMIT_CORE, 0, 0},
		{"RLIMIT_NOFILE", syscall.RLIMIT_NOFILE, maxOpenFiles, maxOpenFiles},
	} {
		if err := syscall.Setrlimit(r.resource, &syscall.Rlimit{Cur: r.cur, Max: r.max}); err != nil {
			return fmt.Errorf("%s: %w", r.name, err)
		}
	}
	return nil
}

// asOverhead is RLIMIT_AS's allowance, on top of the heap, for the Go
// runtime's arena and metadata reservations and new thread stacks.
const asOverhead = 256 << 20

// mappedBytes is the process's current address space (VmSize).
func mappedBytes() (uint64, error) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmSize:"); ok {
			kb, err := strconv.ParseUint(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "kB")), 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse VmSize %q: %w", rest, err)
			}
			return kb << 10, nil
		}
	}
	return 0, fmt.Errorf("no VmSize in /proc/self/status")
}

// maxOpenFiles is RLIMIT_NOFILE: stdin, stdout, stderr and the Go
// runtime's own descriptors.
const maxOpenFiles = 16

// notifyCPULimit delivers the SIGXCPU of the CPU soft limit to c.
func notifyCPULimit(c chan<- os.Signal) { signal.Notify(c, syscall.SIGXCPU) }

// sysProcAttr makes the sandbox die with the proxy (no orphan survives a
// crashed proxy) and puts it in its own process group.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL, Setpgid: true}
}

// workerExecutable is the proxy's own binary. /proc/self/exe is the
// running image even when the file on disk was replaced (an upgrade).
func workerExecutable() (string, error) { return "/proc/self/exe", nil }
