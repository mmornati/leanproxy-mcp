package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/mmornati/leanproxy-mcp/pkg/router"
)

func (g *gatewayTools) ListServers(ctx context.Context) ([]ServerInfo, error) {
	entries, err := g.serverReg.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("gateway: list servers: %w", err)
	}

	tools, _ := g.toolReg.ListTools(ctx)
	toolCountByServer := make(map[string]int)
	for _, t := range tools {
		toolCountByServer[t.ServerID]++
	}

	result := make([]ServerInfo, 0, len(entries))
	for _, entry := range entries {
		info := ServerInfo{
			Name:      entry.ID,
			Status:    string(entry.Health),
			Transport: string(entry.Transport),
			ToolCount: toolCountByServer[entry.ID],
		}
		result = append(result, info)
	}

	g.logger.Debug("list_servers returned", "count", len(result))
	return result, nil
}

func (g *gatewayTools) InvokeTool(ctx context.Context, params InvokeToolParams) (interface{}, error) {
	if params.ServerName == "" {
		return nil, proxy.NewJSONRPCError(proxy.ErrCodeInvalidParams, "server_name is required")
	}
	if params.ToolName == "" {
		return nil, proxy.NewJSONRPCError(proxy.ErrCodeInvalidParams, "tool_name is required")
	}

	entry, err := g.serverReg.Get(ctx, params.ServerName)
	if err != nil {
		return nil, proxy.NewJSONRPCError(proxy.ErrCodeInvalidParams, fmt.Sprintf("server not found: %s", params.ServerName))
	}

	if entry.Health != registry.HealthHealthy && entry.Health != registry.HealthUnknown {
		return nil, proxy.NewJSONRPCError(proxy.ErrCodeInvalidParams, fmt.Sprintf("server not running: %s", params.ServerName))
	}

	allTools, err := g.toolReg.ListTools(ctx)
	if err != nil {
		return nil, proxy.NewJSONRPCError(proxy.ErrCodeInternalError, "failed to list tools")
	}

	var foundTool *router.ToolEntry
	for i := range allTools {
		t := &allTools[i]
		if t.ServerID != params.ServerName {
			continue
		}
		if t.Name == params.ToolName || t.Name == params.ServerName+"."+params.ToolName || strings.HasSuffix(t.Name, "."+params.ToolName) {
			foundTool = t
			break
		}
	}

	if foundTool == nil {
		return nil, proxy.NewJSONRPCError(proxy.ErrCodeInvalidParams, fmt.Sprintf("tool %s not found on server %s", params.ToolName, params.ServerName))
	}

	argsJSON, err := json.Marshal(params.Arguments)
	if err != nil {
		return nil, proxy.NewJSONRPCError(proxy.ErrCodeInvalidParams, "invalid arguments format")
	}

	req := proxy.JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  foundTool.Name,
		Params:  argsJSON,
		ID:      1,
	}

	g.logger.Debug("invoking tool", "server", params.ServerName, "tool", params.ToolName, "full_method", foundTool.Name)

	return map[string]interface{}{
		"status":  "forwarded",
		"server":  params.ServerName,
		"tool":    params.ToolName,
		"request": req,
	}, nil
}