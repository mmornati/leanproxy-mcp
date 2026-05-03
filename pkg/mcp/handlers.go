package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/pool"
)

type ToolCache struct {
	mu    sync.RWMutex
	tools map[string][]Tool
}

type Handler struct {
	pool       *pool.StdioPool
	manifest   *AggregatedManifest
	logger     *slog.Logger
	timeout    time.Duration
	toolCache  *ToolCache
}

type AggregatedManifest struct {
	Tools     []Tool
	Resources []Resource
	Prompts   []Prompt
}

func NewHandler(p *pool.StdioPool, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		pool:     p,
		logger:   logger,
		timeout:  30 * time.Second,
		toolCache: &ToolCache{
			tools: make(map[string][]Tool),
		},
	}
}

func (h *Handler) HandleRequest(ctx context.Context, req *Request) (*Response, error) {
	h.logger.Debug("handling mcp request", "method", req.Method, "id", req.ID)

	switch req.Method {
	case MethodInitialize:
		return h.handleInitialize(ctx, req)
	case MethodInitialized:
		h.logger.Info("received initialized notification from client")
		return nil, nil
	case MethodResourcesList:
		return h.handleResourcesList(ctx, req)
	case MethodPromptsList:
		return h.handlePromptsList(ctx, req)
	case MethodToolsList:
		return h.handleToolsList(ctx, req)
	case MethodToolsCall:
		return h.handleToolsCall(ctx, req)
	case MethodPing:
		return h.handlePing(ctx, req)
	case MethodShutdown:
		return h.handleShutdown(ctx, req)
	default:
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeMethodNotFound, fmt.Sprintf("method not found: %s", req.Method)),
			ID:      req.ID,
		}, nil
	}
}

func (h *Handler) handleInitialize(ctx context.Context, req *Request) (*Response, error) {
	var params InitializeParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &Response{
				JSONRPC: JSONRPCVersion,
				Error:   NewError(ErrCodeInvalidParams, fmt.Sprintf("invalid params: %v", err)),
				ID:      req.ID,
			}, nil
		}
	}

	result := InitializeResult{
		ProtocolVersion: "2024-11-05",
		Capabilities: ServerCapabilities{
			Tools:     &ToolsCapability{ListChanged: false},
			Resources: &ResourcesCapability{ListChanged: false},
			Prompts:   &PromptsCapability{ListChanged: false},
		},
		ServerInfo: ServerInfo{
			Name:    "leanproxy-mcp",
			Version: "1.0.0",
		},
	}

	resultBytes, err := json.Marshal(result)
	if err != nil {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeInternalError, fmt.Sprintf("marshal result: %v", err)),
			ID:      req.ID,
		}, nil
	}

	h.logger.Info("initialized leanproxy-mcp", "client", params.ClientInfo.Name, "version", params.ClientInfo.Version)

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}, nil
}

func (h *Handler) handleToolsList(ctx context.Context, req *Request) (*Response, error) {
	h.logger.Debug("tools/list request received, returning gateway tools only")

	gatewayTools := []Tool{
		{
			Name:        "search_tools",
			Description: "Search for available MCP tools across all proxied servers. Returns tool names with summarized descriptions. Use this to discover what tools are available before invoking them.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Search query to find tools"}}}`),
		},
		{
			Name:        "invoke_tool",
			Description: "Invoke a tool on a specific MCP server. First use search_tools to find the right tool, then invoke it with the server name and parameters.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"server":{"type":"string","description":"Server name"},"tool":{"type":"string","description":"Tool name to invoke"},"arguments":{"type":"object","description":"Tool arguments"}}}`),
		},
		{
			Name:        "list_servers",
			Description: "List all configured MCP servers and their current status.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}

	result := ToolsListResult{Tools: gatewayTools}
	resultBytes, _ := json.Marshal(result)

	h.logger.Info("gateway tools sent to client", "count", len(gatewayTools))

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}, nil
}

func (h *Handler) collectTools(ctx context.Context) (*AggregatedManifest, error) {
	return &AggregatedManifest{
		Tools:     make([]Tool, 0),
		Resources: make([]Resource, 0),
		Prompts:   make([]Prompt, 0),
	}, nil
}

