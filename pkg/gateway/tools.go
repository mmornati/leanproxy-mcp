package gateway

import (
	"context"
	"log/slog"

	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/mmornati/leanproxy-mcp/pkg/router"
)

type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema,omitempty"`
}

type ServerInfo struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Transport string `json:"transport"`
	ToolCount int    `json:"tool_count"`
}

type ToolSearchResult struct {
	ToolName    string `json:"tool_name"`
	ServerName  string `json:"server_name"`
	Description string `json:"description"`
}

type InvokeToolParams struct {
	ServerName string                 `json:"server_name"`
	ToolName   string                 `json:"tool_name"`
	Arguments  map[string]interface{} `json:"arguments,omitempty"`
}

type GatewayTools interface {
	ListTools() []Tool
	InvokeTool(ctx context.Context, params InvokeToolParams) (interface{}, error)
	SearchTools(ctx context.Context, query string) ([]ToolSearchResult, error)
	ListServers(ctx context.Context) ([]ServerInfo, error)
}

type gatewayTools struct {
	serverReg registry.Registry
	toolReg   router.ToolRegistry
	router    router.Router
	logger    *slog.Logger
}

func NewGatewayTools(serverReg registry.Registry, toolReg router.ToolRegistry, router router.Router, logger *slog.Logger) GatewayTools {
	return &gatewayTools{
		serverReg: serverReg,
		toolReg:   toolReg,
		router:    router,
		logger:    logger,
	}
}

var listServersTool = Tool{
	Name:        "list_servers",
	Description: "List all MCP servers configured in this gateway",
	InputSchema: map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{},
	},
}

var invokeToolTool = Tool{
	Name:        "invoke_tool",
	Description: "Invoke a tool on a specific MCP server",
	InputSchema: map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"server_name": map[string]interface{}{
				"type": "string",
			},
			"tool_name": map[string]interface{}{
				"type": "string",
			},
			"arguments": map[string]interface{}{
				"type": "object",
			},
		},
		"required": []string{"server_name", "tool_name"},
	},
}

var listToolsTool = Tool{
	Name:        "list_tools",
	Description: "List all tools available on a specific MCP server",
	InputSchema: map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"server_name": map[string]interface{}{
				"type": "string",
			},
		},
		"required": []string{"server_name"},
	},
}

func (g *gatewayTools) ListTools() []Tool {
	return []Tool{listServersTool, invokeToolTool, listToolsTool}
}