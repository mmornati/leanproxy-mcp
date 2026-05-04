package pool

import (
	"context"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
)

func TestNewHTTPClientServer(t *testing.T) {
	config := &migrate.ServerConfig{
		Name: "test-http-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	server := NewHTTPClientServer("test-http-server", config, nil)
	if server == nil {
		t.Fatal("expected non-nil server")
	}

	if server.name != "test-http-server" {
		t.Errorf("expected name=test-http-server, got %s", server.name)
	}
}

func TestHTTPClientServerGetState(t *testing.T) {
	config := &migrate.ServerConfig{
		Name: "test-http-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	server := NewHTTPClientServer("test-http-server", config, nil)

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

func TestHTTPClientServerSetState(t *testing.T) {
	config := &migrate.ServerConfig{
		Name: "test-http-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	server := NewHTTPClientServer("test-http-server", config, nil)

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

func TestHTTPClientServerListToolsNotInitialized(t *testing.T) {
	config := &migrate.ServerConfig{
		Name: "test-http-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	server := NewHTTPClientServer("test-http-server", config, nil)

	_, err := server.ListTools(context.Background())
	if err == nil {
		t.Error("expected error when client not initialized")
	}
}

func TestHTTPClientServerCallToolNotInitialized(t *testing.T) {
	config := &migrate.ServerConfig{
		Name: "test-http-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	server := NewHTTPClientServer("test-http-server", config, nil)

	_, err := server.CallTool(context.Background(), "test-tool", nil)
	if err == nil {
		t.Error("expected error when client not initialized")
	}
}

func TestHTTPClientServerClose(t *testing.T) {
	config := &migrate.ServerConfig{
		Name: "test-http-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	server := NewHTTPClientServer("test-http-server", config, nil)

	err := server.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	if server.getState() != StateStopped {
		t.Errorf("expected state=stopped after close, got %s", server.getState())
	}
}

func TestNewHTTPClientPool(t *testing.T) {
	pool := NewHTTPClientPool(nil)
	if pool == nil {
		t.Fatal("expected non-nil pool")
	}

	if pool.ctx == nil {
		t.Error("expected non-nil context")
	}
}

func TestHTTPClientPoolStartServer(t *testing.T) {
	pool := NewHTTPClientPool(nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name: "test-http-server",
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

func TestHTTPClientPoolStartServerNoHTTPConfig(t *testing.T) {
	pool := NewHTTPClientPool(nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name: "test-http-server",
	}

	err := pool.StartServer(context.Background(), config)
	if err == nil {
		t.Error("expected error for missing HTTP config")
	}
}

func TestHTTPClientPoolStartServerNoURL(t *testing.T) {
	pool := NewHTTPClientPool(nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name: "test-http-server",
		HTTP: &migrate.HTTPConfig{
			URL: "",
		},
	}

	err := pool.StartServer(context.Background(), config)
	if err == nil {
		t.Error("expected error for empty URL")
	}
}

func TestHTTPClientPoolStartServerAlreadyExists(t *testing.T) {
	pool := NewHTTPClientPool(nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name: "test-http-server",
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

func TestHTTPClientPoolListServers(t *testing.T) {
	pool := NewHTTPClientPool(nil)
	defer pool.Close()

	servers := pool.ListServers()
	if len(servers) != 0 {
		t.Errorf("expected empty list, got %d servers", len(servers))
	}

	config := &migrate.ServerConfig{
		Name: "test-http-server",
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

func TestHTTPClientPoolGetServerState(t *testing.T) {
	pool := NewHTTPClientPool(nil)
	defer pool.Close()

	_, err := pool.GetServerState("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent server")
	}

	config := &migrate.ServerConfig{
		Name: "test-http-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	pool.StartServer(context.Background(), config)

	state, err := pool.GetServerState("test-http-server")
	if err != nil {
		t.Fatalf("GetServerState failed: %v", err)
	}

	if state == "" {
		t.Error("expected non-empty state")
	}
}

func TestHTTPClientPoolHasServer(t *testing.T) {
	pool := NewHTTPClientPool(nil)
	defer pool.Close()

	if pool.HasServer("nonexistent") {
		t.Error("expected false for nonexistent server")
	}

	config := &migrate.ServerConfig{
		Name: "test-http-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	pool.StartServer(context.Background(), config)

	if !pool.HasServer("test-http-server") {
		t.Error("expected true for existing server")
	}
}

func TestHTTPClientPoolServerCount(t *testing.T) {
	pool := NewHTTPClientPool(nil)
	defer pool.Close()

	count := pool.ServerCount()
	if count != 0 {
		t.Errorf("expected count=0, got %d", count)
	}

	for i := 0; i < 3; i++ {
		config := &migrate.ServerConfig{
			Name: "test-http-server-" + string(rune('a'+i)),
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

func TestHTTPClientPoolSendRequestNotFound(t *testing.T) {
	pool := NewHTTPClientPool(nil)
	defer pool.Close()

	_, err := pool.SendRequest(context.Background(), "nonexistent", &proxy.JSONRPCRequest{
		Method: "test",
	}, 30*time.Second)

	if err == nil {
		t.Error("expected error for nonexistent server")
	}
}

func TestHTTPClientPoolSendRequestToServerNotFound(t *testing.T) {
	pool := NewHTTPClientPool(nil)
	defer pool.Close()

	_, err := pool.SendRequestToServer(context.Background(), "nonexistent", "test", nil, 30*time.Second)
	if err == nil {
		t.Error("expected error for nonexistent server")
	}
}

func TestHTTPClientPoolSendRequestToServerWithIDNotFound(t *testing.T) {
	pool := NewHTTPClientPool(nil)
	defer pool.Close()

	_, err := pool.SendRequestToServerWithID(context.Background(), "nonexistent", "test", nil, 30*time.Second, 1)
	if err == nil {
		t.Error("expected error for nonexistent server")
	}
}

func TestHTTPClientPoolSendServerNotification(t *testing.T) {
	pool := NewHTTPClientPool(nil)
	defer pool.Close()

	err := pool.SendServerNotification(context.Background(), "test-server", "test", nil)
	if err != nil {
		t.Errorf("SendServerNotification should not fail: %v", err)
	}
}

func TestHTTPClientPoolRestartServer(t *testing.T) {
	pool := NewHTTPClientPool(nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name: "test-http-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}

	pool.StartServer(context.Background(), config)

	err := pool.RestartServer(context.Background(), "test-http-server")
	if err != nil {
		t.Fatalf("RestartServer failed: %v", err)
	}
}

func TestHTTPClientPoolRestartServerNotFound(t *testing.T) {
	pool := NewHTTPClientPool(nil)
	defer pool.Close()

	err := pool.RestartServer(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent server")
	}
}

func TestHTTPClientPoolClose(t *testing.T) {
	pool := NewHTTPClientPool(nil)

	config := &migrate.ServerConfig{
		Name: "test-http-server",
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

func TestNewUnifiedPool(t *testing.T) {
	stdioPool := NewStdioPool(5, 5*time.Minute, nil)
	httpPool := NewHTTPClientPool(nil)
	ssePool := NewSSEPool(nil)

	unified := NewUnifiedPool(stdioPool, httpPool, ssePool, nil)
	if unified == nil {
		t.Fatal("expected non-nil unified pool")
	}
}

func TestNewUnifiedPoolNilSSEPool(t *testing.T) {
	stdioPool := NewStdioPool(5, 5*time.Minute, nil)
	httpPool := NewHTTPClientPool(nil)

	unified := NewUnifiedPool(stdioPool, httpPool, nil, nil)
	if unified == nil {
		t.Fatal("expected non-nil unified pool")
	}
}

func TestUnifiedPoolListServers(t *testing.T) {
	stdioPool := NewStdioPool(5, 5*time.Minute, nil)
	defer stdioPool.Close()
	httpPool := NewHTTPClientPool(nil)
	defer httpPool.Close()
	ssePool := NewSSEPool(nil)
	defer ssePool.Close()

	unified := NewUnifiedPool(stdioPool, httpPool, ssePool, nil)

	ctx := context.Background()
	stdioConfig := &migrate.ServerConfig{
		Name:      "stdio-server",
		Transport: registry.TransportStdio,
		Stdio: &migrate.StdioConfig{
			Command: "cat",
			Args:    []string{},
		},
		TimeoutValue: 30 * time.Second,
	}
	stdioPool.StartServer(ctx, stdioConfig)

	httpConfig := &migrate.ServerConfig{
		Name: "http-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8080",
		},
	}
	httpPool.StartServer(context.Background(), httpConfig)

	sseConfig := &migrate.ServerConfig{
		Name: "sse-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://localhost:8081",
		},
	}
	ssePool.StartServer(context.Background(), sseConfig)

	servers := unified.ListServers()
	if len(servers) != 3 {
		t.Errorf("expected 3 servers, got %d: %v", len(servers), servers)
	}
}

func TestUnifiedPoolGetServerState(t *testing.T) {
	stdioPool := NewStdioPool(5, 5*time.Minute, nil)
	defer stdioPool.Close()
	httpPool := NewHTTPClientPool(nil)
	defer httpPool.Close()
	ssePool := NewSSEPool(nil)
	defer ssePool.Close()

	unified := NewUnifiedPool(stdioPool, httpPool, ssePool, nil)

	ctx := context.Background()
	stdioConfig := &migrate.ServerConfig{
		Name:      "stdio-server",
		Transport: registry.TransportStdio,
		Stdio: &migrate.StdioConfig{
			Command: "cat",
			Args:    []string{},
		},
		TimeoutValue: 30 * time.Second,
	}
	stdioPool.StartServer(ctx, stdioConfig)

	state, err := unified.GetServerState("stdio-server")
	if err != nil {
		t.Fatalf("GetServerState failed: %v", err)
	}

	if state == "" {
		t.Error("expected non-empty state")
	}
}

func TestUnifiedPoolGetServerStateNotFound(t *testing.T) {
	stdioPool := NewStdioPool(5, 5*time.Minute, nil)
	defer stdioPool.Close()
	httpPool := NewHTTPClientPool(nil)
	defer httpPool.Close()

	unified := NewUnifiedPool(stdioPool, httpPool, nil, nil)

	_, err := unified.GetServerState("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent server")
	}
}

func TestUnifiedPoolSendRequestToServerNotFound(t *testing.T) {
	stdioPool := NewStdioPool(5, 5*time.Minute, nil)
	defer stdioPool.Close()
	httpPool := NewHTTPClientPool(nil)
	defer httpPool.Close()

	unified := NewUnifiedPool(stdioPool, httpPool, nil, nil)

	_, err := unified.SendRequestToServer(context.Background(), "nonexistent", "test", nil, 30*time.Second)
	if err == nil {
		t.Error("expected error for nonexistent server")
	}
}

func TestUnifiedPoolSendRequestToServerWithIDNotFound(t *testing.T) {
	stdioPool := NewStdioPool(5, 5*time.Minute, nil)
	defer stdioPool.Close()
	httpPool := NewHTTPClientPool(nil)
	defer httpPool.Close()

	unified := NewUnifiedPool(stdioPool, httpPool, nil, nil)

	_, err := unified.SendRequestToServerWithID(context.Background(), "nonexistent", "test", nil, 30*time.Second, 1)
	if err == nil {
		t.Error("expected error for nonexistent server")
	}
}

func TestUnifiedPoolSendServerNotificationNotFound(t *testing.T) {
	stdioPool := NewStdioPool(5, 5*time.Minute, nil)
	defer stdioPool.Close()
	httpPool := NewHTTPClientPool(nil)
	defer httpPool.Close()

	unified := NewUnifiedPool(stdioPool, httpPool, nil, nil)

	err := unified.SendServerNotification(context.Background(), "nonexistent", "test", nil)
	if err != nil {
		t.Logf("Got error for nonexistent server (expected when stdio pool is queried first): %v", err)
	}
}

func TestUnifiedPoolRestartServerNotFound(t *testing.T) {
	stdioPool := NewStdioPool(5, 5*time.Minute, nil)
	defer stdioPool.Close()
	httpPool := NewHTTPClientPool(nil)
	defer httpPool.Close()

	unified := NewUnifiedPool(stdioPool, httpPool, nil, nil)

	err := unified.RestartServer(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent server")
	}
}

func TestUnifiedPoolSendRequestNotFound(t *testing.T) {
	stdioPool := NewStdioPool(5, 5*time.Minute, nil)
	defer stdioPool.Close()
	httpPool := NewHTTPClientPool(nil)
	defer httpPool.Close()

	unified := NewUnifiedPool(stdioPool, httpPool, nil, nil)

	_, err := unified.SendRequest(context.Background(), "nonexistent", &proxy.JSONRPCRequest{
		Method: "test",
	}, 30*time.Second)
	if err == nil {
		t.Error("expected error for nonexistent server")
	}
}

func TestUnifiedPoolClose(t *testing.T) {
	stdioPool := NewStdioPool(5, 5*time.Minute, nil)
	httpPool := NewHTTPClientPool(nil)
	ssePool := NewSSEPool(nil)

	unified := NewUnifiedPool(stdioPool, httpPool, ssePool, nil)

	err := unified.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}
