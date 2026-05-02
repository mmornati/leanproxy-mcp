package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

type JITConfig struct {
	Enabled  bool
	CacheSize int
	CacheTTL string
}

type JITHandler struct {
	cache      SchemaCache
	registry   RegistryGetter
	forward    RequestForwarder
	logger     *slog.Logger
	enabled    bool
	serverName string
}

type RegistryGetter interface {
	GetToolSchema(ctx context.Context, serverID, toolName string) (json.RawMessage, error)
}

type RequestForwarder interface {
	ForwardRequest(ctx context.Context, req JSONRPCRequest) (JSONRPCResponse, error)
}

func NewJITHandler(cache SchemaCache, registry RegistryGetter, forward RequestForwarder, logger *slog.Logger, serverName string, enabled bool) *JITHandler {
	return &JITHandler{
		cache:      cache,
		registry:   registry,
		forward:    forward,
		logger:     logger,
		enabled:    enabled,
		serverName: serverName,
	}
}

func (h *JITHandler) HandleGetToolSchema(ctx context.Context, req JSONRPCRequest) (JSONRPCResponse, error) {
	if !h.enabled {
		return h.forward.ForwardRequest(ctx, req)
	}

	toolName, err := h.extractToolName(req.Params)
	if err != nil {
		return newErrorResponse(err, req.ID), nil
	}

	cacheKey := h.serverName + "/" + toolName

	if schema, ok := h.cache.Get(cacheKey); ok {
		if h.logger != nil {
			h.logger.Debug("jit cache hit", "tool", toolName)
		}
		return newSuccessResponse(schema, req.ID), nil
	}

	if h.logger != nil {
		h.logger.Debug("jit cache miss", "tool", toolName)
	}

	schema, err := h.registry.GetToolSchema(ctx, h.serverName, toolName)
	if err != nil || schema == nil {
		if h.logger != nil {
			h.logger.Debug("schema not in registry, forwarding to server", "tool", toolName, "error", err)
		}
		return h.forward.ForwardRequest(ctx, req)
	}

	h.cache.Set(cacheKey, schema)
	return newSuccessResponse(schema, req.ID), nil
}

func (h *JITHandler) extractToolName(params json.RawMessage) (string, error) {
	if params == nil {
		return "", fmt.Errorf("jit: params is nil")
	}

	var paramsMap map[string]interface{}
	if err := json.Unmarshal(params, &paramsMap); err != nil {
		return "", fmt.Errorf("jit: parse params: %w", err)
	}

	nameVal, ok := paramsMap["name"]
	if !ok {
		return "", fmt.Errorf("jit: missing 'name' param")
	}

	name, ok := nameVal.(string)
	if !ok {
		return "", fmt.Errorf("jit: 'name' param is not a string")
	}

	return strings.TrimSpace(name), nil
}

func newSuccessResponse(result interface{}, id interface{}) JSONRPCResponse {
	resultBytes, _ := json.Marshal(result)
	return JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  resultBytes,
		ID:      id,
	}
}

func newErrorResponse(err error, id interface{}) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: "2.0",
		Error: &JSONRPCError{
			Code:    ErrCodeInternalError,
			Message: err.Error(),
		},
		ID: id,
	}
}

func IsGetToolSchemaRequest(method string) bool {
	return strings.EqualFold(method, "get_tool_schema")
}