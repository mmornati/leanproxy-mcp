package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
)

type SSEServer struct {
	name      string
	config    *migrate.ServerConfig
	mcpClient *client.Client
	state     ServerState
	mu        sync.RWMutex
	logger    *slog.Logger
	initOnce  sync.Once
	initErr   error
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

func (s *SSEServer) Initialize(ctx context.Context) error {
	s.initOnce.Do(func() {
		headers := make(map[string]string)
		if s.config.HTTP != nil && s.config.HTTP.Headers != nil {
			for k, v := range s.config.HTTP.Headers {
				headers[k] = v
			}
		}

		baseURL := s.config.HTTP.URL
		s.logger.Debug("sse_pool: creating SSE client", "server", s.name, "url", baseURL)

		c, err := client.NewSSEMCPClient(baseURL, client.WithHeaders(headers))
		if err != nil {
			s.initErr = fmt.Errorf("sse_pool: create client: %w", err)
			s.setState(StateError)
			return
		}

		s.logger.Debug("sse_pool: starting SSE client", "server", s.name)
		startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		if err := c.Start(startCtx); err != nil {
			s.initErr = fmt.Errorf("sse_pool: start: %w", err)
			s.setState(StateError)
			c.Close()
			return
		}

		s.logger.Debug("sse_pool: initializing SSE client", "server", s.name)
		initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		_, err = c.Initialize(initCtx, mcp.InitializeRequest{
			Params: mcp.InitializeParams{
				ProtocolVersion: "2024-11-05",
				Capabilities:    mcp.ClientCapabilities{},
				ClientInfo: mcp.Implementation{
					Name:    "leanproxy-mcp",
					Version: "1.0.0",
				},
			},
		})
		if err != nil {
			s.initErr = fmt.Errorf("sse_pool: initialize: %w", err)
			s.setState(StateError)
			c.Close()
			return
		}

		s.mcpClient = c
		s.setState(StateRunning)
		s.logger.Info("sse_pool: server initialized", "server", s.name)
	})

	return s.initErr
}

func (s *SSEServer) Close() error {
	if s.mcpClient != nil {
		s.mcpClient.Close()
	}
	s.setState(StateStopped)
	return nil
}

func (s *SSEServer) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	if s.mcpClient == nil {
		return nil, fmt.Errorf("sse_pool: client not initialized")
	}

	resp, err := s.mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("sse_pool: list tools: %w", err)
	}

	return resp.Tools, nil
}

func (s *SSEServer) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	if s.mcpClient == nil {
		return nil, fmt.Errorf("sse_pool: client not initialized")
	}

	return s.mcpClient.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	})
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

	if server.mcpClient == nil {
		initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := server.Initialize(initCtx); err != nil {
			return nil, err
		}
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

	server.setState(StateRunning)
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
