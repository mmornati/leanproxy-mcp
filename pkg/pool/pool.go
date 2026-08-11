package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/concurrent"
	"github.com/mmornati/leanproxy-mcp/pkg/errors"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
)

// ServerSource is the interface for sending requests to MCP servers.
type ServerSource interface {
	SendRequestToServer(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration) (*Response, error)
	SendRequestToServerWithID(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration, id int) (*Response, error)
	SendServerNotification(ctx context.Context, name string, method string, params map[string]interface{}) error
	ListServers() []string
	GetServerState(name string) (ServerState, error)
	RestartServer(ctx context.Context, name string) error
	IsServerMCPInitialized(name string) bool
	MarkServerMCPInitialized(name string)
	Close() error
}

// Request represents a JSON-RPC request for an MCP server.
type Request struct {
	Method   string          `json:"method"`
	Params   json.RawMessage `json:"params,omitempty"`
	ID       interface{}     `json:"id"`
	Timeout  time.Duration   `json:"-"`
	ResultCh chan *Response  `json:"-"`
	ErrorCh  chan error      `json:"-"`
}

// MarshalJSON serializes the Request to JSON with JSON-RPC 2.0 formatting.
func (r Request) MarshalJSON() ([]byte, error) {
	type Alias Request
	return json.Marshal(&struct {
		Alias
		JSONRPC string `json:"jsonrpc"`
	}{
		Alias:   Alias(r),
		JSONRPC: "2.0",
	})
}

// Response represents a JSON-RPC response from an MCP server.
type Response struct {
	Result json.RawMessage      `json:"result,omitempty"`
	Error  *errors.JSONRPCError `json:"error,omitempty"`
	ID     interface{}          `json:"id"`
}

// ServerState represents the current state of an MCP server.
type ServerState string

const (
	StateIdle         ServerState = "idle"
	StateRunning      ServerState = "running"
	StateBusy         ServerState = "busy"
	StateStopping     ServerState = "stopping"
	StateStopped      ServerState = "stopped"
	StateStarting     ServerState = "starting"
	StateError        ServerState = "error"
	StateDisconnected ServerState = "disconnected"
	StateUnknown      ServerState = "unknown"
)

// ReconnectSettings controls the automatic restart behavior of stdio servers.
type ReconnectSettings struct {
	// Disabled is the reconnect.enabled=false master switch: crashed servers
	// are left in the error state instead of being auto-restarted. Explicit
	// restarts (request- or operator-triggered) still work.
	Disabled           bool
	MaxRestartAttempts int
	RestartBackoff     time.Duration
	StableWindow       time.Duration
}

func (rs ReconnectSettings) validate() ReconnectSettings {
	if rs.MaxRestartAttempts <= 0 {
		rs.MaxRestartAttempts = 5
	}
	if rs.RestartBackoff <= 0 {
		rs.RestartBackoff = time.Second
	}
	// Clamp into a sane range: below the floor the jitter math could panic
	// and a crash loop would spin; above the cap the documented 1m maximum
	// backoff would not hold.
	if rs.RestartBackoff < minRestartBackoff {
		rs.RestartBackoff = minRestartBackoff
	}
	if rs.RestartBackoff > maxRestartBackoff {
		rs.RestartBackoff = maxRestartBackoff
	}
	if rs.StableWindow <= 0 {
		rs.StableWindow = 2 * time.Minute
	}
	return rs
}

// StdioPool manages multiple stdio-based MCP server subprocesses.
type StdioPool struct {
	servers         map[string]*StdioServerV2
	mu              sync.RWMutex
	maxPerServer    int
	idleTimeout     time.Duration
	logger          *slog.Logger
	ctx             context.Context
	cancel          context.CancelFunc
	requestWaiters  map[string][]chan Request
	waiterMu        sync.Mutex
	rateLimiters    map[string]*concurrent.RateLimiter
	circuitBreakers map[string]*concurrent.CircuitBreaker
	maxQueueSize    int
	workerPool      *concurrent.WorkerPool
	reconnect       ReconnectSettings
}

