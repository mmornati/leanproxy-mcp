package pool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
)

// fakeMCPBackend builds an in-process mcp-go server exposing `echo` and `add`
// and returns the transport's http.Handler (Streamable HTTP or SSE). It is the
// stand-in for a real backend MCP server behind the proxy.
func fakeMCPBackend(sse bool) http.Handler {
	mcpServer := server.NewMCPServer("fake-backend", "1.0.0", server.WithToolCapabilities(true))

	echo := mcp.NewTool("echo", mcp.WithString("message", mcp.Required()))
	mcpServer.AddTool(echo, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		msg, _ := req.GetArguments()["message"].(string)
		return mcp.NewToolResultText("Echo: " + msg), nil
	})

	add := mcp.NewTool("add", mcp.WithNumber("a"), mcp.WithNumber("b"))
	mcpServer.AddTool(add, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a, _ := req.GetArguments()["a"].(float64)
		b, _ := req.GetArguments()["b"].(float64)
		return mcp.NewToolResultText(fmt.Sprintf("Sum: %.0f", a+b)), nil
	})

	if sse {
		return server.NewSSEServer(mcpServer)
	}
	return server.NewStreamableHTTPServer(mcpServer)
}

// envelopePool is the subset of pool behavior the tools/call envelope tests
// need; both HTTPClientPool and SSEPool satisfy it.
type envelopePool interface {
	StartServer(ctx context.Context, config *migrate.ServerConfig) error
	SendRequest(ctx context.Context, serverName string, req *proxy.JSONRPCRequest, timeout time.Duration) (*proxy.JSONRPCResponse, error)
	Close() error
}

func newEnvelopePool(sse bool) envelopePool {
	if sse {
		return NewSSEPool(nil)
	}
	return NewHTTPClientPool(nil)
}

// TestPoolSendRequest_ToolsCallEnvelope is the regression test for issue #281:
// serve mode now routes backend calls as `tools/call` envelopes, so the HTTP
// and SSE pool SendRequest entry points must parse the envelope instead of
// invoking a tool literally named "tools/call". Before the fix, the backend
// would return "tool not found: tools/call".
func TestPoolSendRequest_ToolsCallEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name string
		sse  bool
	}{
		{name: "http", sse: false},
		{name: "sse", sse: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(fakeMCPBackend(tc.sse))
			defer backend.Close()

			pool := newEnvelopePool(tc.sse)
			defer pool.Close()

			// The mcp-go SSE server serves the event stream under /sse and
			// accepts messages under /message; the Streamable HTTP server
			// accepts JSON-RPC POSTs at any path.
			baseURL := backend.URL
			if tc.sse {
				baseURL += "/sse"
			}
			cfg := &migrate.ServerConfig{
				Name: "srv",
				HTTP: &migrate.HTTPConfig{URL: baseURL},
			}
			if err := pool.StartServer(context.Background(), cfg); err != nil {
				t.Fatalf("StartServer: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			// The tool named in the envelope must be the one executed, not
			// the literal method "tools/call".
			resp, err := pool.SendRequest(ctx, "srv", &proxy.JSONRPCRequest{
				JSONRPC: "2.0",
				Method:  "tools/call",
				Params:  json.RawMessage(`{"name":"echo","arguments":{"message":"hi"}}`),
				ID:      1,
			}, 10*time.Second)
			if err != nil {
				t.Fatalf("tools/call envelope rejected: %v", err)
			}
			if resp.Error != nil {
				t.Fatalf("tools/call returned error: %v", resp.Error)
			}
			if !bytes.Contains(resp.Result, []byte("Echo: hi")) {
				t.Fatalf("expected echo tool to run with parsed name, got result: %s", resp.Result)
			}

			// Numeric arguments flow through the envelope untouched.
			resp, err = pool.SendRequest(ctx, "srv", &proxy.JSONRPCRequest{
				JSONRPC: "2.0",
				Method:  "tools/call",
				Params:  json.RawMessage(`{"name":"add","arguments":{"a":2,"b":3}}`),
				ID:      2,
			}, 10*time.Second)
			if err != nil {
				t.Fatalf("tools/call add rejected: %v", err)
			}
			if !bytes.Contains(resp.Result, []byte("Sum: 5")) {
				t.Fatalf("expected add tool to run, got result: %s", resp.Result)
			}

			// Malformed envelope params are a client error, not a mystery
			// tool call.
			if _, err := pool.SendRequest(ctx, "srv", &proxy.JSONRPCRequest{
				JSONRPC: "2.0",
				Method:  "tools/call",
				Params:  json.RawMessage(`not json`),
				ID:      3,
			}, 10*time.Second); err == nil {
				t.Fatal("expected error for malformed tools/call params")
			}
		})
	}
}
