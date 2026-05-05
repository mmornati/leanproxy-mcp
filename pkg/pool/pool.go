package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/concurrent"
	"github.com/mmornati/leanproxy-mcp/pkg/errors"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
)

type ServerSource interface {
	SendRequestToServer(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration) (*Response, error)
	SendRequestToServerWithID(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration, id int) (*Response, error)
	SendServerNotification(ctx context.Context, name string, method string, params map[string]interface{}) error
	ListServers() []string
	GetServerState(name string) (ServerState, error)
	RestartServer(ctx context.Context, name string) error
	Close() error
}

type Request struct {
	Method   string          `json:"method"`
	Params   json.RawMessage `json:"params,omitempty"`
	ID       interface{}     `json:"id"`
	Timeout  time.Duration   `json:"-"`
	ResultCh chan *Response  `json:"-"`
	ErrorCh  chan error      `json:"-"`
}

func (r Request) MarshalJSON() ([]byte, error) {
	type Alias Request
	return json.Marshal(&struct {
		Alias
		JSONRPC string        `json:"jsonrpc"`
		Timeout time.Duration `json:"timeout,omitempty"`
	}{
		Alias:   Alias(r),
		JSONRPC: "2.0",
		Timeout: r.Timeout,
	})
}

type Response struct {
	Result json.RawMessage   `json:"result,omitempty"`
	Error  *errors.JSONRPCError `json:"error,omitempty"`
	ID     interface{}       `json:"id"`
}

type ServerState string

const (
	StateIdle     ServerState = "idle"
	StateRunning  ServerState = "running"
	StateBusy     ServerState = "busy"
	StateStopping ServerState = "stopping"
	StateStopped  ServerState = "stopped"
	StateStarting ServerState = "starting"
	StateError    ServerState = "error"
	StateUnknown  ServerState = "unknown"
)

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
}

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

func (p *StdioPool) StartServer(ctx context.Context, config *migrate.ServerConfig) error {
	if config.Name == "" {
		return fmt.Errorf("pool: server name required")
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
		IdleTimeout:    p.idleTimeout,
		RequestTimeout: config.TimeoutValue,
	}

	server := newServerV2(config.Name, serverConfig, p.logger)
	if err := server.spawn(ctx); err != nil {
		return fmt.Errorf("pool: start %s: %w", config.Name, err)
	}

	p.servers[config.Name] = server

	p.rateLimiters[config.Name] = concurrent.NewRateLimiter(10, time.Second)
	p.circuitBreakers[config.Name] = concurrent.NewCircuitBreaker(5, 50*time.Second, 10*time.Second)

	go server.runRequestLoop(p.ctx, p)

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

func (p *StdioPool) PutRequest(name string, req Request) error {
	server, err := p.GetServer(name)
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
	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return fmt.Errorf("pool: server %s not found", name)
	}

	if err := server.stop(); err != nil {
		return err
	}

	time.Sleep(500 * time.Millisecond)

	currentState := atomic.LoadInt32(&server.state)
	if currentState == stateStopping {
		atomic.StoreInt32(&server.state, stateStopped)
	}

	return server.spawn(ctx)
}

func (p *StdioPool) SendRequest(ctx context.Context, serverName string, req *proxy.JSONRPCRequest, timeout time.Duration) (*proxy.JSONRPCResponse, error) {
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
	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return fmt.Errorf("pool: server %s not found", name)
	}

	return server.sendNotification(ctx, method, params)
}
