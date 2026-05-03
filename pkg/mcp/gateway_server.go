package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
)

type MCPServerInstance struct {
	server    *server.MCPServer
	logger   *slog.Logger
	mcpPool  *pool.StdioPool
}

func NewMCPServerInstance(logger *slog.Logger) *MCPServerInstance {
	if logger == nil {
		logger = slog.Default()
	}

	mcpServer := server.NewMCPServer(
		"LeanProxy MCP Gateway",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(true, true),
		server.WithPromptCapabilities(true),
	)

	return &MCPServerInstance{
		server:  mcpServer,
		logger: logger,
	}
}

func (m *MCPServerInstance) SetStdioPool(p *pool.StdioPool) {
	m.mcpPool = p
}

func (m *MCPServerInstance) SetupGatewayTools(mcpServer *MCPServer) {
	searchToolsTool := mcp.NewTool(
		"search_tools",
		mcp.WithDescription("Search for available MCP tools across all proxied servers. Returns tool names with summarized descriptions. Use this to discover what tools are available before invoking them."),
		mcp.WithString("query",
			mcp.Description("Search query to find tools"),
		),
	)

	invokeTool := mcp.NewTool(
		"invoke_tool",
		mcp.WithDescription("Invoke a tool on a specific MCP server. First use search_tools to find the right tool, then invoke it with the server name and parameters."),
		mcp.WithString("server",
			mcp.Required(),
			mcp.Description("Server name"),
		),
		mcp.WithString("tool",
			mcp.Required(),
			mcp.Description("Tool name to invoke"),
		),
		mcp.WithObject("arguments",
			mcp.Description("Tool arguments as JSON object"),
		),
	)

	listServersTool := mcp.NewTool(
		"list_servers",
		mcp.WithDescription("List all configured MCP servers and their current status."),
	)

	m.server.AddTool(searchToolsTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return m.handleSearchTools(ctx, request, mcpServer)
	})

	m.server.AddTool(invokeTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return m.handleInvokeTool(ctx, request, mcpServer)
	})

	m.server.AddTool(listServersTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return m.handleListServers(ctx, request, mcpServer)
	})

	m.logger.Info("gateway tools registered")
}

func (m *MCPServerInstance) handleSearchTools(ctx context.Context, request mcp.CallToolRequest, mcpServer *MCPServer) (*mcp.CallToolResult, error) {
	var query string
	if request.GetArguments() != nil {
		if q, ok := request.GetArguments()["query"].(string); ok {
			query = q
		}
	}

	m.logger.Info("search_tools called", "query", query)

	results := mcpServer.SearchTools(query)

	if len(results) == 0 {
		return mcp.NewToolResultText("No tools found matching your query. Try a different search term or invoke a tool directly using invoke_tool."), nil
	}

	output := "Available tools:\n"
	for _, r := range results {
		output += "- " + r + "\n"
	}

	return mcp.NewToolResultText(output), nil
}

func (m *MCPServerInstance) handleInvokeTool(ctx context.Context, request mcp.CallToolRequest, mcpServer *MCPServer) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	serverName, _ := args["server"].(string)
	toolName, _ := args["tool"].(string)
	toolArgs, _ := args["arguments"].(map[string]interface{})

	if serverName == "" || toolName == "" {
		return mcp.NewToolResultError("server and tool are required"), nil
	}

	m.logger.Info("invoke_tool called", "server", serverName, "tool", toolName)

	state, err := m.mcpPool.GetServerState(serverName)
	if err != nil {
		m.logger.Warn("server not running", "name", serverName, "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("server %s not found", serverName)), nil
	}

	if state != "idle" && state != "running" && state != "busy" {
		m.logger.Warn("server not running, restarting", "name", serverName, "state", state)
		if err := m.mcpPool.RestartServer(ctx, serverName); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("server %s failed to restart: %v", serverName, err)), nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	params := map[string]interface{}{
		"name":      toolName,
		"arguments": toolArgs,
	}
	paramsBytes, _ := json.Marshal(params)

	resp, err := m.mcpPool.SendRequestToServer(ctx, serverName, "tools/call", paramsBytes, 30*time.Second)
	if err != nil {
		m.logger.Error("invoke_tool failed", "server", serverName, "tool", toolName, "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("tool invocation failed: %v", err)), nil
	}

	if resp != nil && resp.Error != nil {
		return mcp.NewToolResultError(fmt.Sprintf("server error: %v", resp.Error)), nil
	}

	if resp.Result != nil {
		var result map[string]interface{}
		if json.Unmarshal(resp.Result, &result) == nil {
			if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
				text := ""
				for _, c := range content {
					if cMap, ok := c.(map[string]interface{}); ok {
						if t, ok := cMap["text"].(string); ok {
							text += t
						}
					}
				}
				return mcp.NewToolResultText(text), nil
			}
		}
		return mcp.NewToolResultText(string(resp.Result)), nil
	}

	return mcp.NewToolResultText(""), nil
}

func (m *MCPServerInstance) handleListServers(ctx context.Context, request mcp.CallToolRequest, mcpServer *MCPServer) (*mcp.CallToolResult, error) {
	m.logger.Info("list_servers called")

	servers := m.mcpPool.ListServers()
	if len(servers) == 0 {
		return mcp.NewToolResultText("No servers configured."), nil
	}

	output := "Configured servers:\n"
	for _, name := range servers {
		state, _ := m.mcpPool.GetServerState(name)
		output += fmt.Sprintf("- %s (%s)\n", name, state)
	}

	return mcp.NewToolResultText(output), nil
}

func (m *MCPServerInstance) GetServer() *server.MCPServer {
	return m.server
}

func (m *MCPServerInstance) ServeStreamableHTTP(addr string) error {
	httpServer := server.NewStreamableHTTPServer(m.server)
	m.logger.Info("starting Streamable HTTP server", "addr", addr)
	return httpServer.Start(addr)
}

func (m *MCPServerInstance) ServeSSE(addr string) error {
	sseServer := server.NewSSEServer(m.server)
	m.logger.Info("starting SSE server", "addr", addr)
	return sseServer.Start(addr)
}

func (m *MCPServerInstance) ServeStdio() error {
	m.logger.Info("starting stdio server")
	return server.ServeStdio(m.server)
}