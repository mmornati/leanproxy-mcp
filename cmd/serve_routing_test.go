package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/mmornati/leanproxy-mcp/pkg/router"
)

func TestToolCallName(t *testing.T) {
	tests := []struct {
		name string
		req  *proxy.JSONRPCRequest
		want string
	}{
		{"tools/call with name", &proxy.JSONRPCRequest{Method: "tools/call", Params: json.RawMessage(`{"name":"srv.echo"}`)}, "srv.echo"},
		{"tools/call without name", &proxy.JSONRPCRequest{Method: "tools/call", Params: json.RawMessage(`{"arguments":{}}`)}, ""},
		{"invoke_tool", &proxy.JSONRPCRequest{Method: "invoke_tool", Params: json.RawMessage(`{"name":"srv.echo"}`)}, "srv.echo"},
		{"non tool method", &proxy.JSONRPCRequest{Method: "srv.echo", Params: json.RawMessage(`{}`)}, ""},
		{"nil request", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolCallName(tt.req); got != tt.want {
				t.Errorf("toolCallName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCanonicalToolMethod(t *testing.T) {
	tests := []struct{ in, want string }{
		{"srv.echo", "srv.echo"},
		{"srv_echo", "srv.echo"},
		// The underscore form follows the handler's parseToolName convention:
		// the FIRST underscore separates server from tool.
		{"my_server_my_tool", "my.server_my_tool"},
		{"echo", "echo"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := canonicalToolMethod(tt.in); got != tt.want {
			t.Errorf("canonicalToolMethod(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBareToolName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"srv.echo", "echo"},
		{"srv_echo", "echo"},
		// Consistent with parseToolName: the first underscore separates
		// server from tool, everything after it is the tool name.
		{"my_server_my_tool", "server_my_tool"},
		{"echo", "echo"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := bareToolName(tt.in); got != tt.want {
			t.Errorf("bareToolName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestForwardableRequest(t *testing.T) {
	tests := []struct {
		name       string
		req        *proxy.JSONRPCRequest
		wantMethod string
		wantTool   string
		wantArgs   string
	}{
		{
			name:       "tools/call dot form",
			req:        &proxy.JSONRPCRequest{Method: "tools/call", Params: json.RawMessage(`{"name":"srv-a.echo","arguments":{"message":"hi"}}`), ID: 1},
			wantMethod: "tools/call",
			wantTool:   "echo",
			wantArgs:   `{"message":"hi"}`,
		},
		{
			name:       "tools/call underscore form",
			req:        &proxy.JSONRPCRequest{Method: "tools/call", Params: json.RawMessage(`{"name":"srv_echo","arguments":{"message":"hi"}}`), ID: 2},
			wantMethod: "tools/call",
			wantTool:   "echo",
			wantArgs:   `{"message":"hi"}`,
		},
		{
			name:       "namespaced method form",
			req:        &proxy.JSONRPCRequest{Method: "srv-a.echo", Params: json.RawMessage(`{"message":"hi"}`), ID: 3},
			wantMethod: "tools/call",
			wantTool:   "echo",
			wantArgs:   `{"message":"hi"}`,
		},
		{
			name:       "tools/call without arguments",
			req:        &proxy.JSONRPCRequest{Method: "tools/call", Params: json.RawMessage(`{"name":"srv-a.echo"}`), ID: 4},
			wantMethod: "tools/call",
			wantTool:   "echo",
			wantArgs:   `{}`,
		},
		{
			name:       "bare tool method",
			req:        &proxy.JSONRPCRequest{Method: "echo", Params: json.RawMessage(`{"message":"hi"}`), ID: 5},
			wantMethod: "tools/call",
			wantTool:   "echo",
			wantArgs:   `{"message":"hi"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fwd := forwardableRequest(tt.req, "srv-a")
			if fwd.Method != tt.wantMethod {
				t.Errorf("forwarded method = %q, want %q", fwd.Method, tt.wantMethod)
			}
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(fwd.Params, &p); err != nil {
				t.Fatalf("forwarded params not valid: %v (%s)", err, fwd.Params)
			}
			if p.Name != tt.wantTool {
				t.Errorf("forwarded tool name = %q, want %q", p.Name, tt.wantTool)
			}
			if string(p.Arguments) != tt.wantArgs {
				t.Errorf("forwarded arguments = %s, want %s", p.Arguments, tt.wantArgs)
			}
			if fwd.ID != tt.req.ID {
				t.Errorf("forwarded ID = %v, want %v", fwd.ID, tt.req.ID)
			}
		})
	}
}

func TestRouteRequest_ResolvesBackendTool(t *testing.T) {
	ctx := context.Background()
	srvReg := registry.NewRegistry(slog.Default(), "")
	for _, name := range []string{"srv-a", "srv-b"} {
		if err := srvReg.Register(ctx, registry.ServerEntry{ID: name, Transport: registry.TransportStdio}); err != nil {
			t.Fatal(err)
		}
	}
	toolReg := router.NewToolRegistry()
	entries := []router.ToolEntry{
		{Name: "srv-a.echo", Namespace: "srv-a", ServerID: "srv-a"},
		{Name: "srv-a.add", Namespace: "srv-a", ServerID: "srv-a"},
		{Name: "srv-b.echo", Namespace: "srv-b", ServerID: "srv-b"},
	}
	for _, e := range entries {
		if err := toolReg.RegisterTool(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	r := router.NewRouter(toolReg, srvReg, slog.Default())

	cases := []struct {
		name   string
		req    *proxy.JSONRPCRequest
		wantID string
	}{
		{"tools/call dot name", &proxy.JSONRPCRequest{Method: "tools/call", Params: json.RawMessage(`{"name":"srv-b.echo"}`)}, "srv-b"},
		{"tools/call underscore name", &proxy.JSONRPCRequest{Method: "tools/call", Params: json.RawMessage(`{"name":"srv-a_echo"}`)}, "srv-a"},
		{"namespaced method", &proxy.JSONRPCRequest{Method: "srv-a.add"}, "srv-a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, err := routeRequest(ctx, r, tc.req)
			if err != nil {
				t.Fatalf("routeRequest failed: %v", err)
			}
			if server.ID != tc.wantID {
				t.Errorf("routeRequest() = %q, want %q", server.ID, tc.wantID)
			}
		})
	}

	_, err := routeRequest(ctx, r, &proxy.JSONRPCRequest{Method: "tools/call", Params: json.RawMessage(`{"name":"nope.x"}`)})
	if err == nil {
		t.Fatal("expected error routing unknown tool")
	}
}

// fakeToolSource implements pool.ServerSource and serves two static tools per
// server so the handler tool cache can be populated in-process.
type fakeToolSource struct {
	names []string
}

func (f *fakeToolSource) SendRequestToServer(_ context.Context, _ string, method string, _ json.RawMessage, _ time.Duration) (*pool.Response, error) {
	if method == "tools/list" {
		tools := []mcp.Tool{
			{Name: "echo", Description: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "add", Description: "add", InputSchema: json.RawMessage(`{"type":"object"}`)},
		}
		data, _ := json.Marshal(mcp.ToolsListResult{Tools: tools})
		return &pool.Response{Result: data, ID: 1}, nil
	}
	return &pool.Response{Result: json.RawMessage(`{}`), ID: 1}, nil
}

func (f *fakeToolSource) SendRequestToServerWithID(_ context.Context, _ string, _ string, _ json.RawMessage, _ time.Duration, id int) (*pool.Response, error) {
	return &pool.Response{Result: json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{}}`), ID: id}, nil
}

func (f *fakeToolSource) SendServerNotification(context.Context, string, string, map[string]interface{}) error {
	return nil
}

func (f *fakeToolSource) ListServers() []string { return f.names }

func (f *fakeToolSource) GetServerState(string) (pool.ServerState, error) { return pool.StateRunning, nil }

func (f *fakeToolSource) RestartServer(context.Context, string) error { return nil }

func (f *fakeToolSource) IsServerMCPInitialized(string) bool { return true }

func (f *fakeToolSource) MarkServerMCPInitialized(string) {}

func (f *fakeToolSource) Close() error { return nil }

func TestPopulateRouterTools_RegistersCachedTools(t *testing.T) {
	ctx := context.Background()
	h := mcp.NewHandlerWithToolStore(&fakeToolSource{names: []string{"test-server"}}, slog.Default(), nil)
	h.PopulateToolCache(ctx)

	srvReg := registry.NewRegistry(slog.Default(), "")
	if err := srvReg.Register(ctx, registry.ServerEntry{ID: "test-server", Transport: registry.TransportStdio}); err != nil {
		t.Fatal(err)
	}
	toolReg := router.NewToolRegistry()
	populateRouterTools(ctx, h, toolReg)

	r := router.NewRouter(toolReg, srvReg, slog.Default())
	server, err := routeRequest(ctx, r, &proxy.JSONRPCRequest{
		Method: "tools/call",
		Params: json.RawMessage(`{"name":"test-server.echo","arguments":{}}`),
		ID:     1,
	})
	if err != nil {
		t.Fatalf("routeRequest after populateRouterTools failed: %v", err)
	}
	if server.ID != "test-server" {
		t.Errorf("routeRequest() = %q, want %q", server.ID, "test-server")
	}
}

// TestHandleSingleRequest_RealRouterRoutesBackendTool is the regression test
// for #281: a backend tool call through the serve listener must resolve via
// the router (previously always -32601 because no tools were registered), be
// forwarded to the owning server as a bare tools/call, and have its params
// and response redacted in both directions.
func TestHandleSingleRequest_RealRouterRoutesBackendTool(t *testing.T) {
	withBuiltInRedactor(t)
	ctx := context.Background()

	srvReg := registry.NewRegistry(slog.Default(), "")
	if err := srvReg.Register(ctx, registry.ServerEntry{ID: "test-server", Transport: registry.TransportStdio}); err != nil {
		t.Fatal(err)
	}
	toolReg := router.NewToolRegistry()
	if err := toolReg.RegisterTool(ctx, router.ToolEntry{Name: "test-server.echo", Namespace: "test-server", ServerID: "test-server"}); err != nil {
		t.Fatal(err)
	}
	realR := router.NewRouter(toolReg, srvReg, slog.Default())

	var forwarded *proxy.JSONRPCRequest
	mockP := &mockPool{sendRequestFunc: func(_ context.Context, _ string, req *proxy.JSONRPCRequest, _ time.Duration) (*proxy.JSONRPCResponse, error) {
		forwarded = req
		return &proxy.JSONRPCResponse{JSONRPC: "2.0", Result: json.RawMessage(`{"content":[{"type":"text","text":"Echo: ` + testAWSKey + `"}]}`), ID: req.ID}, nil
	}}

	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	handleSingleRequest(ctx,
		[]byte(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"test-server.echo","arguments":{"message":"`+testAWSKey+`"}},"id":1}`),
		w, realR, &mockGatewayTools{}, mockP)
	w.Flush()

	if forwarded == nil {
		t.Fatal("request was not forwarded: router failed to resolve backend tool")
	}
	if forwarded.Method != "tools/call" {
		t.Errorf("forwarded method = %q, want tools/call", forwarded.Method)
	}
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(forwarded.Params, &p); err != nil {
		t.Fatalf("forwarded params not valid JSON: %v", err)
	}
	if p.Name != "echo" {
		t.Errorf("forwarded tool name = %q, want echo", p.Name)
	}
	if bytes.Contains(forwarded.Params, []byte(testAWSKey)) {
		t.Errorf("secret forwarded upstream unredacted: %s", forwarded.Params)
	}
	if !bytes.Contains(forwarded.Params, []byte(bouncer.SecretRedacted)) {
		t.Errorf("expected redacted args forwarded upstream: %s", forwarded.Params)
	}
	if strings.Contains(buf.String(), testAWSKey) {
		t.Errorf("secret in backend response reached client: %s", buf.String())
	}
	if !strings.Contains(buf.String(), bouncer.SecretRedacted) {
		t.Errorf("expected redaction marker in client response: %s", buf.String())
	}
}