// NewStdioPool creates a new StdioPool with the specified maximum servers per name and idle timeout.
func NewStdioPool(maxPerServer int, idleTimeout time.Duration, logger *slog.Logger) *StdioPool {
	if logger == nil {
		logger = slog.Default()
	}

	ctx, cancel := context.WithCancel(context.Background())

	pool := &StdioPool{
		servers:         make(map[string]*StdioServerV2),
		maxPerServer:    maxPerServer,
		idleTimeout:     idleTimeout,
		logger:          logger,
		ctx:             ctx,
		cancel:          cancel,
		requestWaiters:  make(map[string][]chan Request),
		rateLimiters:    make(map[string]*concurrent.RateLimiter),
		circuitBreakers: make(map[string]*concurrent.CircuitBreaker),
		maxQueueSize:    1000,
	}

	pool.workerPool = concurrent.NewWorkerPool(maxPerServer*2, pool.maxQueueSize, logger)

	return pool
}

// SetReconnect applies reconnect settings to the pool. It takes effect on
// servers already in the pool and on every server started afterwards.
func (p *StdioPool) SetReconnect(settings ReconnectSettings) {
	p.mu.Lock()
	p.reconnect = settings.validate()
	p.mu.Unlock()

	names := p.ListServers()
	for _, name := range names {
		p.mu.RLock()
		server, exists := p.servers[name]
		p.mu.RUnlock()
		if !exists {
			continue
		}
		server.applyReconnect(p.reconnect)
	}
}

func (p *StdioPool) StartServer(ctx context.Context, config *migrate.ServerConfig) error {
	if config.Name == "" {
		return fmt.Errorf("pool: server name required")
	}

	if err := errors.ValidateContext(ctx); err != nil {
		return fmt.Errorf("pool: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if server, exists := p.servers[config.Name]; exists {
		if server.isHealthy() {
			return fmt.Errorf("pool: server %s already running", config.Name)
		}
	}

	serverConfig := StdioServerConfig{
		Name:           config.Name,
		Command:        config.Stdio.Command,
		Args:           config.Stdio.Args,
		Env:            config.Stdio.Env,
		CWD:            config.Stdio.CWD,
		MaxConcurrent:  p.maxPerServer,
		IdleTimeout:    config.IdleTimeoutValue,
		RequestTimeout: config.TimeoutValue,
	}

	server := newServerV2(config.Name, serverConfig, p.logger)
	server.applyReconnect(p.reconnect)
	if err := server.spawn(ctx); err != nil {
		return fmt.Errorf("pool: start %s: %w", config.Name, err)
	}

	p.servers[config.Name] = server

	p.rateLimiters[config.Name] = concurrent.NewRateLimiter(10, time.Second)
	p.circuitBreakers[config.Name] = concurrent.NewCircuitBreaker(5, 50*time.Second, 10*time.Second)

	p.logger.Info("server started in pool", "name", config.Name)
	return nil
}

func (p *StdioPool) StartAllServers(ctx context.Context, configs []*migrate.ServerConfig) error {
	for _, cfg := range configs {
		if cfg.Enabled != nil && !*cfg.Enabled {
			continue
		}
		if cfg.Transport != registry.TransportStdio {
			continue
		}
		if err := p.StartServer(ctx, cfg); err != nil {
			p.logger.Warn("failed to start server", "name", cfg.Name, "error", err)
		}
	}
	return nil
}

func (p *StdioPool) GetServer(name string) (*StdioServerV2, error) {
	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("pool: server %s not found", name)
	}

	if !server.isHealthy() {
		return nil, fmt.Errorf("pool: server %s not healthy", name)
	}

	return server, nil
}

