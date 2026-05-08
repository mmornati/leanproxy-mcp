package mcp

import (
	"context"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
)

type MCPServerInstance struct {
	server  *server.MCPServer
	logger  *slog.Logger
	mcpPool *pool.StdioPool
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
		server: mcpServer,
		logger: logger,
	}
}

func (m *MCPServerInstance) SetStdioPool(p *pool.StdioPool) {
	m.mcpPool = p
}

func (m *MCPServerInstance) SetupGatewayTools() {
	listToolsTool := mcp.NewTool(
		"list_tools",
		mcp.WithDescription("List all tools available on a specific MCP server. Always call list_servers first to get server names, then use this tool to see available tools on a specific server."),
		mcp.WithString("server_name",
			mcp.Required(),
			mcp.Description("MCP server name (e.g., 'garmin', 'github', 'filesystem'). Use list_servers to get available server names."),
		),
		mcp.WithNumber("max_description_chars",
			mcp.Description("Max description length (default 200, min 50, max 500)"),
		),
	)

	invokeTool := mcp.NewTool(
		"invoke_tool",
		mcp.WithDescription("Invoke a tool on a configured MCP server. First use list_servers to get server names, then use list_tools to find available tools on that server."),
		mcp.WithString("server",
			mcp.Required(),
			mcp.Description("Server name from list_servers (e.g., 'garmin', 'github')"),
		),
		mcp.WithString("tool",
			mcp.Required(),
			mcp.Description("Tool name from list_tools (e.g., 'get_activities', 'list_issues')"),
		),
		mcp.WithObject("arguments",
			mcp.Description("Tool arguments as key-value pairs (optional, depends on tool)"),
		),
	)

	m.server.AddTool(listToolsTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return m.handleListTools(ctx, request)
	})

	m.server.AddTool(invokeTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return m.handleInvokeTool(ctx, request)
	})

	m.logger.Info("gateway tools registered for SSE/HTTP")
}

func (m *MCPServerInstance) handleListTools(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	serverName, _ := args["server_name"].(string)
	m.logger.Info("list_tools called", "server_name", serverName)
	return mcp.NewToolResultText("List tools functionality available via stdio mode. Use stdio mode for full tool listing."), nil
}

func (m *MCPServerInstance) handleInvokeTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	serverName, _ := args["server"].(string)
	toolName, _ := args["tool"].(string)
	m.logger.Info("invoke_tool called", "server", serverName, "tool", toolName)
	return mcp.NewToolResultText("Tool execution via stdio mode"), nil
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
