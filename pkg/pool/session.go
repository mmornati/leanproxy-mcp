package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/mark3labs/mcp-go/mcp"
)

// MCP methods owned by the pool's handshake (issue #297). Callers never send
// them: the pool performs `initialize` + `notifications/initialized` exactly
// once per server process generation (stdio) or connection (HTTP/SSE).
const (
	methodInitialize              = "initialize"
	methodInitializedNotification = "notifications/initialized"
	// MethodToolsListChanged is the notification an upstream sends when its
	// tool list changed; the pool surfaces it as EventToolsListChanged.
	MethodToolsListChanged = "notifications/tools/list_changed"
	// MethodResourcesListChanged and MethodPromptsListChanged are surfaced
	// as EventResourcesListChanged and EventPromptsListChanged.
	MethodResourcesListChanged = "notifications/resources/list_changed"
	MethodPromptsListChanged   = "notifications/prompts/list_changed"
)

// RequestedProtocolVersion is the MCP revision the pool asks every upstream
// for in its initialize handshake: the latest one LeanProxy speaks (it must
// equal mcp.LatestProtocolVersion; pkg/mcp tests enforce it). The server
// may answer with an older revision; whatever it returns is accepted and
// stored in its InitializeResult with its capabilities.
const RequestedProtocolVersion = "2025-11-25"

// ServerInfo is the upstream server's self-description from its
// InitializeResult.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is what an upstream server answered to the MCP
// initialize handshake of its current session. It is stored per server so
// list_servers can show serverInfo/instructions and so a later protocol
// upgrade can inspect the negotiated version and capabilities.
type InitializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities,omitempty"`
	ServerInfo      ServerInfo      `json:"serverInfo"`
	Instructions    string          `json:"instructions,omitempty"`
	// Generation is the process generation (stdio) or connection number
	// (HTTP/SSE) this result belongs to.
	Generation uint64 `json:"-"`
	// Raw is the result exactly as the server sent it.
	Raw json.RawMessage `json:"-"`
}

// parseInitializeResult decodes an initialize result received on the wire.
func parseInitializeResult(raw json.RawMessage, generation uint64) (*InitializeResult, error) {
	var res InitializeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("invalid initialize result: %w", err)
	}
	res.Generation = generation
	res.Raw = append(json.RawMessage(nil), raw...)
	return &res, nil
}

// fromMCPInitializeResult converts mcp-go's InitializeResult (HTTP/SSE).
func fromMCPInitializeResult(r *mcp.InitializeResult, generation uint64) *InitializeResult {
	if r == nil {
		return nil
	}
	raw, err := json.Marshal(r)
	if err != nil {
		raw = nil
	}
	caps, err := json.Marshal(r.Capabilities)
	if err != nil {
		caps = nil
	}
	return &InitializeResult{
		ProtocolVersion: r.ProtocolVersion,
		Capabilities:    caps,
		ServerInfo:      ServerInfo{Name: r.ServerInfo.Name, Version: r.ServerInfo.Version},
		Instructions:    r.Instructions,
		Generation:      generation,
		Raw:             raw,
	}
}

// initializeResultJSON is the JSON-RPC result returned to a caller that
// asks a pool for `initialize` explicitly: the stored result of the current
// session instead of a second handshake.
func initializeResultJSON(res *InitializeResult) json.RawMessage {
	if res == nil {
		return json.RawMessage(`{}`)
	}
	if len(res.Raw) > 0 {
		return res.Raw
	}
	b, err := json.Marshal(res)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// ServerEventKind identifies a server lifecycle event.
type ServerEventKind int

const (
	// EventSessionStarted fires when a server gets a new session: a stdio
	// process generation was spawned (initial start or restart) or an
	// HTTP/SSE client (re)connected. Its tool list may have changed.
	EventSessionStarted ServerEventKind = iota + 1
	// EventToolsListChanged fires when an upstream sent
	// notifications/tools/list_changed.
	EventToolsListChanged
	// EventResourcesListChanged fires when an upstream sent
	// notifications/resources/list_changed.
	EventResourcesListChanged
	// EventPromptsListChanged fires when an upstream sent
	// notifications/prompts/list_changed.
	EventPromptsListChanged
)

func (k ServerEventKind) String() string {
	switch k {
	case EventSessionStarted:
		return "session_started"
	case EventToolsListChanged:
		return "tools_list_changed"
	case EventResourcesListChanged:
		return "resources_list_changed"
	case EventPromptsListChanged:
		return "prompts_list_changed"
	default:
		return "unknown"
	}
}

// ServerEvent is a lifecycle event of one upstream server.
type ServerEvent struct {
	Server     string
	Kind       ServerEventKind
	Generation uint64
}

// ServerEventHandler receives server events. It is called on its own
// goroutine and may block.
type ServerEventHandler func(ServerEvent)

// SessionInfoProvider is implemented by pools that store each server's
// InitializeResult.
type SessionInfoProvider interface {
	ServerInitializeResult(name string) (*InitializeResult, bool)
}

// ServerEventSource is implemented by pools that report server lifecycle
// events (new session, tools/list_changed).
type ServerEventSource interface {
	SetServerEventHandler(fn ServerEventHandler)
}

// eventHub holds the (single) event handler of a pool.
type eventHub struct {
	fn atomic.Pointer[ServerEventHandler]
}

func (h *eventHub) set(fn ServerEventHandler) {
	if fn == nil {
		h.fn.Store(nil)
		return
	}
	h.fn.Store(&fn)
}

// emit delivers ev asynchronously so no pool goroutine (the stdout reader,
// spawn) ever blocks on a handler.
func (h *eventHub) emit(ev ServerEvent) {
	if h == nil {
		return
	}
	p := h.fn.Load()
	if p == nil {
		return
	}
	go (*p)(ev)
}

// handshakeAttempt is the initialize handshake of a generation, shared by
// every caller that needs its session.
type handshakeAttempt struct {
	done chan struct{}
	err  error
}

// handshakeState is the MCP session state of one stdio process generation.
// It lives on the generation's stdioConn, so a respawn always starts
// un-initialized.
type handshakeState struct {
	mu      sync.Mutex
	done    bool
	attempt *handshakeAttempt
	result  *InitializeResult
}

// initialized reports whether the generation completed its handshake.
func (h *handshakeState) initialized() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.done
}

