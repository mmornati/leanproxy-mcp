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
	if h.manifest != nil {
		result := ToolsListResult{Tools: h.manifest.Tools}
		resultBytes, _ := json.Marshal(result)
		return &Response{
			JSONRPC: JSONRPCVersion,
			Result:  resultBytes,
			ID:      req.ID,
		}, nil
	}

	var params ToolsListParams
	if req.Params != nil {
		json.Unmarshal(req.Params, &params)
	}

	manifest, err := h.collectTools(ctx)
	if err != nil {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeInternalError, fmt.Sprintf("failed to collect tools: %v", err)),
			ID:      req.ID,
		}, nil
	}

	h.manifest = manifest
	result := ToolsListResult{Tools: manifest.Tools}
	resultBytes, _ := json.Marshal(result)

	h.logger.Info("tools list aggregated", "count", len(manifest.Tools))

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

	for _, serverName := range servers {
		state, _ := h.pool.GetServerState(serverName)
		h.logger.Debug("checking server for tools", "name", serverName, "state", state)

		if state != "idle" && state != "running" && state != "busy" {
			h.logger.Debug("server not in running state, skipping", "name", serverName, "state", state)
			continue
		}

		if err := h.initializeServer(ctx, serverName); err != nil {
			h.logger.Warn("failed to initialize server", "name", serverName, "error", err)
			continue
		}

		resp, err := h.pool.SendRequestToServer(ctx, serverName, MethodToolsList, nil, 10*time.Second)
		if err != nil {
			h.logger.Warn("failed to get tools from server", "name", serverName, "error", err)
			continue
		}

		if resp.Result != nil {
			var toolsResult ToolsListResult
			if err := json.Unmarshal(resp.Result, &toolsResult); err == nil {
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
	var params ToolsCallParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &Response{
				JSONRPC: JSONRPCVersion,
				Error:   NewError(ErrCodeInvalidParams, fmt.Sprintf("invalid params: %v", err)),
				ID:      req.ID,
			}, nil
		}
	}

	if params.Name == "" {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeInvalidParams, "tool name is required"),
			ID:      req.ID,
		}, nil
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