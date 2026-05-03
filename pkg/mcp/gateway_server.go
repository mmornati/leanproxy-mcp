package mcp

import (
	"context"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
)

type MCPServerInstance struct {
	server *server.MCPServer
	logger *slog.Logger
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
	searchToolsTool := mcp.NewTool(
		"search_tools",
		mcp.WithDescription("Search for available MCP tools across all proxied servers"),
		mcp.WithString("query",
			mcp.Description("Search query to find tools"),
		),
	)

	invokeTool := mcp.NewTool(
		"invoke_tool",
		mcp.WithDescription("Invoke a tool on a specific MCP server"),
		mcp.WithString("server",
			mcp.Required(),
			mcp.Description("Server name"),
		),
		mcp.WithString("tool",
			mcp.Required(),
			mcp.Description("Tool name to invoke"),
		),
		mcp.WithObject("arguments",
			mcp.Description("Tool arguments"),
		),
	)

	m.server.AddTool(searchToolsTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return m.handleSearchTools(ctx, request)
	})

	m.server.AddTool(invokeTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return m.handleInvokeTool(ctx, request)
	})

	m.logger.Info("gateway tools registered for SSE/HTTP")
}

func (m *MCPServerInstance) handleSearchTools(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	query, _ := args["query"].(string)
	m.logger.Info("search_tools called", "query", query)
	return mcp.NewToolResultText("Search functionality available via stdio mode"), nil
}

func (m *MCPServerInstance) handleInvokeTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	serverName, _ := args["server"].(string)
	toolName, _ := args["tool"].(string)
	m.logger.Info("invoke_tool called", "server", serverName, "tool", toolName)
	return mcp.NewToolResultText("Tool execution via stdio mode"), nil
}

func (m *MCPServerInstance) handleListServers(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	servers := m.mcpPool.ListServers()
	text := "Configured servers:\n"
	for _, s := range servers {
		state, _ := m.mcpPool.GetServerState(s)
		text += "- " + s + " (" + string(state) + ")\n"
	}
	return mcp.NewToolResultText(text), nil
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