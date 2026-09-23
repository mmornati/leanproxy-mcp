package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/errors"
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
		"invoke_tool":  false,
		"list_tools":   false,
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
		rpcErr, ok := err.(*errors.JSONRPCError)
		if !ok {
			t.Fatalf("InvokeTool() returned error type %T, want *errors.JSONRPCError", err)
		}
		if rpcErr.Code != errors.ErrCodeInvalidParams {
			t.Errorf("InvokeTool() error code = %d, want %d", rpcErr.Code, errors.ErrCodeInvalidParams)
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
		rpcErr, ok := err.(*errors.JSONRPCError)
		if !ok {
			t.Fatalf("InvokeTool() returned error type %T, want *errors.JSONRPCError", err)
		}
		if rpcErr.Code != errors.ErrCodeInvalidParams {
			t.Errorf("InvokeTool() error code = %d, want %d", rpcErr.Code, errors.ErrCodeInvalidParams)
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
			Arguments:  json.RawMessage(`{"title": "Test Issue"}`),
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

// TestInvokeTool_BigIntegerArgumentsByteFaithful is the regression test for
// serve's simple gateway mode losing precision on integers above 2^53: the
// arguments used to be decoded into a map (float64), so 9007199254740993
// was forwarded as 9007199254740992.
func TestInvokeTool_BigIntegerArgumentsByteFaithful(t *testing.T) {
	ctx := context.Background()
	gw := newInvokeTestGateway(t)

	const args = `{"id":9007199254740993,"nested":{"big":-123456789012345678901234567890,"f":1.50}}`
	var params InvokeToolParams
	if err := json.Unmarshal([]byte(`{"server_name":"github-1","tool_name":"create_issue","arguments":`+args+`}`), &params); err != nil {
		t.Fatal(err)
	}
	result, err := gw.InvokeTool(ctx, params)
	if err != nil {
		t.Fatalf("InvokeTool() error = %v", err)
	}
	out, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"params":`+args) {
		t.Fatalf("arguments not relayed byte for byte:\n got %s\nwant params %s", out, args)
	}
	if strings.Contains(string(out), "9007199254740992") {
		t.Fatalf("2^53+1 was rounded: %s", out)
	}
}

func TestInvokeTool_ArgumentsMustBeAnObject(t *testing.T) {
	ctx := context.Background()
	gw := newInvokeTestGateway(t)
	for _, args := range []string{`[1,2]`, `"x"`, `42`} {
		_, err := gw.InvokeTool(ctx, InvokeToolParams{ServerName: "github-1", ToolName: "create_issue", Arguments: json.RawMessage(args)})
		rpcErr, ok := err.(*errors.JSONRPCError)
		if !ok || rpcErr.Code != errors.ErrCodeInvalidParams {
			t.Errorf("arguments %s: err = %v, want invalid params", args, err)
		}
	}
	for _, args := range []string{``, `null`} {
		result, err := gw.InvokeTool(ctx, InvokeToolParams{ServerName: "github-1", ToolName: "create_issue", Arguments: json.RawMessage(args)})
		if err != nil {
			t.Fatalf("arguments %q: %v", args, err)
		}
		out, _ := json.Marshal(result)
		if !strings.Contains(string(out), `"params":{}`) {
			t.Errorf("arguments %q: want empty object params, got %s", args, out)
		}
	}
}

func TestGatewayToolsInterface(t *testing.T) {
	var _ GatewayTools = (*gatewayTools)(nil)
}

// newInvokeTestGateway returns a gateway with one healthy server
// ("github-1") exposing github.create_issue.
func newInvokeTestGateway(t *testing.T) GatewayTools {
	t.Helper()
	ctx := context.Background()
	logger := slog.Default()
	serverReg := registry.NewRegistry(logger, "")
	toolReg := router.NewToolRegistry()
	r := router.NewRouter(toolReg, serverReg, logger)
	if err := serverReg.Register(ctx, registry.ServerEntry{ID: "github-1", Transport: registry.TransportStdio, Health: registry.HealthHealthy}); err != nil {
		t.Fatal(err)
	}
	if err := toolReg.RegisterTool(ctx, router.ToolEntry{Name: "github.create_issue", Namespace: "github", ServerID: "github-1"}); err != nil {
		t.Fatal(err)
	}
	return NewGatewayTools(serverReg, toolReg, r, logger)
}
