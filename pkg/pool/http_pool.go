package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
	"github.com/mmornati/leanproxy-mcp/pkg/telemetry"
)

type HTTPClientServer struct {
	name        string
	config      *migrate.ServerConfig
	mcpClient   *client.Client
	state       ServerState
	mu          sync.RWMutex
	reconnectMu sync.Mutex
	logger      *slog.Logger
	// initResult is the InitializeResult of the current connection, from
	// mcp-go's own Initialize (the handshake is never sent as a tool call).
	initResult atomic.Pointer[InitializeResult]
	// generation counts successful (re)connections.
	generation atomic.Uint64
	// events receives this server's lifecycle events (set by the pool).
	events    *eventHub
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
		name:      name,
		config:    config,
		state:     StateStarting,
		logger:    logger,
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

func (s *HTTPClientServer) buildClient() (*client.Client, error) {
	baseURL := s.config.HTTP.URL
	s.logger.Debug("http_pool: creating StreamableHTTP client", "server", s.name, "url", baseURL)

	headers := make(map[string]string)
	if s.config.HTTP != nil && s.config.HTTP.Headers != nil {
		for k, v := range s.config.HTTP.Headers {
			headers[k] = v
		}
	}

	// A basic http.Client whose Transport injects the W3C `traceparent`
	// header from the calling request's context (issue #317). This is
	// always installed, telemetry enabled or not: with telemetry disabled
	// the global propagator is the SDK's default no-op, so injection is a
	// cheap no-op (see telemetry.WrapTransport).
	httpClient := &http.Client{Transport: telemetry.WrapTransport(http.DefaultTransport)}
	opts := []transport.StreamableHTTPCOption{
		transport.WithHTTPHeaders(headers),
		transport.WithHTTPBasicClient(httpClient),
	}
	opts = append(opts, s.oauthOpts...)

	c, err := client.NewStreamableHttpClient(baseURL, opts...)
	if err != nil {
		return nil, fmt.Errorf("http_pool: create client: %w", err)
	}
	return c, nil
}

func (s *HTTPClientServer) closeClient() {
	s.mu.Lock()
	c := s.mcpClient
	s.mcpClient = nil
	s.mu.Unlock()
	if c != nil {
		c.Close()
	}
}

func (s *HTTPClientServer) ensureConnected(ctx context.Context) (*client.Client, error) {
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
		return nil, fmt.Errorf("http_pool: server %s is closed", s.name)
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
	c.OnNotification(func(n mcp.JSONRPCNotification) {
		if kind, ok := remoteNotificationEvent(n.Method); ok {
			s.events.emit(ServerEvent{Server: s.name, Kind: kind, Generation: s.generation.Load()})
		}
	})

	s.logger.Debug("http_pool: starting StreamableHTTP client", "server", s.name)
	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := c.Start(startCtx); err != nil {
		s.setState(StateError)
		c.Close()
		return nil, fmt.Errorf("http_pool: start: %w", err)
	}

	s.logger.Debug("http_pool: initializing StreamableHTTP client", "server", s.name)
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	initRes, err := c.Initialize(initCtx, mcpInitializeRequest())
	if err != nil {
		s.setState(StateError)
		c.Close()
		return nil, fmt.Errorf("http_pool: initialize: %w", err)
	}

	s.mu.Lock()
	s.mcpClient = c
	s.mu.Unlock()
	s.setState(StateRunning)
	gen := s.generation.Add(1)
	s.initResult.Store(fromMCPInitializeResult(initRes, gen))
	s.events.emit(ServerEvent{Server: s.name, Kind: EventSessionStarted, Generation: gen})
	s.logger.Info("http_pool: server initialized", "server", s.name)
	return c, nil
}

func (s *HTTPClientServer) Initialize(ctx context.Context) error {
	_, err := s.ensureConnected(ctx)
	return err
}

// sessionMethod answers the session-level methods the pool owns instead of
// forwarding them as a tool call: `initialize` returns the stored result of
// the connection's handshake (performed by mcp-go's Initialize) and `ping`
// is a real MCP ping. handled is false for every other method.
func (s *HTTPClientServer) sessionMethod(ctx context.Context, c *client.Client, method string) (json.RawMessage, bool, error) {
	switch method {
	case methodInitialize:
		return initializeResultJSON(s.initResult.Load()), true, nil
	case "ping":
		if err := c.Ping(ctx); err != nil {
			return nil, true, fmt.Errorf("http_pool: ping: %w", err)
		}
		return json.RawMessage(`{}`), true, nil
	default:
		return nil, false, nil
	}
}

func (s *HTTPClientServer) Close() error {
	s.closeClient()
	s.setState(StateStopped)
	return nil
}

func (s *HTTPClientServer) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	c, err := s.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil && isTransportError(err) {
		s.logger.Warn("http_pool: list tools failed, reconnecting", "server", s.name, "error", err)
		s.setState(StateDisconnected)
		c, rerr := s.ensureConnected(ctx)
		if rerr != nil {
			return nil, fmt.Errorf("http_pool: reconnect: %w", rerr)
		}
		resp, err = c.ListTools(ctx, mcp.ListToolsRequest{})
	}
	if err != nil {
		return nil, fmt.Errorf("http_pool: list tools: %w", err)
	}

	return resp.Tools, nil
}

