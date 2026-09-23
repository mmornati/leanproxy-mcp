package pool

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

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
	GetServerTransport(name string) (string, error)
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

// StdioPool manages multiple stdio-based MCP server subprocesses. Each
// server multiplexes concurrent requests over its single stdio pipe (see
// StdioServerV2.sendRequest), capped per server by max_in_flight.
type StdioPool struct {
	// mu guards servers, rateLimiters and reconnect.
	mu           sync.RWMutex
	servers      map[string]*StdioServerV2
	idleTimeout  time.Duration
	logger       *slog.Logger
	ctx          context.Context
	cancel       context.CancelFunc
	rateLimiters *serverRateLimiters
	reconnect    ReconnectSettings
	// stopGrace is the SIGTERM→SIGKILL grace period given to every server
	// started by this pool (0 means defaultStopGracePeriod). Tests shorten
	// it.
	stopGrace time.Duration
}

// closeDeadlineMargin is added to the longest stop grace period to bound
// how long Close waits for all servers.
const closeDeadlineMargin = 5 * time.Second

// NewStdioPool creates a new StdioPool with the given idle timeout.
//
// maxPerServer is retained for API compatibility and is ignored: the number
// of concurrent requests per server is set by the server's max_in_flight
// config (default DefaultMaxInFlight).
func NewStdioPool(maxPerServer int, idleTimeout time.Duration, logger *slog.Logger) *StdioPool {
	_ = maxPerServer
	if logger == nil {
		logger = slog.Default()
	}

	ctx, cancel := context.WithCancel(context.Background())

	pool := &StdioPool{
		servers:      make(map[string]*StdioServerV2),
		idleTimeout:  idleTimeout,
		logger:       logger,
		ctx:          ctx,
		cancel:       cancel,
		rateLimiters: newServerRateLimiters(),
	}

	return pool
}

