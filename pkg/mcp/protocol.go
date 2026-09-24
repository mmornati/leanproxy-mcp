package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
)

// MCP protocol revisions LeanProxy speaks (issue #307).
const (
	ProtocolVersion20241105 = "2024-11-05"
	ProtocolVersion20250326 = "2025-03-26"
	ProtocolVersion20250618 = "2025-06-18"
	ProtocolVersion20251125 = "2025-11-25"

	// LatestProtocolVersion is the newest revision LeanProxy supports. It
	// is what a client asking for an unknown revision is offered, and what
	// the pool requests from upstream servers (pool.RequestedProtocolVersion
	// must stay equal to it; a test enforces that).
	LatestProtocolVersion = ProtocolVersion20251125
)

// SupportedProtocolVersions lists every MCP revision the front ends
// negotiate, newest first. It is the single source of truth for version
// negotiation.
var SupportedProtocolVersions = []string{
	ProtocolVersion20251125,
	ProtocolVersion20250618,
	ProtocolVersion20250326,
	ProtocolVersion20241105,
}

// IsSupportedProtocolVersion reports whether v is one of
// SupportedProtocolVersions.
func IsSupportedProtocolVersion(v string) bool {
	for _, s := range SupportedProtocolVersions {
		if s == v {
			return true
		}
	}
	return false
}

// NegotiateProtocolVersion implements the MCP version negotiation rule: the
// server answers with the version the client requested when it supports it,
// and otherwise with the latest version it supports (the client then
// decides whether to continue).
func NegotiateProtocolVersion(requested string) string {
	if IsSupportedProtocolVersion(requested) {
		return requested
	}
	return LatestProtocolVersion
}

// ProtocolAtLeast reports whether revision v is min or newer. Revisions are
// ISO dates, so they compare as strings. An empty v (a client that never
// initialized) counts as the oldest revision.
func ProtocolAtLeast(v, minVersion string) bool {
	if v == "" {
		v = ProtocolVersion20241105
	}
	return v >= minVersion
}

// Server-to-client notifications LeanProxy emits.
const (
	NotificationResourcesListChanged = "notifications/resources/list_changed"
	NotificationPromptsListChanged   = "notifications/prompts/list_changed"
	NotificationResourcesUpdated     = "notifications/resources/updated"
	NotificationProgress             = "notifications/progress"
	NotificationCancelled            = "notifications/cancelled" //nolint:misspell // MCP protocol method name
	NotificationRootsListChanged     = "notifications/roots/list_changed"
	NotificationElicitationComplete  = "notifications/elicitation/complete"
)

// NotifyFunc writes one JSON-RPC notification to a client. Front ends
// implement it on top of their (serialized) writer.
type NotifyFunc func(method string, params json.RawMessage)

// ClientSession is the MCP session state of one connected client: the
// protocol revision negotiated by its initialize request, what it declared
// about itself, and how to send it notifications. `server run --stdio` has
// one session for its lifetime; `serve` opens one per TCP connection.
type ClientSession struct {
	notify NotifyFunc

	mu              sync.RWMutex
	initialized     bool
	protocolVersion string
	clientInfo      ClientInfo
	capabilities    json.RawMessage

	// Server-to-client requests (issue #308, see session_requests.go).
	reqMu       sync.Mutex
	sendRequest ContextRequestFunc
	pending     map[string]chan clientReply
	reqClosed   bool
	nextReqID   atomic.Uint64

	// Relayed upstream notifications are written by a per-session
	// goroutine (see sendQueued), so a client that stops reading never
	// blocks an upstream's reader.
	queueOnce sync.Once
	queue     chan queuedNotification
	queueDone chan struct{}
	closeOnce sync.Once

	// approvals are the "server.tool" calls the user approved for the
	// rest of this session at a policy confirmation (#314).
	approvalsMu sync.Mutex
	approvals   map[string]struct{}
}

// policyApproved reports whether the user approved key ("server.tool")
// for this session.
func (s *ClientSession) policyApproved(key string) bool {
	if s == nil {
		return false
	}
	s.approvalsMu.Lock()
	defer s.approvalsMu.Unlock()
	_, ok := s.approvals[key]
	return ok
}

