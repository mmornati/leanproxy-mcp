package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/errors"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/toolstore"
)

type ParamInfo struct {
	Name        string
	Type        string
	IsRequired  bool
	Description string
}

type ToolCache struct {
	mu    sync.RWMutex
	tools map[string][]Tool
}

// CachedTools returns a snapshot of the tools known per server name. It is
// used by the serve listener to register backend tools with the router so
// tool calls can be routed to the owning server.
func (h *Handler) CachedTools() map[string][]Tool {
	h.toolCache.mu.RLock()
	defer h.toolCache.mu.RUnlock()
	out := make(map[string][]Tool, len(h.toolCache.tools))
	for name, tools := range h.toolCache.tools {
		snapshot := make([]Tool, len(tools))
		copy(snapshot, tools)
		out[name] = snapshot
	}
	return out
}

type Handler struct {
	pool           pool.ServerSource
	logger         *slog.Logger
	timeout        time.Duration
	timeouts       map[string]time.Duration
	toolCache      *ToolCache
	toolStore      toolstore.Cache
	manifest       *AggregatedManifest
	cacheRefreshes atomic.Uint64
	cacheFailures  atomic.Uint64

	// pipelineMu guards middlewares; pipeline holds the composed chain
	// (middlewares around dispatch) so HandleRequest can load it lock-free.
	pipelineMu  sync.Mutex
	middlewares []Middleware
	pipeline    atomic.Pointer[Next]
}

type AggregatedManifest struct {
	Tools     []Tool
	Resources []Resource
	Prompts   []Prompt
}

func NewHandler(p pool.ServerSource, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		pool:    p,
		logger:  logger,
		timeout: 30 * time.Second,
		toolCache: &ToolCache{
			tools: make(map[string][]Tool),
		},
	}
}

func NewHandlerWithToolStore(p pool.ServerSource, logger *slog.Logger, store toolstore.Cache) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		pool:    p,
		logger:  logger,
		timeout: 30 * time.Second,
		toolCache: &ToolCache{
			tools: make(map[string][]Tool),
		},
		toolStore: store,
	}
}

// SetTimeout registers a per-server request timeout. The handler falls back
// to its default timeout (30s unless changed via SetDefaultTimeout) for any
// server that has no explicit entry. A zero duration clears the entry.
func (h *Handler) SetTimeout(serverName string, timeout time.Duration) {
	if h.timeouts == nil {
		h.timeouts = make(map[string]time.Duration)
	}
	if timeout <= 0 {
		delete(h.timeouts, serverName)
		return
	}
	h.timeouts[serverName] = timeout
}

// SetDefaultTimeout overrides the fallback timeout used when a server has no
// explicit per-server entry. A zero or negative value is ignored (the 30s
// default remains in effect).
func (h *Handler) SetDefaultTimeout(timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	h.timeout = timeout
}

func (h *Handler) timeoutFor(serverName string) time.Duration {
	if d, ok := h.timeouts[serverName]; ok && d > 0 {
		return d
	}
	return h.timeout
}

// HandleRequest runs req through the middleware pipeline registered with Use
// and, innermost, the handler's own method dispatch.
func (h *Handler) HandleRequest(ctx context.Context, req *Request) (*Response, error) {
	if chain := h.pipeline.Load(); chain != nil {
		resp, err := (*chain)(ctx, req)
		return guardResponse(resp), err
	}
	resp, err := h.dispatch(ctx, req)
	return guardResponse(resp), err
}