// SetReconnect applies reconnect settings to the pool. It takes effect on
// servers already in the pool and on every server started afterwards.
func (p *StdioPool) SetReconnect(settings ReconnectSettings) {
	settings = settings.validate()
	p.mu.Lock()
	p.reconnect = settings
	p.mu.Unlock()

	names := p.ListServers()
	for _, name := range names {
		p.mu.RLock()
		server, exists := p.servers[name]
		p.mu.RUnlock()
		if !exists {
			continue
		}
		server.applyReconnect(settings)
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
		Name:            config.Name,
		Command:         config.Stdio.Command,
		Args:            config.Stdio.Args,
		Env:             config.Stdio.Env,
		CWD:             config.Stdio.CWD,
		MaxInFlight:     config.MaxInFlight,
		IdleTimeout:     config.IdleTimeoutValue,
		RequestTimeout:  config.TimeoutValue,
		MaxResponseSize: config.MaxResponseBytes,
		StopGracePeriod: p.stopGrace,
	}

	server := newServerV2(config.Name, serverConfig, p.logger)
	server.applyReconnect(p.reconnect)
	if err := server.spawn(ctx); err != nil {
		return fmt.Errorf("pool: start %s: %w", config.Name, err)
	}

	p.servers[config.Name] = server

	// No rate limit by default: local stdio servers don't need one. A
	// server only gets a limiter when its config sets rate_limit with a
	// positive requests_per_second.
	p.rateLimiters.set(config.Name, config.RateLimit)

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

// limiters returns the current rate limiter set under p.mu, since Close
// replaces it.
func (p *StdioPool) limiters() *serverRateLimiters {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.rateLimiters
}

// admit resolves (and if needed restarts) the named server and waits for
// its optional rate limiter. The wait is bounded by the request's timeout
// so a configured limit delays a request rather than rejecting it, but never
// beyond its own deadline. Internal methods bypass the limiter (see
// ratelimit.go).
func (p *StdioPool) admit(ctx context.Context, name string, req Request) (*StdioServerV2, error) {
	server, err := p.GetOrStartServer(ctx, name)
	if err != nil {
		return nil, err
	}

	timeout := req.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := p.limiters().wait(waitCtx, name, req.Method); err != nil {
		return nil, fmt.Errorf("pool: %w", err)
	}
	return server, nil
}

// PutRequest submits req to the named server asynchronously: once the
// server is resolved and any rate limit is satisfied it returns, and the
// response is delivered to req.ResultCh (always, with Error set on failure)
// and the failure, if any, to req.ErrorCh. The request is multiplexed with
// every other in-flight request to that server.
func (p *StdioPool) PutRequest(name string, req Request) error {
	server, err := p.admit(p.ctx, name, req)
	if err != nil {
		return err
	}

	go func() {
		resp, sendErr := server.processRequest(p.ctx, req)
		if req.ResultCh != nil {
			select {
			case req.ResultCh <- resp:
			default:
			}
		}
		if req.ErrorCh != nil && sendErr != nil {
			select {
			case req.ErrorCh <- sendErr:
			default:
			}
		}
	}()
	return nil
}

// do runs req synchronously on the named server with the caller's context,
// so a caller that gives up cancels its request on the server too. An
// upstream JSON-RPC error is returned as resp.Error with a nil error; a
// transport or proxy failure (timeout, process exit, ...) is returned as
// the error.
func (p *StdioPool) do(ctx context.Context, name string, req Request) (*Response, error) {
	server, err := p.admit(ctx, name, req)
	if err != nil {
		return nil, fmt.Errorf("pool: send request: %w", err)
	}

	resp, sendErr := server.processRequest(ctx, req)
	if sendErr != nil {
		var upstreamErr *errors.JSONRPCError
		if stderrors.As(sendErr, &upstreamErr) {
			return resp, nil
		}
		return nil, sendErr
	}
	return resp, nil
}

// Close stops every server. The server list is taken (and the pool emptied)
// under the lock; the servers are then stopped in parallel outside it, so
// Close takes about one stop grace period however many servers ignore
// SIGTERM, bounded by an overall deadline.
func (p *StdioPool) Close() error {
	p.cancel()

	p.mu.Lock()
	servers := p.servers
	p.servers = make(map[string]*StdioServerV2)
	// golang.org/x/time/rate.Limiter holds no background goroutine or
	// resources to release, so the rate limiters just get a fresh map.
	p.rateLimiters = newServerRateLimiters()
	p.mu.Unlock()

	var wg sync.WaitGroup
	longestGrace := time.Duration(0)
	for name, server := range servers {
		// Mark closed before stopping so that a concurrent in-flight restart
		// (e.g. health-triggered) aborts instead of respawning a process the
		// pool will never see again.
		server.closed.Store(true)
		if server.stopGrace > longestGrace {
			longestGrace = server.stopGrace
		}
		wg.Add(1)
		go func(name string, server *StdioServerV2) {
			defer wg.Done()
			if err := server.stop(); err != nil {
				p.logger.Warn("server stop failed", "name", name, "error", err)
			}
			p.logger.Info("server stopped", "name", name)
		}(name, server)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(longestGrace + closeDeadlineMargin)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		p.logger.Warn("timed out waiting for stdio servers to stop", "servers", len(servers))
	}
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

// GetServerTransport reports the transport this server is configured with.
// It always returns "stdio" for a StdioPool member.
func (p *StdioPool) GetServerTransport(name string) (string, error) {
	p.mu.RLock()
	_, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("pool: server %s not found", name)
	}
	return "stdio", nil
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

	resp, err := p.do(ctx, serverName, Request{
		Method:  req.Method,
		Params:  req.Params,
		ID:      id,
		Timeout: timeout,
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return &proxy.JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  resp.Result,
		ID:      resp.ID,
	}, nil
}

func (p *StdioPool) SendRequestToServer(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration) (*Response, error) {
	return p.SendRequestToServerWithID(ctx, name, method, params, timeout, 1)
}

// SendRequestToServerWithID sends one request and waits for its response.
// Concurrent calls to the same server are multiplexed over its pipe. An
// upstream JSON-RPC error comes back as resp.Error (err == nil); a
// transport or proxy failure comes back as err.
func (p *StdioPool) SendRequestToServerWithID(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration, id int) (*Response, error) {
	if err := errors.ValidateContext(ctx); err != nil {
		return nil, fmt.Errorf("pool: %w", err)
	}

	return p.do(ctx, name, Request{
		Method:  method,
		Params:  params,
		ID:      id,
		Timeout: timeout,
	})
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
	if len(params) > 0 {
		if err := json.Unmarshal(params, &paramsMap); err != nil {
			return fmt.Errorf("pool: invalid notification params: %w", err)
		}
	}

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
