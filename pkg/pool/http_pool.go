package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
)

type HTTPClientServer struct {
	name      string
	config    *migrate.ServerConfig
	mcpClient *client.Client
	state     ServerState
	mu        sync.RWMutex
	logger    *slog.Logger
	initOnce  sync.Once
	initErr   error
	oauthOpts []transport.StreamableHTTPCOption
}

func NewHTTPClientServer(name string, config *migrate.ServerConfig, logger *slog.Logger) *HTTPClientServer {
	if logger == nil {
		logger = slog.Default()
	}

	var oauthOpts []transport.StreamableHTTPCOption
	if config.HTTP != nil && config.HTTP.Auth != nil {
		authCfg := config.HTTP.Auth
		authType := strings.ToLower(strings.TrimSpace(authCfg.Type))

		switch authType {
		case "bearer":
			if authCfg.ClientSecret == "" {
				logger.Warn("http_pool: bearer auth configured but client_secret is empty", "server", name)
				break
			}
			oauthOpts = append(oauthOpts, transport.WithHTTPHeaders(map[string]string{
				"Authorization": "Bearer " + authCfg.ClientSecret,
			}))
		case "oauth2":
			if authCfg.ClientID == "" || authCfg.ClientSecret == "" {
				logger.Warn("http_pool: oauth2 auth configured but client_id or client_secret is empty", "server", name)
				break
			}
			oauthCfg := transport.OAuthConfig{
				ClientID:     authCfg.ClientID,
				ClientSecret: authCfg.ClientSecret,
				Scopes:       authCfg.Scopes,
			}
			oauthOpts = append(oauthOpts, transport.WithHTTPOAuth(oauthCfg))
		default:
			if authType != "" {
				logger.Warn("http_pool: unknown auth type, skipping authentication", "server", name, "auth_type", authCfg.Type)
			}
		}
	}

	return &HTTPClientServer{
		name:     name,
		config:   config,
		state:   StateStarting,
		logger:  logger,
		oauthOpts: oauthOpts,
	}
}

func (s *HTTPClientServer) getState() ServerState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *HTTPClientServer) setState(state ServerState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

func (s *HTTPClientServer) Initialize(ctx context.Context) error {
	s.initOnce.Do(func() {
		baseURL := s.config.HTTP.URL
		s.logger.Debug("http_pool: creating StreamableHTTP client", "server", s.name, "url", baseURL)

		headers := make(map[string]string)
		if s.config.HTTP != nil && s.config.HTTP.Headers != nil {
			for k, v := range s.config.HTTP.Headers {
				headers[k] = v
			}
		}

		opts := []transport.StreamableHTTPCOption{transport.WithHTTPHeaders(headers)}
		opts = append(opts, s.oauthOpts...)

		c, err := client.NewStreamableHttpClient(baseURL, opts...)
		if err != nil {
			s.initErr = fmt.Errorf("http_pool: create client: %w", err)
			s.setState(StateError)
			return
		}

		s.logger.Debug("http_pool: initializing StreamableHTTP client", "server", s.name)
		startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		if err := c.Start(startCtx); err != nil {
			s.initErr = fmt.Errorf("http_pool: start: %w", err)
			s.setState(StateError)
			c.Close()
			return
		}

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
			s.initErr = fmt.Errorf("http_pool: initialize: %w", err)
			s.setState(StateError)
			c.Close()
			return
		}

		s.mcpClient = c
		s.setState(StateRunning)
		s.logger.Info("http_pool: server initialized", "server", s.name)
	})

	return s.initErr
}

func (s *HTTPClientServer) Close() error {
	if s.mcpClient != nil {
		s.mcpClient.Close()
	}
	s.setState(StateStopped)
	return nil
}

func (s *HTTPClientServer) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	if s.mcpClient == nil {
		return nil, fmt.Errorf("http_pool: client not initialized")
	}

	resp, err := s.mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("http_pool: list tools: %w", err)
	}

	return resp.Tools, nil
}

