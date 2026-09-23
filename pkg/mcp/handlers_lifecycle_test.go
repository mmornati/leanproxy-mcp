package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/stretchr/testify/require"
)

// lifecyclePool is a ServerSource whose servers answer tools/list after a
// per-server delay (or never, for a "hung" server) and that counts every
// method it receives. It also implements pool.ServerEventSource and
// pool.SessionInfoProvider.
type lifecyclePool struct {
	mu      sync.Mutex
	servers []string
	delay   map[string]time.Duration
	hung    map[string]bool
	tools   map[string][]Tool
	calls   map[string]int // "server method" -> count
	events  pool.ServerEventHandler
	info    map[string]*pool.InitializeResult
}

func newLifecyclePool(servers ...string) *lifecyclePool {
	return &lifecyclePool{
		servers: servers,
		delay:   map[string]time.Duration{},
		hung:    map[string]bool{},
		tools:   map[string][]Tool{},
		calls:   map[string]int{},
		info:    map[string]*pool.InitializeResult{},
	}
}

func (p *lifecyclePool) count(server, method string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[server+" "+method]
}

func (p *lifecyclePool) setTools(server string, tools ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tools[server] = nil
	for _, name := range tools {
		p.tools[server] = append(p.tools[server], Tool{Name: name, Description: name, InputSchema: json.RawMessage(`{}`)})
	}
}

func (p *lifecyclePool) emit(ev pool.ServerEvent) {
	p.mu.Lock()
	fn := p.events
	p.mu.Unlock()
	if fn != nil {
		fn(ev)
	}
}

func (p *lifecyclePool) SendRequestToServer(ctx context.Context, name, method string, params json.RawMessage, timeout time.Duration) (*pool.Response, error) {
	p.mu.Lock()
	p.calls[name+" "+method]++
	delay, hung := p.delay[name], p.hung[name]
	tools := p.tools[name]
	p.mu.Unlock()

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if hung {
		<-ctx.Done()
		return nil, fmt.Errorf("request timeout: %w", ctx.Err())
	}
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if method == MethodToolsList {
		b, _ := json.Marshal(ToolsListResult{Tools: tools})
		return &pool.Response{Result: b}, nil
	}
	return &pool.Response{Result: json.RawMessage(`{"content":[]}`)}, nil
}

func (p *lifecyclePool) SendRequestToServerWithID(ctx context.Context, name, method string, params json.RawMessage, timeout time.Duration, _ int) (*pool.Response, error) {
	return p.SendRequestToServer(ctx, name, method, params, timeout)
}

func (p *lifecyclePool) SendServerNotification(_ context.Context, name, method string, _ map[string]interface{}) error {
	p.mu.Lock()
	p.calls[name+" "+method]++
	p.mu.Unlock()
	return nil
}
func (p *lifecyclePool) ListServers() []string                           { return append([]string(nil), p.servers...) }
func (p *lifecyclePool) GetServerState(string) (pool.ServerState, error) { return pool.StateIdle, nil }
func (p *lifecyclePool) GetServerTransport(string) (string, error)       { return "stdio", nil }
func (p *lifecyclePool) RestartServer(context.Context, string) error     { return nil }
func (p *lifecyclePool) Close() error                                    { return nil }
func (p *lifecyclePool) SetServerEventHandler(fn pool.ServerEventHandler) {
	p.mu.Lock()
	p.events = fn
	p.mu.Unlock()
}
func (p *lifecyclePool) ServerInitializeResult(name string) (*pool.InitializeResult, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	res, ok := p.info[name]
	return res, ok
}