func (s *HTTPClientServer) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
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
		s.logger.Warn("http_pool: call tool failed, reconnecting", "server", s.name, "tool", name, "error", err)
		s.setState(StateDisconnected)
		c, rerr := s.ensureConnected(ctx)
		if rerr != nil {
			return nil, fmt.Errorf("http_pool: reconnect: %w", rerr)
		}
		result, err = call(c)
	}
	if err != nil {
		return nil, fmt.Errorf("http_pool: call tool: %w", err)
	}

	return result, nil
}

type HTTPClientPool struct {
	servers      map[string]*HTTPClientServer
	mu           sync.RWMutex
	logger       *slog.Logger
	ctx          context.Context
	cancel       context.CancelFunc
	rateLimiters *serverRateLimiters
	events       eventHub
}

func NewHTTPClientPool(logger *slog.Logger) *HTTPClientPool {
	if logger == nil {
		logger = slog.Default()
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &HTTPClientPool{
		servers:      make(map[string]*HTTPClientServer),
		logger:       logger,
		ctx:          ctx,
		cancel:       cancel,
		rateLimiters: newServerRateLimiters(),
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
	server.events = &p.events
	p.servers[config.Name] = server

	// Off by default; only enabled when the config sets rate_limit with a
	// positive requests_per_second (see pkg/pool/ratelimit.go).
	p.rateLimiters.set(config.Name, config.RateLimit)

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

// GetServerTransport reports the transport this server is configured with.
// It always returns "http" for an HTTPClientPool member.
func (p *HTTPClientPool) GetServerTransport(name string) (string, error) {
	p.mu.RLock()
	_, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("http_pool: server %s not found", name)
	}
	return "http", nil
}

func (p *HTTPClientPool) SendRequest(ctx context.Context, serverName string, req *proxy.JSONRPCRequest, timeout time.Duration) (*proxy.JSONRPCResponse, error) {
	p.mu.RLock()
	server, exists := p.servers[serverName]
	p.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("http_pool: server %s not found", serverName)
	}

	ctx, cancel := boundedContext(ctx, timeout)
	defer cancel()
	if err := p.rateLimiters.wait(ctx, serverName, req.Method); err != nil {
		return nil, fmt.Errorf("http_pool: %w", err)
	}

	c, err := server.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	if result, handled, err := server.sessionMethod(ctx, c, req.Method); handled {
		if err != nil {
			return nil, err
		}
		return &proxy.JSONRPCResponse{JSONRPC: "2.0", Result: result, ID: req.ID}, nil
	}
	if forwardsRaw(req.Method) {
		result, rpcErr, err := relayRaw(ctx, server, "http_pool", req.Method, req.Params)
		if err != nil {
			return nil, err
		}
		return &proxy.JSONRPCResponse{JSONRPC: "2.0", Result: result, Error: rpcErr, ID: req.ID}, nil
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

	ctx, cancel := boundedContext(ctx, timeout)
	defer cancel()
	if err := p.rateLimiters.wait(ctx, name, method); err != nil {
		return nil, fmt.Errorf("http_pool: %w", err)
	}

	c, err := server.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}

	if result, handled, err := server.sessionMethod(ctx, c, method); handled {
		if err != nil {
			return nil, err
		}
		return &Response{Result: result, ID: id}, nil
	}

	if forwardsRaw(method) {
		result, rpcErr, err := relayRaw(ctx, server, "http_pool", method, params)
		if err != nil {
			return nil, err
		}
		return &Response{Result: result, Error: rpcErr, ID: id}, nil
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

	server.setState(StateDisconnected)
	if err := server.Initialize(ctx); err != nil {
		p.logger.Error("http_pool: restart failed", "name", name, "error", err)
		return err
	}
	p.logger.Info("http_pool: server restarted", "name", name)
	return nil
}

// SetServerEventHandler registers the handler that receives every server's
// lifecycle events (new connection, tools/list_changed).
func (p *HTTPClientPool) SetServerEventHandler(fn ServerEventHandler) {
	p.events.set(fn)
}

// ServerInitializeResult returns the InitializeResult of the named server's
// current connection.
func (p *HTTPClientPool) ServerInitializeResult(name string) (*InitializeResult, bool) {
	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()
	if !exists {
		return nil, false
	}
	res := server.initResult.Load()
	return res, res != nil
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

// owningTransport identifies which pool a server name belongs to, resolved
// once per call by membership (not by trying pools in order and falling
// through on error) so a real error from the owning pool - a timeout, a
// rate limit, an upstream tool error - is returned to the caller instead of
// being masked by "not found" errors from pools that were never asked about
// that server.
type owningTransport int

const (
	transportNone owningTransport = iota
	transportStdio
	transportHTTP
	transportSSE
)

func (p *UnifiedPool) resolveTransport(name string) owningTransport {
	if p.stdioPool.HasServer(name) {
		return transportStdio
	}
	if p.httpPool.HasServer(name) {
		return transportHTTP
	}
	if p.ssePool != nil && p.ssePool.HasServer(name) {
		return transportSSE
	}
	return transportNone
}

func (p *UnifiedPool) GetServerState(name string) (ServerState, error) {
	switch p.resolveTransport(name) {
	case transportStdio:
		state, err := p.stdioPool.GetServerState(name)
		if err != nil {
			return "", fmt.Errorf("unified_pool: %w", err)
		}
		return state, nil
	case transportHTTP:
		state, err := p.httpPool.GetServerState(name)
		if err != nil {
			return "", fmt.Errorf("unified_pool: %w", err)
		}
		return state, nil
	case transportSSE:
		state, err := p.ssePool.GetServerState(name)
		if err != nil {
			return "", fmt.Errorf("unified_pool: %w", err)
		}
		return state, nil
	default:
		return "", fmt.Errorf("server %s not found in any pool", name)
	}
}

// GetServerTransport reports which transport ("stdio", "http" or "sse")
// owns the named server.
func (p *UnifiedPool) GetServerTransport(name string) (string, error) {
	switch p.resolveTransport(name) {
	case transportStdio:
		return p.stdioPool.GetServerTransport(name)
	case transportHTTP:
		return p.httpPool.GetServerTransport(name)
	case transportSSE:
		return p.ssePool.GetServerTransport(name)
	default:
		return "", fmt.Errorf("server %s not found in any pool", name)
	}
}

func (p *UnifiedPool) SendRequestToServer(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration) (*Response, error) {
	switch p.resolveTransport(name) {
	case transportStdio:
		resp, err := p.stdioPool.SendRequestToServer(ctx, name, method, params, timeout)
		if err != nil {
			return nil, fmt.Errorf("unified_pool: %w", err)
		}
		return resp, nil
	case transportHTTP:
		resp, err := p.httpPool.SendRequestToServer(ctx, name, method, params, timeout)
		if err != nil {
			return nil, fmt.Errorf("unified_pool: %w", err)
		}
		return resp, nil
	case transportSSE:
		resp, err := p.ssePool.SendRequestToServer(ctx, name, method, params, timeout)
		if err != nil {
			return nil, fmt.Errorf("unified_pool: %w", err)
		}
		return resp, nil
	default:
		return nil, fmt.Errorf("server %s not found in any pool", name)
	}
}

func (p *UnifiedPool) RestartServer(ctx context.Context, name string) error {
	switch p.resolveTransport(name) {
	case transportStdio:
		if err := p.stdioPool.RestartServer(ctx, name); err != nil {
			return fmt.Errorf("unified_pool: %w", err)
		}
		return nil
	case transportHTTP:
		if err := p.httpPool.RestartServer(ctx, name); err != nil {
			return fmt.Errorf("unified_pool: %w", err)
		}
		return nil
	case transportSSE:
		if err := p.ssePool.RestartServer(ctx, name); err != nil {
			return fmt.Errorf("unified_pool: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("server %s not found", name)
	}
}

// ServerInitializeResult returns the stored InitializeResult of the named
// server's current session, whatever its transport.
func (p *UnifiedPool) ServerInitializeResult(name string) (*InitializeResult, bool) {
	switch p.resolveTransport(name) {
	case transportStdio:
		return p.stdioPool.ServerInitializeResult(name)
	case transportHTTP:
		return p.httpPool.ServerInitializeResult(name)
	case transportSSE:
		return p.ssePool.ServerInitializeResult(name)
	default:
		return nil, false
	}
}

// SetServerEventHandler registers fn with every underlying pool.
func (p *UnifiedPool) SetServerEventHandler(fn ServerEventHandler) {
	p.stdioPool.SetServerEventHandler(fn)
	p.httpPool.SetServerEventHandler(fn)
	if p.ssePool != nil {
		p.ssePool.SetServerEventHandler(fn)
	}
}

func (p *UnifiedPool) SendRequestToServerWithID(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration, id int) (*Response, error) {
	switch p.resolveTransport(name) {
	case transportStdio:
		resp, err := p.stdioPool.SendRequestToServerWithID(ctx, name, method, params, timeout, id)
		if err != nil {
			return nil, fmt.Errorf("unified_pool: %w", err)
		}
		return resp, nil
	case transportHTTP:
		resp, err := p.httpPool.SendRequestToServerWithID(ctx, name, method, params, timeout, id)
		if err != nil {
			return nil, fmt.Errorf("unified_pool: %w", err)
		}
		return resp, nil
	case transportSSE:
		resp, err := p.ssePool.SendRequestToServerWithID(ctx, name, method, params, timeout, id)
		if err != nil {
			return nil, fmt.Errorf("unified_pool: %w", err)
		}
		return resp, nil
	default:
		return nil, fmt.Errorf("server %s not found in any pool", name)
	}
}

func (p *UnifiedPool) SendServerNotification(ctx context.Context, name string, method string, params map[string]interface{}) error {
	switch p.resolveTransport(name) {
	case transportStdio:
		if err := p.stdioPool.SendServerNotification(ctx, name, method, params); err != nil {
			return fmt.Errorf("unified_pool: %w", err)
		}
		return nil
	case transportHTTP:
		if err := p.httpPool.SendServerNotification(ctx, name, method, params); err != nil {
			return fmt.Errorf("unified_pool: %w", err)
		}
		return nil
	case transportSSE:
		if err := p.ssePool.SendServerNotification(ctx, name, method, params); err != nil {
			return fmt.Errorf("unified_pool: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("server %s not found", name)
	}
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
	switch p.resolveTransport(serverName) {
	case transportStdio:
		resp, err := p.stdioPool.SendRequest(ctx, serverName, req, timeout)
		if err != nil {
			return nil, fmt.Errorf("unified_pool: %w", err)
		}
		return resp, nil
	case transportHTTP:
		resp, err := p.httpPool.SendRequest(ctx, serverName, req, timeout)
		if err != nil {
			return nil, fmt.Errorf("unified_pool: %w", err)
		}
		return resp, nil
	case transportSSE:
		resp, err := p.ssePool.SendRequest(ctx, serverName, req, timeout)
		if err != nil {
			return nil, fmt.Errorf("unified_pool: %w", err)
		}
		return resp, nil
	default:
		return nil, fmt.Errorf("server %s not found", serverName)
	}
}
