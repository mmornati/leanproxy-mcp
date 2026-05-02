package pool

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

type HealthStatus string

const (
	HealthUnknown   HealthStatus = "unknown"
	HealthHealthy  HealthStatus = "healthy"
	HealthDegraded HealthStatus = "degraded"
	HealthUnhealthy HealthStatus = "unhealthy"
	HealthError    HealthStatus = "error"
)

type HealthCheckResult struct {
	ServerName    string
	Status        HealthStatus
	LatencyMs     float64
	Error         string
	CheckedAt     time.Time
}

type HealthChecker struct {
	pool    *StdioPool
	logger  *slog.Logger
	checks  map[string]*healthCheck
	mu      sync.RWMutex
	stopCh  chan struct{}
}

type healthCheck struct {
	serverName    string
	lastCheck     time.Time
	lastStatus    HealthStatus
	lastLatencyMs float64
	lastError     string
	consecutiveFailures int
	mu             sync.Mutex
}

func NewHealthChecker(pool *StdioPool, logger *slog.Logger) *HealthChecker {
	if logger == nil {
		logger = slog.Default()
	}

	return &HealthChecker{
		pool:   pool,
		logger: logger,
		checks: make(map[string]*healthCheck),
		stopCh: make(chan struct{}),
	}
}

func (hc *HealthChecker) Start(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hc.checkAllServers(ctx)
		case <-ctx.Done():
			return
		case <-hc.stopCh:
			return
		}
	}
}

func (hc *HealthChecker) Stop() {
	close(hc.stopCh)
}

func (hc *HealthChecker) checkAllServers(ctx context.Context) {
	servers := hc.pool.ListServers()

	for _, name := range servers {
		result := hc.CheckServer(ctx, name)

		hc.mu.Lock()
		check, exists := hc.checks[name]
		if !exists {
			check = &healthCheck{serverName: name}
			hc.checks[name] = check
		}
		hc.mu.Unlock()

		check.mu.Lock()
		check.lastCheck = time.Now()
		check.lastStatus = result.Status
		check.lastLatencyMs = result.LatencyMs
		check.lastError = result.Error

		if result.Status == HealthUnhealthy || result.Status == HealthError {
			check.consecutiveFailures++
		} else {
			check.consecutiveFailures = 0
		}
		check.mu.Unlock()

		if check.consecutiveFailures >= 3 {
			hc.logger.Warn("server marked unhealthy after consecutive failures",
				"name", name, "failures", check.consecutiveFailures)
		}
	}
}

func (hc *HealthChecker) CheckServer(ctx context.Context, name string) HealthCheckResult {
	result := HealthCheckResult{
		ServerName: name,
		CheckedAt:  time.Now(),
	}

	_, err := hc.pool.GetServer(name)
	if err != nil {
		result.Status = HealthUnhealthy
		result.Error = err.Error()
		return result
	}

	state, _ := hc.pool.GetServerState(name)
	stats, _ := hc.pool.GetServerStats(name)

	switch state {
	case StateIdle, StateRunning, StateBusy:
		result.Status = HealthHealthy
	case StateError:
		result.Status = HealthUnhealthy
		result.Error = "server in error state"
	case StateStopping:
		result.Status = HealthDegraded
		result.Error = "server is stopping"
	default:
		result.Status = HealthUnknown
	}

	if stats.ErrorCount > 0 {
		errorRate := float64(stats.ErrorCount) / float64(stats.RequestCount+stats.ErrorCount)
		if errorRate > 0.1 {
			result.Status = HealthDegraded
			result.Error = "high error rate"
		}
	}

	result.LatencyMs = stats.AvgLatencyMs

	return result
}

func (hc *HealthChecker) GetServerHealth(name string) (HealthStatus, error) {
	hc.mu.RLock()
	check, exists := hc.checks[name]
	hc.mu.RUnlock()

	if !exists {
		return HealthUnknown, nil
	}

	check.mu.Lock()
	status := check.lastStatus
	check.mu.Unlock()

	return status, nil
}

func (hc *HealthChecker) GetAllHealth() map[string]HealthCheckResult {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	results := make(map[string]HealthCheckResult)

	for name, check := range hc.checks {
		check.mu.Lock()
		result := HealthCheckResult{
			ServerName:    name,
			Status:        check.lastStatus,
			LatencyMs:     check.lastLatencyMs,
			Error:         check.lastError,
			CheckedAt:     check.lastCheck,
		}
		check.mu.Unlock()
		results[name] = result
	}

	return results
}

type PingRequest struct {
	ID      interface{} `json:"id"`
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
}

type PingResponse struct {
	ID      interface{} `json:"id"`
	JSONRPC string      `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

func (hc *HealthChecker) performPingCheck(ctx context.Context, server *StdioServerV2) (bool, float64) {
	req := Request{
		Method:  "ping",
		Params:  nil,
		ID:      time.Now().UnixNano(),
		Timeout: 5 * time.Second,
	}

	start := time.Now()

	done := make(chan *Response, 1)
	req.ResultCh = done

	err := hc.pool.PutRequest(server.config.Name, req)
	if err != nil {
		return false, 0
	}

	select {
	case resp := <-done:
		latency := time.Since(start).Seconds() * 1000
		if resp.Error != nil {
			return false, latency
		}
		return true, latency
	case <-time.After(5 * time.Second):
		return false, time.Since(start).Seconds() * 1000
	case <-ctx.Done():
		return false, 0
	}
}

func (hc *HealthChecker) RegisterServer(name string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	if _, exists := hc.checks[name]; !exists {
		hc.checks[name] = &healthCheck{serverName: name}
	}
}

func (hc *HealthChecker) UnregisterServer(name string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	delete(hc.checks, name)
}