package registry

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

func TestToolRegistry_RegisterTool(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg := NewToolRegistry(logger).(interface {
		RegisterTool(ctx context.Context, serverID, toolName string) error
	})

	ctx := context.Background()

	err := reg.RegisterTool(ctx, "server1", "namespace.tool1")
	if err != nil {
		t.Fatalf("RegisterTool() failed: %v", err)
	}

	err = reg.RegisterTool(ctx, "server1", "namespace.tool2")
	if err != nil {
		t.Fatalf("RegisterTool() second tool failed: %v", err)
	}

	err = reg.RegisterTool(ctx, "server2", "namespace.tool1")
	if err != nil {
		t.Fatalf("RegisterTool() same tool different server should succeed: %v", err)
	}
}

func TestToolRegistry_RegisterToolWithoutServer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg := NewToolRegistry(logger).(interface {
		RegisterTool(ctx context.Context, serverID, toolName string) error
	})

	ctx := context.Background()

	err := reg.RegisterTool(ctx, "", "namespace.tool1")
	if err == nil {
		t.Error("Expected error for empty server ID")
	}
}

func TestToolRegistry_RegisterToolWithoutName(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg := NewToolRegistry(logger).(interface {
		RegisterTool(ctx context.Context, serverID, toolName string) error
	})

	ctx := context.Background()

	err := reg.RegisterTool(ctx, "server1", "")
	if err == nil {
		t.Error("Expected error for empty tool name")
	}
}

func TestToolRegistry_UnregisterTool(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	toolReg := NewToolRegistry(logger).(interface {
		RegisterTool(ctx context.Context, serverID, toolName string) error
		UnregisterTool(ctx context.Context, serverID, toolName string) error
		GetToolServer(ctx context.Context, toolName string) (string, error)
	})

	ctx := context.Background()

	err := toolReg.RegisterTool(ctx, "server1", "namespace.tool1")
	if err != nil {
		t.Fatalf("RegisterTool() failed: %v", err)
	}

	err = toolReg.UnregisterTool(ctx, "server1", "namespace.tool1")
	if err != nil {
		t.Fatalf("UnregisterTool() failed: %v", err)
	}

	_, err = toolReg.GetToolServer(ctx, "namespace.tool1")
	if err == nil {
		t.Error("Expected error after unregistering tool")
	}
}

func TestToolRegistry_UnregisterToolNonExistent(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	toolReg := NewToolRegistry(logger).(interface {
		UnregisterTool(ctx context.Context, serverID, toolName string) error
	})

	ctx := context.Background()

	err := toolReg.UnregisterTool(ctx, "server1", "namespace.nonexistent")
	if err == nil {
		t.Error("Expected error for unregistering non-existent tool")
	}
}

func TestToolRegistry_GetToolServer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	toolReg := NewToolRegistry(logger).(interface {
		RegisterTool(ctx context.Context, serverID, toolName string) error
		GetToolServer(ctx context.Context, toolName string) (string, error)
	})

	ctx := context.Background()

	err := toolReg.RegisterTool(ctx, "server1", "namespace.tool1")
	if err != nil {
		t.Fatalf("RegisterTool() failed: %v", err)
	}

	serverID, err := toolReg.GetToolServer(ctx, "namespace.tool1")
	if err != nil {
		t.Fatalf("GetToolServer() failed: %v", err)
	}
	if serverID != "server1" {
		t.Errorf("GetToolServer() serverID = %v, want server1", serverID)
	}

	_, err = toolReg.GetToolServer(ctx, "namespace.nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent tool")
	}
}

func TestToolRegistry_GetToolServerAmbiguous(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	toolReg := NewToolRegistry(logger).(interface {
		RegisterTool(ctx context.Context, serverID, toolName string) error
		GetToolServer(ctx context.Context, toolName string) (string, error)
	})

	ctx := context.Background()

	toolReg.RegisterTool(ctx, "server1", "namespace.sametool")
	toolReg.RegisterTool(ctx, "server2", "namespace.sametool")

	_, err := toolReg.GetToolServer(ctx, "namespace.sametool")
	if err == nil {
		t.Error("Expected error for ambiguous tool")
	}
}