// dispatch routes a request to its method handler. It is the innermost Next
// of the pipeline.
func (h *Handler) dispatch(ctx context.Context, req *Request) (*Response, error) {
	h.logger.Debug("handling mcp request", "method", req.Method, "id", req.ID)

	if err := errors.ValidateContext(ctx); err != nil {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeInternalError, err.Error()),
			ID:      req.ID,
		}, nil
	}

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

	gatewayTools := make([]Tool, 0)
	for _, def := range GetAllToolDefinitions() {
		gatewayTools = append(gatewayTools, Tool{
			Name:        def.Name,
			Description: def.Description,
			InputSchema: def.InputSchema,
		})
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
	h.logger.Info("initializing server", "name", serverName)

	initParams := InitializeParams{
		ProtocolVersion: "2024-11-05",
		Capabilities:    ClientCapabilities{},
		ClientInfo: ClientInfo{
			Name:    "leanproxy-mcp",
			Version: "1.0.0",
		},
	}
	paramsBytes, _ := json.Marshal(initParams)

	h.logger.Debug("sending initialize request", "name", serverName, "params", string(paramsBytes))

	resp, err := h.pool.SendRequestToServerWithID(ctx, serverName, MethodInitialize, paramsBytes, 120*time.Second, 1)
	if err != nil {
		h.logger.Error("initialize request failed", "name", serverName, "error", err)
		return fmt.Errorf("initialize request failed: %w", err)
	}

	if resp != nil && resp.Error != nil {
		h.logger.Error("server returned initialize error", "name", serverName, "error", resp.Error.Message)
		return fmt.Errorf("server returned error: %s", resp.Error.Message)
	}

	h.logger.Debug("server initialized, sending initialized notification", "name", serverName)

	notifyErr := h.pool.SendServerNotification(ctx, serverName, "notifications/initialized", map[string]interface{}{
		"capabilities": ServerCapabilities{},
	})
	if notifyErr != nil {
		h.logger.Warn("failed to send initialized notification", "name", serverName, "error", notifyErr)
	}

	h.logger.Info("server ready", "name", serverName)
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

	if params.Name == "list_tools" || params.Name == "invoke_tool" {
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

	// Perform MCP initialize handshake if not yet done for this server instance.
	if !h.pool.IsServerMCPInitialized(serverName) {
		h.logger.Debug("initializing MCP session with server", "name", serverName)
		if err := h.initializeServer(ctx, serverName); err != nil {
			return &Response{
				JSONRPC: JSONRPCVersion,
				Error:   NewError(ErrCodeServerError, fmt.Sprintf("server initialization failed: %v", err)),
				ID:      req.ID,
			}, nil
		}
		h.pool.MarkServerMCPInitialized(serverName)
	}

	newParams := ToolsCallParams{
		Name:      toolName,
		Arguments: params.Arguments,
	}
	paramsBytes, _ := json.Marshal(newParams)

	resp, err := h.pool.SendRequestToServer(ctx, serverName, MethodToolsCall, paramsBytes, h.timeoutFor(serverName))
	if err != nil {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeServerError, fmt.Sprintf("tool call failed: %v", err)),
			ID:      req.ID,
		}, nil
	}

	if resp != nil && resp.Error != nil {
		// Forward the upstream JSON-RPC error instead of an empty response.
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   &Error{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data},
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
	case "list_tools":
		return h.handleListTools(ctx, req, params)
	case "invoke_tool":
		return h.handleInvokeTool(ctx, req, params)
	default:
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeMethodNotFound, fmt.Sprintf("unknown gateway tool: %s", params.Name)),
			ID:      req.ID,
		}, nil
	}
}

