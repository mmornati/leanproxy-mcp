package router

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/registry"
)

func TestParseMethod(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		wantNS   string
		wantTool string
		wantErr  bool
	}{
		{
			name:     "namespace.tool format",
			method:   "github.create_issue",
			wantNS:   "github",
			wantTool: "create_issue",
		},
		{
			name:     "tool only format",
			method:   "read_file",
			wantNS:   "read_file",
			wantTool: "read_file",
		},
		{
			name:     "multiple dots",
			method:   "github.api.v3.create_issue",
			wantNS:   "github",
			wantTool: "api.v3.create_issue",
		},
		{
			name:     "leading dot",
			method:   ".create_issue",
			wantNS:   "",
			wantTool: "create_issue",
		},
		{
			name:     "empty method",
			method:   "",
			wantNS:   "",
			wantTool: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns, tool := parseMethod(tt.method)
			if ns != tt.wantNS || tool != tt.wantTool {
				t.Errorf("parseMethod(%q) = (%q, %q), want (%q, %q)", tt.method, ns, tool, tt.wantNS, tt.wantTool)
			}
		})
	}
}

type mockLogger struct {
	debugs []string
}

func (m *mockLogger) Debug(msg string, args ...any) {
	m.debugs = append(m.debugs, msg)
}

func TestRouter_Route(t *testing.T) {
	ctx := context.Background()
	logger := &mockLogger{}

	serverReg := registry.NewRegistry(slog.Default(), "")
	toolReg := NewToolRegistry()

	githubServer := registry.ServerEntry{
		ID:        "github-1",
		Transport: registry.TransportStdio,
	}
	_ = serverReg.Register(ctx, githubServer)
	_ = toolReg.RegisterTool(ctx, ToolEntry{
		Name:      "github.create_issue",
		Namespace: "github",
		ServerID:  "github-1",
	})
	_ = toolReg.RegisterTool(ctx, ToolEntry{
		Name:      "github.read_file",
		Namespace: "github",
		ServerID:  "github-1",
	})

	r := NewRouter(toolReg, serverReg, logger)

	t.Run("route by namespace.tool", func(t *testing.T) {
		server, err := r.Route(ctx, "github.create_issue")
		if err != nil {
			t.Fatalf("Route() error = %v", err)
		}
		if server == nil {
			t.Fatal("Route() returned nil server")
		}
		if server.ID != "github-1" {
			t.Errorf("Route() server.ID = %q, want %q", server.ID, "github-1")
		}
	})

	t.Run("unknown tool returns error", func(t *testing.T) {
		_, err := r.Route(ctx, "nonexistent.do_something")
		if err == nil {
			t.Error("Route() expected error for unknown tool, got nil")
		}
	})

	t.Run("empty method returns error", func(t *testing.T) {
		_, err := r.Route(ctx, "")
		if err == nil {
			t.Error("Route() expected error for empty method, got nil")
		}
	})
}

func TestRouter_RouteBatch(t *testing.T) {
	ctx := context.Background()
	logger := &mockLogger{}

	serverReg := registry.NewRegistry(slog.Default(), "")
	toolReg := NewToolRegistry()

	githubServer := registry.ServerEntry{
		ID:        "github-1",
		Transport: registry.TransportStdio,
	}
	filesystemServer := registry.ServerEntry{
		ID:        "fs-1",
		Transport: registry.TransportStdio,
	}
	_ = serverReg.Register(ctx, githubServer)
	_ = serverReg.Register(ctx, filesystemServer)

	_ = toolReg.RegisterTool(ctx, ToolEntry{
		Name:      "github.create_issue",
		Namespace: "github",
		ServerID:  "github-1",
	})
	_ = toolReg.RegisterTool(ctx, ToolEntry{
		Name:      "filesystem.read_file",
		Namespace: "filesystem",
		ServerID:  "fs-1",
	})

	r := NewRouter(toolReg, serverReg, logger)

	methods := []string{"github.create_issue", "filesystem.read_file"}
	servers, errs := r.RouteBatch(ctx, methods)

	if len(servers) != len(methods) {
		t.Errorf("RouteBatch() returned %d servers, want %d", len(servers), len(methods))
	}

	for i, err := range errs {
		if err != nil {
			t.Errorf("RouteBatch()[%d] error = %v", i, err)
		}
	}
}

func TestToolRegistry(t *testing.T) {
	ctx := context.Background()
	reg := NewToolRegistry()

	t.Run("register and find tool", func(t *testing.T) {
		err := reg.RegisterTool(ctx, ToolEntry{
			Name:      "github.create_issue",
			Namespace: "github",
			ServerID:  "server-1",
		})
		if err != nil {
			t.Fatalf("RegisterTool() error = %v", err)
		}

		serverIDs, err := reg.FindByNamespace(ctx, "github")
		if err != nil {
			t.Fatalf("FindByNamespace() error = %v", err)
		}
		if len(serverIDs) != 1 {
			t.Errorf("FindByNamespace() returned %d servers, want 1", len(serverIDs))
		}
	})

	t.Run("unregister tool", func(t *testing.T) {
		err := reg.UnregisterTool(ctx, "github.create_issue")
		if err != nil {
			t.Fatalf("UnregisterTool() error = %v", err)
		}

		serverIDs, err := reg.FindByNamespace(ctx, "github")
		if err != nil {
			t.Fatalf("FindByNamespace() error = %v", err)
		}
		if len(serverIDs) != 0 {
			t.Errorf("FindByNamespace() returned %d servers after unregister, want 0", len(serverIDs))
		}
	})
}

func TestRouterError(t *testing.T) {
	t.Run("error unwrapping", func(t *testing.T) {
		err := NewRouterError(ErrCodeMethodNotFound, "not found", ErrToolNotFound)
		if err.Unwrap() != ErrToolNotFound {
			t.Errorf("Unwrap() = %v, want %v", err.Unwrap(), ErrToolNotFound)
		}
	})

	t.Run("error message", func(t *testing.T) {
		err := NewRouterError(ErrCodeMethodNotFound, "not found", ErrToolNotFound)
		expected := "not found: tool not found in any registered server"
		if err.Error() != expected {
			t.Errorf("Error() = %q, want %q", err.Error(), expected)
		}
	})
}

func TestParseMethodPerformance(t *testing.T) {
	start := time.Now()
	for i := 0; i < 10000; i++ {
		parseMethod("github.api.v3.create_issue")
	}
	elapsed := time.Since(start)
	if elapsed > 50*time.Millisecond {
		t.Errorf("parseMethod() took %v, want < 50ms for 10000 iterations", elapsed)
	}
}