func (h *Handler) initializeServer(ctx context.Context, serverName string) error {
	h.logger.Debug("initializing server", "name", serverName)

	initParams := InitializeParams{
		ProtocolVersion: "2024-11-05",
		Capabilities: ClientCapabilities{},
		ClientInfo: ClientInfo{
			Name:    "leanproxy-mcp",
			Version: "1.0.0",
		},
	}
	paramsBytes, _ := json.Marshal(initParams)

	resp, err := h.pool.SendRequestToServerWithID(ctx, serverName, MethodInitialize, paramsBytes, 10*time.Second, 1)
	if err != nil {
		return fmt.Errorf("initialize request failed: %w", err)
	}

	if resp != nil && resp.Error != nil {
		return fmt.Errorf("server returned error: %s", resp.Error.Message)
	}

	h.logger.Debug("server initialized, sending initialized notification", "name", serverName)

	h.pool.SendServerNotification(ctx, serverName, "notifications/initialized", map[string]interface{}{
		"capabilities": ServerCapabilities{},
	})

	h.logger.Debug("server ready", "name", serverName)
	return nil
}

func (h *Handler) handleToolsCall(ctx context.Context, req *Request) (*Response, error) {
	h.logger.Debug("handleToolsCall called", "params", string(req.Params))

	var params ToolsCallParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			h.logger.Warn("failed to unmarshal tools/call params", "error", err)
			return &Response{
				JSONRPC: JSONRPCVersion,
				Error:   NewError(ErrCodeInvalidParams, fmt.Sprintf("invalid params: %v", err)),
				ID:      req.ID,
			}, nil
		}
	}

	h.logger.Debug("tools/call request", "name", params.Name)

	if params.Name == "" {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeInvalidParams, "tool name is required"),
			ID:      req.ID,
		}, nil
	}

	if params.Name == "search_tools" || params.Name == "invoke_tool" || params.Name == "list_servers" {
		return h.handleLeanproxyTool(ctx, req, params)
	}

	serverName, toolName, err := h.parseToolName(params.Name)
	if err != nil {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeInvalidParams, err.Error()),
			ID:      req.ID,
		}, nil
	}

	newParams := ToolsCallParams{
		Name:      toolName,
		Arguments: params.Arguments,
	}
	paramsBytes, _ := json.Marshal(newParams)

	resp, err := h.pool.SendRequestToServer(ctx, serverName, MethodToolsCall, paramsBytes, h.timeout)
	if err != nil {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeServerError, fmt.Sprintf("tool call failed: %v", err)),
			ID:      req.ID,
		}, nil
	}

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resp.Result,
		ID:      req.ID,
	}, nil
}

func (h *Handler) handleLeanproxyTool(ctx context.Context, req *Request, params ToolsCallParams) (*Response, error) {
	switch params.Name {
	case "search_tools":
		return h.handleSearchTools(ctx, req, params)
	case "invoke_tool":
		return h.handleInvokeTool(ctx, req, params)
	case "list_servers":
		return h.handleListServers(ctx, req)
	default:
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeMethodNotFound, fmt.Sprintf("unknown gateway tool: %s", params.Name)),
			ID:      req.ID,
		}, nil
	}
}

func (h *Handler) handleSearchTools(ctx context.Context, req *Request, params ToolsCallParams) (*Response, error) {
	var query string
	if params.Arguments != nil {
		var args map[string]interface{}
		if err := json.Unmarshal(params.Arguments, &args); err == nil {
			if q, ok := args["query"].(string); ok {
				query = q
			}
		}
	}

	h.logger.Info("search_tools called", "query", query)

	h.toolCache.mu.RLock()
	cachePopulated := len(h.toolCache.tools) > 0
	h.toolCache.mu.RUnlock()

	if !cachePopulated {
		h.populateToolCache(ctx)
	}

	results := h.searchToolCache(query)

	h.logger.Info("search_tools completed", "results", len(results))

	if len(results) == 0 {
		result := map[string]interface{}{
			"content": []map[string]string{
				{"type": "text", "text": "No tools found matching your query. Try a different search term or invoke a tool directly using invoke_tool."},
			},
		}
		resultBytes, _ := json.Marshal(result)
		return &Response{
			JSONRPC: JSONRPCVersion,
			Result:  resultBytes,
			ID:      req.ID,
		}, nil
	}

	result := map[string]interface{}{
		"content": []map[string]string{
			{"type": "text", "text": fmt.Sprintf("Available tools:\n%s", strings.Join(results, "\n"))},
		},
	}
	resultBytes, _ := json.Marshal(result)

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}, nil
}

