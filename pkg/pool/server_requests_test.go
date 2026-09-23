package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	errs "github.com/mmornati/leanproxy-mcp/pkg/errors"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

// recordingHandler is a ServerMessageHandler that records what the pool
// hands it and answers requests with answer.
type recordingHandler struct {
	mu       sync.Mutex
	requests []string
	notes    []string
	ctxs     []context.Context
	answer   func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, *errs.JSONRPCError)
}

func (r *recordingHandler) HandleServerRequest(ctx context.Context, server, method string, params json.RawMessage) (json.RawMessage, *errs.JSONRPCError) {
	r.mu.Lock()
	r.requests = append(r.requests, server+" "+method+" "+string(params))
	r.ctxs = append(r.ctxs, ctx)
	answer := r.answer
	r.mu.Unlock()
	if answer == nil {
		return json.RawMessage(`{}`), nil
	}
	return answer(ctx, method, params)
}

func (r *recordingHandler) HandleServerNotification(_ context.Context, server, method string, params json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notes = append(r.notes, server+" "+method+" "+string(params))
}

func (r *recordingHandler) snapshot() (requests, notes []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requests...), append([]string(nil), r.notes...)
}

func TestRequestIDKey(t *testing.T) {
	for raw, want := range map[string]string{
		`1`: "n:1", ` 42 `: "n:42", `"1"`: "s:1", `"lp-7"`: "s:lp-7", `1.5`: "n:1.5",
	} {
		got, ok := RequestIDKey(json.RawMessage(raw))
		assert.True(t, ok, raw)
		assert.Equal(t, want, got, raw)
	}
	for _, raw := range []string{``, `null`, `{}`, `[1]`, `true`, `nope`} {
		_, ok := RequestIDKey(json.RawMessage(raw))
		assert.False(t, ok, raw)
	}
}

func TestInboundRequests(t *testing.T) {
	var in inboundRequests
	ctx, done, rpcErr := in.start(context.Background(), "s:a")
	require.Nil(t, rpcErr)
	_, _, rpcErr = in.start(context.Background(), "s:a")
	require.NotNil(t, rpcErr, "a duplicate id is refused")
	assert.True(t, in.cancel("s:a"))
	assert.Error(t, ctx.Err())
	assert.False(t, in.cancel("s:unknown"))
	done()
	assert.Equal(t, 0, in.count())

	for i := 0; i < maxInboundRequests; i++ {
		_, _, rpcErr := in.start(context.Background(), fmt.Sprintf("n:%d", i))
		require.Nil(t, rpcErr)
	}
	_, _, rpcErr = in.start(context.Background(), "n:over")
	require.NotNil(t, rpcErr, "over the cap")
	assert.Contains(t, rpcErr.Message, "too many")

	ctx, _, rpcErr = in.start(context.Background(), "s:last")
	require.NotNil(t, rpcErr)
	require.Nil(t, ctx)
	in.cancelAll()
	assert.Equal(t, 0, in.count())
	_, _, rpcErr = in.start(context.Background(), "s:after")
	require.NotNil(t, rpcErr, "a closed connection refuses new requests")
}

func TestUpstreamClientCapabilities(t *testing.T) {
	caps := UpstreamClientCapabilities(&migrate.ServerConfig{}, true)
	require.NotNil(t, caps.Roots)
	assert.True(t, caps.Roots.ListChanged)
	require.NotNil(t, caps.Elicitation)
	assert.NotNil(t, caps.Elicitation.URL)
	assert.Nil(t, caps.Sampling, "sampling is opt-in")

	caps = UpstreamClientCapabilities(&migrate.ServerConfig{AllowSampling: true, Roots: []migrate.RootConfig{{URI: "file:///x"}}}, true)
	assert.NotNil(t, caps.Sampling)
	assert.False(t, caps.Roots.ListChanged, "static roots never change")

	caps = UpstreamClientCapabilities(&migrate.ServerConfig{AllowSampling: true}, transportAcceptsRequests(migrate.TransportSSE))
	raw, err := json.Marshal(caps)
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(raw), "nothing is declared over a transport that cannot receive requests")
	assert.True(t, transportAcceptsRequests(migrate.TransportHTTP))
}

