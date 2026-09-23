package pool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/mark3labs/mcp-go/mcp"

	errs "github.com/mmornati/leanproxy-mcp/pkg/errors"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

// Server-initiated messages (issue #308). MCP is bidirectional: an upstream
// server may send requests to its client (the proxy) — sampling,
// elicitation, roots, ping — and notifications (progress, cancellation,
// resource updates, ...). The pool answers ping itself and hands everything
// else to the ServerMessageHandler registered by the MCP handler, which
// relays it to the right client session.
//
// The pool keeps the upstream's own request id: it only ever travels
// between the pool and that upstream. The handler relays the request to the
// client under an id of its own (see pkg/mcp ClientSession), so ids from
// different upstreams and clients never collide.

// MCP methods an upstream server may send to the proxy.
const (
	MethodPing                  = "ping"
	MethodSamplingCreateMessage = "sampling/createMessage"
	MethodElicitationCreate     = "elicitation/create"
	MethodRootsList             = "roots/list"

	MethodProgressNotification            = "notifications/progress"
	MethodCancelledNotification           = methodCancelledNotification
	MethodResourcesUpdatedNotification    = "notifications/resources/updated"
	MethodElicitationCompleteNotification = "notifications/elicitation/complete"
	MethodRootsListChangedNotification    = "notifications/roots/list_changed"
)

// maxInboundRequests caps the server-to-client requests one upstream
// connection may have outstanding at once. A server over the cap gets an
// error for the extra requests instead of making the proxy hold an
// unbounded number of goroutines.
const maxInboundRequests = 32

// ServerMessageHandler receives what an upstream initiates and the pool
// does not handle itself.
type ServerMessageHandler interface {
	// HandleServerRequest answers a server-to-client request from the
	// named upstream. It runs on its own goroutine. ctx ends when the
	// upstream cancels the request (cancel notification) or its
	// connection ends. When an HTTP upstream sends the request on the
	// response stream of a call, ctx also carries that call's values; a
	// request on the GET stream (or from a stdio upstream) carries none.
	// The returned error, when non-nil, is sent to the upstream as the
	// JSON-RPC error of the answer.
	HandleServerRequest(ctx context.Context, server, method string, params json.RawMessage) (json.RawMessage, *errs.JSONRPCError)
	// HandleServerNotification receives an upstream notification the pool
	// does not consume itself (progress, resource updates, ...). It is
	// called inline by the connection's reader, in arrival order, so it
	// must not block for long.
	HandleServerNotification(ctx context.Context, server, method string, params json.RawMessage)
}

// ServerMessageSource is implemented by pools that relay server-initiated
// messages.
type ServerMessageSource interface {
	SetServerMessageHandler(h ServerMessageHandler)
}

// Every pool relays server-initiated messages.
var (
	_ ServerMessageSource = (*StdioPool)(nil)
	_ ServerMessageSource = (*HTTPClientPool)(nil)
	_ ServerMessageSource = (*SSEPool)(nil)
	_ ServerMessageSource = (*UnifiedPool)(nil)
)

// messageHub holds the (single) message handler of a pool.
type messageHub struct {
	h atomic.Pointer[handlerBox]
}

type handlerBox struct{ h ServerMessageHandler }

func (m *messageHub) set(h ServerMessageHandler) {
	if h == nil {
		m.h.Store(nil)
		return
	}
	m.h.Store(&handlerBox{h: h})
}

func (m *messageHub) handler() ServerMessageHandler {
	if m == nil {
		return nil
	}
	if b := m.h.Load(); b != nil {
		return b.h
	}
	return nil
}

// request answers a server-to-client request: ping locally, everything else
// through the registered handler, or "method not found" when there is none.
func (m *messageHub) request(ctx context.Context, server, method string, params json.RawMessage) (json.RawMessage, *errs.JSONRPCError) {
	if method == MethodPing {
		return json.RawMessage(`{}`), nil
	}
	h := m.handler()
	if h == nil {
		return nil, methodNotSupported(method)
	}
	return h.HandleServerRequest(ctx, server, method, params)
}

// notify hands an upstream notification to the registered handler.
func (m *messageHub) notify(ctx context.Context, server, method string, params json.RawMessage) {
	if h := m.handler(); h != nil {
		h.HandleServerNotification(ctx, server, method, params)
	}
}