func (h *Handler) populateToolCache(ctx context.Context) {
	h.logger.Info("populating tool cache from backend servers")

	servers := h.pool.ListServers()

	for _, serverName := range servers {
		state, _ := h.pool.GetServerState(serverName)
		h.logger.Debug("checking server for cache population", "name", serverName, "state", state)

		if state != "idle" && state != "running" && state != "busy" {
			h.logger.Debug("server not running, attempting restart", "name", serverName, "state", state)
			if err := h.pool.RestartServer(ctx, serverName); err != nil {
				h.logger.Warn("failed to restart server for cache", "name", serverName, "error", err)
				continue
			}
			time.Sleep(500 * time.Millisecond)
		}

		if err := h.initializeServer(ctx, serverName); err != nil {
			h.logger.Warn("failed to initialize server for cache", "name", serverName, "error", err)
			continue
		}

		h.logger.Debug("requesting tools/list for cache", "name", serverName)
		resp, err := h.pool.SendRequestToServer(ctx, serverName, MethodToolsList, nil, 10*time.Second)
		if err != nil {
			h.logger.Warn("failed to get tools for cache", "name", serverName, "error", err)
			continue
		}

		if resp != nil && resp.Error != nil {
			h.logger.Warn("server error during cache population", "name", serverName, "error", resp.Error.Message)
			continue
		}

		if resp == nil || resp.Result == nil {
			h.logger.Warn("server returned no result for cache", "name", serverName, "resp", fmt.Sprintf("%+v", resp))
			continue
		}

		if len(resp.Result) == 0 || string(resp.Result) == "null" {
			h.logger.Warn("server returned null/empty result for cache", "name", serverName, "resp", fmt.Sprintf("%+v", resp))
			continue
		}

		var toolsResult ToolsListResult
		if err := json.Unmarshal(resp.Result, &toolsResult); err != nil {
			h.logger.Warn("failed to parse tools for cache", "name", serverName, "error", err, "result", string(resp.Result))
			continue
		}

		h.logger.Debug("caching tools from server", "name", serverName, "count", len(toolsResult.Tools))

		h.toolCache.mu.Lock()
		h.toolCache.tools[serverName] = toolsResult.Tools
		h.toolCache.mu.Unlock()
	}

	h.logger.Info("tool cache population complete")
}

func (h *Handler) searchToolCache(query string) []string {
	h.toolCache.mu.RLock()
	defer h.toolCache.mu.RUnlock()

	var results []string
	queryLower := strings.ToLower(query)

	for serverName, tools := range h.toolCache.tools {
		for _, tool := range tools {
			combined := fmt.Sprintf("%s_%s: %s", serverName, tool.Name, compactDescription(tool.Description))
			if query == "" || strings.Contains(strings.ToLower(combined), queryLower) {
				results = append(results, combined)
			}
		}
	}

return results
}

func compactDescription(description string) string {
	if len(description) <= 50 {
		return description
	}
	return description[:47] + "..."
}