func (p *StdioPool) GetOrStartServer(ctx context.Context, name string) (*StdioServerV2, error) {
	server, err := p.GetServer(name)
	if err == nil {
		return server, nil
	}

	if !strings.Contains(err.Error(), "not healthy") {
		return nil, err
	}

	p.logger.Info("server not healthy, attempting to restart", "name", name)

	if err := p.RestartServer(ctx, name); err != nil {
		p.logger.Error("failed to restart server", "name", name, "error", err)
		return nil, fmt.Errorf("pool: failed to restart server %s: %w", name, err)
	}

	if err := p.waitForServerReady(ctx, name, 10*time.Second); err != nil {
		p.logger.Warn("server may not be fully initialized", "name", name, "error", err)
	}

	server, err = p.GetServer(name)
	if err != nil {
		return nil, fmt.Errorf("pool: server still not available after restart: %w", err)
	}

	p.logger.Info("server restarted and ready", "name", name)
	return server, nil
}

func (p *StdioPool) IsServerMCPInitialized(name string) bool {
	server, err := p.GetServer(name)
	if err != nil {
		return false
	}
	return server.IsMCPInitialized()
}

func (p *StdioPool) MarkServerMCPInitialized(name string) {
	server, err := p.GetServer(name)
	if err != nil {
		return
	}
	server.SetMCPInitialized()
}

func (p *StdioPool) waitForServerReady(ctx context.Context, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Until(deadline)):
			return fmt.Errorf("timeout waiting for server ready")
		case <-ticker.C:
			server, err := p.GetServer(name)
			if err == nil && server.isHealthy() {
				return nil
			}
		}
	}
}

func (p *StdioPool) PutRequest(name string, req Request) error {
	server, err := p.GetOrStartServer(p.ctx, name)
	if err != nil {
		return err
	}

	if cb, exists := p.circuitBreakers[name]; exists {
		if cb.State() == concurrent.StateOpen {
			return fmt.Errorf("pool: circuit breaker open for %s", name)
		}
	}

	if rl, exists := p.rateLimiters[name]; exists {
		if !rl.Allow() {
			return fmt.Errorf("pool: rate limit exceeded for %s", name)
		}
	}

	if !server.canAcceptRequest() {
		return fmt.Errorf("pool: server %s at max capacity", name)
	}

	timeout := req.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	select {
	case server.requestCh <- req:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("pool: request timeout for %s", name)
	case <-p.ctx.Done():
		return p.ctx.Err()
	}
}

func (p *StdioPool) Close() error {
	p.cancel()

	if p.workerPool != nil {
		p.workerPool.Shutdown()
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for name, server := range p.servers {
		// Mark closed before stopping so that a concurrent in-flight restart
		// (e.g. health-triggered) aborts instead of respawning a process the
		// pool will never see again.
		server.closed.Store(true)
		server.stop()
		p.logger.Info("server stopped", "name", name)
	}

	for name, limiter := range p.rateLimiters {
		limiter.Close()
		p.logger.Info("rate limiter closed", "name", name)
	}
	p.rateLimiters = make(map[string]*concurrent.RateLimiter)

	p.servers = make(map[string]*StdioServerV2)
	return nil
}

func (p *StdioPool) ListServers() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	names := make([]string, 0, len(p.servers))
	for name := range p.servers {
		names = append(names, name)
	}
	return names
}

func (p *StdioPool) ServerCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.servers)
}

func (p *StdioPool) HasServer(name string) bool {
	p.mu.RLock()
	_, exists := p.servers[name]
	p.mu.RUnlock()
	return exists
}

func (p *StdioPool) GetServerState(name string) (ServerState, error) {
	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("pool: server %s not found", name)
	}

	return server.getState(), nil
}

func (p *StdioPool) GetServerStats(name string) (ServerStats, error) {
	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return ServerStats{}, fmt.Errorf("pool: server %s not found", name)
	}

	return server.getStats(), nil
}

func (p *StdioPool) StopServer(name string) error {
	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return fmt.Errorf("pool: server %s not found", name)
	}

	return server.stop()
}