func (h *Handler) handleListTools(ctx context.Context, req *Request, params ToolsCallParams) (*Response, error) {
	var serverName string
	var maxDescChars int
	if params.Arguments != nil {
		var args map[string]interface{}
		if err := json.Unmarshal(params.Arguments, &args); err == nil {
			args = ApplyDefaults("list_tools", args)
			if s, ok := args["server_name"].(string); ok {
				serverName = s
			}
			if m, ok := args["max_description_chars"].(float64); ok {
				maxDescChars = int(m)
			}
		}
	}

	if serverName == "" {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeInvalidParams, "server_name parameter is required. Use list_servers to get available server names, then use list_tools to see tools on a specific server."),
			ID:      req.ID,
		}, nil
	}

	if valid, msg := ValidateParam("list_tools", "max_description_chars", float64(maxDescChars)); !valid {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeInvalidParams, fmt.Sprintf("max_description_chars %s", msg)),
			ID:      req.ID,
		}, nil
	}

	if maxDescChars == 0 {
		maxDescChars = 200
	}

	h.logger.Info("list_tools called", "server_name", serverName, "max_desc_chars", maxDescChars)

	servers := h.pool.ListServers()
	serverFound := false
	for _, s := range servers {
		if s == serverName {
			serverFound = true
			break
		}
	}

	if !serverFound {
		serversList := strings.Join(servers, ", ")
		result := map[string]interface{}{
			"content": []map[string]string{
				{"type": "text", "text": fmt.Sprintf("Server '%s' not found. Available servers: %s. Use list_servers to see all available servers.", serverName, serversList)},
			},
		}
		resultBytes, _ := json.Marshal(result)
		return &Response{
			JSONRPC: JSONRPCVersion,
			Result:  resultBytes,
			ID:      req.ID,
		}, nil
	}

	h.toolCache.mu.RLock()
	tools, exists := h.toolCache.tools[serverName]
	h.toolCache.mu.RUnlock()

	if !exists || len(tools) == 0 {
		h.PopulateToolCache(ctx)
		h.toolCache.mu.RLock()
		tools = h.toolCache.tools[serverName]
		h.toolCache.mu.RUnlock()
	}

	if len(tools) == 0 {
		result := map[string]interface{}{
			"content": []map[string]string{
				{"type": "text", "text": fmt.Sprintf("No tools available on server '%s'. The server may be unavailable or have no tools.", serverName)},
			},
		}
		resultBytes, _ := json.Marshal(result)
		return &Response{
			JSONRPC: JSONRPCVersion,
			Result:  resultBytes,
			ID:      req.ID,
		}, nil
	}

	formattedTools := make([]string, 0, len(tools))
	for _, tool := range tools {
		formatted := formatTool(tool, serverName, maxDescChars)
		formattedTools = append(formattedTools, formatted)
	}

	h.logger.Info("list_tools completed", "server", serverName, "results", len(formattedTools))

	result := map[string]interface{}{
		"content": []map[string]string{
			{"type": "text", "text": fmt.Sprintf("%s tools (%d):\n%s", serverName, len(tools), strings.Join(formattedTools, "\n"))},
		},
	}
	resultBytes, _ := json.Marshal(result)

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}, nil
}

func (h *Handler) PopulateToolCache(ctx context.Context) {
	h.logger.Info("populating tool cache from backend servers")

	if h.toolStore != nil {
		h.loadFromPersistentCache(ctx)
	}

	h.refreshToolCacheFromServers(ctx)

	h.logger.Info("tool cache population complete")
}

func (h *Handler) loadFromPersistentCache(ctx context.Context) {
	servers := h.pool.ListServers()
	for _, serverName := range servers {
		cachedTools, err := h.toolStore.GetTools(serverName)
		if err != nil {
			h.logger.Warn("failed to load tools from persistent cache", "server", serverName, "error", err)
			continue
		}
		if cachedTools == nil {
			continue
		}

		tools := make([]Tool, len(cachedTools))
		for i, ct := range cachedTools {
			tools[i] = Tool{
				Name:        ct.Name,
				Description: ct.Description,
				InputSchema: ct.InputSchema,
			}
		}

		h.toolCache.mu.Lock()
		h.toolCache.tools[serverName] = tools
		h.toolCache.mu.Unlock()

		h.logger.Debug("loaded tools from persistent cache", "server", serverName, "count", len(tools))
	}
}