func listToolsText(t *testing.T, h *Handler, server string) string {
	t.Helper()
	args, _ := json.Marshal(map[string]interface{}{"server_name": server})
	resp, err := h.HandleRequest(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		Method:  MethodToolsCall,
		Params:  mustMarshal(t, ToolsCallParams{Name: "list_tools", Arguments: args}),
		ID:      1,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Nil(t, resp.Error)
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	require.NotEmpty(t, result.Content)
	return result.Content[0].Text
}

// TestHandlerNeverSendsHandshake: the MCP handshake is the pool's job
// (#297). No handler path — tool calls, invoke_tool, list_tools, the tool
// refresh — sends initialize or notifications/initialized.
func TestHandlerNeverSendsHandshake(t *testing.T) {
	p := newLifecyclePool("s")
	p.setTools("s", "echo")
	h := NewHandler(p, nil)

	h.PopulateToolCache(context.Background())
	_ = listToolsText(t, h, "s")
	for _, name := range []string{"s_echo", "invoke_tool"} {
		params := ToolsCallParams{Name: name, Arguments: json.RawMessage(`{}`)}
		if name == "invoke_tool" {
			params.Arguments = json.RawMessage(`{"server":"s","tool":"echo","arguments":{}}`)
		}
		resp, err := h.HandleRequest(context.Background(), &Request{JSONRPC: JSONRPCVersion, Method: MethodToolsCall, Params: mustMarshal(t, params), ID: 2})
		require.NoError(t, err)
		require.Nil(t, resp.Error, "%s failed: %+v", name, resp.Error)
	}

	require.Equal(t, 0, p.count("s", MethodInitialize))
	require.Equal(t, 0, p.count("s", "notifications/initialized"))
	require.Equal(t, 2, p.count("s", MethodToolsCall))
}

// TestListToolsRefreshesOnlyThatServer: list_tools for a server with no
// cached tools refreshes that server only, concurrent callers share one
// tools/list, and a hung server elsewhere is never touched.
func TestListToolsRefreshesOnlyThatServer(t *testing.T) {
	p := newLifecyclePool("fast", "hung")
	p.setTools("fast", "a", "b")
	p.delay["fast"] = 50 * time.Millisecond
	p.hung["hung"] = true
	h := NewHandler(p, nil)

	var wg sync.WaitGroup
	texts := make([]string, 10)
	for i := range texts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			texts[i] = listToolsText(t, h, "fast")
		}(i)
	}
	wg.Wait()
	for _, text := range texts {
		require.Contains(t, text, "fast tools (2)")
	}
	require.Equal(t, 1, p.count("fast", MethodToolsList), "concurrent list_tools must share one refresh")
	require.Equal(t, 0, p.count("hung", MethodToolsList), "other servers must never be touched")
}

// TestListToolsHungServerFailsAfterItsTimeout: list_tools for a hung server
// returns an explanatory failure after that server's timeout, not later.
func TestListToolsHungServerFailsAfterItsTimeout(t *testing.T) {
	p := newLifecyclePool("hung")
	p.hung["hung"] = true
	h := NewHandler(p, nil)
	h.SetTimeout("hung", 200*time.Millisecond)

	start := time.Now()
	text := listToolsText(t, h, "hung")
	elapsed := time.Since(start)
	require.Contains(t, text, "No tools available on server 'hung'")
	require.Contains(t, text, "failed")
	require.Less(t, elapsed, 3*time.Second, "list_tools must give up at the server's timeout")
}