func TestStdioConn_RelaysServerRequests(t *testing.T) {
	stdin := &lockedBuffer{}
	c := newStdioConn("unit", stdin, slog.New(slog.NewTextHandler(io.Discard, nil)))
	got := make(chan string, 4)
	c.onRequest = func(_ context.Context, method string, params json.RawMessage) (json.RawMessage, *errs.JSONRPCError) {
		got <- method + " " + string(params)
		if method == "sampling/createMessage" {
			return nil, &errs.JSONRPCError{Code: -1, Message: "user rejected"}
		}
		return json.RawMessage(`{"roots":[{"uri":"file:///x"}]}`), nil
	}

	c.handleLine([]byte(`{"jsonrpc":"2.0","id":"srv-1","method":"roots/list"}`))
	c.handleLine([]byte(`{"jsonrpc":"2.0","id":7,"method":"sampling/createMessage","params":{"maxTokens":1}}`))
	c.handleLine([]byte(`{"jsonrpc":"2.0","id":{"bad":1},"method":"roots/list"}`))
	require.Eventually(t, func() bool {
		out := stdin.String()
		return strings.Contains(out, `"id":"srv-1"`) && strings.Contains(out, `"id":7`)
	}, 2*time.Second, time.Millisecond)
	lines := strings.Split(strings.TrimSpace(stdin.String()), "\n")
	require.Len(t, lines, 2)
	for _, line := range lines {
		if strings.Contains(line, `"srv-1"`) {
			assert.JSONEq(t, `{"jsonrpc":"2.0","id":"srv-1","result":{"roots":[{"uri":"file:///x"}]}}`, line)
		} else {
			assert.JSONEq(t, `{"jsonrpc":"2.0","id":7,"error":{"code":-1,"message":"user rejected"}}`, line)
		}
	}
	assert.Len(t, got, 2, "an invalid id is ignored")
	require.Eventually(t, func() bool { return c.inbound.count() == 0 }, time.Second, time.Millisecond)
}

func TestStdioConn_ServerCancelsItsRequest(t *testing.T) {
	stdin := &lockedBuffer{}
	c := newStdioConn("unit", stdin, slog.New(slog.NewTextHandler(io.Discard, nil)))
	started := make(chan struct{})
	ended := make(chan error, 1)
	c.onRequest = func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, *errs.JSONRPCError) {
		close(started)
		<-ctx.Done()
		ended <- ctx.Err()
		return json.RawMessage(`{}`), nil
	}
	var notes []string
	c.onNotification = func(method string, _ json.RawMessage) { notes = append(notes, method) }

	c.handleLine([]byte(`{"jsonrpc":"2.0","id":"e-1","method":"elicitation/create","params":{}}`))
	<-started
	c.handleLine([]byte(`{"jsonrpc":"2.0","method":"` + methodCancelledNotification + `","params":{"requestId":"e-1"}}`))
	select {
	case err := <-ended:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("the relayed request was not canceled")
	}
	require.Eventually(t, func() bool { return c.inbound.count() == 0 }, time.Second, time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, stdin.String(), "a request the server canceled is not answered")
	assert.Empty(t, notes, "the cancel of a relayed request is consumed")

	// A cancel for anything else is an ordinary notification.
	c.handleLine([]byte(`{"jsonrpc":"2.0","method":"` + methodCancelledNotification + `","params":{"requestId":"other"}}`))
	assert.Equal(t, []string{methodCancelledNotification}, notes)
}

