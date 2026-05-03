package proxy

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

type ProcessHealth struct {
	PID         int
	MemoryMB    int64
	CPUPercent  float64
	Status      string
	IsAlive     bool
}

type ProcessHealthChecker struct {
}

func NewProcessHealthChecker() *ProcessHealthChecker {
	return &ProcessHealthChecker{}
}

func (phc *ProcessHealthChecker) CheckProcessHealth(pid int) ProcessHealth {
	health := ProcessHealth{
		PID:     pid,
		Status:  "unknown",
		IsAlive: false,
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		health.Status = fmt.Sprintf("error: %v", err)
		return health
	}

	if process.Pid == 0 {
		health.Status = "zombie"
		return health
	}

	if runtime.GOOS == "darwin" {
		return phc.checkProcessHealthDarwin(pid, health)
	}

	return phc.checkProcessHealthLinux(pid, health)
}

func (phc *ProcessHealthChecker) checkProcessHealthDarwin(pid int, health ProcessHealth) ProcessHealth {
	exists := processExists(pid)
	if !exists {
		health.Status = "process not found"
		health.IsAlive = false
		return health
	}

	memKB, err := phc.getDarwinMemory(pid)
	if err != nil {
		health.MemoryMB = 0
		health.IsAlive = true
		health.Status = "running"
	} else {
		health.MemoryMB = memKB / 1024
		health.IsAlive = true
		health.Status = "running"
	}

	return health
}

func (phc *ProcessHealthChecker) getDarwinMemory(pid int) (int64, error) {
	cmd := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid))
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	trimmed := strings.TrimSpace(string(output))
	kb, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, err
	}

	return kb, nil
}

func (phc *ProcessHealthChecker) checkProcessHealthLinux(pid int, health ProcessHealth) ProcessHealth {
	memKB, err := phc.getLinuxMemory(pid)
	if err == nil {
		health.MemoryMB = memKB / 1024
		health.IsAlive = true
		health.Status = "running"
	} else {
		health.Status = fmt.Sprintf("cannot read memory: %v", err)
		health.IsAlive = processExists(pid)
	}

	return health
}

func (phc *ProcessHealthChecker) getLinuxMemory(pid int) (int64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}

	var memKB int64

	lines := splitLines(string(data))
	for _, line := range lines {
		if len(line) > 5 && line[:5] == "VmRSS" {
			parts := splitWords(line)
			if len(parts) >= 2 {
				memKB, err = strconv.ParseInt(parts[1], 10, 64)
				if err != nil {
					return 0, err
				}
				return memKB, nil
			}
		}
	}

	return 0, fmt.Errorf("VmRSS not found in status file")
}

func processExists(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}

	if err == os.ErrProcessDone {
		return false
	}

	parseErr, ok := err.(*os.PathError)
	if !ok {
		return false
	}

	errno, ok := parseErr.Err.(syscall.Errno)
	if !ok {
		return false
	}

	return errno != syscall.ESRCH && errno != syscall.EPERM
}



func splitLines(s string) []string {
	result := make([]string, 0)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if start < i {
				result = append(result, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

func splitWords(s string) []string {
	result := make([]string, 0)
	start := 0
	inSpace := false
	for i := 0; i <= len(s); i++ {
		isSpace := i >= len(s) || s[i] == ' ' || s[i] == '\t'
		if !isSpace && inSpace {
			start = i
		}
		if isSpace && !inSpace && start < i {
			result = append(result, s[start:i])
		}
		inSpace = isSpace
	}
	return result
}