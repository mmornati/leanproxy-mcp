package pool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gomcp "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
)

// slowMCPBackend builds an in-process mcp-go server exposing a "slow" tool
// whose handler blocks until its context is canceled (simulating a hung
// remote MCP server) or a fixed delay elapses, whichever comes first.
func slowMCPBackend(sse bool, delay time.Duration) http.Handler {
	mcpServer := server.NewMCPServer("slow-backend", "1.0.0", server.WithToolCapabilities(true))

	slow := gomcp.NewTool("slow")
	mcpServer.AddTool(slow, func(ctx context.Context, _ gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
		select {
		case <-time.After(delay):
			return gomcp.NewToolResultText("done"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})

	if sse {
		return server.NewSSEServer(mcpServer)
	}
	return server.NewStreamableHTTPServer(mcpServer)
}

// TestHTTPAndSSEPool_SendRequest_RespectsTimeout is the regression test for
// #296 point 3: HTTPClientPool and SSEPool ignored the `timeout` argument on
// SendRequest*, so a hung remote MCP server would block forever. A tool call
// against a handler that never answers within the configured timeout must
// fail promptly instead of hanging.
func TestHTTPAndSSEPool_SendRequest_RespectsTimeout(t *testing.T) {
	const (
		requestTimeout = 150 * time.Millisecond
		handlerDelay   = 5 * time.Second
		testDeadline   = 3 * time.Second
	)

	for _, tc := range []struct {
		name string
		sse  bool
	}{
		{name: "http", sse: false},
		{name: "sse", sse: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(slowMCPBackend(tc.sse, handlerDelay))
			defer backend.Close()

			p := newEnvelopePool(tc.sse)
			defer p.Close()

			baseURL := backend.URL
			if tc.sse {
				baseURL += "/sse"
			}
			cfg := &migrate.ServerConfig{
				Name: "slow-srv",
				HTTP: &migrate.HTTPConfig{URL: baseURL},
			}
			if err := p.StartServer(context.Background(), cfg); err != nil {
				t.Fatalf("StartServer: %v", err)
			}

			// Give the pool's async connect/initialize a moment to finish so
			// the timeout under test only covers the tool call itself, not
			// connection setup.
			waitForConnected(t, p, "slow-srv", 5*time.Second)

			ctx, cancel := context.WithTimeout(context.Background(), testDeadline)
			defer cancel()

			start := time.Now()
			_, err := p.SendRequest(ctx, "slow-srv", &proxy.JSONRPCRequest{
				JSONRPC: "2.0",
				Method:  "tools/call",
				Params:  []byte(`{"name":"slow","arguments":{}}`),
				ID:      1,
			}, requestTimeout)
			elapsed := time.Since(start)

			if err == nil {
				t.Fatal("expected SendRequest to fail once the request timeout elapsed")
			}
			if elapsed >= handlerDelay {
				t.Fatalf("SendRequest did not respect the %v timeout: took %v (handler delay %v)", requestTimeout, elapsed, handlerDelay)
			}
			if elapsed > testDeadline {
				t.Fatalf("SendRequest exceeded the test deadline: took %v", elapsed)
			}
		})
	}
}

// stateGetter is satisfied by both HTTPClientPool and SSEPool.
type stateGetter interface {
	GetServerState(name string) (ServerState, error)
}

// waitForConnected polls until the server reaches StateRunning (or the
// deadline elapses), so a timeout test measures only the request itself.
func waitForConnected(t *testing.T, p envelopePool, name string, timeout time.Duration) {
	t.Helper()
	sg, ok := p.(stateGetter)
	if !ok {
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if state, err := sg.GetServerState(name); err == nil && state == StateRunning {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("waitForConnected: %s did not reach running state within %v", name, timeout)
}
