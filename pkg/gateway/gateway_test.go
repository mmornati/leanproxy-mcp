package gateway

import (
	"context"
	"log/slog"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/mmornati/leanproxy-mcp/pkg/router"
)

func TestListTools(t *testing.T) {
	logger := slog.Default()

	serverReg := registry.NewRegistry(logger, "")
	toolReg := router.NewToolRegistry()
	r := router.NewRouter(toolReg, serverReg, logger)

	gw := NewGatewayTools(serverReg, toolReg, r, logger)

	tools := gw.ListTools()

	if len(tools) != 3 {
		t.Errorf("ListTools() returned %d tools, want 3", len(tools))
	}

	expectedTools := map[string]bool{
		"list_servers": false,
		"invoke_tool":   false,
		"search_tools": false,
	}

	for _, tool := range tools {
		if _, ok := expectedTools[tool.Name]; ok {
			expectedTools[tool.Name] = true
		} else {
			t.Errorf("ListTools() returned unexpected tool: %s", tool.Name)
		}
	}

	for name, found := range expectedTools {
		if !found {
			t.Errorf("ListTools() missing expected tool: %s", name)
		}
	}
}

func TestListServers(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	serverReg := registry.NewRegistry(logger, "")
	toolReg := router.NewToolRegistry()
	r := router.NewRouter(toolReg, serverReg, logger)

	gw := NewGatewayTools(serverReg, toolReg, r, logger)

	githubServer := registry.ServerEntry{
		ID:        "github-1",
		Transport: registry.TransportStdio,
		Health:    registry.HealthHealthy,
	}
	_ = serverReg.Register(ctx, githubServer)

	_ = toolReg.RegisterTool(ctx, router.ToolEntry{
		Name:      "github.create_issue",
		Namespace: "github",
		ServerID:  "github-1",
	})

	servers, err := gw.ListServers(ctx)
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}

	if len(servers) != 1 {
		t.Errorf("ListServers() returned %d servers, want 1", len(servers))
	}

	if servers[0].Name != "github-1" {
		t.Errorf("ListServers()[0].Name = %q, want %q", servers[0].Name, "github-1")
	}

	if servers[0].Status != "healthy" {
		t.Errorf("ListServers()[0].Status = %q, want %q", servers[0].Status, "healthy")
	}

	if servers[0].Transport != "stdio" {
		t.Errorf("ListServers()[0].Transport = %q, want %q", servers[0].Transport, "stdio")
	}

	if servers[0].ToolCount != 1 {
		t.Errorf("ListServers()[0].ToolCount = %d, want %d", servers[0].ToolCount, 1)
	}
}

func TestListServersEmpty(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	serverReg := registry.NewRegistry(logger, "")
	toolReg := router.NewToolRegistry()
	r := router.NewRouter(toolReg, serverReg, logger)

	gw := NewGatewayTools(serverReg, toolReg, r, logger)

	servers, err := gw.ListServers(ctx)
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}

	if len(servers) != 0 {
		t.Errorf("ListServers() returned %d servers, want 0", len(servers))
	}
}

