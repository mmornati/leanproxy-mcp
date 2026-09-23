package cmd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/cache"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp/responsecache"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
)

// resourceSource is a pool.ServerSource with one upstream ("docs") that
// serves resources and prompts (#307). Its resource leaks a secret.
type resourceSource struct {
	reads atomic.Int64
}

func (s *resourceSource) SendRequestToServer(_ context.Context, _ string, method string, params json.RawMessage, _ time.Duration) (*pool.Response, error) {
	switch method {
	case mcp.MethodInitialize:
		return &pool.Response{Result: json.RawMessage(`{"protocolVersion":"2025-06-18","capabilities":{"resources":{},"prompts":{}},"serverInfo":{"name":"docs","version":"1"}}`)}, nil
	case mcp.MethodToolsList:
		return &pool.Response{Result: json.RawMessage(`{"tools":[]}`)}, nil
	case mcp.MethodResourcesList:
		return &pool.Response{Result: json.RawMessage(`{"resources":[{"uri":"file:///guide.md","name":"guide"}]}`)}, nil
	case mcp.MethodResourcesRead:
		n := s.reads.Add(1)
		return &pool.Response{Result: json.RawMessage(`{"contents":[{"uri":"file:///guide.md","text":"read ` + string(rune('0'+n)) + ` token ` + testGHPat + `","echo":` + string(params) + `}]}`)}, nil
	case mcp.MethodPromptsList:
		return &pool.Response{Result: json.RawMessage(`{"prompts":[{"name":"summarize"}]}`)}, nil
	case mcp.MethodPromptsGet:
		return &pool.Response{Result: json.RawMessage(`{"messages":[],"echo":` + string(params) + `}`)}, nil
	}
	return &pool.Response{Result: json.RawMessage(`{}`)}, nil
}

func (s *resourceSource) SendRequestToServerWithID(ctx context.Context, name, method string, params json.RawMessage, timeout time.Duration, _ int) (*pool.Response, error) {
	return s.SendRequestToServer(ctx, name, method, params, timeout)
}
func (s *resourceSource) SendServerNotification(context.Context, string, string, map[string]interface{}) error {
	return nil
}
func (s *resourceSource) ListServers() []string { return []string{"docs"} }
func (s *resourceSource) GetServerState(string) (pool.ServerState, error) {
	return pool.StateRunning, nil
}
func (s *resourceSource) GetServerTransport(string) (string, error)   { return "stdio", nil }
func (s *resourceSource) RestartServer(context.Context, string) error { return nil }
func (s *resourceSource) Close() error                                { return nil }

// withServeMCPHandler installs h as serve's MCP handler for one test.
func withServeMCPHandler(t *testing.T, h *mcp.Handler) {
	t.Helper()
	prev := serveMCPHandler.Load()
	t.Cleanup(func() { serveMCPHandler.Store(prev) })
	serveMCPHandler.Store(h)
}

func serveCall(ctx context.Context, method, params string, id int) *proxy.JSONRPCResponse {
	req := &proxy.JSONRPCRequest{JSONRPC: "2.0", Method: method, ID: id}
	if params != "" {
		req.Params = json.RawMessage(params)
	}
	upstreamMustNotBeCalled := &mockPool{sendRequestFunc: func(context.Context, string, *proxy.JSONRPCRequest, time.Duration) (*proxy.JSONRPCResponse, error) {
		panic("protocol methods must not be routed to a single backend")
	}}
	return serveRequest(ctx, req, &mockRouter{}, &mockGatewayTools{}, upstreamMustNotBeCalled)
}

// TestServe_ProtocolMethodsNegotiatedPerConnection: serve answers
// initialize with version negotiation per connection session, and
// aggregates resources/prompts through the shared handler, redacted.
func TestServe_ProtocolMethodsNegotiatedPerConnection(t *testing.T) {
	withBuiltInRedactor(t)
	src := &resourceSource{}
	h := mcp.NewHandler(src, slog.New(slog.NewTextHandler(io.Discard, nil)))
	withServeMCPHandler(t, h)

	modern, closeModern := h.OpenSession(nil)
	defer closeModern()
	legacy, closeLegacy := h.OpenSession(nil)
	defer closeLegacy()
	modernCtx := mcp.WithClientSession(context.Background(), modern)
	legacyCtx := mcp.WithClientSession(context.Background(), legacy)

	resp := serveCall(modernCtx, "initialize", `{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"a","version":"1"}}`, 1)
	if resp.Error != nil || !strings.Contains(string(resp.Result), `"protocolVersion":"2025-06-18"`) ||
		!strings.Contains(string(resp.Result), `"resources":{"listChanged":true}`) || !strings.Contains(string(resp.Result), `"prompts":{"listChanged":true}`) {
		t.Fatalf("modern initialize = %s %+v", resp.Result, resp.Error)
	}
	resp = serveCall(legacyCtx, "initialize", `{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"b","version":"1"}}`, 1)
	if resp.Error != nil || !strings.Contains(string(resp.Result), `"protocolVersion":"2024-11-05"`) {
		t.Fatalf("legacy initialize = %s %+v", resp.Result, resp.Error)
	}
	if modern.ProtocolVersion() != "2025-06-18" || legacy.ProtocolVersion() != "2024-11-05" {
		t.Fatalf("sessions: %q %q", modern.ProtocolVersion(), legacy.ProtocolVersion())
	}

	resp = serveCall(modernCtx, "resources/list", "", 2)
	if resp.Error != nil || !strings.Contains(string(resp.Result), `"uri":"leanproxy://docs/file:///guide.md"`) {
		t.Fatalf("resources/list = %s %+v", resp.Result, resp.Error)
	}
	resp = serveCall(modernCtx, "resources/read", `{"uri":"leanproxy://docs/file:///guide.md"}`, 3)
	if resp.Error != nil || !strings.Contains(string(resp.Result), `"echo":{"uri":"file:///guide.md"}`) {
		t.Fatalf("resources/read = %s %+v", resp.Result, resp.Error)
	}
	if strings.Contains(string(resp.Result), testGHPat) {
		t.Fatalf("resources/read leaked a secret: %s", resp.Result)
	}
	resp = serveCall(modernCtx, "prompts/list", "", 4)
	if resp.Error != nil || !strings.Contains(string(resp.Result), `"name":"docs.summarize"`) {
		t.Fatalf("prompts/list = %s %+v", resp.Result, resp.Error)
	}
	resp = serveCall(modernCtx, "prompts/get", `{"name":"docs.summarize","arguments":{"n":9007199254740993}}`, 5)
	if resp.Error != nil || !strings.Contains(string(resp.Result), `"echo":{"arguments":{"n":9007199254740993},"name":"summarize"}`) {
		t.Fatalf("prompts/get = %s %+v", resp.Result, resp.Error)
	}
	// notifications/initialized gets no response.
	if resp := serveCall(modernCtx, "notifications/initialized", "", 0); resp != nil {
		t.Fatalf("notification answered: %+v", resp)
	}
}

