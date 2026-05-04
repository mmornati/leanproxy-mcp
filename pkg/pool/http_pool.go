package pool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
)

type HTTPConfig struct {
	URL     string
	Headers map[string]string
}

type HTTPServer struct {
	name    string
	config  *migrate.ServerConfig
	httpCfg HTTPConfig
	client  *http.Client
	state   ServerState
	mu      sync.RWMutex
	logger  *slog.Logger
}

func NewHTTPServer(name string, config *migrate.ServerConfig, logger *slog.Logger) *HTTPServer {
	httpCfg := HTTPConfig{
		URL:     config.HTTP.URL,
		Headers: config.HTTP.Headers,
	}

	timeout := 30 * time.Second
	if config.TimeoutValue > 0 {
		timeout = config.TimeoutValue
	}

	return &HTTPServer{
		name:    name,
		config:  config,
		httpCfg: httpCfg,
		client: &http.Client{
			Timeout: timeout,
		},
		state:  StateRunning,
		logger: logger,
	}
}

func (s *HTTPServer) getState() ServerState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *HTTPServer) setState(state ServerState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

func (s *HTTPServer) SendRequest(ctx context.Context, method string, params json.RawMessage, timeout time.Duration) (*Response, error) {
	var reqParams interface{}
	if params != nil && len(params) > 0 {
		if err := json.Unmarshal(params, &reqParams); err != nil {
			reqParams = params
		}
	} else {
		reqParams = map[string]interface{}{}
	}

	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  reqParams,
		"id":      1,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("http_pool: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.httpCfg.URL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("http_pool: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range s.httpCfg.Headers {
		req.Header.Set(k, v)
	}

	s.logger.Debug("http_pool: sending request", "server", s.name, "method", method, "url", s.httpCfg.URL)

	resp, err := s.client.Do(req)
	if err != nil {
		s.setState(StateError)
		return nil, fmt.Errorf("http_pool: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.setState(StateError)
		respBody, _ := io.ReadAll(resp.Body)
		errMsg := string(respBody)
		if errMsg == "" {
			errMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("http_pool: server returned %s", errMsg)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("http_pool: read response: %w", err)
	}

	var rpcResp Response
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("http_pool: unmarshal response: %w", err)
	}

	s.setState(StateRunning)
	return &rpcResp, nil
}

type HTTPTransportType string

const HTTPTransport HTTPTransportType = "http"

type HTTPPool struct {
	servers map[string]*HTTPServer
	mu      sync.RWMutex
	logger  *slog.Logger
}

func NewHTTPPool(logger *slog.Logger) *HTTPPool {
	if logger == nil {
		logger = slog.Default()
	}
	return &HTTPPool{
		servers: make(map[string]*HTTPServer),
		logger:  logger,
	}
}

func (p *HTTPPool) StartServer(ctx context.Context, config *migrate.ServerConfig) error {
	if config.HTTP == nil || config.HTTP.URL == "" {
		return fmt.Errorf("http_pool: HTTP config is required")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.servers[config.Name]; exists {
		p.logger.Debug("http_pool: server already exists", "name", config.Name)
		return nil
	}

	server := NewHTTPServer(config.Name, config, p.logger)
	p.servers[config.Name] = server

	p.logger.Info("http_pool: server started", "name", config.Name, "url", config.HTTP.URL)
	return nil
}

func (p *HTTPPool) ListServers() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	names := make([]string, 0, len(p.servers))
	for name := range p.servers {
		names = append(names, name)
	}
	return names
}

func (p *HTTPPool) GetServerState(name string) (ServerState, error) {
	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("http_pool: server %s not found", name)
	}

	return server.getState(), nil
}

func (p *HTTPPool) SendRequest(ctx context.Context, serverName string, req *proxy.JSONRPCRequest, timeout time.Duration) (*proxy.JSONRPCResponse, error) {
	p.mu.RLock()
	server, exists := p.servers[serverName]
	p.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("http_pool: server %s not found", serverName)
	}

	resp, err := server.SendRequest(ctx, req.Method, req.Params, timeout)
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

func (p *HTTPPool) SendRequestToServer(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration) (*Response, error) {
	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("http_pool: server %s not found", name)
	}

	return server.SendRequest(ctx, method, params, timeout)
}

func (p *HTTPPool) SendRequestToServerWithID(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration, id int) (*Response, error) {
	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("http_pool: server %s not found", name)
	}

	return server.SendRequest(ctx, method, params, timeout)
}

