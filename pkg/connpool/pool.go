package connpool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

type PoolConfig struct {
	MaxSize     int           `yaml:"max_size"`
	MaxWaitTime time.Duration `yaml:"max_wait_time"`
	IdleTimeout time.Duration `yaml:"idle_timeout"`
	HealthCheck time.Duration `yaml:"health_check_interval"`
	InitialSize int           `yaml:"initial_size"`
}

func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxSize:     5,
		MaxWaitTime: 30 * time.Second,
		IdleTimeout: 5 * time.Minute,
		HealthCheck: 10 * time.Second,
		InitialSize: 2,
	}
}

type PoolMetrics struct {
	TotalRequests     int64
	ActiveClients    int64
	AvailableClients int64
	WaitingClients  int64
	Timeouts       int64
	Errors         int64
	AvgLatencyMs   float64
}

type Response struct {
	Result json.RawMessage `json:"result,omitempty"`
	ID     interface{}   `json:"id"`
}

type ClientConnection struct {
	client   *client.Client
	server   string
	lastUsed time.Time
	mu       sync.Mutex
	healthy bool
}

func (c *ClientConnection) IsHealthy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.healthy
}

func (c *ClientConnection) SetHealthy(healthy bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.healthy = healthy
}

func (c *ClientConnection) GetClient() *client.Client {
	return c.client
}

type ServerPool struct {
	mu           sync.Mutex
	available   chan *ClientConnection
	active      map[*ClientConnection]struct{}
	maxSize     int
	waitingQueue chan waitRequest
	maxQueueSize int
	config      PoolConfig
	logger     *slog.Logger
	metrics   PoolMetrics
}

type waitRequest struct {
	client   chan *ClientConnection
	timeout <-chan time.Time
}

func NewServerPool(maxSize int, config PoolConfig, logger *slog.Logger) *ServerPool {
	maxQueueSize := maxSize * 2
	if maxQueueSize < 10 {
		maxQueueSize = 10
	}

	return &ServerPool{
		available:    make(chan *ClientConnection, maxSize),
		active:       make(map[*ClientConnection]struct{}),
		maxSize:      maxSize,
		waitingQueue: make(chan waitRequest, maxQueueSize),
		maxQueueSize: maxQueueSize,
		config:      config,
		logger:     logger,
	}
}

func (sp *ServerPool) GetClient(ctx context.Context, createFunc func() (*client.Client, error)) (*ClientConnection, error) {
	select {
	case conn := <-sp.available:
		if conn.IsHealthy() {
			sp.mu.Lock()
			sp.active[conn] = struct{}{}
			sp.mu.Unlock()
			atomic.AddInt64(&sp.metrics.AvailableClients, -1)
			atomic.AddInt64(&sp.metrics.ActiveClients, 1)
			return conn, nil
		}
		sp.logger.Debug("client unhealthy, creating new one", "server", conn.server)
		conn.client.Close()
		atomic.AddInt64(&sp.metrics.AvailableClients, -1)
	default:
		sp.mu.Lock()
		currentActive := len(sp.active)
		sp.mu.Unlock()

		if currentActive < sp.maxSize {
			atomic.AddInt64(&sp.metrics.ActiveClients, 1)
			conn, err := sp.createNewClient(ctx, createFunc)
			if err != nil {
				atomic.AddInt64(&sp.metrics.ActiveClients, -1)
				return nil, err
			}
			return conn, nil
		}
	}

	return sp.waitForClient(ctx, createFunc)
}

func (sp *ServerPool) createNewClient(ctx context.Context, createFunc func() (*client.Client, error)) (*ClientConnection, error) {
	mcpClient, err := createFunc()
	if err != nil {
		atomic.AddInt64(&sp.metrics.Errors, 1)
		return nil, fmt.Errorf("create client: %w", err)
	}

	conn := &ClientConnection{
		client:   mcpClient,
		lastUsed: time.Now(),
		healthy: true,
	}

	sp.mu.Lock()
	sp.active[conn] = struct{}{}
	sp.mu.Unlock()

	return conn, nil
}

func (sp *ServerPool) waitForClient(ctx context.Context, createFunc func() (*client.Client, error)) (*ClientConnection, error) {
	clientChan := make(chan *ClientConnection, 1)
	timeoutChan := time.After(sp.config.MaxWaitTime)

	atomic.AddInt64(&sp.metrics.WaitingClients, 1)

	select {
	case sp.waitingQueue <- waitRequest{client: clientChan, timeout: timeoutChan}:
	case <-ctx.Done():
		atomic.AddInt64(&sp.metrics.WaitingClients, -1)
		atomic.AddInt64(&sp.metrics.Timeouts, 1)
		return nil, ctx.Err()
	case <-timeoutChan:
		atomic.AddInt64(&sp.metrics.WaitingClients, -1)
		atomic.AddInt64(&sp.metrics.Timeouts, 1)
		return nil, fmt.Errorf("pool: wait timeout after %v", sp.config.MaxWaitTime)
	}

	select {
	case conn := <-clientChan:
		atomic.AddInt64(&sp.metrics.WaitingClients, -1)
		atomic.AddInt64(&sp.metrics.ActiveClients, 1)
		return conn, nil
	case <-ctx.Done():
		atomic.AddInt64(&sp.metrics.WaitingClients, -1)
		return nil, ctx.Err()
	case <-timeoutChan:
		atomic.AddInt64(&sp.metrics.WaitingClients, -1)
		atomic.AddInt64(&sp.metrics.Timeouts, 1)
		return nil, fmt.Errorf("pool: wait timeout after %v", sp.config.MaxWaitTime)
	}
}

