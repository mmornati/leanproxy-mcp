package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
)

type Request struct {
	Method     string          `json:"method"`
	Params     json.RawMessage `json:"params,omitempty"`
	ID         interface{}     `json:"id"`
	Timeout    time.Duration
	ResultCh   chan *Response
	ErrorCh    chan error
}

type Response struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *JSONRPCError   `json:"error,omitempty"`
	ID     interface{}     `json:"id"`
}

type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *JSONRPCError) Error() string {
	return fmt.Sprintf("jsonrpc: error %d: %s", e.Code, e.Message)
}

const (
	ErrCodeInternalError = -32603
	ErrCodeServerError   = -32000
	ErrCodeTimeout       = -32001
)

type ServerState string

const (
	StateIdle     ServerState = "idle"
	StateRunning  ServerState = "running"
	StateBusy     ServerState = "busy"
	StateStopping ServerState = "stopping"
	StateStopped  ServerState = "stopped"
	StateStarting ServerState = "starting"
	StateError    ServerState = "error"
)

type StdioPool struct {
	servers       map[string]*StdioServerV2
	mu            sync.RWMutex
	maxPerServer  int
	idleTimeout   time.Duration
	logger        *slog.Logger
	ctx           context.Context
	cancel        context.CancelFunc
	requestWaiters map[string][]chan Request
	waiterMu      sync.Mutex
}

func NewStdioPool(maxPerServer int, idleTimeout time.Duration, logger *slog.Logger) *StdioPool {
	if logger == nil {
		logger = slog.Default()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &StdioPool{
		servers:        make(map[string]*StdioServerV2),
		maxPerServer:   maxPerServer,
		idleTimeout:    idleTimeout,
		logger:         logger,
		ctx:            ctx,
		cancel:         cancel,
		requestWaiters: make(map[string][]chan Request),
	}
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
		Name:          config.Name,
		Command:       config.Stdio.Command,
		Args:          config.Stdio.Args,
		Env:           config.Stdio.Env,
		CWD:           config.Stdio.CWD,
		MaxConcurrent: p.maxPerServer,
		IdleTimeout:   p.idleTimeout,
		RequestTimeout: config.TimeoutValue,
	}

	server := newServerV2(config.Name, serverConfig, p.logger)
	if err := server.spawn(ctx); err != nil {
		return fmt.Errorf("pool: start %s: %w", config.Name, err)
	}

	p.servers[config.Name] = server

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

	p.mu.Lock()
	defer p.mu.Unlock()

	for name, server := range p.servers {
		server.stop()
		p.logger.Info("server stopped", "name", name)
	}

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

	server.mu.Lock()
	if server.state == StateStopping {
		server.state = StateStopped
	}
	server.mu.Unlock()

	return server.spawn(ctx)
}