func (p *HTTPPool) RestartServer(ctx context.Context, name string) error {
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

func (p *HTTPPool) SendServerNotification(ctx context.Context, name string, method string, params map[string]interface{}) error {
	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()

	if !exists {
		return fmt.Errorf("http_pool: server %s not found", name)
	}

	paramsBytes, _ := json.Marshal(params)
	_, err := server.SendRequest(ctx, method, paramsBytes, 10*time.Second)
	return err
}

func (p *HTTPPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for name, server := range p.servers {
		server.setState(StateStopped)
		p.logger.Debug("http_pool: server stopped", "name", name)
	}
	p.servers = make(map[string]*HTTPServer)
	return nil
}

func (p *HTTPPool) ServerCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.servers)
}

type ServerSource interface {
	ListServers() []string
	GetServerState(name string) (ServerState, error)
	SendRequestToServer(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration) (*Response, error)
	SendRequestToServerWithID(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration, id int) (*Response, error)
	SendServerNotification(ctx context.Context, name string, method string, params map[string]interface{}) error
	RestartServer(ctx context.Context, name string) error
	Close() error
}

var _ ServerSource = (*StdioPool)(nil)
var _ ServerSource = (*HTTPPool)(nil)

type UnifiedPool struct {
	stdioPool *StdioPool
	httpPool  *HTTPPool
	logger    *slog.Logger
}

func NewUnifiedPool(stdioPool *StdioPool, httpPool *HTTPPool, logger *slog.Logger) *UnifiedPool {
	if logger == nil {
		logger = slog.Default()
	}
	return &UnifiedPool{
		stdioPool: stdioPool,
		httpPool:  httpPool,
		logger:    logger,
	}
}

func (p *UnifiedPool) ListServers() []string {
	var servers []string
	servers = append(servers, p.stdioPool.ListServers()...)
	servers = append(servers, p.httpPool.ListServers()...)
	return servers
}

func (p *UnifiedPool) GetServerState(name string) (ServerState, error) {
	state, err := p.stdioPool.GetServerState(name)
	if err == nil {
		return state, nil
	}
	return p.httpPool.GetServerState(name)
}

func (p *UnifiedPool) SendRequestToServer(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration) (*Response, error) {
	resp, err := p.stdioPool.SendRequestToServer(ctx, name, method, params, timeout)
	if err == nil {
		return resp, nil
	}
	return p.httpPool.SendRequestToServer(ctx, name, method, params, timeout)
}

func (p *UnifiedPool) RestartServer(ctx context.Context, name string) error {
	err := p.stdioPool.RestartServer(ctx, name)
	if err == nil {
		return nil
	}
	return p.httpPool.RestartServer(ctx, name)
}

func (p *UnifiedPool) SendRequestToServerWithID(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration, id int) (*Response, error) {
	resp, err := p.stdioPool.SendRequestToServerWithID(ctx, name, method, params, timeout, id)
	if err == nil {
		return resp, nil
	}
	return p.httpPool.SendRequestToServerWithID(ctx, name, method, params, timeout, id)
}

func (p *UnifiedPool) SendServerNotification(ctx context.Context, name string, method string, params map[string]interface{}) error {
	err := p.stdioPool.SendServerNotification(ctx, name, method, params)
	if err == nil {
		return nil
	}
	return p.httpPool.SendServerNotification(ctx, name, method, params)
}

func (p *UnifiedPool) Close() error {
	p.stdioPool.Close()
	p.httpPool.Close()
	return nil
}

func (p *UnifiedPool) SendRequest(ctx context.Context, serverName string, req *proxy.JSONRPCRequest, timeout time.Duration) (*proxy.JSONRPCResponse, error) {
	resp, err := p.stdioPool.SendRequest(ctx, serverName, req, timeout)
	if err == nil {
		return resp, nil
	}
	return p.httpPool.SendRequest(ctx, serverName, req, timeout)
}

var _ ServerSource = (*UnifiedPool)(nil)