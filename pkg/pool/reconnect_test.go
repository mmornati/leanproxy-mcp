package pool

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/stretchr/testify/require"
)

// startMCPTestHTTPServer serves handler on 127.0.0.1 (a fresh port when addr
// is empty, or the given addr so a listener can be recreated after a kill).
// It returns the listener address and a stop function.
func startMCPTestHTTPServer(t *testing.T, handler http.Handler, addr string) (string, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := &http.Server{Handler: handler}
	go func() {
		_ = srv.Serve(ln)
	}()

	return ln.Addr().String(), func() {
		_ = srv.Close()
		_ = ln.Close()
	}
}

// waitPoolState polls a remote pool's GetServerState until it equals want.
func waitPoolState(t *testing.T, getState func(string) (ServerState, error), name string, want ServerState) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		state, err := getState(name)
		if err == nil && state == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for state %s", want)
}

// newTestMCPServer builds a real mcp-go server with one tool registered so
// tools/list requests are supported.
func newTestMCPServer() *mcpserver.MCPServer {
	mcpServer := mcpserver.NewMCPServer("test-mcp", "1.0.0")
	mcpServer.AddTool(
		mcp.NewTool("echo"),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "echo"}},
			}, nil
		},
	)
	return mcpServer
}

// TestSSEPoolReconnectsAfterServerDeath verifies that when the remote SSE
// server dies mid-session, the next request transparently reconnects.
func TestSSEPoolReconnectsAfterServerDeath(t *testing.T) {
	mcpServer := newTestMCPServer()
	handler := mcpserver.NewSSEServer(mcpServer)

	addr, stop := startMCPTestHTTPServer(t, handler, "")
	defer stop()

	pool := NewSSEPool(nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name: "test-sse-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://" + addr + "/sse",
		},
	}
	require.NoError(t, pool.StartServer(context.Background(), config))

	waitPoolState(t, func(name string) (ServerState, error) {
		return pool.GetServerState(name)
	}, "test-sse-server", StateRunning)

	ctx := context.Background()
	resp, err := pool.SendRequestToServer(ctx, "test-sse-server", "tools/list", nil, 30*time.Second)
	require.NoError(t, err, "request before server death should succeed")
	require.NotNil(t, resp)

	// Kill the remote server, then bring it back on the same address.
	stop()
	_, stop2 := startMCPTestHTTPServer(t, handler, addr)
	defer stop2()

	time.Sleep(200 * time.Millisecond)

	resp, err = pool.SendRequestToServer(ctx, "test-sse-server", "tools/list", nil, 30*time.Second)
	require.NoError(t, err, "request after server death should succeed via reconnect")
	require.NotNil(t, resp)
}

// TestHTTPClientPoolReconnectsAfterServerDeath is the streamable-HTTP variant
// of the SSE reconnect test.
func TestHTTPClientPoolReconnectsAfterServerDeath(t *testing.T) {
	mcpServer := newTestMCPServer()
	handler := mcpserver.NewStreamableHTTPServer(mcpServer)

	addr, stop := startMCPTestHTTPServer(t, handler, "")
	defer stop()

	pool := NewHTTPClientPool(nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name: "test-http-server",
		HTTP: &migrate.HTTPConfig{
			URL: "http://" + addr + "/mcp",
		},
	}
	require.NoError(t, pool.StartServer(context.Background(), config))

	waitPoolState(t, func(name string) (ServerState, error) {
		return pool.GetServerState(name)
	}, "test-http-server", StateRunning)

	ctx := context.Background()
	resp, err := pool.SendRequestToServer(ctx, "test-http-server", "tools/list", nil, 30*time.Second)
	require.NoError(t, err, "request before server death should succeed")
	require.NotNil(t, resp)

	stop()
	_, stop2 := startMCPTestHTTPServer(t, handler, addr)
	defer stop2()

	time.Sleep(200 * time.Millisecond)

	resp, err = pool.SendRequestToServer(ctx, "test-http-server", "tools/list", nil, 30*time.Second)
	require.NoError(t, err, "request after server death should succeed via reconnect")
	require.NotNil(t, resp)
}