func TestStdioConn_FailCancelsRelayedRequests(t *testing.T) {
	c := newStdioConn("unit", &lockedBuffer{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ended := make(chan struct{})
	c.onRequest = func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, *errs.JSONRPCError) {
		<-ctx.Done()
		close(ended)
		return nil, nil
	}
	c.handleLine([]byte(`{"jsonrpc":"2.0","id":1,"method":"roots/list"}`))
	require.Eventually(t, func() bool { return c.inbound.count() == 1 }, time.Second, time.Millisecond)
	c.fail(fmt.Errorf("gone"))
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("fail did not cancel the relayed request")
	}
}

func TestStdioConn_PingWithoutHandler(t *testing.T) {
	stdin := &lockedBuffer{}
	c := newStdioConn("unit", stdin, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.handleLine([]byte(`{"jsonrpc":"2.0","id":3,"method":"ping"}`))
	require.Eventually(t, func() bool { return strings.Contains(stdin.String(), `"id":3`) }, time.Second, time.Millisecond)
	assert.JSONEq(t, `{"jsonrpc":"2.0","id":3,"result":{}}`, strings.TrimSpace(stdin.String()))
}

func TestMessageHub(t *testing.T) {
	var hub messageHub
	res, rpcErr := hub.request(context.Background(), "s", MethodPing, nil)
	require.Nil(t, rpcErr)
	assert.JSONEq(t, `{}`, string(res))
	_, rpcErr = hub.request(context.Background(), "s", MethodRootsList, nil)
	require.NotNil(t, rpcErr)
	assert.Equal(t, errs.ErrCodeMethodNotFound, rpcErr.Code)
	hub.notify(context.Background(), "s", MethodProgressNotification, nil) // no handler: dropped

	h := &recordingHandler{}
	hub.set(h)
	_, rpcErr = hub.request(context.Background(), "s", MethodRootsList, json.RawMessage(`{}`))
	require.Nil(t, rpcErr)
	hub.notify(context.Background(), "s", MethodProgressNotification, json.RawMessage(`{"progress":1}`))
	reqs, notes := h.snapshot()
	assert.Equal(t, []string{"s roots/list {}"}, reqs)
	assert.Equal(t, []string{`s notifications/progress {"progress":1}`}, notes)
	hub.set(nil)
	var nilHub *messageHub
	assert.Nil(t, nilHub.handler())
}

// TestStdioPool_RelaysServerRequestsToTheHandler: a real server's
// roots/list reaches the registered handler and its answer the server.
func TestStdioPool_RelaysServerRequestsToTheHandler(t *testing.T) {
	p := startConcurrentMCP(t, "relay-s2c", 0)
	h := &recordingHandler{answer: func(context.Context, string, json.RawMessage) (json.RawMessage, *errs.JSONRPCError) {
		return json.RawMessage(`{"roots":[{"uri":"file:///from-client"}]}`), nil
	}}
	p.SetServerMessageHandler(h)
	resp, err := callTool(context.Background(), p, "relay-s2c", "ask_client", nil, 5*time.Second)
	require.NoError(t, err)
	require.Nil(t, resp.Error)
	assert.Contains(t, string(resp.Result), "file:///from-client")
	reqs, _ := h.snapshot()
	require.Len(t, reqs, 1)
	assert.True(t, strings.HasPrefix(reqs[0], "relay-s2c roots/list"), reqs[0])
}

// TestHTTPPool_RelaysServerRequestsAndProgress drives a real mcp-go
// Streamable HTTP server whose tool asks the client for input (elicitation)
// and reports progress mid-call. mcp-go sends both on the GET stream, which
// the pool opens: the request reaches the handler (without the values of
// the call that happened to connect), its answer reaches the server, and
// the progress notification reaches the handler.
func TestHTTPPool_RelaysServerRequestsAndProgress(t *testing.T) {
	srv := server.NewMCPServer("up", "1", server.WithElicitation())
	srv.AddTool(mcp.NewTool("ask"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if req.Params.Meta != nil && req.Params.Meta.ProgressToken != nil {
			_ = srv.SendNotificationToClient(ctx, MethodProgressNotification, map[string]any{"progressToken": req.Params.Meta.ProgressToken, "progress": 1})
		}
		res, err := srv.RequestElicitation(ctx, mcp.ElicitationRequest{Params: mcp.ElicitationParams{
			Message:         "name?",
			RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}},
		}})
		if err != nil {
			return mcp.NewToolResultError("elicitation failed: " + err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("action=%s content=%v", res.Action, res.Content)), nil
	})
	ts := httptest.NewServer(server.NewStreamableHTTPServer(srv))
	t.Cleanup(ts.Close)

	p := NewHTTPClientPool(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = p.Close() })
	type ctxKey struct{}
	h := &recordingHandler{answer: func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, *errs.JSONRPCError) {
		// mcp-go servers send their requests on the GET stream, which
		// must never carry the values of the call that connected first.
		if ctx.Value(ctxKey{}) != nil {
			return nil, &errs.JSONRPCError{Code: -32603, Message: "leaked caller context"}
		}
		return json.RawMessage(`{"action":"accept","content":{"name":"Ada"}}`), nil
	}}
	p.SetServerMessageHandler(h)
	require.NoError(t, p.StartServer(context.Background(), &migrate.ServerConfig{Name: "remote", Transport: migrate.TransportHTTP, HTTP: &migrate.HTTPConfig{URL: ts.URL}}))

	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), ctxKey{}, "caller"), 15*time.Second)
	defer cancel()
	resp, err := p.SendRequestToServer(ctx, "remote", "tools/call", json.RawMessage(`{"name":"ask","arguments":{},"_meta":{"progressToken":"lp-progress-9"}}`), 10*time.Second)
	require.NoError(t, err)
	require.Nil(t, resp.Error)
	assert.Contains(t, string(resp.Result), "action=accept")
	assert.Contains(t, string(resp.Result), "Ada")

	reqs, notes := h.snapshot()
	require.Len(t, reqs, 1)
	assert.Contains(t, reqs[0], "remote elicitation/create")
	require.Eventually(t, func() bool {
		_, notes = h.snapshot()
		for _, n := range notes {
			if strings.Contains(n, `"progressToken":"lp-progress-9"`) {
				return true
			}
		}
		return false
	}, 2*time.Second, 10*time.Millisecond, "progress notifications: %v", notes)

	init, ok := p.ServerInitializeResult("remote")
	require.True(t, ok)
	assert.NotEmpty(t, init.ProtocolVersion)
}