func (sp *ServerPool) ReturnClient(conn *ClientConnection) {
	sp.mu.Lock()
	delete(sp.active, conn)
	sp.mu.Unlock()

	atomic.AddInt64(&sp.metrics.ActiveClients, -1)

	if !conn.IsHealthy() {
		sp.logger.Debug("client unhealthy on return, closing", "server", conn.server)
		conn.client.Close()
		return
	}

	select {
	case sp.available <- conn:
		atomic.AddInt64(&sp.metrics.AvailableClients, 1)
		conn.lastUsed = time.Now()
	default:
		sp.logger.Debug("pool full on return, closing client", "server", conn.server)
		conn.client.Close()
	}
}

func (sp *ServerPool) Close() error {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	close(sp.available)

	for conn := range sp.available {
		if conn.client != nil {
			conn.client.Close()
		}
	}

	for conn := range sp.active {
		if conn.client != nil {
			conn.client.Close()
		}
	}

	sp.active = make(map[*ClientConnection]struct{})
	return nil
}

func (sp *ServerPool) GetMetrics() PoolMetrics {
	sp.mu.Lock()
	metrics := sp.metrics
	sp.mu.Unlock()

	metrics.AvailableClients = int64(len(sp.available))
	metrics.ActiveClients = int64(len(sp.active))

	return metrics
}

type ConnectionPool struct {
	mu         sync.RWMutex
	pools     map[string]*ServerPool
	config   PoolConfig
	logger   *slog.Logger
	ctx      context.Context
	cancel   context.CancelFunc
	serverCfgs map[string]*migrate.ServerConfig
	stopped  bool
}

func NewConnectionPool(config PoolConfig, logger *slog.Logger) *ConnectionPool {
	if logger == nil {
		logger = slog.Default()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &ConnectionPool{
		pools:       make(map[string]*ServerPool),
		config:     config,
		logger:    logger,
		ctx:       ctx,
		cancel:    cancel,
		serverCfgs: make(map[string]*migrate.ServerConfig),
	}
}

func (cp *ConnectionPool) RegisterServer(name string, cfg *migrate.ServerConfig) {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	if _, exists := cp.pools[name]; exists {
		cp.logger.Debug("server already registered", "name", name)
		return
	}

	serverPool := NewServerPool(cp.config.MaxSize, cp.config, cp.logger)
	cp.pools[name] = serverPool
	cp.serverCfgs[name] = cfg

	cp.logger.Info("server registered in connection pool", "name", name, "max_size", cp.config.MaxSize)
}

func (cp *ConnectionPool) UnregisterServer(name string) {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	if pool, exists := cp.pools[name]; exists {
		pool.Close()
		delete(cp.pools, name)
		delete(cp.serverCfgs, name)
		cp.logger.Info("server unregistered from connection pool", "name", name)
	}
}

func (cp *ConnectionPool) GetClient(ctx context.Context, serverName string) (*ClientConnection, error) {
	cp.mu.RLock()
	pool, exists := cp.pools[serverName]
	cfg := cp.serverCfgs[serverName]
	cp.mu.RUnlock()

	if !exists || pool == nil {
		return nil, fmt.Errorf("pool: server %s not registered", serverName)
	}

	createFunc := func() (*client.Client, error) {
		return cp.createMCPClient(ctx, serverName, cfg)
	}

	return pool.GetClient(ctx, createFunc)
}

func (cp *ConnectionPool) createMCPClient(ctx context.Context, serverName string, cfg *migrate.ServerConfig) (*client.Client, error) {
	if cfg.HTTP == nil || cfg.HTTP.URL == "" {
		return nil, fmt.Errorf("http config is required")
	}

	baseURL := cfg.HTTP.URL
	headers := make(map[string]string)
	if cfg.HTTP.Headers != nil {
		for k, v := range cfg.HTTP.Headers {
			headers[k] = v
		}
	}

	opts := []transport.StreamableHTTPCOption{transport.WithHTTPHeaders(headers)}

	if cfg.HTTP.Auth != nil {
		authType := strings.ToLower(strings.TrimSpace(cfg.HTTP.Auth.Type))
		switch authType {
		case "bearer":
			if cfg.HTTP.Auth.ClientSecret != "" {
				opts = append(opts, transport.WithHTTPHeaders(map[string]string{
					"Authorization": "Bearer " + cfg.HTTP.Auth.ClientSecret,
				}))
			}
		case "oauth2":
			if cfg.HTTP.Auth.ClientID != "" && cfg.HTTP.Auth.ClientSecret != "" {
				oauthCfg := transport.OAuthConfig{
					ClientID:     cfg.HTTP.Auth.ClientID,
					ClientSecret: cfg.HTTP.Auth.ClientSecret,
					Scopes:      cfg.HTTP.Auth.Scopes,
				}
				opts = append(opts, transport.WithHTTPOAuth(oauthCfg))
			}
		}
	}

	mcpClient, err := client.NewStreamableHttpClient(baseURL, opts...)
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}

	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := mcpClient.Start(startCtx); err != nil {
		mcpClient.Close()
		return nil, fmt.Errorf("start client: %w", err)
	}

	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err = mcpClient.Initialize(initCtx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: "2024-11-05",
			Capabilities:   mcp.ClientCapabilities{},
			ClientInfo: mcp.Implementation{
				Name:    "leanproxy-mcp",
				Version: "1.0.0",
			},
		},
	})
	if err != nil {
		mcpClient.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	return mcpClient, nil
}

