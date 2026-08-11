package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
)

type SSEServer struct {
	name        string
	config      *migrate.ServerConfig
	mcpClient   *client.Client
	state       ServerState
	mu          sync.RWMutex
	reconnectMu sync.Mutex
	logger      *slog.Logger
}

func NewSSEServer(name string, config *migrate.ServerConfig, logger *slog.Logger) *SSEServer {
	if logger == nil {
		logger = slog.Default()
	}

	return &SSEServer{
		name:   name,
		config: config,
		state:  StateStarting,
		logger: logger,
	}
}

func (s *SSEServer) getState() ServerState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *SSEServer) setState(state ServerState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

func (s *SSEServer) buildClient() (*client.Client, error) {
	headers := make(map[string]string)
	if s.config.HTTP != nil && s.config.HTTP.Headers != nil {
		for k, v := range s.config.HTTP.Headers {
			headers[k] = v
		}
	}

	baseURL := s.config.HTTP.URL
	s.logger.Debug("sse_pool: creating SSE client", "server", s.name, "url", baseURL)

	// Bound the TCP connect phase: mcp-go's default http.Client has no dial
	// timeout, so a blackholed endpoint would block Start (holding
	// reconnectMu) for the OS TCP timeout. No http.Client.Timeout is set on
	// purpose — that would also kill the long-lived SSE stream.
	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		},
	}

	c, err := client.NewSSEMCPClient(baseURL, client.WithHeaders(headers), client.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("sse_pool: create client: %w", err)
	}

	c.OnConnectionLost(func(err error) {
		// mcp-go invokes this handler on ANY read-loop exit, including the
		// deliberate Close() of a previous client generation, and it does so
		// asynchronously. Ignore callbacks from any client that is no longer
		// the current one, or a stale notification would clobber the state of
		// a freshly reconnected client and cause a reconnect storm.
		s.mu.RLock()
		current := s.mcpClient
		s.mu.RUnlock()
		if current != c {
			return
		}
		s.logger.Warn("sse_pool: connection lost", "server", s.name, "error", err)
		s.setState(StateDisconnected)
	})

	return c, nil
}

func (s *SSEServer) closeClient() {
	s.mu.Lock()
	c := s.mcpClient
	s.mcpClient = nil
	s.mu.Unlock()
	if c != nil {
		c.Close()
	}
}

func (s *SSEServer) ensureConnected(ctx context.Context) (*client.Client, error) {
	s.reconnectMu.Lock()
	defer s.reconnectMu.Unlock()

	// Read the client and state under a single lock and use the locked local
	// from here on: returning s.mcpClient after unlocking races a concurrent
	// Close() (which does not hold reconnectMu) nil-ing and closing it.
	s.mu.RLock()
	current := s.mcpClient
	state := s.state
	s.mu.RUnlock()

	if state == StateStopped {
		// A deliberately closed server must never be resurrected by an
		// in-flight request racing shutdown.
		return nil, fmt.Errorf("sse_pool: server %s is closed", s.name)
	}

	if current != nil && state != StateDisconnected && state != StateError {
		return current, nil
	}

	s.closeClient()

	c, err := s.buildClient()
	if err != nil {
		s.setState(StateError)
		return nil, err
	}

	s.logger.Debug("sse_pool: starting SSE client", "server", s.name)
	// Start's context must outlive this call: mcp-go's SSE transport binds its
	// reader loop to it, so canceling it here would drop the connection right
	// after Initialize. Start() enforces its own connect timeout internally.
	if err := c.Start(context.WithoutCancel(ctx)); err != nil {
		s.setState(StateError)
		c.Close()
		return nil, fmt.Errorf("sse_pool: start: %w", err)
	}

	s.logger.Debug("sse_pool: initializing SSE client", "server", s.name)
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := c.Initialize(initCtx, mcpInitializeRequest()); err != nil {
		s.setState(StateError)
		c.Close()
		return nil, fmt.Errorf("sse_pool: initialize: %w", err)
	}

	s.mu.Lock()
	s.mcpClient = c
	s.mu.Unlock()
	s.setState(StateRunning)
	s.logger.Info("sse_pool: server initialized", "server", s.name)
	return c, nil
}

func (s *SSEServer) Initialize(ctx context.Context) error {
	_, err := s.ensureConnected(ctx)
	return err
}

func (s *SSEServer) Close() error {
	s.closeClient()
	s.setState(StateStopped)
	return nil
}

func (s *SSEServer) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	c, err := s.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil && isTransportError(err) {
		s.logger.Warn("sse_pool: list tools failed, reconnecting", "server", s.name, "error", err)
		s.setState(StateDisconnected)
		c, rerr := s.ensureConnected(ctx)
		if rerr != nil {
			return nil, fmt.Errorf("sse_pool: reconnect: %w", rerr)
		}
		resp, err = c.ListTools(ctx, mcp.ListToolsRequest{})
	}
	if err != nil {
		return nil, fmt.Errorf("sse_pool: list tools: %w", err)
	}

	return resp.Tools, nil
}

func (s *SSEServer) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	c, err := s.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}

	call := func(c *client.Client) (*mcp.CallToolResult, error) {
		return c.CallTool(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      name,
				Arguments: args,
			},
		})
	}

	result, err := call(c)
	if err != nil && isTransportError(err) {
		s.logger.Warn("sse_pool: call tool failed, reconnecting", "server", s.name, "tool", name, "error", err)
		s.setState(StateDisconnected)
		c, rerr := s.ensureConnected(ctx)
		if rerr != nil {
			return nil, fmt.Errorf("sse_pool: reconnect: %w", rerr)
		}
		result, err = call(c)
	}
	if err != nil {
		return nil, fmt.Errorf("sse_pool: call tool: %w", err)
	}

	return result, nil
}