// methodNotSupported is the error an upstream gets for a request the proxy
// cannot relay.
func methodNotSupported(method string) *errs.JSONRPCError {
	return &errs.JSONRPCError{
		Code:    errs.ErrCodeMethodNotFound,
		Message: fmt.Sprintf("Method not found: %s (not supported by leanproxy)", method),
	}
}

// inboundRequests tracks the server-to-client requests of one upstream
// connection that are being answered, so an upstream
// cancel notification can cancel the right one and a dead connection
// cancels them all.
type inboundRequests struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	closed  bool
}

// start registers the request with id key and returns its context and the
// func that unregisters it. It returns an error (sent to the upstream as is)
// when the connection is closed, the id is already in use or the connection
// is at maxInboundRequests.
func (t *inboundRequests) start(parent context.Context, key string) (ctx context.Context, done func(), err *errs.JSONRPCError) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, nil, &errs.JSONRPCError{Code: errs.ErrCodeInternalError, Message: "connection closed"}
	}
	if t.cancels == nil {
		t.cancels = make(map[string]context.CancelFunc)
	}
	if _, dup := t.cancels[key]; dup {
		return nil, nil, &errs.JSONRPCError{Code: errs.ErrCodeInvalidRequest, Message: "duplicate request id"}
	}
	if len(t.cancels) >= maxInboundRequests {
		return nil, nil, &errs.JSONRPCError{Code: errs.ErrCodeInternalError, Message: fmt.Sprintf("too many concurrent server-to-client requests (max %d)", maxInboundRequests)}
	}
	ctx, cancel := context.WithCancel(parent)
	t.cancels[key] = cancel
	return ctx, func() {
		t.mu.Lock()
		delete(t.cancels, key)
		t.mu.Unlock()
		cancel()
	}, nil
}

// cancel cancels the request with id key. It reports whether it was known.
func (t *inboundRequests) cancel(key string) bool {
	t.mu.Lock()
	cancel, ok := t.cancels[key]
	t.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// cancelAll cancels every request and refuses new ones.
func (t *inboundRequests) cancelAll() {
	t.mu.Lock()
	t.closed = true
	cancels := t.cancels
	t.cancels = nil
	t.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// count returns the number of requests being answered.
func (t *inboundRequests) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.cancels)
}

// RequestIDKey normalizes a raw JSON-RPC id into a map key: strings and
// numbers get distinct prefixes (1 and "1" are different ids). ok is false
// for a missing, null or non-scalar id.
func RequestIDKey(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return "", false
	}
	switch id := v.(type) {
	case string:
		return "s:" + id, true
	case json.Number:
		return "n:" + id.String(), true
	default:
		return "", false
	}
}

// cancelledRequestKey returns the key of params.requestId of a
// cancel notification.
func cancelledRequestKey(params json.RawMessage) (string, bool) {
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if len(params) == 0 || json.Unmarshal(params, &p) != nil {
		return "", false
	}
	return RequestIDKey(p.RequestID)
}

// UpstreamClientCapabilities returns the client capabilities the pool
// declares to an upstream in its initialize handshake (issue #308):
//
//   - roots: always (answered from servers[].roots when configured, else
//     relayed to the client; listChanged only in the relayed case, where
//     the client's notifications/roots/list_changed is forwarded);
//   - elicitation (form and URL mode): relayed to the client;
//   - sampling: only with servers[].allow_sampling: true.
//
// acceptsRequests is false for a transport that cannot receive
// server-to-client requests (legacy SSE): nothing is declared then, so the
// upstream never sends a request that could not be answered.
func UpstreamClientCapabilities(cfg *migrate.ServerConfig, acceptsRequests bool) mcp.ClientCapabilities {
	var caps mcp.ClientCapabilities
	if !acceptsRequests {
		return caps
	}
	caps.Roots = &struct {
		ListChanged bool `json:"listChanged,omitempty"`
	}{ListChanged: cfg == nil || len(cfg.Roots) == 0}
	caps.Elicitation = &mcp.ElicitationCapability{Form: &struct{}{}, URL: &struct{}{}}
	if cfg != nil && cfg.AllowSampling {
		caps.Sampling = &struct{}{}
	}
	return caps
}

// transportAcceptsRequests reports whether the pool can receive
// server-to-client requests over a server's transport.
func transportAcceptsRequests(t migrate.TransportType) bool {
	return t != migrate.TransportSSE
}
