package pool

import (
	"context"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

func TestNewSSEServer(t *testing.T) {
	config := &migrate.ServerConfig{
		Name: "test-sse-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	server := NewSSEServer("test-sse-server", config, nil)
	if server == nil {
		t.Fatal("expected non-nil server")
	}

	if server.name != "test-sse-server" {
		t.Errorf("expected name=test-sse-server, got %s", server.name)
	}
}

func TestSSEServerGetState(t *testing.T) {
	config := &migrate.ServerConfig{
		Name: "test-sse-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	server := NewSSEServer("test-sse-server", config, nil)

	state := server.getState()
	if state != StateStarting {
		t.Errorf("expected state=starting, got %s", state)
	}

	server.setState(StateRunning)
	state = server.getState()
	if state != StateRunning {
		t.Errorf("expected state=running, got %s", state)
	}
}

func TestSSEServerSetState(t *testing.T) {
	config := &migrate.ServerConfig{
		Name: "test-sse-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	server := NewSSEServer("test-sse-server", config, nil)

	tests := []ServerState{
		StateStarting,
		StateRunning,
		StateIdle,
		StateBusy,
		StateStopping,
		StateStopped,
		StateError,
	}

	for _, expectedState := range tests {
		server.setState(expectedState)
		if server.getState() != expectedState {
			t.Errorf("expected state=%s, got %s", expectedState, server.getState())
		}
	}
}

func TestSSEServerListToolsNotInitialized(t *testing.T) {
	config := &migrate.ServerConfig{
		Name: "test-sse-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	server := NewSSEServer("test-sse-server", config, nil)

	_, err := server.ListTools(context.Background())
	if err == nil {
		t.Error("expected error when client not initialized")
	}
}

func TestSSEServerCallToolNotInitialized(t *testing.T) {
	config := &migrate.ServerConfig{
		Name: "test-sse-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	server := NewSSEServer("test-sse-server", config, nil)

	_, err := server.CallTool(context.Background(), "test-tool", nil)
	if err == nil {
		t.Error("expected error when client not initialized")
	}
}

func TestSSEServerClose(t *testing.T) {
	config := &migrate.ServerConfig{
		Name: "test-sse-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	server := NewSSEServer("test-sse-server", config, nil)

	err := server.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	if server.getState() != StateStopped {
		t.Errorf("expected state=stopped after close, got %s", server.getState())
	}
}

func TestNewSSEPool(t *testing.T) {
	pool := NewSSEPool(nil)
	if pool == nil {
		t.Fatal("expected non-nil pool")
	}

	if pool.ctx == nil {
		t.Error("expected non-nil context")
	}
}

func TestSSEPoolStartServer(t *testing.T) {
	pool := NewSSEPool(nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name: "test-sse-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	err := pool.StartServer(context.Background(), config)
	if err != nil {
		t.Fatalf("StartServer failed: %v", err)
	}

	count := pool.ServerCount()
	if count != 1 {
		t.Errorf("expected server count=1, got %d", count)
	}
}

func TestSSEPoolStartServerNoHTTPConfig(t *testing.T) {
	pool := NewSSEPool(nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name: "test-sse-server",
	}

	err := pool.StartServer(context.Background(), config)
	if err == nil {
		t.Error("expected error for missing HTTP config")
	}
}

func TestSSEPoolStartServerNoURL(t *testing.T) {
	pool := NewSSEPool(nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name: "test-sse-server",
		HTTP: &migrate.HTTPConfig{
			URL: "",
		},
	}

	err := pool.StartServer(context.Background(), config)
	if err == nil {
		t.Error("expected error for empty URL")
	}
}

func TestSSEPoolStartServerAlreadyExists(t *testing.T) {
	pool := NewSSEPool(nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name: "test-sse-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	err := pool.StartServer(context.Background(), config)
	if err != nil {
		t.Fatalf("first StartServer failed: %v", err)
	}

	err = pool.StartServer(context.Background(), config)
	if err != nil {
		t.Error("second StartServer should not fail for existing server")
	}
}

func TestSSEPoolListServers(t *testing.T) {
	pool := NewSSEPool(nil)
	defer pool.Close()

	servers := pool.ListServers()
	if len(servers) != 0 {
		t.Errorf("expected empty list, got %d servers", len(servers))
	}

	config := &migrate.ServerConfig{
		Name: "test-sse-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	pool.StartServer(context.Background(), config)

	servers = pool.ListServers()
	if len(servers) != 1 {
		t.Errorf("expected 1 server, got %d", len(servers))
	}
}

func TestSSEPoolGetServerState(t *testing.T) {
	pool := NewSSEPool(nil)
	defer pool.Close()

	_, err := pool.GetServerState("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent server")
	}

	config := &migrate.ServerConfig{
		Name: "test-sse-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	pool.StartServer(context.Background(), config)

	state, err := pool.GetServerState("test-sse-server")
	if err != nil {
		t.Fatalf("GetServerState failed: %v", err)
	}

	if state == "" {
		t.Error("expected non-empty state")
	}
}

func TestSSEPoolHasServer(t *testing.T) {
	pool := NewSSEPool(nil)
	defer pool.Close()

	if pool.HasServer("nonexistent") {
		t.Error("expected false for nonexistent server")
	}

	config := &migrate.ServerConfig{
		Name: "test-sse-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	pool.StartServer(context.Background(), config)

	if !pool.HasServer("test-sse-server") {
		t.Error("expected true for existing server")
	}
}

func TestSSEPoolServerCount(t *testing.T) {
	pool := NewSSEPool(nil)
	defer pool.Close()

	count := pool.ServerCount()
	if count != 0 {
		t.Errorf("expected count=0, got %d", count)
	}

	for i := 0; i < 3; i++ {
		config := &migrate.ServerConfig{
			Name: "test-sse-server-" + string(rune('a'+i)),
			HTTP: &migrate.HTTPConfig{
				URL: "http://localhost:8080",
			},
		}
		pool.StartServer(context.Background(), config)
	}

	count = pool.ServerCount()
	if count != 3 {
		t.Errorf("expected count=3, got %d", count)
	}
}

func TestSSEPoolSendRequestNotFound(t *testing.T) {
	pool := NewSSEPool(nil)
	defer pool.Close()

	_, err := pool.SendRequest(context.Background(), "nonexistent", nil, 30*time.Second)
	if err == nil {
		t.Error("expected error for nonexistent server")
	}
}

func TestSSEPoolSendRequestToServerNotFound(t *testing.T) {
	pool := NewSSEPool(nil)
	defer pool.Close()

	_, err := pool.SendRequestToServer(context.Background(), "nonexistent", "test", nil, 30*time.Second)
	if err == nil {
		t.Error("expected error for nonexistent server")
	}
}

func TestSSEPoolSendRequestToServerWithIDNotFound(t *testing.T) {
	pool := NewSSEPool(nil)
	defer pool.Close()

	_, err := pool.SendRequestToServerWithID(context.Background(), "nonexistent", "test", nil, 30*time.Second, 1)
	if err == nil {
		t.Error("expected error for nonexistent server")
	}
}

func TestSSEPoolSendServerNotification(t *testing.T) {
	pool := NewSSEPool(nil)
	defer pool.Close()

	err := pool.SendServerNotification(context.Background(), "test-server", "test", nil)
	if err != nil {
		t.Errorf("SendServerNotification should not fail: %v", err)
	}
}

func TestSSEPoolRestartServer(t *testing.T) {
	pool := NewSSEPool(nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name: "test-sse-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	pool.StartServer(context.Background(), config)

	err := pool.RestartServer(context.Background(), "test-sse-server")
	if err != nil {
		t.Fatalf("RestartServer failed: %v", err)
	}
}

func TestSSEPoolRestartServerNotFound(t *testing.T) {
	pool := NewSSEPool(nil)
	defer pool.Close()

	err := pool.RestartServer(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent server")
	}
}

func TestSSEPoolClose(t *testing.T) {
	pool := NewSSEPool(nil)

	config := &migrate.ServerConfig{
		Name: "test-sse-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	pool.StartServer(context.Background(), config)

	err := pool.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	count := pool.ServerCount()
	if count != 0 {
		t.Errorf("expected count=0 after close, got %d", count)
	}
}
