package pool

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/stretchr/testify/require"
)

// sessionStats is the part of the fake server's stats the handshake tests
// read (see testdata/concurrentmcp).
type sessionStats struct {
	Received      int64 `json:"received"`
	Initializes   int64 `json:"initializes"`
	Initialized   int64 `json:"initialized"`
	EarlyRequests int64 `json:"earlyRequests"`
	PID           int   `json:"pid"`
}

func getSessionStats(t *testing.T, p *StdioPool, name string) sessionStats {
	t.Helper()
	resp, err := callTool(context.Background(), p, name, "stats", nil, 5*time.Second)
	require.NoError(t, err)
	require.Nil(t, resp.Error)
	var st sessionStats
	require.NoError(t, json.Unmarshal(resp.Result, &st))
	return st
}

// startSessionMCP starts a pool with one concurrentmcp server run with args.
func startSessionMCP(t *testing.T, name string, timeout time.Duration, args ...string) *StdioPool {
	t.Helper()
	bin := concurrentMCPBinary(t)
	p := NewStdioPool(5, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.SetReconnect(ReconnectSettings{RestartBackoff: 20 * time.Millisecond})
	t.Cleanup(func() { _ = p.Close() })
	require.NoError(t, p.StartServer(context.Background(), &migrate.ServerConfig{
		Name:         name,
		Transport:    registry.TransportStdio,
		Stdio:        &migrate.StdioConfig{Command: bin, Args: args},
		TimeoutValue: timeout,
	}))
	return p
}

// eventRecorder collects server events.
type eventRecorder struct {
	mu     sync.Mutex
	events []ServerEvent
}

func (r *eventRecorder) record(ev ServerEvent) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

func (r *eventRecorder) has(kind ServerEventKind, generation uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range r.events {
		if ev.Kind == kind && (generation == 0 || ev.Generation == generation) {
			return true
		}
	}
	return false
}

// TestStdioHandshake_OncePerGeneration: many concurrent first callers share
// one initialize + notifications/initialized, written before any other
// request of the generation; the server's InitializeResult is stored.
func TestStdioHandshake_OncePerGeneration(t *testing.T) {
	p := startSessionMCP(t, "hs", 30*time.Second, "--init-delay-ms", "100", "--instructions", "Use echo for tests.")
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := callTool(ctx, p, "hs", "echo", map[string]interface{}{"tag": "x"}, 10*time.Second)
			if err == nil && resp.Error != nil {
				err = resp.Error
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	st := getSessionStats(t, p, "hs")
	require.Equal(t, int64(1), st.Initializes, "exactly one initialize per generation")
	require.Equal(t, int64(1), st.Initialized, "exactly one notifications/initialized per generation")
	require.Equal(t, int64(0), st.EarlyRequests, "no request may precede the handshake")
	require.Equal(t, int64(51), st.Received)
	require.True(t, p.IsServerMCPInitialized("hs"))

	res, ok := p.ServerInitializeResult("hs")
	require.True(t, ok)
	require.Equal(t, "2024-11-05", res.ProtocolVersion)
	require.Equal(t, "concurrentmcp", res.ServerInfo.Name)
	require.Equal(t, "1.0.0", res.ServerInfo.Version)
	require.Equal(t, "Use echo for tests.", res.Instructions)
	require.JSONEq(t, `{"tools":{"listChanged":true}}`, string(res.Capabilities))
	require.Equal(t, uint64(1), res.Generation)

	// An explicit initialize is answered from the stored result and never
	// reaches the server a second time; so is notifications/initialized.
	resp, err := p.SendRequestToServer(ctx, "hs", "initialize", json.RawMessage(`{}`), 5*time.Second)
	require.NoError(t, err)
	require.Nil(t, resp.Error)
	require.Contains(t, string(resp.Result), "concurrentmcp")
	require.NoError(t, p.SendServerNotification(ctx, "hs", "notifications/initialized", nil))
	st = getSessionStats(t, p, "hs")
	require.Equal(t, int64(1), st.Initializes)
	require.Equal(t, int64(1), st.Initialized)
}

// TestStdioHandshake_CallerGivingUpDoesNotAbortIt: a caller whose own
// timeout ends during the handshake fails, but the shared handshake goes on
// and the next caller uses it (still one initialize).
func TestStdioHandshake_CallerGivingUpDoesNotAbortIt(t *testing.T) {
	p := startSessionMCP(t, "hs-slow", 30*time.Second, "--init-delay-ms", "300")
	ctx := context.Background()

	_, err := callTool(ctx, p, "hs-slow", "echo", map[string]interface{}{"tag": "a"}, 50*time.Millisecond)
	require.Error(t, err)

	resp, err := callTool(ctx, p, "hs-slow", "echo", map[string]interface{}{"tag": "b"}, 10*time.Second)
	require.NoError(t, err)
	require.Equal(t, "b", resultTag(t, resp))
	st := getSessionStats(t, p, "hs-slow")
	require.Equal(t, int64(1), st.Initializes)
	require.Equal(t, int64(0), st.EarlyRequests)
}

// TestStdioHandshake_HungServerFailsAfterTimeout: a server that never
// answers initialize fails the request after the server timeout, without
// hanging.
func TestStdioHandshake_HungServerFailsAfterTimeout(t *testing.T) {
	p := startSessionMCP(t, "hs-hung", 200*time.Millisecond, "--init-delay-ms", "60000")
	start := time.Now()
	_, err := p.SendRequestToServer(context.Background(), "hs-hung", "tools/list", nil, 0)
	require.Error(t, err)
	require.Less(t, time.Since(start), 5*time.Second)
	require.False(t, p.IsServerMCPInitialized("hs-hung"))
}

// TestStdioHandshake_FreshAfterRestart: after the child dies the pool
// respawns it, reports a new session, and the next call succeeds with a new
// handshake on the new process.
func TestStdioHandshake_FreshAfterRestart(t *testing.T) {
	p := startSessionMCP(t, "hs-crash", 30*time.Second)
	rec := &eventRecorder{}
	p.SetServerEventHandler(rec.record)
	ctx := context.Background()

	before := getSessionStats(t, p, "hs-crash")
	require.Equal(t, int64(1), before.Initializes)

	_, _ = callTool(ctx, p, "hs-crash", "exit", map[string]interface{}{"code": 1}, 5*time.Second)
	require.Eventually(t, func() bool { return rec.has(EventSessionStarted, 2) }, 10*time.Second, 5*time.Millisecond,
		"the respawn must be reported as a new session")

	var after sessionStats
	require.Eventually(t, func() bool {
		resp, err := callTool(ctx, p, "hs-crash", "stats", nil, 5*time.Second)
		if err != nil || resp.Error != nil {
			return false
		}
		return json.Unmarshal(resp.Result, &after) == nil
	}, 10*time.Second, 10*time.Millisecond)
	require.NotEqual(t, before.PID, after.PID, "a new process must serve the call")
	require.Equal(t, int64(1), after.Initializes, "the new generation performs its own handshake")
	require.Equal(t, int64(1), after.Initialized)
	require.Equal(t, int64(0), after.EarlyRequests)

	res, ok := p.ServerInitializeResult("hs-crash")
	require.True(t, ok)
	require.Equal(t, uint64(2), res.Generation)
}

// TestStdioToolsListChangedNotification: an upstream
// notifications/tools/list_changed surfaces as EventToolsListChanged.
func TestStdioToolsListChangedNotification(t *testing.T) {
	p := startSessionMCP(t, "hs-notify", 30*time.Second)
	rec := &eventRecorder{}
	p.SetServerEventHandler(rec.record)

	resp, err := callTool(context.Background(), p, "hs-notify", "list_changed", nil, 5*time.Second)
	require.NoError(t, err)
	require.Nil(t, resp.Error)
	require.Eventually(t, func() bool { return rec.has(EventToolsListChanged, 0) }, 5*time.Second, 5*time.Millisecond)
}

// countingMCPBackend is an in-process Streamable HTTP MCP server that
// counts initialize requests and tool calls (by name).
type countingMCPBackend struct {
	initializes atomic.Int32
	mu          sync.Mutex
	toolCalls   map[string]int
	mcp         *server.MCPServer
}

func newCountingMCPBackend() *countingMCPBackend {
	b := &countingMCPBackend{toolCalls: map[string]int{}}
	hooks := &server.Hooks{}
	hooks.AddBeforeInitialize(func(context.Context, any, *mcp.InitializeRequest) { b.initializes.Add(1) })
	hooks.AddBeforeCallTool(func(_ context.Context, _ any, req *mcp.CallToolRequest) {
		b.mu.Lock()
		b.toolCalls[req.Params.Name]++
		b.mu.Unlock()
	})
	b.mcp = server.NewMCPServer("http-backend", "3.2.1",
		server.WithToolCapabilities(true),
		server.WithInstructions("Remote docs server."),
		server.WithHooks(hooks))
	b.mcp.AddTool(mcp.NewTool("echo"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	return b
}

func (b *countingMCPBackend) calls(name string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.toolCalls[name]
}

// TestHTTPPool_SessionFromMCPClient: an HTTP server's handshake is mcp-go's
// own Initialize. Its result is stored; an explicit initialize (or ping)
// through the pool never becomes a tools/call, and the connection is
// reported as a new session.
func TestHTTPPool_SessionFromMCPClient(t *testing.T) {
	backend := newCountingMCPBackend()
	ts := httptest.NewServer(server.NewStreamableHTTPServer(backend.mcp))
	defer ts.Close()

	p := NewHTTPClientPool(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer p.Close()
	rec := &eventRecorder{}
	p.SetServerEventHandler(rec.record)
	unified := NewUnifiedPool(NewStdioPool(0, 0, nil), p, nil, nil)
	defer unified.Close()

	require.NoError(t, p.StartServer(context.Background(), &migrate.ServerConfig{
		Name: "remote",
		HTTP: &migrate.HTTPConfig{URL: ts.URL},
	}))
	waitForConnected(t, p, "remote", 5*time.Second)
	require.Eventually(t, func() bool { return rec.has(EventSessionStarted, 1) }, 5*time.Second, 5*time.Millisecond)

	res, ok := unified.ServerInitializeResult("remote")
	require.True(t, ok)
	require.Equal(t, "http-backend", res.ServerInfo.Name)
	require.Equal(t, "3.2.1", res.ServerInfo.Version)
	require.Equal(t, "Remote docs server.", res.Instructions)

	ctx := context.Background()
	resp, err := unified.SendRequestToServer(ctx, "remote", "initialize", json.RawMessage(`{}`), 5*time.Second)
	require.NoError(t, err)
	require.Contains(t, string(resp.Result), "http-backend")
	_, err = unified.SendRequestToServer(ctx, "remote", "ping", nil, 5*time.Second)
	require.NoError(t, err)
	_, err = unified.SendRequestToServer(ctx, "remote", "tools/call", json.RawMessage(`{"name":"echo","arguments":{}}`), 5*time.Second)
	require.NoError(t, err)

	require.Equal(t, int32(1), backend.initializes.Load(), "one initialize per connection")
	require.Equal(t, 0, backend.calls("initialize"), "initialize must never be sent as a tools/call")
	require.Equal(t, 0, backend.calls("ping"), "ping must never be sent as a tools/call")
	require.Equal(t, 1, backend.calls("echo"))
}

// TestStdioResourcePromptListChangedNotifications (#307): upstream
// notifications/resources/list_changed and notifications/prompts/list_changed
// surface as their own server events.
func TestStdioResourcePromptListChangedNotifications(t *testing.T) {
	p := startSessionMCP(t, "hs-lists", 30*time.Second)
	rec := &eventRecorder{}
	p.SetServerEventHandler(rec.record)

	for _, kind := range []string{"resources", "prompts"} {
		resp, err := callTool(context.Background(), p, "hs-lists", "list_changed", map[string]interface{}{"tag": kind}, 5*time.Second)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
	}
	require.Eventually(t, func() bool {
		return rec.has(EventResourcesListChanged, 0) && rec.has(EventPromptsListChanged, 0)
	}, 5*time.Second, 5*time.Millisecond)
	require.False(t, rec.has(EventToolsListChanged, 0))
}

// TestStdioHandshake_RequestsLatestProtocolVersion (#307): the pool asks
// the upstream for the latest MCP revision and keeps whatever it answers.
func TestStdioHandshake_RequestsLatestProtocolVersion(t *testing.T) {
	p := startSessionMCP(t, "hs-version", 30*time.Second)
	resp, err := callTool(context.Background(), p, "hs-version", "stats", nil, 5*time.Second)
	require.NoError(t, err)
	require.Nil(t, resp.Error)
	var stats struct {
		RequestedProtocol string `json:"requestedProtocol"`
	}
	require.NoError(t, json.Unmarshal(resp.Result, &stats))
	require.Equal(t, RequestedProtocolVersion, stats.RequestedProtocol)

	res, ok := p.ServerInitializeResult("hs-version")
	require.True(t, ok)
	require.Equal(t, "2024-11-05", res.ProtocolVersion, "the server's (older) answer is accepted as is")
}