// cancelRecorder is a Streamable HTTP upstream whose tools/call never
// answers; it records the cancel notifications it receives.
type cancelRecorder struct {
	mu      sync.Mutex
	cancels []string
}

func (c *cancelRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(body, &msg)
	switch msg.Method {
	case "initialize":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+string(msg.ID)+`,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"c","version":"1"}}}`)
	case "tools/call":
		<-r.Context().Done()
	case methodCancelledNotification:
		c.mu.Lock()
		c.cancels = append(c.cancels, string(msg.Params))
		c.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

func TestHTTPPool_CanceledCallNotifiesUpstream(t *testing.T) {
	rec := &cancelRecorder{}
	ts := httptest.NewServer(rec)
	t.Cleanup(ts.Close)
	p := NewHTTPClientPool(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = p.Close() })
	require.NoError(t, p.StartServer(context.Background(), &migrate.ServerConfig{Name: "remote", Transport: migrate.TransportHTTP, HTTP: &migrate.HTTPConfig{URL: ts.URL}}))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	_, err := p.SendRequestToServer(ctx, "remote", "tools/call", json.RawMessage(`{"name":"slow"}`), 10*time.Second)
	require.Error(t, err)
	require.Eventually(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return len(rec.cancels) == 1 && strings.Contains(rec.cancels[0], `"requestId":"lp-`) && strings.Contains(rec.cancels[0], `"reason":"canceled"`)
	}, 5*time.Second, 10*time.Millisecond)
}