type SSEPool struct {
	servers map[string]*SSEServer
	mu      sync.RWMutex
	logger  *slog.Logger
	ctx     context.Context
	cancel  context.CancelFunc
}

func NewSSEPool(logger *slog.Logger) *SSEPool {
	if logger == nil {
		logger = slog.Default()
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &SSEPool{
		servers: make(map[string]*SSEServer),
		logger:  logger,
		ctx:     ctx,
		cancel:  cancel,
	}
}

func (p *SSEPool) StartServer(ctx context.Context, config *migrate.ServerConfig) error {
	if config.HTTP == nil || config.HTTP.URL == "" {
		return fmt.Errorf("sse_pool: HTTP config is required")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.servers[config.Name]; exists {
		p.logger.Debug("sse_pool: server already exists", "name", config.Name)
		return nil
	}

	server := NewSSEServer(config.Name, config, p.logger)
	p.servers[config.Name] = server

	p.logger.Info("sse_pool: server created", "name", config.Name, "url", config.HTTP.URL)

	go func() {
		initCtx, cancel := context.WithTimeout(p.ctx, 30*time.Second)
		defer cancel()
		if err := server.Initialize(initCtx); err != nil {
			p.logger.Warn("sse_pool: failed to initialize server", "name", config.Name, "error", err)
		}
	}()

	return nil
}

func (p *SSEPool) ListServers() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	names := make([]string, 0, len(p.servers))
	for name := range p.servers {
		names = append(names, name)
	}
	return names
}

func (p *SSEPool) GetServerState(name string) (ServerState, error) {
	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("sse_pool: server %s not found", name)
	}

	return server.getState(), nil
}

func (p *SSEPool) SendRequest(ctx context.Context, serverName string, req *proxy.JSONRPCRequest, timeout time.Duration) (*proxy.JSONRPCResponse, error) {
	p.mu.RLock()
	server, exists := p.servers[serverName]
	p.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("sse_pool: server %s not found", serverName)
	}

	toolArgs := make(map[string]interface{})
	if req.Params != nil {
		_ = json.Unmarshal(req.Params, &toolArgs)
	}

	result, err := server.CallTool(ctx, req.Method, toolArgs)
	if err != nil {
		return nil, err
	}

	resultBytes, _ := json.Marshal(result)
	return &proxy.JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  resultBytes,
		ID:      req.ID,
	}, nil
}

func (p *SSEPool) SendRequestToServer(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration) (*Response, error) {
	return p.SendRequestToServerWithID(ctx, name, method, params, timeout, 1)
}

func (p *SSEPool) SendRequestToServerWithID(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration, id int) (*Response, error) {
	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("sse_pool: server %s not found", name)
	}

	if _, err := server.ensureConnected(ctx); err != nil {
		return nil, err
	}

	if method == "tools/list" {
		tools, err := server.ListTools(ctx)
		if err != nil {
			return nil, err
		}
		result := mcp.ListToolsResult{Tools: tools}
		resultBytes, _ := json.Marshal(result)
		return &Response{
			Result: resultBytes,
			ID:     id,
		}, nil
	}

	if method == "tools/call" {
		var toolParams struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(params, &toolParams); err != nil {
			return nil, fmt.Errorf("sse_pool: invalid tools/call params: %w", err)
		}
		result, err := server.CallTool(ctx, toolParams.Name, toolParams.Arguments)
		if err != nil {
			return nil, err
		}
		resultBytes, _ := json.Marshal(result)
		return &Response{
			Result: resultBytes,
			ID:     id,
		}, nil
	}

	toolArgs := make(map[string]interface{})
	if len(params) > 0 {
		_ = json.Unmarshal(params, &toolArgs)
	}

	result, err := server.CallTool(ctx, method, toolArgs)
	if err != nil {
		return nil, err
	}

	resultBytes, _ := json.Marshal(result)
	return &Response{
		Result: resultBytes,
		ID:     id,
	}, nil
}

func (p *SSEPool) SendServerNotification(ctx context.Context, name string, method string, params map[string]interface{}) error {
	return nil
}

func (p *SSEPool) RestartServer(ctx context.Context, name string) error {
	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return fmt.Errorf("sse_pool: server %s not found", name)
	}

	server.setState(StateDisconnected)
	if err := server.Initialize(ctx); err != nil {
		p.logger.Error("sse_pool: restart failed", "name", name, "error", err)
		return err
	}
	p.logger.Info("sse_pool: server restarted", "name", name)
	return nil
}

func (p *SSEPool) IsServerMCPInitialized(name string) bool {
	return true
}

func (p *SSEPool) MarkServerMCPInitialized(name string) {
}

func (p *SSEPool) Close() error {
	p.cancel()

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, server := range p.servers {
		server.Close()
	}
	p.servers = make(map[string]*SSEServer)
	return nil
}

func (p *SSEPool) ServerCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.servers)
}

func (p *SSEPool) HasServer(name string) bool {
	p.mu.RLock()
	_, exists := p.servers[name]
	p.mu.RUnlock()
	return exists
}