// approvePolicy remembers that the user approved key for this session.
func (s *ClientSession) approvePolicy(key string) {
	s.approvalsMu.Lock()
	defer s.approvalsMu.Unlock()
	if s.approvals == nil {
		s.approvals = make(map[string]struct{})
	}
	s.approvals[key] = struct{}{}
}

// ProtocolVersion returns the negotiated revision, or "" before initialize.
func (s *ClientSession) ProtocolVersion() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.protocolVersion
}

// Initialized reports whether the client completed initialize.
func (s *ClientSession) Initialized() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.initialized
}

// ClientInfo returns what the client said about itself in initialize.
func (s *ClientSession) ClientInfo() ClientInfo {
	if s == nil {
		return ClientInfo{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clientInfo
}

// Capabilities returns the client capabilities declared in initialize, as
// sent.
func (s *ClientSession) Capabilities() json.RawMessage {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.capabilities
}

// AtLeast reports whether the session negotiated revision minVersion or newer.
func (s *ClientSession) AtLeast(minVersion string) bool {
	return ProtocolAtLeast(s.ProtocolVersion(), minVersion)
}

func (s *ClientSession) setInitialized(version string, params InitializeParams, caps json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initialized = true
	s.protocolVersion = version
	s.clientInfo = params.ClientInfo
	s.capabilities = append(json.RawMessage(nil), caps...)
}

// send delivers a notification when the session has a writer and completed
// initialize (a client must not get notifications before that).
func (s *ClientSession) send(method string, params json.RawMessage) {
	if s == nil || s.notify == nil || !s.Initialized() {
		return
	}
	s.notify(method, params)
}

type clientSessionKey struct{}

// WithClientSession returns a context carrying s: requests handled with it
// read and update that session.
func WithClientSession(ctx context.Context, s *ClientSession) context.Context {
	return context.WithValue(ctx, clientSessionKey{}, s)
}

// ClientSessionFrom returns the session carried by ctx, or nil.
func ClientSessionFrom(ctx context.Context) *ClientSession {
	s, _ := ctx.Value(clientSessionKey{}).(*ClientSession)
	return s
}

type responseRouteKey struct{}

// WithResponseRoute returns a context carrying route, an opaque value a
// front end attaches to a client request so that the server-to-client
// messages it causes go back where that request is answered (the
// Streamable HTTP front end, #309, uses the request's response stream).
// The relay keeps the route of every upstream call, so a request an
// upstream sends during a call carries the route of that call.
func WithResponseRoute(ctx context.Context, route any) context.Context {
	return context.WithValue(ctx, responseRouteKey{}, route)
}

// ResponseRouteFrom returns the route carried by ctx, or nil.
func ResponseRouteFrom(ctx context.Context) any {
	return ctx.Value(responseRouteKey{})
}

// OpenSession registers a new client session whose notifications are
// written with notify, and returns it with the function that unregisters
// it (call it when the client disconnects). Requests must carry the
// session in their context (WithClientSession).
func (h *Handler) OpenSession(notify NotifyFunc) (*ClientSession, func()) {
	s := &ClientSession{notify: notify}
	h.sessionsMu.Lock()
	if h.sessions == nil {
		h.sessions = make(map[*ClientSession]struct{})
	}
	h.sessions[s] = struct{}{}
	h.sessionsMu.Unlock()
	var once sync.Once
	return s, func() {
		once.Do(func() {
			h.sessionsMu.Lock()
			delete(h.sessions, s)
			h.sessionsMu.Unlock()
			s.closeRequests()
			h.dropRelaySession(s)
		})
	}
}

// sessionFor returns the session of the request context, or the handler's
// default session for callers that do not open one (tests, embedders with a
// single client).
func (h *Handler) sessionFor(ctx context.Context) *ClientSession {
	if s := ClientSessionFrom(ctx); s != nil {
		return s
	}
	return h.defaultSession
}

// notifySessions sends a notification to every open, initialized session.
func (h *Handler) notifySessions(method string) {
	h.sessionsMu.Lock()
	targets := make([]*ClientSession, 0, len(h.sessions))
	for s := range h.sessions {
		targets = append(targets, s)
	}
	h.sessionsMu.Unlock()
	for _, s := range targets {
		s.send(method, nil)
	}
}
