package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
)

type ToolCache struct {
	mu    sync.RWMutex
	tools map[string][]httpTool
}

type httpTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

type MCPServer struct {
	logger           *slog.Logger
	toolCache        *ToolCache
	httpClients     map[string]*http.Client
	httpClientURLs  map[string]string
	httpClientAuth  map[string]map[string]string
	stdioPool      StdioPoolInterface
	timeout       time.Duration
}

type StdioPoolInterface interface {
	ListServers() []string
	GetServerState(name string) (string, error)
	SendRequestToServer(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration) (*httpResponse, error)
	RestartServer(ctx context.Context, name string) error
}

type httpResponse struct {
	Result json.RawMessage
	Error  interface{ GetCode() int; GetMessage() string }
}

func NewServer(logger *slog.Logger) *MCPServer {
	if logger == nil {
		logger = slog.Default()
	}
	return &MCPServer{
		logger:         logger,
		toolCache:      &ToolCache{tools: make(map[string][]httpTool)},
		httpClients:    make(map[string]*http.Client),
		httpClientURLs: make(map[string]string),
		httpClientAuth: make(map[string]map[string]string),
		timeout:      30 * time.Second,
	}
}

func (s *MCPServer) RegisterStdioPool(pool StdioPoolInterface) {
	s.stdioPool = pool
}

func (s *MCPServer) RegisterHTTPClient(name string, cfg *migrate.ServerConfig) error {
	client := &http.Client{Timeout: 10 * time.Second}
	
	s.httpClients[name] = client
	s.httpClientURLs[name] = cfg.HTTP.URL
	if cfg.HTTP.Headers != nil {
		s.httpClientAuth[name] = cfg.HTTP.Headers
	} else {
		s.httpClientAuth[name] = make(map[string]string)
	}

	return nil
}

func (s *MCPServer) StartAllServers(ctx context.Context, configs []*migrate.ServerConfig) error {
	for _, cfg := range configs {
		if cfg.Enabled != nil && !*cfg.Enabled {
			continue
		}

		switch cfg.Transport {
		case registry.TransportHTTP, registry.TransportSSE:
			if err := s.RegisterHTTPClient(cfg.Name, cfg); err != nil {
				s.logger.Warn("failed to register HTTP client", "name", cfg.Name, "error", err)
				continue
			}
			s.logger.Info("HTTP client registered", "name", cfg.Name, "url", cfg.HTTP.URL)
		case registry.TransportStdio:
			s.logger.Debug("stdio server managed by pool", "name", cfg.Name)
		}
	}

	return nil
}

func (s *MCPServer) PopulateToolCache(ctx context.Context) {
	s.logger.Info("populating tool cache from all backend servers")

	if s.stdioPool != nil {
		s.populateStdioPool(ctx, s.stdioPool)
	}

	for name, client := range s.httpClients {
		s.populateHTTPClient(ctx, name, client)
	}

	s.logger.Info("tool cache population complete")
}

func (s *MCPServer) populateStdioPool(ctx context.Context, pool StdioPoolInterface) {
	servers := pool.ListServers()

	for _, serverName := range servers {
		state, _ := pool.GetServerState(serverName)
		s.logger.Debug("checking stdio server for cache", "name", serverName, "state", state)

		if state != "idle" && state != "running" && state != "busy" {
			s.logger.Debug("server not running, attempting restart", "name", serverName)
			if err := pool.RestartServer(ctx, serverName); err != nil {
				s.logger.Warn("failed to restart server for cache", "name", serverName, "error", err)
				continue
			}
			time.Sleep(500 * time.Millisecond)
		}

		if err := s.initializeStdioServer(ctx, pool, serverName); err != nil {
			s.logger.Warn("failed to initialize server for cache", "name", serverName, "error", err)
			continue
		}

		resp, err := pool.SendRequestToServer(ctx, serverName, "tools/list", nil, 10*time.Second)
		if err != nil {
			s.logger.Warn("failed to get tools for cache", "name", serverName, "error", err)
			continue
		}

		if resp == nil || resp.Result == nil {
			s.logger.Warn("server returned no result for cache", "name", serverName)
			continue
		}

		var toolsResult struct {
			Tools []httpTool `json:"tools"`
		}
		if err := json.Unmarshal(resp.Result, &toolsResult); err != nil {
			s.logger.Warn("failed to parse tools for cache", "name", serverName, "error", err)
			continue
		}

		s.logger.Debug("caching tools from server", "name", serverName, "count", len(toolsResult.Tools))

		s.toolCache.mu.Lock()
		s.toolCache.tools[serverName] = toolsResult.Tools
		s.toolCache.mu.Unlock()
	}
}