// markInitialized marks the generation initialized without a handshake
// (used when the session was set up out of band, e.g. by tests).
func (h *handshakeState) markInitialized() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.done = true
}

// ensureHandshake makes sure this generation's MCP session is initialized
// before any other request is written on it: the first caller starts the
// handshake in the background and every caller waits for that same
// handshake (bounded by its own ctx; a caller giving up never aborts it).
//
// Exactly one initialize is ever sent per generation. The handshake is not
// bounded by a timeout, only by the generation itself: a server that answers
// initialize late still gets its session, and one that never answers keeps
// failing its callers at their own timeouts until the generation ends (a
// crash, a restart, or the health checker replacing the wedged process). A
// handshake that failed (initialize answered with an error, or the process
// died) fails every later caller of that generation at once.
func (s *StdioServerV2) ensureHandshake(ctx context.Context, conn *stdioConn, generation uint64) error {
	hs := &conn.handshake
	hs.mu.Lock()
	if hs.done {
		hs.mu.Unlock()
		return nil
	}
	a := hs.attempt
	if a == nil {
		a = &handshakeAttempt{done: make(chan struct{})}
		hs.attempt = a
		// Detached from the caller: a caller giving up must not abort the
		// handshake every other caller of this generation shares.
		go s.runHandshake(context.WithoutCancel(ctx), conn, a, generation)
	}
	hs.mu.Unlock()

	select {
	case <-a.done:
		return a.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runHandshake performs initialize + notifications/initialized on conn. It
// ends when the server answers or when the generation ends (conn.fail fails
// the pending initialize and the stdin write when the process goes away).
func (s *StdioServerV2) runHandshake(ctx context.Context, conn *stdioConn, a *handshakeAttempt, generation uint64) {
	res, err := s.handshake(ctx, conn, generation)

	hs := &conn.handshake
	hs.mu.Lock()
	if err == nil {
		hs.done = true
		hs.result = res
	}
	hs.mu.Unlock()

	if err != nil {
		a.err = fmt.Errorf("pool: MCP initialize handshake with %s failed: %w", s.name, err)
		s.logger.Warn("MCP initialize handshake failed", "name", s.name, "generation", generation, "error", err)
	} else {
		s.initResult.Store(res)
		s.logger.Info("MCP session initialized",
			"name", s.name,
			"generation", generation,
			"protocol_version", res.ProtocolVersion,
			"server_name", res.ServerInfo.Name,
			"server_version", res.ServerInfo.Version)
	}
	close(a.done)
}

func (s *StdioServerV2) handshake(ctx context.Context, conn *stdioConn, generation uint64) (*InitializeResult, error) {
	params, err := json.Marshal(mcpInitializeRequest().Params)
	if err != nil {
		return nil, err
	}
	s.logger.Info("sending MCP initialize", "name", s.name, "generation", generation)
	raw, err := s.roundTrip(ctx, conn, methodInitialize, params)
	if err != nil {
		return nil, err
	}
	res, err := parseInitializeResult(raw, generation)
	if err != nil {
		return nil, err
	}
	if err := conn.writeNotification(ctx, methodInitializedNotification); err != nil {
		return nil, fmt.Errorf("send %s: %w", methodInitializedNotification, err)
	}
	return res, nil
}

// Every pool exposes its servers' sessions and lifecycle events.
var (
	_ SessionInfoProvider = (*StdioPool)(nil)
	_ SessionInfoProvider = (*HTTPClientPool)(nil)
	_ SessionInfoProvider = (*SSEPool)(nil)
	_ SessionInfoProvider = (*UnifiedPool)(nil)
	_ ServerEventSource   = (*StdioPool)(nil)
	_ ServerEventSource   = (*HTTPClientPool)(nil)
	_ ServerEventSource   = (*SSEPool)(nil)
	_ ServerEventSource   = (*UnifiedPool)(nil)
)
