package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/compactor"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
)

type Handler struct {
	pool       *pool.StdioPool
	compactor  *compactor.Compactor
	manifest   *AggregatedManifest
	logger     *slog.Logger
	timeout    time.Duration
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
	}
}

func NewHandlerWithCompactor(p *pool.StdioPool, c *compactor.Compactor, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		pool:      p,
		compactor: c,
		logger:    logger,
		timeout:   30 * time.Second,
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
	case MethodToolsList:
		return h.handleToolsList(ctx, req)
	case MethodToolsCall:
		return h.handleToolsCall(ctx, req)
	case MethodResourcesList:
		return h.handleResourcesList(ctx, req)
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
	h.logger.Debug("tools/list request received, returning leanproxy tools only")

	leanproxyTools := []Tool{
		{
			Name:        "leanproxy_savings",
			Description: "Display token savings statistics",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "leanproxy_report",
			Description: "Generate a token savings report",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"format":{"type":"string","enum":["markdown","json"]}}}}`),
		},
		{
			Name:        "leanproxy_status",
			Description: "Show status of proxied MCP servers",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"server":{"type":"string"}}}`),
		},
	}

	result := ToolsListResult{Tools: leanproxyTools}
	resultBytes, _ := json.Marshal(result)

	h.logger.Info("tools list sent to client", "count", len(leanproxyTools))

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}, nil
}

func (h *Handler) collectTools(ctx context.Context) (*AggregatedManifest, error) {
	servers := h.pool.ListServers()
	manifest := &AggregatedManifest{
		Tools:     make([]Tool, 0),
		Resources: make([]Resource, 0),
		Prompts:   make([]Prompt, 0),
	}

	processor := compactor.NewManifestProcessor(h.logger)

	time.Sleep(2 * time.Second)

	for _, serverName := range servers {
		state, _ := h.pool.GetServerState(serverName)
		h.logger.Debug("checking server for tools", "name", serverName, "state", state)

		if state != "idle" && state != "running" && state != "busy" {
			h.logger.Debug("server not in running state, skipping", "name", serverName, "state", state)
			continue
		}

		h.logger.Debug("initializing backend server", "name", serverName)
		if err := h.initializeServer(ctx, serverName); err != nil {
			h.logger.Warn("failed to initialize server", "name", serverName, "error", err)
			continue
		}

		h.logger.Debug("requesting tools/list from server", "name", serverName)
		resp, err := h.pool.SendRequestToServer(ctx, serverName, MethodToolsList, nil, 10*time.Second)
		if err != nil {
			h.logger.Warn("failed to get tools from server", "name", serverName, "error", err)
			continue
		}

		if resp.Result != nil {
			var toolsResult ToolsListResult
			if err := json.Unmarshal(resp.Result, &toolsResult); err == nil {
				h.logger.Debug("received tools from server", "name", serverName, "count", len(toolsResult.Tools))

				rawTools := make([]compactor.RawTool, 0, len(toolsResult.Tools))
				for _, tool := range toolsResult.Tools {
					paramsBytes, _ := json.Marshal(tool.InputSchema)
					rawTools = append(rawTools, compactor.RawTool{
						Name:        tool.Name,
						Description: tool.Description,
						Parameters:  paramsBytes,
					})
				}

				rawManifest := compactor.RawManifest{
					Name:  serverName,
					Tools: rawTools,
				}

				distilled, err := processor.Process(ctx, rawManifest)
				if err != nil {
					h.logger.Warn("failed to compact manifest, using raw tools", "name", serverName, "error", err)
					for _, tool := range toolsResult.Tools {
						tool.Name = fmt.Sprintf("%s_%s", serverName, tool.Name)
						manifest.Tools = append(manifest.Tools, tool)
					}
				} else {
					for _, tool := range distilled.Tools {
						paramsBytes, _ := json.Marshal(tool.Parameters)
						manifest.Tools = append(manifest.Tools, Tool{
							Name:        fmt.Sprintf("%s_%s", serverName, tool.Name),
							Description: tool.Description,
							InputSchema: paramsBytes,
						})
					}
					h.logger.Info("collected and distilled tools from server", "name", serverName, "original_count", len(toolsResult.Tools), "distilled_count", len(distilled.Tools))
				}
			} else {
				h.logger.Warn("failed to parse tools list from server", "name", serverName, "error", err)
			}
		} else if resp.Error != nil {
			h.logger.Warn("server returned error", "name", serverName, "error", resp.Error.Message)
		} else {
			h.logger.Warn("server returned no result and no error", "name", serverName)
		}
	}

	return manifest, nil
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

	resp, err := h.pool.SendRequestToServer(ctx, serverName, MethodInitialize, paramsBytes, 10*time.Second)
	if err != nil {
		return fmt.Errorf("initialize request failed: %w", err)
	}

	if resp.Error != nil {
		return fmt.Errorf("server returned error: %s", resp.Error.Message)
	}

	h.logger.Debug("server initialized, sending initialized notification", "name", serverName)

	initializedNotification := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "initialized",
		"params": map[string]interface{}{
			"capabilities": ServerCapabilities{},
		},
	}
	notifBytes, _ := json.Marshal(initializedNotification)
	h.pool.SendRequestToServer(ctx, serverName, "initialized", notifBytes, 5*time.Second)

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

	if strings.HasPrefix(params.Name, "leanproxy_") {
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
	tool := strings.TrimPrefix(params.Name, "leanproxy_")

	result := map[string]interface{}{
		"content": []map[string]string{
			{"type": "text", "text": fmt.Sprintf("LeanProxy tool '%s' called. Tool execution requires routing to backend MCP servers which is not yet fully implemented in this mode.", tool)},
		},
	}

	resultBytes, _ := json.Marshal(result)
	h.logger.Info("leanproxy tool called", "tool", tool)

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}, nil
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