func (h *Handler) refreshToolCacheFromServers(ctx context.Context) {
	h.cacheRefreshes.Add(1)
	servers := h.pool.ListServers()

	if len(servers) == 0 {
		h.logger.Debug("no servers to refresh")
		return
	}

	type serverToolResult struct {
		name      string
		tools     []Tool
		err       error
		initErr   error
		respError string
		hasResult bool
	}

	var wg sync.WaitGroup
	results := make(chan serverToolResult, len(servers))

	for _, serverName := range servers {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				results <- serverToolResult{name: name, err: ctx.Err()}
				return
			default:
			}

			serverCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			h.logger.Debug("checking server for cache refresh", "name", name)

			state, _ := h.pool.GetServerState(name)
			h.logger.Debug("server state", "name", name, "state", state)

			if state != "idle" && state != "running" && state != "busy" {
				h.logger.Warn("server not running, attempting restart for cache refresh", "name", name, "state", state)

				var restartErr error
				for attempt := 0; attempt < 3; attempt++ {
					if attempt > 0 {
						h.logger.Info("retrying server restart for cache", "name", name, "attempt", attempt+1)
						time.Sleep(time.Duration(attempt) * time.Second)
					}

					if err := h.pool.RestartServer(serverCtx, name); err != nil {
						restartErr = err
						continue
					}
					restartErr = nil
					break
				}

				if restartErr != nil {
					h.logger.Error("failed to restart server for cache after retries", "name", name, "error", restartErr)
					h.cacheFailures.Add(1)
					results <- serverToolResult{name: name, err: restartErr}
					return
				}
			}

			initErr := h.initializeServer(serverCtx, name)
			if initErr != nil {
				h.logger.Warn("failed to initialize server, will try without initialization", "name", name, "error", initErr)
			}

			h.logger.Debug("requesting tools/list for cache", "name", name)
			resp, err := h.pool.SendRequestToServer(serverCtx, name, MethodToolsList, nil, 10*time.Second)
			if err != nil {
				h.logger.Error("failed to get tools for cache", "name", name, "error", err)
				h.cacheFailures.Add(1)
				results <- serverToolResult{name: name, err: err, initErr: initErr}
				return
			}

			if resp != nil && resp.Error != nil {
				h.logger.Error("server error during cache population", "name", name, "error", resp.Error.Message)
				h.cacheFailures.Add(1)
				results <- serverToolResult{name: name, respError: resp.Error.Message, initErr: initErr}
				return
			}

			if resp == nil || resp.Result == nil {
				h.logger.Error("server returned no result for cache", "name", name, "resp", fmt.Sprintf("%+v", resp))
				h.cacheFailures.Add(1)
				results <- serverToolResult{name: name, initErr: initErr}
				return
			}

			if len(resp.Result) == 0 || string(resp.Result) == "null" {
				h.logger.Error("server returned null/empty result for cache", "name", name, "resp", fmt.Sprintf("%+v", resp))
				h.cacheFailures.Add(1)
				results <- serverToolResult{name: name, initErr: initErr}
				return
			}

			var toolsResult ToolsListResult
			if err := json.Unmarshal(resp.Result, &toolsResult); err != nil {
				h.logger.Error("failed to parse tools for cache", "name", name, "error", err, "result", string(resp.Result))
				h.cacheFailures.Add(1)
				results <- serverToolResult{name: name, err: err, initErr: initErr}
				return
			}

			h.logger.Debug("caching tools from server", "name", name, "count", len(toolsResult.Tools))
			results <- serverToolResult{name: name, tools: toolsResult.Tools, hasResult: true, initErr: initErr}
		}(serverName)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for result := range results {
		if !result.hasResult {
			continue
		}

		h.toolCache.mu.Lock()
		h.toolCache.tools[result.name] = result.tools
		h.toolCache.mu.Unlock()

		if h.toolStore != nil {
			if err := h.toolStore.SetTools(result.name, toolsToCachedTools(result.tools)); err != nil {
				h.logger.Warn("failed to persist tools to cache", "name", result.name, "error", err)
			}
		}

		h.logger.Debug("cached tools from server", "name", result.name, "count", len(result.tools))
	}
}

func matchesQuery(text string, queryWords []string) bool {
	for _, word := range queryWords {
		if !strings.Contains(text, word) {
			return false
		}
	}
	return true
}

