package pool

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/errors"
)

type HealthStatus string

const (
	HealthUnknown   HealthStatus = "unknown"
	HealthHealthy   HealthStatus = "healthy"
	HealthDegraded  HealthStatus = "degraded"
	HealthUnhealthy HealthStatus = "unhealthy"
	HealthError     HealthStatus = "error"
)

type HealthCheckResult struct {
	ServerName string
	Status     HealthStatus
	LatencyMs  float64
	Error      string
	CheckedAt  time.Time
}

type HealthChecker struct {
	pool        *StdioPool
	logger      *slog.Logger
	checks      map[string]*healthCheck
	mu          sync.RWMutex
	stopCh      chan struct{}
	maxFailures int
	restarting  map[string]bool
}

type healthCheck struct {
	serverName          string
	lastCheck           time.Time
	lastStatus          HealthStatus
	lastLatencyMs       float64
	lastError           string
	consecutiveFailures int
	mu                  sync.Mutex
}

func NewHealthChecker(pool *StdioPool, logger *slog.Logger) *HealthChecker {
	if logger == nil {
		logger = slog.Default()
	}

	return &HealthChecker{
		pool:        pool,
		logger:      logger,
		checks:      make(map[string]*healthCheck),
		stopCh:      make(chan struct{}),
		maxFailures: 3,
		restarting:  make(map[string]bool),
	}
}

func (hc *HealthChecker) SetMaxFailures(n int) {
	if n < 1 {
		n = 3
	}
	hc.mu.Lock()
	hc.maxFailures = n
	hc.mu.Unlock()
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
			if check.consecutiveFailures > 0 {
				hc.logger.Info("server recovered from consecutive failures",
					"name", name,
					"previous_failures", check.consecutiveFailures)
			}
			check.consecutiveFailures = 0
		}
		failures := check.consecutiveFailures
		status := check.lastStatus
		check.mu.Unlock()

		hc.mu.RLock()
		maxFailures := hc.maxFailures
		hc.mu.RUnlock()

		if failures >= maxFailures && status != HealthHealthy {
			hc.logger.Warn("server had consecutive failures, triggering auto-reconnect",
				"name", name,
				"failures", failures,
				"last_error", result.Error)
			hc.triggerRestart(name)
			check.mu.Lock()
			check.consecutiveFailures = 0
			check.mu.Unlock()
		}
	}
}

func (hc *HealthChecker) triggerRestart(name string) {
	hc.mu.Lock()
	if hc.restarting[name] {
		hc.mu.Unlock()
		return
	}
	hc.restarting[name] = true
	hc.mu.Unlock()

	go func() {
		defer func() {
			hc.mu.Lock()
			delete(hc.restarting, name)
			hc.mu.Unlock()
		}()

		hc.logger.Info("auto-reconnecting unhealthy server", "name", name)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := hc.pool.RestartServer(ctx, name); err != nil {
			hc.logger.Error("auto-reconnect failed", "name", name, "error", err)
			return
		}
		hc.logger.Info("auto-reconnect complete", "name", name)
	}()
}

func (hc *HealthChecker) CheckServer(ctx context.Context, name string) HealthCheckResult {
	result := HealthCheckResult{
		ServerName: name,
		CheckedAt:  time.Now(),
	}

	server, err := hc.pool.GetServer(name)
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

	// Liveness probe: a process can be alive yet unresponsive. Ping healthy
	// idle/running servers to detect wedged processes. Skip busy servers to
	// avoid interfering with in-flight tool calls, and only probe servers
	// that completed the MCP initialize handshake so the ping is valid.
	if result.Status == HealthHealthy && (state == StateIdle || state == StateRunning) && server.IsMCPInitialized() {
		ok, latency := hc.performPingCheck(ctx, server)
		if ok {
			result.LatencyMs = latency
		} else {
			result.Status = HealthUnhealthy
			result.Error = "MCP ping failed"
		}
	} else {
		result.LatencyMs = stats.AvgLatencyMs
	}

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
			ServerName: name,
			Status:     check.lastStatus,
			LatencyMs:  check.lastLatencyMs,
			Error:      check.lastError,
			CheckedAt:  check.lastCheck,
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
	ID      interface{}          `json:"id"`
	JSONRPC string               `json:"jsonrpc"`
	Result  json.RawMessage      `json:"result,omitempty"`
	Error   *errors.JSONRPCError `json:"error,omitempty"`
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