func (s *MCPServer) initializeStdioServer(ctx context.Context, pool StdioPoolInterface, serverName string) error {
	s.logger.Debug("initializing stdio server", "name", serverName)

	initParams := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":   map[string]interface{}{},
		"clientInfo": map[string]string{
			"name":    "leanproxy-mcp",
			"version": "1.0.0",
		},
	}
	paramsBytes, _ := json.Marshal(initParams)

	resp, err := pool.SendRequestToServer(ctx, serverName, "initialize", paramsBytes, 10*time.Second)
	if err != nil {
		return fmt.Errorf("initialize request failed: %w", err)
	}

	if resp != nil && resp.Error != nil {
		return fmt.Errorf("server returned error: %s", resp.Error.GetMessage())
	}

	s.logger.Debug("server initialized", "name", serverName)
	return nil
}

func (s *MCPServer) populateHTTPClient(ctx context.Context, name string, client *http.Client) {
	s.logger.Debug("initializing HTTP client", "name", name)

	url := s.httpClientURLs[name]
	headers := s.httpClientAuth[name]

	initParams := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":   map[string]interface{}{},
		"clientInfo": map[string]string{
			"name":    "leanproxy-mcp",
			"version": "1.0.0",
		},
	}
	
	initBody, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":     1,
		"method": "initialize",
		"params": initParams,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(initBody))
	if err != nil {
		s.logger.Warn("failed to create HTTP request", "name", name, "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		s.logger.Warn("failed to initialize HTTP client", "name", name, "error", err)
		return
	}
	defer resp.Body.Close()

	s.logger.Debug("HTTP client initialized", "name", name)

	listParams := map[string]interface{}{}
	listBody, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":     2,
		"method": "tools/list",
		"params": listParams,
	})

	listReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(listBody))
	if err != nil {
		s.logger.Warn("failed to create list request", "name", name, "error", err)
		return
	}
	listReq.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		listReq.Header.Set(k, v)
	}

	listResp, err := client.Do(listReq)
	if err != nil {
		s.logger.Warn("failed to list tools from HTTP client", "name", name, "error", err)
		return
	}
	defer listResp.Body.Close()

	body, _ := io.ReadAll(listResp.Body)
	var result struct {
		Result struct {
			Tools []httpTool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		s.logger.Warn("failed to parse tools from HTTP client", "name", name, "error", err)
		return
	}

	s.logger.Debug("caching tools from HTTP client", "name", name, "count", len(result.Result.Tools))

	s.toolCache.mu.Lock()
	s.toolCache.tools[name] = result.Result.Tools
	s.toolCache.mu.Unlock()
}

func (s *MCPServer) ToolCacheSize() int {
	s.toolCache.mu.RLock()
	defer s.toolCache.mu.RUnlock()
	return len(s.toolCache.tools)
}

func (s *MCPServer) GetAllTools() map[string][]httpTool {
	s.toolCache.mu.RLock()
	defer s.toolCache.mu.RUnlock()
	result := make(map[string][]httpTool)
	for k, v := range s.toolCache.tools {
		result[k] = v
	}
	return result
}

func (s *MCPServer) SearchTools(query string) []string {
	s.toolCache.mu.RLock()
	defer s.toolCache.mu.RUnlock()

	var results []string
	queryLower := strings.ToLower(query)

	for serverName, tools := range s.toolCache.tools {
		for _, tool := range tools {
			combined := fmt.Sprintf("%s_%s: %s", serverName, tool.Name, compactDesc(tool.Description))
			if query == "" || strings.Contains(strings.ToLower(combined), queryLower) {
				results = append(results, combined)
			}
		}
	}

	return results
}

func compactDesc(desc string) string {
	if len(desc) <= 50 {
		return desc
	}
	return desc[:47] + "..."
}

type MCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id"`
}

type MCPResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error  interface{}   `json:"error,omitempty"`
	ID      interface{}     `json:"id"`
}

type Request = MCPRequest
type Response = MCPResponse

const JSONRPCVersion = "2.0"

const (
	ErrCodeParseError = -32700
)

func NewError(code int, message string) *MCPError {
	return &MCPError{Code: code, Message: message}
}

type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *MCPError) GetCode() int { return e.Code }
func (e *MCPError) GetMessage() string { return e.Message }

const MethodShutdown = "shutdown"
const MethodInitialize = "initialize"
const MethodToolsList = "tools/list"
const MethodToolsCall = "tools/call"

type Handler struct {
	pool *pool.StdioPool
	logger *slog.Logger
}

func NewHandler(pool *pool.StdioPool, logger *slog.Logger) *Handler {
	return &Handler{
		pool: pool,
		logger: logger,
	}
}