// invokeToolEnvelope is the invoke_tool gateway tool's params, decoded
// without going through map[string]interface{} so that Arguments travels to
// the upstream server byte-for-byte (no float64 round-trip that would lose
// precision on large integers, and no risk of silently dropping a
// non-object payload).
type invokeToolEnvelope struct {
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

func (h *Handler) handleInvokeTool(ctx context.Context, req *Request, params ToolsCallParams) (*Response, error) {
	var envelope invokeToolEnvelope
	if params.Arguments != nil {
		if err := json.Unmarshal(params.Arguments, &envelope); err != nil {
			h.logger.Warn("failed to unmarshal invoke_tool params", "error", err)
			return &Response{
				JSONRPC: JSONRPCVersion,
				Error:   NewError(ErrCodeInvalidParams, fmt.Sprintf("invalid params: %v", err)),
				ID:      req.ID,
			}, nil
		}
	}
	// ApplyDefaults("invoke_tool", ...) is a documented no-op today (see
	// tool_defaults.go): invoke_tool has no defaultable proxy-owned keys.
	// It is intentionally not called here so it can never reach into
	// (and mutate) the upstream `arguments` payload.

	serverName := envelope.Server
	toolName := envelope.Tool
	arguments := envelope.Arguments

	if len(arguments) > 0 && string(arguments) != "null" {
		trimmed := strings.TrimSpace(string(arguments))
		if !strings.HasPrefix(trimmed, "{") {
			return &Response{
				JSONRPC: JSONRPCVersion,
				Error:   NewError(ErrCodeInvalidParams, "arguments must be a JSON object"),
				ID:      req.ID,
			}, nil
		}
	} else {
		arguments = nil
	}

	if serverName == "" || toolName == "" {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeInvalidParams, "server and tool are required. Use list_servers to get server names, then list_tools to discover available tools."),
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

		var restartErr error
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				h.logger.Info("retrying server restart", "name", serverName, "attempt", attempt+1)
				time.Sleep(time.Duration(attempt) * time.Second)
			}

			if err := h.pool.RestartServer(ctx, serverName); err != nil {
				restartErr = err
				continue
			}
			restartErr = nil
			break
		}

		if restartErr != nil {
			h.logger.Error("failed to restart server after retries", "name", serverName, "error", restartErr)
			enrichedError := FormatErrorWithHint(
				fmt.Sprintf("server %s is not running (state: %s) and failed to restart after retries: %v", serverName, state, restartErr),
				serverName, toolName,
			)
			return &Response{
				JSONRPC: JSONRPCVersion,
				Error:   NewError(ErrCodeServerError, enrichedError),
				ID:      req.ID,
			}, nil
		}
		h.logger.Info("server restarted successfully", "name", serverName)
	}

	// Perform MCP initialize handshake if not yet done for this server instance.
	// The MCP protocol requires initialize + notifications/initialized before any tool call.
	if !h.pool.IsServerMCPInitialized(serverName) {
		h.logger.Debug("initializing MCP session with server", "name", serverName)
		if err := h.initializeServer(ctx, serverName); err != nil {
			h.logger.Error("invoke_tool: server initialization failed", "server", serverName, "error", err)
			schema := h.lookupToolSchema(serverName, toolName)
			enrichedError := FormatErrorWithHint(fmt.Sprintf("server initialization failed: %v", err), serverName, toolName)
			errResp := NewError(ErrCodeServerError, enrichedError)
			if schema != nil {
				dataBytes, _ := json.Marshal(map[string]interface{}{
					"tool":   toolName,
					"schema": json.RawMessage(schema),
				})
				errResp.Data = dataBytes
			}
			return &Response{
				JSONRPC: JSONRPCVersion,
				Error:   errResp,
				ID:      req.ID,
			}, nil
		}
		h.pool.MarkServerMCPInitialized(serverName)
	}

	newParams := ToolsCallParams{
		Name:      toolName,
		Arguments: arguments,
	}
	paramsBytes, _ := json.Marshal(newParams)

	resp, err := h.pool.SendRequestToServer(ctx, serverName, MethodToolsCall, paramsBytes, h.timeoutFor(serverName))
	if err != nil {
		// A transport/proxy-level failure (timeout, connection error, server
		// not reachable) rather than a structured upstream JSON-RPC error:
		// keep the enriched, hint-carrying ErrCodeServerError.
		h.logger.Error("invoke_tool failed", "server", serverName, "tool", toolName, "error", err)
		schema := h.lookupToolSchema(serverName, toolName)
		enrichedError := FormatErrorWithHint(fmt.Sprintf("tool invocation failed: %v", err), serverName, toolName)
		errResp := NewError(ErrCodeServerError, enrichedError)
		if schema != nil {
			dataBytes, _ := json.Marshal(map[string]interface{}{
				"tool":   toolName,
				"schema": json.RawMessage(schema),
			})
			errResp.Data = dataBytes
		}
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   errResp,
			ID:      req.ID,
		}, nil
	}

	if resp != nil && resp.Error != nil {
		// Forward the upstream JSON-RPC error unchanged (same code and
		// message the upstream server sent, redacted downstream by the
		// firewall middleware). This used to be dropped entirely, leaving
		// the client with a response that had neither result nor error.
		h.logger.Error("invoke_tool: upstream error", "server", serverName, "tool", toolName, "code", resp.Error.Code, "message", resp.Error.Message)
		errResp := &Error{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}
		if schema := h.lookupToolSchema(serverName, toolName); schema != nil {
			dataBytes, marshalErr := json.Marshal(map[string]interface{}{
				"tool":          toolName,
				"schema":        json.RawMessage(schema),
				"upstream_data": rawOrNull(resp.Error.Data),
			})
			if marshalErr == nil {
				errResp.Data = dataBytes
			}
		}
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   errResp,
			ID:      req.ID,
		}, nil
	}

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resp.Result,
		ID:      req.ID,
	}, nil
}

