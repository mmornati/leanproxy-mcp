package registry

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type ServerState string

const (
	StateRunning  ServerState = "running"
	StateStopped  ServerState = "stopped"
	StateError    ServerState = "error"
	StateStarting ServerState = "starting"
)

type ServerConfig struct {
	ID      string
	Name    string
	Command []string
	Env     []string
	Dir     string
	Port    int
}

type ServerHandle struct {
	ID     string
	Config ServerConfig
	PID    int
}

type ServerStatus struct {
	ID         string
	State      ServerState
	Uptime     time.Duration
	MemoryMB   float64
	CPUPercent float64
	ExitCode   int
	Error      string
}

type LifecycleManager interface {
	Start(ctx context.Context, config ServerConfig) (ServerHandle, error)
	Stop(ctx context.Context, id string) error
	Kill(ctx context.Context, id string) error
	Restart(ctx context.Context, id string) error
	Status(ctx context.Context, id string) (ServerStatus, error)
	List(ctx context.Context) ([]ServerStatus, error)
}

type serverEntry struct {
	config    ServerConfig
	handle    ServerHandle
	proc      *exec.Cmd
	startTime time.Time
	status    ServerStatus
	mu        sync.RWMutex
}

type lifecycleManager struct {
	servers map[string]*serverEntry
	logger  *slog.Logger
	mu      sync.RWMutex
}

func NewLifecycleManager(logger *slog.Logger) LifecycleManager {
	m := &lifecycleManager{
		servers: make(map[string]*serverEntry),
		logger:  logger,
	}
	go m.reapProcesses()
	return m
}

func (m *lifecycleManager) Start(ctx context.Context, config ServerConfig) (ServerHandle, error) {
	if config.ID == "" {
		return ServerHandle{}, fmt.Errorf("lifecycle: server ID is required")
	}
	if len(config.Command) == 0 {
		return ServerHandle{}, fmt.Errorf("lifecycle: command is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.servers[config.ID]; exists {
		return ServerHandle{}, fmt.Errorf("lifecycle: server already exists: %s", config.ID)
	}

	cmd := exec.CommandContext(ctx, config.Command[0], config.Command[1:]...)
	if config.Dir != "" {
		cmd.Dir = config.Dir
	}
	if len(config.Env) > 0 {
		cmd.Env = append(os.Environ(), config.Env...)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if err := cmd.Start(); err != nil {
		return ServerHandle{}, fmt.Errorf("lifecycle: start server: %w", err)
	}

	entry := &serverEntry{
		config:    config,
		handle:    ServerHandle{ID: config.ID, Config: config, PID: cmd.Process.Pid},
		proc:     cmd,
		startTime: time.Now(),
		status:    ServerStatus{ID: config.ID, State: StateRunning},
	}

	m.servers[config.ID] = entry

	m.logger.Info("server started", "id", config.ID, "pid", cmd.Process.Pid, "name", config.Name)

	go m.waitForExit(entry)

	return entry.handle, nil
}

func (m *lifecycleManager) waitForExit(entry *serverEntry) {
	proc := entry.proc
	err := proc.Wait()
	entry.mu.Lock()
	defer entry.mu.Unlock()

	if err != nil {
		entry.status.State = StateError
		entry.status.Error = err.Error()
		m.logger.Error("server process error", "id", entry.config.ID, "error", err)
	} else {
		entry.status.State = StateStopped
		m.logger.Info("server process exited", "id", entry.config.ID)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.servers, entry.config.ID)
}

func (m *lifecycleManager) Stop(ctx context.Context, id string) error {
	m.mu.Lock()
	entry, exists := m.servers[id]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("lifecycle: server not found: %s", id)
	}
	m.mu.Unlock()

	if entry.proc.Process != nil {
		if err := entry.proc.Process.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("lifecycle: send SIGTERM: %w", err)
		}
	}

	done := make(chan error, 1)
	go func() {
		werr := entry.proc.Wait()
		done <- werr
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("lifecycle: stop timeout: %w", ctx.Err())
	case err := <-done:
		if err != nil {
			m.logger.Debug("server stop error", "id", id, "error", err)
		}
		return nil
	}
}

func (m *lifecycleManager) Kill(ctx context.Context, id string) error {
	m.mu.Lock()
	entry, exists := m.servers[id]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("lifecycle: server not found: %s", id)
	}
	m.mu.Unlock()

	if entry.proc.Process != nil {
		if err := entry.proc.Process.Signal(syscall.SIGKILL); err != nil {
			return fmt.Errorf("lifecycle: send SIGKILL: %w", err)
		}
	}

	m.logger.Warn("server killed", "id", id, "pid", entry.handle.PID)

	m.mu.Lock()
	delete(m.servers, id)
	m.mu.Unlock()

	return nil
}

func (m *lifecycleManager) Restart(ctx context.Context, id string) error {
	m.mu.RLock()
	entry, exists := m.servers[id]
	if !exists {
		m.mu.RUnlock()
		return fmt.Errorf("lifecycle: server not found: %s", id)
	}
	config := entry.config
	m.mu.RUnlock()

	if err := m.Stop(ctx, id); err != nil {
		m.logger.Warn("restart stop failed", "id", id, "error", err)
	}

	time.Sleep(100 * time.Millisecond)

	_, err := m.Start(ctx, config)
	if err != nil {
		return fmt.Errorf("lifecycle: restart: %w", err)
	}

	m.logger.Info("server restarted", "id", id)
	return nil
}

func (m *lifecycleManager) Status(ctx context.Context, id string) (ServerStatus, error) {
	m.mu.RLock()
	entry, exists := m.servers[id]
	if !exists {
		m.mu.RUnlock()
		return ServerStatus{}, fmt.Errorf("lifecycle: server not found: %s", id)
	}
	m.mu.RUnlock()

	entry.mu.RLock()
	status := entry.status
	uptime := time.Since(entry.startTime)
	entry.mu.RUnlock()

	status.Uptime = uptime

	if entry.proc.Process != nil {
		pid := entry.proc.Process.Pid
		if memMB, cpu, err := getProcessStats(pid); err == nil {
			status.MemoryMB = memMB
			status.CPUPercent = cpu
		}
	}

	return status, nil
}

func (m *lifecycleManager) List(ctx context.Context) ([]ServerStatus, error) {
	m.mu.RLock()
	ids := make([]string, 0, len(m.servers))
	for id := range m.servers {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	result := make([]ServerStatus, 0, len(ids))
	for _, id := range ids {
		status, err := m.Status(ctx, id)
		if err != nil {
			continue
		}
		result = append(result, status)
	}

	return result, nil
}

func (m *lifecycleManager) reapProcesses() {
	for range time.Tick(5 * time.Second) {
		m.mu.Lock()
		for id, entry := range m.servers {
			entry.mu.RLock()
			state := entry.status.State
			entry.mu.RUnlock()
			if state == StateStopped || state == StateError {
				delete(m.servers, id)
				m.logger.Debug("reaped dead server", "id", id)
			}
		}
		m.mu.Unlock()
	}
}

func getProcessStats(pid int) (float64, float64, error) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, 0, err
	}

	rusage := &syscall.Rusage{}
	err = syscall.Getrusage(syscall.RUSAGE_SELF, rusage)
	if err != nil {
		return 0, 0, err
	}

	maxRSS := float64(proc.Pid) / 1024.0

	return maxRSS, 0, nil
}