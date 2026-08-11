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
	stopOnce    sync.Once
	maxFailures int
	restarting  map[string]bool
	// ctx is the context passed to Start; in-flight restarts derive their
	// timeout from it so a shutdown cancels them.
	ctx context.Context
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
	hc.mu.Lock()
	hc.ctx = ctx
	hc.mu.Unlock()

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

// Stop signals the checker loop to exit. It is idempotent.
func (hc *HealthChecker) Stop() {
	hc.stopOnce.Do(func() { close(hc.stopCh) })
}

func (hc *HealthChecker) checkAllServers(ctx context.Context) {
	servers := hc.pool.ListServers()

	// Drop health entries for servers that left the pool so the map (and
	// GetAllHealth reporting) does not grow monotonically with ghosts.
	hc.mu.Lock()
	known := make(map[string]struct{}, len(servers))
	for _, name := range servers {
		known[name] = struct{}{}
	}
	for name := range hc.checks {
		if _, ok := known[name]; !ok {
			delete(hc.checks, name)
		}
	}
	hc.mu.Unlock()

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

		// Only genuine liveness failures (HealthUnhealthy: ping failures,
		// wedged processes) count toward a restart. Stopped servers are
		// deliberately off (idle_timeout) and error-state servers are owned
		// by crash recovery — counting those would restart-churn servers the
		// health checker is not responsible for.
		if result.Status == HealthUnhealthy {
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

		if failures >= maxFailures && status == HealthUnhealthy {
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
	parentCtx := hc.ctx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	hc.mu.Unlock()

	go func() {
		defer func() {
			hc.mu.Lock()
			delete(hc.restarting, name)
			hc.mu.Unlock()
		}()

		// Re-validate at trigger time: the failure count may be stale by the
		// time we get here. Never restart a server that is busy (a long tool
		// call can starve the ping behind it and look like a wedge), already
		// stopped (idle_timeout), or in an error state owned by crash
		// recovery — GetServer only succeeds for healthy-state servers.
		server, err := hc.pool.GetServer(name)
		if err != nil {
			hc.logger.Debug("auto-reconnect skipped, server not in a healthy state", "name", name)
			return
		}
		if !server.isIdle() {
			hc.logger.Info("auto-reconnect skipped, server is busy", "name", name)
			return
		}

		hc.logger.Info("auto-reconnecting unhealthy server", "name", name)
		ctx, cancel := context.WithTimeout(parentCtx, 60*time.Second)
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

	state, err := hc.pool.GetServerState(name)
	if err != nil {
		result.Status = HealthUnhealthy
		result.Error = err.Error()
		return result
	}

	stats, _ := hc.pool.GetServerStats(name)

	switch state {
	case StateStopped, StateStopping:
		// Deliberately stopped (idle_timeout or operator action): not a
		// failure. The server must NOT be restarted by the health checker —
		// it revives lazily on its next request.
		result.Status = HealthUnknown
		result.Error = "server is stopped"
		return result
	case StateStarting:
		result.Status = HealthUnknown
		result.Error = "server is starting"
		return result
	case StateError:
		// Crash recovery owns this state, including the terminal
		// budget-exhausted case. Report it, but never count it as a
		// health-check failure and never restart it from here.
		result.Status = HealthError
		result.Error = "server in error state"
		return result
	}

	// States idle/running/busy: the process is supposed to be alive.
	result.Status = HealthHealthy
	result.LatencyMs = stats.AvgLatencyMs

	if stats.ErrorCount > 0 {
		errorRate := float64(stats.ErrorCount) / float64(stats.RequestCount+stats.ErrorCount)
		if errorRate > 0.1 {
			result.Status = HealthDegraded
			result.Error = "high error rate"
		}
	}

	server, err := hc.pool.GetServer(name)
	if err != nil {
		// Raced with a state transition between the two lookups; report
		// unknown rather than counting a spurious failure.
		result.Status = HealthUnknown
		result.Error = err.Error()
		return result
	}

	// A process alive but wedged before the first initialize handshake is
	// invisible to the ping probe (pings are only valid on an initialized
	// session). If requests are already failing in this generation, that is
	// a liveness failure: restart the server.
	if !server.IsMCPInitialized() {
		if server.failedSinceSpawn() {
			result.Status = HealthUnhealthy
			result.Error = "server not initialized and requests are failing"
		}
		return result
	}

	// Liveness probe: a process can be alive yet unresponsive. Ping healthy
	// idle/running servers to detect wedged processes. Skip busy servers to
	// avoid interfering with in-flight tool calls.
	if result.Status == HealthHealthy && (state == StateIdle || state == StateRunning) {
		ok, latency := hc.performPingCheck(ctx, server)
		if ok {
			result.LatencyMs = latency
		} else if server.isIdle() {
			result.Status = HealthUnhealthy
			result.Error = "MCP ping failed"
		} else {
			// The ping starved behind a long in-flight request and timed
			// out: an artifact of the probe, not a liveness signal.
			hc.logger.Debug("ping inconclusive, server became busy", "name", name)
		}
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
	case <-time.After(7 * time.Second):
		// The outer timeout deliberately exceeds the request's own 5s
		// timeout: if we land here, the ping never even got processed (e.g.
		// it starved behind a long in-flight tool call), which is an
		// artifact of probing a busy server — not a liveness signal.
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
