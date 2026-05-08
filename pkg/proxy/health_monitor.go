package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type ServerHealthStatus string

const (
	StatusRunning      ServerHealthStatus = "running"
	StatusError        ServerHealthStatus = "error"
	StatusStopped      ServerHealthStatus = "stopped"
	StatusStarting     ServerHealthStatus = "starting"
	StatusUnresponsive ServerHealthStatus = "unresponsive"
)

type ServerStatus struct {
	Name             string             `json:"name"`
	Status           ServerHealthStatus `json:"status"`
	Uptime           time.Duration      `json:"uptime"`
	LastResponseTime time.Time          `json:"last_response_time"`
	LastError        string             `json:"last_error,omitempty"`
	RestartCount     int                `json:"restart_count"`
	RequestCount     int64              `json:"request_count"`
	ErrorRate        float64            `json:"error_rate"`
	MemoryMB         int64              `json:"memory_mb,omitempty"`
	CPUPercent       float64            `json:"cpu_percent,omitempty"`
}

type ServerStatusList struct {
	Timestamp time.Time      `json:"timestamp"`
	Servers   []ServerStatus `json:"servers"`
}

type HealthConfig struct {
	CheckInterval      time.Duration
	ResponseTimeout    time.Duration
	MaxRestartAttempts int
	RestartBackoff     time.Duration
}

func DefaultHealthConfig() *HealthConfig {
	return &HealthConfig{
		CheckInterval:      1 * time.Second,
		ResponseTimeout:    30 * time.Second,
		MaxRestartAttempts: 3,
		RestartBackoff:     5 * time.Second,
	}
}

type ManagedServer interface {
	Name() string
	PID() int
	IsRunning() bool
	StartTime() time.Time
	LastResponseTime() time.Time
	RequestCount() int64
	ErrorCount() int64
	OnCrash(callback func())
	OnRestart(callback func())
}

type HealthMonitor struct {
	config         *HealthConfig
	logger         *slog.Logger
	servers        map[string]ManagedServer
	mu             sync.RWMutex
	stopChan       chan struct{}
	wg             sync.WaitGroup
	statusChan     chan ServerStatusList
	processChecker *ProcessHealthChecker
	ticker         *time.Ticker
}

func NewHealthMonitor(config *HealthConfig, logger *slog.Logger) *HealthMonitor {
	if config == nil {
		config = DefaultHealthConfig()
	}
	return &HealthMonitor{
		config:         config,
		logger:         logger,
		servers:        make(map[string]ManagedServer),
		stopChan:       make(chan struct{}),
		statusChan:     make(chan ServerStatusList, 1),
		processChecker: NewProcessHealthChecker(),
	}
}

func (hm *HealthMonitor) RegisterServer(server ManagedServer) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	server.OnCrash(func() {
		if hm.logger != nil {
			hm.logger.Warn("server crash detected",
				"name", server.Name(),
				"pid", server.PID())
		}
	})
	server.OnRestart(func() {
		if hm.logger != nil {
			hm.logger.Info("server restarted",
				"name", server.Name(),
				"pid", server.PID())
		}
	})

	hm.servers[server.Name()] = server
}

func (hm *HealthMonitor) UnregisterServer(name string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	delete(hm.servers, name)
}

func (hm *HealthMonitor) Start() error {
	hm.wg.Add(1)
	go hm.monitorLoop()
	if hm.logger != nil {
		hm.logger.Info("health monitor started",
			"check_interval", hm.config.CheckInterval)
	}
	return nil
}

func (hm *HealthMonitor) Stop() {
	close(hm.stopChan)
	hm.wg.Wait()
	if hm.logger != nil {
		hm.logger.Info("health monitor stopped")
	}
}

func (hm *HealthMonitor) monitorLoop() {
	defer hm.wg.Done()

	ticker := time.NewTicker(hm.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-hm.stopChan:
			return
		case <-ticker.C:
			statusList := hm.checkAllServers()
			select {
			case hm.statusChan <- statusList:
			default:
			}
		}
	}
}

func (hm *HealthMonitor) checkAllServers() ServerStatusList {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	now := time.Now()
	servers := make([]ServerStatus, 0, len(hm.servers))

	for _, server := range hm.servers {
		status := hm.checkServer(server, now)
		servers = append(servers, status)
	}

	return ServerStatusList{
		Timestamp: now,
		Servers:   servers,
	}
}

func (hm *HealthMonitor) checkServer(server ManagedServer, now time.Time) ServerStatus {
	status := ServerStatus{
		Name:             server.Name(),
		Uptime:           now.Sub(server.StartTime()),
		LastResponseTime: server.LastResponseTime(),
		RequestCount:     server.RequestCount(),
	}

	if !server.IsRunning() {
		status.Status = StatusStopped
		return status
	}

	processHealth := hm.processChecker.CheckProcessHealth(server.PID())

	if !processHealth.IsAlive {
		if processHealth.Status != "" && processHealth.Status != "zombie" && processHealth.Status != "process not found" {
			status.Status = StatusError
			status.LastError = processHealth.Status
			if hm.logger != nil {
				hm.logger.Warn("server process error",
					"name", server.Name(),
					"pid", server.PID(),
					"status", processHealth.Status)
			}
			return status
		}
	}

	status.MemoryMB = processHealth.MemoryMB
	status.CPUPercent = processHealth.CPUPercent

	if server.RequestCount() > 0 && server.ErrorCount() > 0 {
		status.ErrorRate = float64(server.ErrorCount()) / float64(server.RequestCount()) * 100
	}

	timeSinceLastResponse := now.Sub(server.LastResponseTime())
	if timeSinceLastResponse > hm.config.ResponseTimeout {
		status.Status = StatusUnresponsive
		status.LastError = fmt.Sprintf("no response for %v", timeSinceLastResponse)
		if hm.logger != nil {
			hm.logger.Warn("server unresponsive",
				"name", server.Name(),
				"last_response", server.LastResponseTime())
		}
		return status
	}

	status.Status = StatusRunning
	return status
}

func (hm *HealthMonitor) GetStatus() ServerStatusList {
	return hm.checkAllServers()
}

func (hm *HealthMonitor) WatchStatus(ctx context.Context, interval time.Duration) <-chan ServerStatusList {
	outputChan := make(chan ServerStatusList)

	go func() {
		defer close(outputChan)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-hm.stopChan:
				return
			case <-ticker.C:
				statusList := hm.checkAllServers()
				select {
				case outputChan <- statusList:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return outputChan
}