// withSemanticCache installs an exact-match semantic cache for one test.
func withSemanticCache(t *testing.T) *cache.SemanticCache {
	t.Helper()
	prev := cache.GlobalSemanticCache()
	t.Cleanup(func() { cache.SetGlobalSemanticCache(prev) })
	sc := cache.NewSemanticCache(nil, slog.Default(), 0)
	cache.SetGlobalSemanticCache(sc)
	return sc
}

// TestSemanticCache_NeverServesResourcesOrPrompts is the #307 carry-over:
// serve's semantic cache used to answer every non-tools/call method it
// routed, including resources/read and prompts/get. A poisoned entry must
// never be served, and nothing is stored for them.
func TestSemanticCache_NeverServesResourcesOrPrompts(t *testing.T) {
	withBuiltInRedactor(t)
	withServeMCPHandler(t, nil) // the routing path, where the cache lives
	sc := withSemanticCache(t)
	withResponseCache(t, &responsecache.Config{Enabled: true, Tools: []string{"resources/read", "prompts/get", "*"}})

	for _, method := range []string{"resources/read", "prompts/get", "resources/templates/list", "completion/complete"} {
		params := `{"uri":"file:///x","name":"p"}`
		if err := sc.Set(ctx, method+":"+params, json.RawMessage(`{"poisoned":true}`), method, nil); err != nil {
			t.Fatal(err)
		}
		var calls atomic.Int64
		p := &mockPool{sendRequestFunc: func(_ context.Context, _ string, req *proxy.JSONRPCRequest, _ time.Duration) (*proxy.JSONRPCResponse, error) {
			calls.Add(1)
			return &proxy.JSONRPCResponse{JSONRPC: "2.0", Result: json.RawMessage(`{"fresh":true}`), ID: req.ID}, nil
		}}
		for i := 1; i <= 2; i++ {
			resp := serveRequest(ctx, &proxy.JSONRPCRequest{JSONRPC: "2.0", Method: method, Params: json.RawMessage(params), ID: i}, &mockRouter{}, &mockGatewayTools{}, p)
			if resp == nil || resp.Error != nil || strings.Contains(string(resp.Result), "poisoned") {
				t.Fatalf("%s call %d served from the semantic cache: %+v", method, i, resp)
			}
		}
		if calls.Load() != 2 {
			t.Fatalf("%s: upstream called %d times, want 2 (never cached)", method, calls.Load())
		}
	}
}

// TestSemanticCache_FollowsResponseCachePolicy: a tool addressed by its
// namespaced method is served from the semantic cache only when the
// response_cache allowlist declares it cacheable, like on the stdio path.
func TestSemanticCache_FollowsResponseCachePolicy(t *testing.T) {
	withBuiltInRedactor(t)
	withServeMCPHandler(t, nil)

	run := func(t *testing.T, cfg *responsecache.Config) int64 {
		withSemanticCache(t)
		withResponseCache(t, cfg)
		var calls atomic.Int64
		p := &mockPool{sendRequestFunc: func(_ context.Context, _ string, req *proxy.JSONRPCRequest, _ time.Duration) (*proxy.JSONRPCResponse, error) {
			calls.Add(1)
			return &proxy.JSONRPCResponse{JSONRPC: "2.0", Result: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`), ID: req.ID}, nil
		}}
		for i := 1; i <= 2; i++ {
			resp := serveRequest(ctx, &proxy.JSONRPCRequest{JSONRPC: "2.0", Method: "docs.read_file", Params: json.RawMessage(`{"path":"a"}`), ID: i}, &mockRouter{}, &mockGatewayTools{}, p)
			if resp == nil || resp.Error != nil {
				t.Fatalf("call %d: %+v", i, resp)
			}
		}
		return calls.Load()
	}
	t.Run("not allowlisted", func(t *testing.T) {
		if n := run(t, nil); n != 2 {
			t.Fatalf("upstream called %d times, want 2", n)
		}
		if n := run(t, &responsecache.Config{Enabled: true, Tools: []string{"docs.other"}}); n != 2 {
			t.Fatalf("upstream called %d times, want 2", n)
		}
	})
	t.Run("allowlisted", func(t *testing.T) {
		if n := run(t, &responsecache.Config{Enabled: true, Tools: []string{"docs.read_file"}}); n != 1 {
			t.Fatalf("upstream called %d times, want 1 (second call cached)", n)
		}
	})
}