func (cp *ConnectionPool) ReturnClient(serverName string, conn *ClientConnection) {
	cp.mu.RLock()
	pool, exists := cp.pools[serverName]
	cp.mu.RUnlock()

	if !exists || pool == nil {
		cp.logger.Warn("returning client to unregistered pool", "server", serverName)
		if conn.client != nil {
			conn.client.Close()
		}
		return
	}

	pool.ReturnClient(conn)
}

func (cp *ConnectionPool) SendToolRequest(ctx context.Context, serverName string, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	conn, err := cp.GetClient(ctx, serverName)
	if err != nil {
		return nil, fmt.Errorf("get client: %w", err)
	}
	defer cp.ReturnClient(serverName, conn)

	start := time.Now()

	result, err := conn.client.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: args,
		},
	})

	latency := time.Since(start).Seconds() * 1000

	cp.mu.RLock()
	pool := cp.pools[serverName]
	cp.mu.RUnlock()

	if pool != nil {
		atomic.AddInt64(&pool.metrics.TotalRequests, 1)
		metrics := pool.GetMetrics()
		if metrics.TotalRequests > 0 {
			newAvg := (metrics.AvgLatencyMs*float64(metrics.TotalRequests-1) + latency) / float64(metrics.TotalRequests)
			pool.mu.Lock()
			pool.metrics.AvgLatencyMs = newAvg
			pool.mu.Unlock()
		}
	}

	if err != nil {
		if pool != nil {
			atomic.AddInt64(&pool.metrics.Errors, 1)
		}
		conn.SetHealthy(false)
		return nil, err
	}

	return result, nil
}

func (cp *ConnectionPool) ListTools(ctx context.Context, serverName string) ([]mcp.Tool, error) {
	conn, err := cp.GetClient(ctx, serverName)
	if err != nil {
		return nil, fmt.Errorf("get client: %w", err)
	}
	defer cp.ReturnClient(serverName, conn)

	resp, err := conn.GetClient().ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, err
	}
	return resp.Tools, nil
}

func (cp *ConnectionPool) GetMetrics(serverName string) (PoolMetrics, error) {
	cp.mu.RLock()
	pool, exists := cp.pools[serverName]
	cp.mu.RUnlock()

	if !exists {
		return PoolMetrics{}, fmt.Errorf("pool: server %s not found", serverName)
	}

	return pool.GetMetrics(), nil
}

func (cp *ConnectionPool) GetAllMetrics() map[string]PoolMetrics {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	allMetrics := make(map[string]PoolMetrics)
	for name, pool := range cp.pools {
		allMetrics[name] = pool.GetMetrics()
	}

	return allMetrics
}

func (cp *ConnectionPool) Close() error {
	cp.cancel()

	cp.mu.Lock()
	cp.stopped = true
	cp.mu.Unlock()

	cp.mu.RLock()
	defer cp.mu.RUnlock()

	var errs []error
	for name, pool := range cp.pools {
		if err := pool.Close(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}

	cp.pools = make(map[string]*ServerPool)

	if len(errs) > 0 {
		return fmt.Errorf("close errors: %v", errs)
	}

	return nil
}

func (cp *ConnectionPool) HasServer(name string) bool {
	cp.mu.RLock()
	_, exists := cp.pools[name]
	cp.mu.RUnlock()
	return exists
}

func (cp *ConnectionPool) ListServers() []string {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	names := make([]string, 0, len(cp.pools))
	for name := range cp.pools {
		names = append(names, name)
	}
	return names
}