// TestBackgroundRefresh covers the startup path: StartBackgroundRefresh
// returns at once even with a hung server, fills the cache in the
// background, and refreshes a server again on a pool event
// (tools/list_changed or a new session after a restart), notifying the
// OnToolsChanged listeners.
func TestBackgroundRefresh(t *testing.T) {
	p := newLifecyclePool("ok", "hung")
	p.setTools("ok", "one")
	p.hung["hung"] = true
	h := NewHandler(p, nil)
	h.SetTimeout("hung", time.Hour)

	var changes atomic.Int32
	var lastMu sync.Mutex
	var last []Tool
	h.OnToolsChanged(func(server string, tools []Tool) {
		if server != "ok" {
			return
		}
		lastMu.Lock()
		last = tools
		lastMu.Unlock()
		changes.Add(1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	h.StartBackgroundRefresh(ctx)
	require.Less(t, time.Since(start), time.Second, "StartBackgroundRefresh must not block")

	require.Eventually(t, func() bool { return h.toolCountFor("ok") == 1 }, 5*time.Second, 5*time.Millisecond)
	// The cache is published before the listeners run (on the refresh
	// goroutine), so seeing the tools cached does not mean the listener has
	// been called yet. Wait for the refresh to finish: RefreshServerTools
	// joins the one in progress (or, if it already finished, runs a new one
	// with an unchanged list, which must not notify).
	waitRefreshed := func() {
		t.Helper()
		require.NoError(t, h.RefreshServerTools(ctx, "ok"))
	}
	waitRefreshed()
	require.Equal(t, int32(1), changes.Load())

	p.setTools("ok", "one", "two")
	p.emit(pool.ServerEvent{Server: "ok", Kind: pool.EventToolsListChanged})
	require.Eventually(t, func() bool { return h.toolCountFor("ok") == 2 }, 5*time.Second, 5*time.Millisecond)
	require.Eventually(t, func() bool { return changes.Load() == 2 }, 5*time.Second, 5*time.Millisecond)
	lastMu.Lock()
	require.Len(t, last, 2)
	lastMu.Unlock()

	// A new session with an unchanged tool list refreshes but does not
	// notify listeners again.
	before := p.count("ok", MethodToolsList)
	p.emit(pool.ServerEvent{Server: "ok", Kind: pool.EventSessionStarted, Generation: 2})
	// The event handler starts (or joins) the refresh synchronously, so
	// this waits for the refresh triggered by the event to complete,
	// listeners included.
	waitRefreshed()
	require.Greater(t, p.count("ok", MethodToolsList), before)
	require.Equal(t, int32(2), changes.Load())
}

// TestBackgroundRefreshRetriesUnknownServers: a server that fails at
// startup and becomes healthy later gets its tools without a restart of the
// proxy, through the periodic retry.
func TestBackgroundRefreshRetriesUnknownServers(t *testing.T) {
	p := newLifecyclePool("late")
	p.hung["late"] = true
	h := NewHandler(p, nil)
	h.SetTimeout("late", 50*time.Millisecond)
	h.SetToolRefreshRetryInterval(20 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.StartBackgroundRefresh(ctx)
	require.Eventually(t, func() bool { return p.count("late", MethodToolsList) >= 1 }, 5*time.Second, 5*time.Millisecond)
	require.Equal(t, 0, h.toolCountFor("late"))

	p.mu.Lock()
	p.hung["late"] = false
	p.mu.Unlock()
	p.setTools("late", "x")
	require.Eventually(t, func() bool { return h.toolCountFor("late") == 1 }, 5*time.Second, 5*time.Millisecond)
}

// TestListServersShowsSessionInfo: list_servers shows the serverInfo and
// the first 120 characters of the stored instructions.
func TestListServersShowsSessionInfo(t *testing.T) {
	p := newLifecyclePool("docs")
	p.info["docs"] = &pool.InitializeResult{
		ServerInfo:   pool.ServerInfo{Name: "docs-server", Version: "2.1.0"},
		Instructions: "Use search first.\n" + strings.Repeat("x", 300),
	}
	h := NewHandler(p, nil)
	h.setServerTools("docs", []Tool{{Name: "search"}})

	resp, err := h.HandleRequest(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		Method:  MethodToolsCall,
		Params:  mustMarshal(t, ToolsCallParams{Name: "list_servers"}),
		ID:      1,
	})
	require.NoError(t, err)
	require.Nil(t, resp.Error)
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	text := result.Content[0].Text
	require.Contains(t, text, "docs (stdio, healthy, 1 tools) [docs-server 2.1.0] instructions: Use search first. xxx")
	_, instr, _ := strings.Cut(text, "instructions: ")
	require.Equal(t, 120, len([]rune(instr)))
	require.True(t, strings.HasSuffix(instr, "..."))
	require.NotContains(t, text, "\n", "instructions must stay on the server's line")
}

// TestHandleShutdownDoesNotClosePool: the front end owns the shutdown
// order, so the handler's shutdown must not close the pool.
func TestHandleShutdownDoesNotClosePool(t *testing.T) {
	p := &closeCountingPool{lifecyclePool: newLifecyclePool()}
	h := NewHandler(p, nil)
	resp, err := h.HandleRequest(context.Background(), &Request{JSONRPC: JSONRPCVersion, Method: MethodShutdown, ID: 1})
	require.NoError(t, err)
	require.Nil(t, resp.Error)
	require.Equal(t, int32(0), p.closes.Load())
}

type closeCountingPool struct {
	*lifecyclePool
	closes atomic.Int32
}

func (p *closeCountingPool) Close() error {
	p.closes.Add(1)
	return nil
}