// rawOrNull returns data unchanged, or a JSON null literal when data is
// empty, so it always marshals to a valid JSON value inside a map.
func rawOrNull(data json.RawMessage) json.RawMessage {
	if len(data) == 0 {
		return json.RawMessage("null")
	}
	return data
}

// parseToolName splits a namespaced tool reference into its server and tool
// parts by matching the longest configured server name that is a prefix of
// fullName, followed by '_' or '.'. See SplitToolName.
func (h *Handler) parseToolName(fullName string) (serverName, toolName string, err error) {
	return SplitToolName(fullName, h.pool.ListServers())
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

func parseInputSchema(schema json.RawMessage) (required, optional []ParamInfo) {
	var schemaMap map[string]interface{}
	if err := json.Unmarshal(schema, &schemaMap); err != nil {
		return nil, nil
	}

	properties, ok := schemaMap["properties"].(map[string]interface{})
	if !ok {
		return nil, nil
	}

	var requiredNames []string
	if req, ok := schemaMap["required"].([]interface{}); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				requiredNames = append(requiredNames, s)
			}
		}
	}

	isRequired := make(map[string]bool)
	for _, name := range requiredNames {
		isRequired[name] = true
	}

	for name, prop := range properties {
		propMap, ok := prop.(map[string]interface{})
		if !ok {
			continue
		}
		typeVal, _ := propMap["type"].(string)
		descVal, _ := propMap["description"].(string)

		param := ParamInfo{
			Name:        name,
			Type:        typeVal,
			IsRequired:  isRequired[name],
			Description: descVal,
		}

		if isRequired[name] {
			required = append(required, param)
		} else {
			optional = append(optional, param)
		}
	}
	return required, optional
}

func formatToolSearchResult(serverName, toolName, description string, required, optional []ParamInfo, maxDescChars int) string {
	var sb strings.Builder
	sb.WriteString(serverName)
	sb.WriteString("_")
	sb.WriteString(toolName)
	sb.WriteString(": ")
	sb.WriteString(truncateDescription(description, maxDescChars))

	if len(required) > 0 {
		sb.WriteString(" [")
		for i, p := range required {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(p.Name)
			sb.WriteString(": ")
			sb.WriteString(p.Type)
		}
		sb.WriteString("]")
	}

	if len(optional) > 0 {
		sb.WriteString(" {")
		for i, p := range optional {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(p.Name)
			sb.WriteString(": ")
			sb.WriteString(p.Type)
		}
		sb.WriteString("}")
	}

	return sb.String()
}

func formatTool(tool Tool, serverName string, maxDescChars int) string {
	required, optional := parseInputSchema(tool.InputSchema)
	return formatToolSearchResult(serverName, tool.Name, tool.Description, required, optional, maxDescChars)
}

func truncateDescription(description string, maxChars int) string {
	if maxChars <= 0 || len(description) <= maxChars {
		return description
	}
	if maxChars < 3 {
		return description[:maxChars]
	}
	return description[:maxChars-3] + "..."
}

func (h *Handler) lookupToolSchema(serverName, toolName string) json.RawMessage {
	h.toolCache.mu.RLock()
	defer h.toolCache.mu.RUnlock()

	tools, ok := h.toolCache.tools[serverName]
	if !ok {
		return nil
	}

	for _, tool := range tools {
		if tool.Name == toolName {
			return tool.InputSchema
		}
	}
	return nil
}

func toolsToCachedTools(tools []Tool) []toolstore.CachedTool {
	result := make([]toolstore.CachedTool, len(tools))
	for i, t := range tools {
		result[i] = toolstore.CachedTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}
	return result
}