func (s *HTTPClientServer) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	if s.mcpClient == nil {
		return nil, fmt.Errorf("http_pool: client not initialized")
	}

	return s.mcpClient.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	})
}

type HTTPClientPool struct {
	servers map[string]*HTTPClientServer
	mu      sync.RWMutex
	logger  *slog.Logger
	ctx     context.Context
	cancel  context.CancelFunc
}

func NewHTTPClientPool(logger *slog.Logger) *HTTPClientPool {
	if logger == nil {
		logger = slog.Default()
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &HTTPClientPool{
		servers: make(map[string]*HTTPClientServer),
		logger:  logger,
		ctx:     ctx,
		cancel:  cancel,
	}
}

func (p *HTTPClientPool) StartServer(ctx context.Context, config *migrate.ServerConfig) error {
	if config.HTTP == nil || config.HTTP.URL == "" {
		return fmt.Errorf("http_pool: HTTP config is required")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.servers[config.Name]; exists {
		p.logger.Debug("http_pool: server already exists", "name", config.Name)
		return nil
	}

	server := NewHTTPClientServer(config.Name, config, p.logger)
	p.servers[config.Name] = server

	p.logger.Info("http_pool: server created", "name", config.Name, "url", config.HTTP.URL)

	go func() {
		initCtx, cancel := context.WithTimeout(p.ctx, 30*time.Second)
		defer cancel()
		if err := server.Initialize(initCtx); err != nil {
			p.logger.Warn("http_pool: failed to initialize server", "name", config.Name, "error", err)
		}
	}()

	return nil
}

func (p *HTTPClientPool) ListServers() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	names := make([]string, 0, len(p.servers))
	for name := range p.servers {
		names = append(names, name)
	}
	return names
}

func (p *HTTPClientPool) GetServerState(name string) (ServerState, error) {
	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("http_pool: server %s not found", name)
	}

	return server.getState(), nil
}

func (p *HTTPClientPool) SendRequest(ctx context.Context, serverName string, req *proxy.JSONRPCRequest, timeout time.Duration) (*proxy.JSONRPCResponse, error) {
	p.mu.RLock()
	server, exists := p.servers[serverName]
	p.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("http_pool: server %s not found", serverName)
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

func (p *HTTPClientPool) SendRequestToServer(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration) (*Response, error) {
	return p.SendRequestToServerWithID(ctx, name, method, params, timeout, 1)
}

func (p *HTTPClientPool) SendRequestToServerWithID(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration, id int) (*Response, error) {
	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("http_pool: server %s not found", name)
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
			return nil, fmt.Errorf("http_pool: invalid tools/call params: %w", err)
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

func (p *HTTPClientPool) SendServerNotification(ctx context.Context, name string, method string, params map[string]interface{}) error {
	return nil
}

func (p *HTTPClientPool) RestartServer(ctx context.Context, name string) error {
	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return fmt.Errorf("http_pool: server %s not found", name)
	}

	server.setState(StateRunning)
	p.logger.Info("http_pool: server restarted", "name", name)
	return nil
}

func (p *HTTPClientPool) Close() error {
	p.cancel()

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, server := range p.servers {
		server.Close()
	}
	p.servers = make(map[string]*HTTPClientServer)
	return nil
}

func (p *HTTPClientPool) ServerCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.servers)
}

func (p *HTTPClientPool) HasServer(name string) bool {
	p.mu.RLock()
	_, exists := p.servers[name]
	p.mu.RUnlock()
	return exists
}

type UnifiedPool struct {
	stdioPool *StdioPool
	httpPool  *HTTPClientPool
	ssePool   *SSEPool
	logger    *slog.Logger
}

func NewUnifiedPool(stdio *StdioPool, http *HTTPClientPool, sse *SSEPool, logger *slog.Logger) *UnifiedPool {
	return &UnifiedPool{
		stdioPool: stdio,
		httpPool:  http,
		ssePool:   sse,
		logger:    logger,
	}
}