func TestToolRegistry_SearchTools(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	toolReg := NewToolRegistry(logger).(interface {
		RegisterTool(ctx context.Context, serverID, toolName string) error
		SearchTools(ctx context.Context, query string) []ToolMatch
	})

	ctx := context.Background()

	toolReg.RegisterTool(ctx, "server1", "code.complete")
	toolReg.RegisterTool(ctx, "server1", "code.diagnostics")
	toolReg.RegisterTool(ctx, "server2", "code.completion")
	toolReg.RegisterTool(ctx, "server3", "search.find")

	matches := toolReg.SearchTools(ctx, "complete")
	if len(matches) == 0 {
		t.Error("SearchTools() returned no matches for 'complete'")
	}

	matches = toolReg.SearchTools(ctx, "diagnostics")
	if len(matches) != 1 {
		t.Errorf("SearchTools() returned %d matches, want 1", len(matches))
	}

	matches = toolReg.SearchTools(ctx, "code")
	if len(matches) < 3 {
		t.Errorf("SearchTools() returned %d matches, want at least 3 for 'code'", len(matches))
	}
}

func TestToolRegistry_SearchToolsExactMatch(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	toolReg := NewToolRegistry(logger).(interface {
		RegisterTool(ctx context.Context, serverID, toolName string) error
		SearchTools(ctx context.Context, query string) []ToolMatch
	})

	ctx := context.Background()

	toolReg.RegisterTool(ctx, "server1", "exact.match")

	matches := toolReg.SearchTools(ctx, "exact.match")
	if len(matches) != 1 {
		t.Errorf("SearchTools() returned %d matches, want 1", len(matches))
	}
	if matches[0].Score != 100.0 {
		t.Errorf("SearchTools() score = %v, want 100 for exact match", matches[0].Score)
	}
}

func TestToolRegistry_ListAllTools(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	toolReg := NewToolRegistry(logger).(interface {
		RegisterTool(ctx context.Context, serverID, toolName string) error
		ListAllTools(ctx context.Context) []ToolEntry
	})

	ctx := context.Background()

	toolReg.RegisterTool(ctx, "server1", "tool1")
	toolReg.RegisterTool(ctx, "server2", "tool2")
	toolReg.RegisterTool(ctx, "server1", "tool3")

	tools := toolReg.ListAllTools(ctx)
	if len(tools) != 3 {
		t.Errorf("ListAllTools() returned %d tools, want 3", len(tools))
	}
}

func TestToolRegistry_ToolSubscription(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	toolReg := NewToolRegistry(logger).(interface {
		RegisterTool(ctx context.Context, serverID, toolName string) error
		UnregisterTool(ctx context.Context, serverID, toolName string) error
		SubscribeTools(ch chan<- ToolEvent) func()
	})

	ctx := context.Background()

	ch := make(chan ToolEvent, 10)
	unsubscribe := toolReg.SubscribeTools(ch)
	defer unsubscribe()

	toolReg.RegisterTool(ctx, "server1", "event.tool1")
	toolReg.RegisterTool(ctx, "server1", "event.tool2")
	toolReg.UnregisterTool(ctx, "server1", "event.tool1")

	eventCount := 0
	timeout := time.After(2 * time.Second)
	for eventCount < 3 {
		select {
		case event := <-ch:
			eventCount++
			t.Logf("Received tool event %d: type=%d, tool=%s", eventCount, event.Type, event.ToolName)
		case <-timeout:
			t.Fatalf("Timeout waiting for events, received %d of 3", eventCount)
		}
	}

	if eventCount != 3 {
		t.Errorf("Received %d events, want 3", eventCount)
	}
}

func TestToolRegistry_Concurrency(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	toolReg := NewToolRegistry(logger).(interface {
		RegisterTool(ctx context.Context, serverID, toolName string) error
		GetToolServer(ctx context.Context, toolName string) (string, error)
	})

	ctx := context.Background()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			toolReg.RegisterTool(ctx, "server1", "namespace.tool1")
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			toolReg.GetToolServer(ctx, "namespace.tool1")
		}(i)
	}

	wg.Wait()
}

func TestToolRegistry_SearchToolsPerformance(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	toolReg := NewToolRegistry(logger).(interface {
		RegisterTool(ctx context.Context, serverID, toolName string) error
		SearchTools(ctx context.Context, query string) []ToolMatch
	})

	ctx := context.Background()

	for i := 0; i < 1000; i++ {
		toolReg.RegisterTool(ctx, "server1", "namespace.tool"+string(rune('a'+i%26))+string(rune('0'+i%10)))
	}

	start := time.Now()
	matches := toolReg.SearchTools(ctx, "tool")
	elapsed := time.Since(start)

	if len(matches) == 0 {
		t.Error("SearchTools() returned no matches")
	}

	if elapsed.Seconds() > 1.0 {
		t.Errorf("SearchTools() took %v, want < 1s for 1000 tools", elapsed)
	}
}