func TestInvokeTool(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	serverReg := registry.NewRegistry(logger, "")
	toolReg := router.NewToolRegistry()
	r := router.NewRouter(toolReg, serverReg, logger)

	gw := NewGatewayTools(serverReg, toolReg, r, logger)

	githubServer := registry.ServerEntry{
		ID:        "github-1",
		Transport: registry.TransportStdio,
		Health:    registry.HealthHealthy,
	}
	_ = serverReg.Register(ctx, githubServer)

	_ = toolReg.RegisterTool(ctx, router.ToolEntry{
		Name:      "github.create_issue",
		Namespace: "github",
		ServerID:  "github-1",
	})

	t.Run("missing server_name", func(t *testing.T) {
		params := InvokeToolParams{
			ServerName: "",
			ToolName:   "create_issue",
		}
		_, err := gw.InvokeTool(ctx, params)
		if err == nil {
			t.Error("InvokeTool() expected error for missing server_name, got nil")
		}
		rpcErr, ok := err.(*proxy.JSONRPCError)
		if !ok {
			t.Fatalf("InvokeTool() returned error type %T, want *proxy.JSONRPCError", err)
		}
		if rpcErr.Code != proxy.ErrCodeInvalidParams {
			t.Errorf("InvokeTool() error code = %d, want %d", rpcErr.Code, proxy.ErrCodeInvalidParams)
		}
	})

	t.Run("missing tool_name", func(t *testing.T) {
		params := InvokeToolParams{
			ServerName: "github-1",
			ToolName:   "",
		}
		_, err := gw.InvokeTool(ctx, params)
		if err == nil {
			t.Error("InvokeTool() expected error for missing tool_name, got nil")
		}
	})

	t.Run("nonexistent server", func(t *testing.T) {
		params := InvokeToolParams{
			ServerName: "nonexistent",
			ToolName:   "create_issue",
		}
		_, err := gw.InvokeTool(ctx, params)
		if err == nil {
			t.Error("InvokeTool() expected error for nonexistent server, got nil")
		}
		rpcErr, ok := err.(*proxy.JSONRPCError)
		if !ok {
			t.Fatalf("InvokeTool() returned error type %T, want *proxy.JSONRPCError", err)
		}
		if rpcErr.Code != proxy.ErrCodeInvalidParams {
			t.Errorf("InvokeTool() error code = %d, want %d", rpcErr.Code, proxy.ErrCodeInvalidParams)
		}
	})

	t.Run("stopped server", func(t *testing.T) {
		stoppedServer := registry.ServerEntry{
			ID:        "stopped-1",
			Transport: registry.TransportStdio,
			Health:    registry.HealthUnhealthy,
		}
		_ = serverReg.Register(ctx, stoppedServer)

		params := InvokeToolParams{
			ServerName: "stopped-1",
			ToolName:   "some_tool",
		}
		_, err := gw.InvokeTool(ctx, params)
		if err == nil {
			t.Error("InvokeTool() expected error for stopped server, got nil")
		}
	})

	t.Run("valid invoke", func(t *testing.T) {
		params := InvokeToolParams{
			ServerName: "github-1",
			ToolName:   "create_issue",
			Arguments:  map[string]interface{}{"title": "Test Issue"},
		}
		result, err := gw.InvokeTool(ctx, params)
		if err != nil {
			t.Fatalf("InvokeTool() error = %v", err)
		}
		if result == nil {
			t.Fatal("InvokeTool() returned nil result")
		}
	})
}

func TestSearchTools(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	serverReg := registry.NewRegistry(logger, "")
	toolReg := router.NewToolRegistry()
	r := router.NewRouter(toolReg, serverReg, logger)

	gw := NewGatewayTools(serverReg, toolReg, r, logger)

	githubServer := registry.ServerEntry{
		ID:        "github-1",
		Transport: registry.TransportStdio,
	}
	_ = serverReg.Register(ctx, githubServer)

	_ = toolReg.RegisterTool(ctx, router.ToolEntry{
		Name:      "github.create_issue",
		Namespace: "github",
		ServerID:  "github-1",
	})
	_ = toolReg.RegisterTool(ctx, router.ToolEntry{
		Name:      "github.read_file",
		Namespace: "github",
		ServerID:  "github-1",
	})
	_ = toolReg.RegisterTool(ctx, router.ToolEntry{
		Name:      "github.close_issue",
		Namespace: "github",
		ServerID:  "github-1",
	})

	t.Run("search with query", func(t *testing.T) {
		results, err := gw.SearchTools(ctx, "create")
		if err != nil {
			t.Fatalf("SearchTools() error = %v", err)
		}

		if len(results) != 1 {
			t.Errorf("SearchTools() returned %d results, want 1", len(results))
		}

		if len(results) > 0 && results[0].ToolName != "github.create_issue" {
			t.Errorf("SearchTools()[0].ToolName = %q, want %q", results[0].ToolName, "github.create_issue")
		}
	})

	t.Run("search empty query returns all", func(t *testing.T) {
		results, err := gw.SearchTools(ctx, "")
		if err != nil {
			t.Fatalf("SearchTools() error = %v", err)
		}

		if len(results) != 3 {
			t.Errorf("SearchTools() returned %d results, want 3", len(results))
		}
	})

	t.Run("search no matches", func(t *testing.T) {
		results, err := gw.SearchTools(ctx, "nonexistent")
		if err != nil {
			t.Fatalf("SearchTools() error = %v", err)
		}

		if len(results) != 0 {
			t.Errorf("SearchTools() returned %d results, want 0", len(results))
		}
	})
}

func TestGatewayToolsInterface(t *testing.T) {
	var _ GatewayTools = (*gatewayTools)(nil)
}