func (h *Handler) handleInvokeTool(ctx context.Context, req *Request, params ToolsCallParams) (*Response, error) {
	var serverName, toolName string
	var arguments json.RawMessage

	if params.Arguments != nil {
		var args map[string]interface{}
		if err := json.Unmarshal(params.Arguments, &args); err == nil {
			if s, ok := args["server"].(string); ok {
				serverName = s
			}
			if t, ok := args["tool"].(string); ok {
				toolName = t
			}
			if a, ok := args["arguments"].(map[string]interface{}); ok {
				arguments, _ = json.Marshal(a)
			}
		}
	}

	if serverName == "" || toolName == "" {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeInvalidParams, "server and tool are required"),
			ID:      req.ID,
		}, nil
	}

	if strings.HasPrefix(toolName, serverName+"_") {
		toolName = strings.TrimPrefix(toolName, serverName+"_")
	}

	h.logger.Info("invoke_tool called", "server", serverName, "tool", toolName)

	state, stateErr := h.pool.GetServerState(serverName)
	h.logger.Debug("server current state", "name", serverName, "state", state, "error", stateErr)

	if state != "idle" && state != "running" && state != "busy" {
		h.logger.Warn("server not running, attempting to restart", "name", serverName, "state", state)
		if err := h.pool.RestartServer(ctx, serverName); err != nil {
			h.logger.Error("failed to restart server", "name", serverName, "error", err)
			return &Response{
				JSONRPC: JSONRPCVersion,
				Error:   NewError(ErrCodeServerError, fmt.Sprintf("server %s is not running (state: %s) and failed to restart: %v", serverName, state, err)),
				ID:      req.ID,
			}, nil
		}
		h.logger.Info("server restarted successfully", "name", serverName)
		time.Sleep(500 * time.Millisecond)
	}

	newParams := ToolsCallParams{
		Name:      toolName,
		Arguments: arguments,
	}
	paramsBytes, _ := json.Marshal(newParams)

	resp, err := h.pool.SendRequestToServer(ctx, serverName, MethodToolsCall, paramsBytes, h.timeout)
	if err != nil {
		h.logger.Error("invoke_tool failed", "server", serverName, "tool", toolName, "error", err)
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeServerError, fmt.Sprintf("tool invocation failed: %v", err)),
			ID:      req.ID,
		}, nil
	}

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resp.Result,
		ID:      req.ID,
	}, nil
}

func (h *Handler) handleListServers(ctx context.Context, req *Request) (*Response, error) {
	h.logger.Info("list_servers called")

	servers := h.pool.ListServers()
	serverList := make([]map[string]interface{}, 0)

	for _, serverName := range servers {
		state, _ := h.pool.GetServerState(serverName)
		serverList = append(serverList, map[string]interface{}{
			"name":  serverName,
			"state": string(state),
		})
	}

	result := map[string]interface{}{
		"content": []map[string]string{
			{"type": "text", "text": fmt.Sprintf("Configured servers:\n%s", formatServerList(serverList))},
		},
	}
	resultBytes, _ := json.Marshal(result)

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}, nil
}

func formatServerList(servers []map[string]interface{}) string {
	var lines []string
	for _, s := range servers {
		lines = append(lines, fmt.Sprintf("- %s (%s)", s["name"], s["state"]))
	}
	return strings.Join(lines, "\n")
}

func (h *Handler) parseToolName(fullName string) (serverName, toolName string, err error) {
	parts := strings.SplitN(fullName, "_", 2)
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid tool name '%s': expected format is 'serverName_toolName'", fullName)
	}
	return parts[0], parts[1], nil
}

func (h *Handler) handleResourcesList(ctx context.Context, req *Request) (*Response, error) {
	result := ResourcesListResult{
		Resources: make([]Resource, 0),
	}
	resultBytes, _ := json.Marshal(result)

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}, nil
}

func (h *Handler) handlePromptsList(ctx context.Context, req *Request) (*Response, error) {
	result := PromptsListResult{
		Prompts: make([]Prompt, 0),
	}
	resultBytes, _ := json.Marshal(result)

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}, nil
}

func (h *Handler) handlePing(ctx context.Context, req *Request) (*Response, error) {
	result := map[string]string{"status": "ok"}
	resultBytes, _ := json.Marshal(result)

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}, nil
}

func (h *Handler) handleShutdown(ctx context.Context, req *Request) (*Response, error) {
	result := map[string]string{"status": "shutdown"}
	resultBytes, _ := json.Marshal(result)

	h.pool.Close()

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}, nil
}

func (h *Handler) ResetManifest() {
	h.manifest = nil
}