func (p *StdioPool) RestartServer(ctx context.Context, name string) error {
	if err := errors.ValidateContext(ctx); err != nil {
		return fmt.Errorf("pool: %w", err)
	}

	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return fmt.Errorf("pool: server %s not found", name)
	}

	// server.restart performs the full stop→spawn cycle and guarantees a fresh
	// request loop is running for the new process generation.
	if err := server.restart(ctx); err != nil {
		return err
	}

	// Wait for server process to be healthy (up to 15s)
	if err := p.waitForServerReady(ctx, name, 15*time.Second); err != nil {
		p.logger.Warn("server restarted but not ready yet, proceeding anyway", "name", name, "error", err)
	}

	// Reset circuit breaker on successful restart to avoid cascading failures
	if cb, exists := p.circuitBreakers[name]; exists {
		cb.Reset()
		p.logger.Info("circuit breaker reset after restart", "name", name)
	}

	return nil
}

func (p *StdioPool) SendRequest(ctx context.Context, serverName string, req *proxy.JSONRPCRequest, timeout time.Duration) (*proxy.JSONRPCResponse, error) {
	if err := errors.ValidateContext(ctx); err != nil {
		return nil, fmt.Errorf("pool: %w", err)
	}

	id := req.ID
	if id == nil {
		id = 1
	}

	resultCh := make(chan *Response, 1)
	errorCh := make(chan error, 1)

	poolReq := Request{
		Method:   req.Method,
		Params:   req.Params,
		ID:       id,
		Timeout:  timeout,
		ResultCh: resultCh,
		ErrorCh:  errorCh,
	}

	if err := p.PutRequest(serverName, poolReq); err != nil {
		return nil, fmt.Errorf("pool: send request: %w", err)
	}

	select {
	case resp := <-resultCh:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return &proxy.JSONRPCResponse{
			JSONRPC: "2.0",
			Result:  resp.Result,
			ID:      resp.ID,
		}, nil
	case err := <-errorCh:
		return nil, err
	case <-time.After(timeout):
		return nil, fmt.Errorf("pool: request timeout after %v", timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *StdioPool) SendRequestToServer(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration) (*Response, error) {
	return p.SendRequestToServerWithID(ctx, name, method, params, timeout, 1)
}

func (p *StdioPool) SendRequestToServerWithID(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration, id int) (*Response, error) {
	if err := errors.ValidateContext(ctx); err != nil {
		return nil, fmt.Errorf("pool: %w", err)
	}

	resultCh := make(chan *Response, 1)
	errorCh := make(chan error, 1)

	poolReq := Request{
		Method:   method,
		Params:   params,
		ID:       id,
		Timeout:  timeout,
		ResultCh: resultCh,
		ErrorCh:  errorCh,
	}

	if err := p.PutRequest(name, poolReq); err != nil {
		return nil, fmt.Errorf("pool: send request: %w", err)
	}

	select {
	case resp := <-resultCh:
		return resp, nil
	case err := <-errorCh:
		return nil, err
	case <-time.After(timeout):
		return nil, fmt.Errorf("request timeout after %v", timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *StdioPool) SendNotificationToServer(ctx context.Context, name string, method string, params json.RawMessage) error {
	if err := errors.ValidateContext(ctx); err != nil {
		return fmt.Errorf("pool: %w", err)
	}

	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return fmt.Errorf("pool: server %s not found", name)
	}

	var paramsMap map[string]interface{}
	json.Unmarshal(params, &paramsMap)

	return server.sendNotification(ctx, method, paramsMap)
}

func (p *StdioPool) SendServerNotification(ctx context.Context, name string, method string, params map[string]interface{}) error {
	if err := errors.ValidateContext(ctx); err != nil {
		return fmt.Errorf("pool: %w", err)
	}

	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return fmt.Errorf("pool: server %s not found", name)
	}

	return server.sendNotification(ctx, method, params)
}