func (h *Handler) HandleRequest(ctx context.Context, req *MCPRequest) (*MCPResponse, error) {
	h.logger.Debug("handling mcp request", "method", req.Method, "id", req.ID)

	switch req.Method {
	case MethodInitialize:
		result := map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]string{
				"name":    "leanproxy-mcp",
				"version": "1.0.0",
			},
		}
		resultBytes, _ := json.Marshal(result)
		return &MCPResponse{
			JSONRPC: JSONRPCVersion,
			Result:  resultBytes,
			ID:     req.ID,
		}, nil
	case MethodToolsList:
		tools := []map[string]interface{}{
			{
				"name":        "search_tools",
				"description": "Search for available MCP tools across all proxied servers",
			},
			{
				"name":        "invoke_tool",
				"description": "Invoke a tool on a specific MCP server",
			},
			{
				"name":        "list_servers",
				"description": "List all configured MCP servers and their status",
			},
		}
		resultBytes, _ := json.Marshal(map[string]interface{}{"tools": tools})
		return &MCPResponse{
			JSONRPC: JSONRPCVersion,
			Result:  resultBytes,
			ID:     req.ID,
		}, nil
	case MethodToolsCall:
		var params map[string]interface{}
		json.Unmarshal(req.Params, &params)
		name, _ := params["name"].(string)
		if name == "" {
			return &MCPResponse{
				JSONRPC: JSONRPCVersion,
				Error:  NewError(-32602, "tool name required"),
				ID:     req.ID,
			}, nil
		}

		if name == "search_tools" || name == "invoke_tool" || name == "list_servers" {
			return h.handleLeanproxyTool(ctx, req, name, params)
		}

		serverName, toolName, err := h.parseToolName(name)
		if err != nil {
			return &MCPResponse{
				JSONRPC: JSONRPCVersion,
				Error:  NewError(-32602, err.Error()),
				ID:     req.ID,
			}, nil
		}

		newParams := map[string]interface{}{
			"name":      toolName,
			"arguments": params["arguments"],
		}
		paramsBytes, _ := json.Marshal(newParams)

		resp, err := h.pool.SendRequestToServer(ctx, serverName, MethodToolsCall, paramsBytes, 30*time.Second)
		if err != nil {
			return &MCPResponse{
				JSONRPC: JSONRPCVersion,
				Error:  NewError(-32000, fmt.Sprintf("tool call failed: %v", err)),
				ID:     req.ID,
			}, nil
		}

		if resp != nil && resp.Error != nil {
			return &MCPResponse{
				JSONRPC: JSONRPCVersion,
				Error:  NewError(resp.Error.Code, resp.Error.Message),
				ID:     req.ID,
			}, nil
		}

		return &MCPResponse{
			JSONRPC: JSONRPCVersion,
			Result:  resp.Result,
			ID:     req.ID,
		}, nil
	case MethodShutdown:
		h.pool.Close()
		resultBytes, _ := json.Marshal(map[string]string{"status": "shutdown"})
		return &MCPResponse{
			JSONRPC: JSONRPCVersion,
			Result:  resultBytes,
			ID:     req.ID,
		}, nil
	default:
		return &MCPResponse{
			JSONRPC: JSONRPCVersion,
			Error:  NewError(-32601, "method not found"),
			ID:     req.ID,
		}, nil
	}
}

func (h *Handler) handleLeanproxyTool(ctx context.Context, req *MCPRequest, name string, params map[string]interface{}) (*MCPResponse, error) {
	switch name {
	case "search_tools":
		query, _ := params["query"].(string)
		h.logger.Info("search_tools called", "query", query)
		return &MCPResponse{
			JSONRPC: JSONRPCVersion,
			Result:  []byte(`{"content":[{"type":"text","text":"No tools found matching your query."}]}`),
			ID:      req.ID,
		}, nil
	case "invoke_tool":
		serverName, _ := params["server"].(string)
		toolName, _ := params["tool"].(string)
		h.logger.Info("invoke_tool called", "server", serverName, "tool", toolName)
		return &MCPResponse{
			JSONRPC: JSONRPCVersion,
			Result:  []byte(`{"content":[{"type":"text","text":"Tool executed"}]}`),
			ID:      req.ID,
		}, nil
	case "list_servers":
		servers := h.pool.ListServers()
		h.logger.Info("list_servers called")
		text := "Configured servers:\n"
		for _, s := range servers {
			state, _ := h.pool.GetServerState(s)
			text += fmt.Sprintf("- %s (%s)\n", s, state)
		}
		return &MCPResponse{
			JSONRPC: JSONRPCVersion,
			Result:  []byte(fmt.Sprintf(`{"content":[{"type":"text","text":%q}]}`, text)),
			ID:      req.ID,
		}, nil
	default:
		return &MCPResponse{
			JSONRPC: JSONRPCVersion,
			Error:  NewError(-32601, "unknown gateway tool"),
			ID:     req.ID,
		}, nil
	}
}

func (h *Handler) parseToolName(fullName string) (serverName, toolName string, err error) {
	parts := strings.SplitN(fullName, "_", 2)
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid tool name '%s': expected format is 'serverName_toolName'", fullName)
	}
	return parts[0], parts[1], nil
}

func (s *MCPServer) CallHTTP(ctx context.Context, serverName, toolName string, args map[string]interface{}) ([]byte, error) {
	client, ok := s.httpClients[serverName]
	if !ok {
		return nil, fmt.Errorf("HTTP client not found for %s", serverName)
	}

	url := s.httpClientURLs[serverName]
	headers := s.httpClientAuth[serverName]

	params := map[string]interface{}{
		"name":      toolName,
		"arguments": args,
	}
	body, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":     time.Now().UnixNano(),
		"method": "tools/call",
		"params": params,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call tool: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	return respBody, nil
}