func (p *UnifiedPool) ListServers() []string {
	var servers []string
	servers = append(servers, p.stdioPool.ListServers()...)
	servers = append(servers, p.httpPool.ListServers()...)
	if p.ssePool != nil {
		servers = append(servers, p.ssePool.ListServers()...)
	}
	return servers
}

func (p *UnifiedPool) GetServerState(name string) (ServerState, error) {
	state, err := p.stdioPool.GetServerState(name)
	if err == nil {
		return state, nil
	}
	state, err = p.httpPool.GetServerState(name)
	if err == nil {
		return state, nil
	}
	if p.ssePool != nil {
		return p.ssePool.GetServerState(name)
	}
	return "", fmt.Errorf("server %s not found in any pool", name)
}

func (p *UnifiedPool) SendRequestToServer(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration) (*Response, error) {
	if p.stdioPool.HasServer(name) {
		resp, err := p.stdioPool.SendRequestToServer(ctx, name, method, params, timeout)
		if err == nil {
			return resp, nil
		}
		return nil, err
	}
	if p.httpPool.HasServer(name) {
		resp, err := p.httpPool.SendRequestToServer(ctx, name, method, params, timeout)
		if err == nil {
			return resp, nil
		}
		return nil, err
	}
	if p.ssePool != nil && p.ssePool.HasServer(name) {
		return p.ssePool.SendRequestToServer(ctx, name, method, params, timeout)
	}
	return nil, fmt.Errorf("server %s not found in any pool", name)
}

func (p *UnifiedPool) RestartServer(ctx context.Context, name string) error {
	err := p.stdioPool.RestartServer(ctx, name)
	if err == nil {
		return nil
	}
	err = p.httpPool.RestartServer(ctx, name)
	if err == nil {
		return nil
	}
	if p.ssePool != nil {
		return p.ssePool.RestartServer(ctx, name)
	}
	return fmt.Errorf("server %s not found", name)
}

func (p *UnifiedPool) SendRequestToServerWithID(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration, id int) (*Response, error) {
	if p.stdioPool.HasServer(name) {
		resp, err := p.stdioPool.SendRequestToServerWithID(ctx, name, method, params, timeout, id)
		if err == nil {
			return resp, nil
		}
		return nil, err
	}
	if p.httpPool.HasServer(name) {
		resp, err := p.httpPool.SendRequestToServerWithID(ctx, name, method, params, timeout, id)
		if err == nil {
			return resp, nil
		}
		return nil, err
	}
	if p.ssePool != nil && p.ssePool.HasServer(name) {
		return p.ssePool.SendRequestToServerWithID(ctx, name, method, params, timeout, id)
	}
	return nil, fmt.Errorf("server %s not found in any pool", name)
}

func (p *UnifiedPool) SendServerNotification(ctx context.Context, name string, method string, params map[string]interface{}) error {
	err := p.stdioPool.SendServerNotification(ctx, name, method, params)
	if err == nil {
		return nil
	}
	err = p.httpPool.SendServerNotification(ctx, name, method, params)
	if err == nil {
		return nil
	}
	if p.ssePool != nil {
		return p.ssePool.SendServerNotification(ctx, name, method, params)
	}
	return fmt.Errorf("server %s not found", name)
}

func (p *UnifiedPool) Close() error {
	p.stdioPool.Close()
	p.httpPool.Close()
	if p.ssePool != nil {
		p.ssePool.Close()
	}
	return nil
}

func (p *UnifiedPool) SendRequest(ctx context.Context, serverName string, req *proxy.JSONRPCRequest, timeout time.Duration) (*proxy.JSONRPCResponse, error) {
	resp, err := p.stdioPool.SendRequest(ctx, serverName, req, timeout)
	if err == nil {
		return resp, nil
	}
	resp, err = p.httpPool.SendRequest(ctx, serverName, req, timeout)
	if err == nil {
		return resp, nil
	}
	if p.ssePool != nil {
		return p.ssePool.SendRequest(ctx, serverName, req, timeout)
	}
	return nil, fmt.Errorf("server %s not found", serverName)
}
