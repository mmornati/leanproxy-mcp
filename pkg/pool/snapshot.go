package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
)

// ServerSnapshot is the runtime state of one upstream server, whatever its
// transport: what an operator needs to answer "is it up, and if not, why".
// It is a read-only copy; nothing in it is a secret (stderr lines are
// already redacted when they are captured).
type ServerSnapshot struct {
	Name      string      `json:"name"`
	Transport string      `json:"transport"`
	State     ServerState `json:"state"`

	// Initialized is true once the MCP handshake of the current process
	// (stdio) or connection (HTTP/SSE) completed.
	Initialized     bool     `json:"initialized"`
	ProtocolVersion string   `json:"protocol_version,omitempty"`
	ServerName      string   `json:"server_name,omitempty"`
	ServerVersion   string   `json:"server_version,omitempty"`
	Capabilities    []string `json:"capabilities,omitempty"`
	// Generation counts the process spawns (stdio) or connections
	// (HTTP/SSE) of this server since the proxy started.
	Generation uint64 `json:"generation,omitempty"`

	Requests      int64     `json:"requests"`
	Errors        int64     `json:"errors"`
	AvgLatencyMs  float64   `json:"avg_latency_ms"`
	InFlight      int       `json:"in_flight"`
	LastRequestAt time.Time `json:"last_request_at,omitzero"`

	// LastError is the latest failure: a crash or spawn error (stdio), a
	// failed connection (HTTP/SSE), or the latest failed call.
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitzero"`

	// Stdio only.
	PID          int       `json:"pid,omitempty"`
	StartedAt    time.Time `json:"started_at,omitzero"`
	RestartCount int       `json:"restart_count,omitempty"`
	RecentStderr string    `json:"recent_stderr,omitempty"`

	// ConnectedAt is when the current connection was established (HTTP/SSE).
	ConnectedAt time.Time `json:"connected_at,omitzero"`
}

// connStatus records the outcome of a remote server's connection attempts,
// so a server that cannot connect (an expired token, a DNS failure) shows
// why instead of a bare "error" state.
type connStatus struct {
	mu          sync.Mutex
	lastErr     string
	lastErrAt   time.Time
	connectedAt time.Time
	generation  uint64
}

// note records the result of one ensureConnected call. gen is the server's
// connection generation after the call.
func (c *connStatus) note(err error, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.lastErr = err.Error()
		c.lastErrAt = time.Now()
		return
	}
	if gen != c.generation {
		// A new connection: whatever failed before is history.
		c.generation = gen
		c.connectedAt = time.Now()
		c.lastErr = ""
		c.lastErrAt = time.Time{}
	}
}

func (c *connStatus) snapshot() (lastErr string, lastErrAt, connectedAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr, c.lastErrAt, c.connectedAt
}

// callStats are the request counters of one remote (HTTP/SSE) server. The
// stdio pool keeps its own, per process.
type callStats struct {
	requests      int64
	errors        int64
	inFlight      int
	totalLatency  time.Duration
	lastRequestAt time.Time
	lastErr       string
	lastErrAt     time.Time
}

// callRecorder counts the calls the unified pool sends to remote servers.
type callRecorder struct {
	mu    sync.Mutex
	stats map[string]*callStats
}

func (r *callRecorder) entry(name string) *callStats {
	if r.stats == nil {
		r.stats = make(map[string]*callStats)
	}
	st := r.stats[name]
	if st == nil {
		st = &callStats{}
		r.stats[name] = st
	}
	return st
}

// begin marks a call in flight and returns the function that ends it.
func (r *callRecorder) begin(name string) func(resp *Response, err error) {
	start := time.Now()
	r.mu.Lock()
	r.entry(name).inFlight++
	r.mu.Unlock()
	return func(resp *Response, err error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		st := r.entry(name)
		st.inFlight--
		st.requests++
		st.totalLatency += time.Since(start)
		st.lastRequestAt = time.Now()
		switch {
		case err != nil:
			st.errors++
			st.lastErr = err.Error()
			st.lastErrAt = st.lastRequestAt
		case resp != nil && resp.Error != nil:
			st.errors++
			st.lastErr = resp.Error.Message
			st.lastErrAt = st.lastRequestAt
		}
	}
}

func (r *callRecorder) get(name string) callStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st := r.stats[name]; st != nil {
		return *st
	}
	return callStats{}
}

func (r *callRecorder) forget(name string) {
	r.mu.Lock()
	delete(r.stats, name)
	r.mu.Unlock()
}

// capabilityNames lists the top-level keys of an initialize result's
// capabilities object ("tools", "resources", ...), sorted.
func capabilityNames(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var caps map[string]json.RawMessage
	if json.Unmarshal(raw, &caps) != nil {
		return nil
	}
	names := make([]string, 0, len(caps))
	for k := range caps {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func (s *ServerSnapshot) applyInit(res *InitializeResult, ok bool) {
	if !ok || res == nil {
		return
	}
	s.Initialized = true
	s.ProtocolVersion = res.ProtocolVersion
	s.ServerName = res.ServerInfo.Name
	s.ServerVersion = res.ServerInfo.Version
	s.Capabilities = capabilityNames(res.Capabilities)
	s.Generation = res.Generation
}

// later returns the more recent of two (message, time) pairs.
func later(msgA string, atA time.Time, msgB string, atB time.Time) (string, time.Time) {
	if atB.After(atA) {
		return msgB, atB
	}
	return msgA, atA
}

// Snapshot returns the runtime state of the named server.
func (p *UnifiedPool) Snapshot(name string) (ServerSnapshot, bool) {
	snap := ServerSnapshot{Name: name}
	switch p.resolveTransport(name) {
	case transportStdio:
		snap.Transport = "stdio"
		snap.State, _ = p.stdioPool.GetServerState(name)
		stats, _ := p.stdioPool.GetServerStats(name)
		snap.Requests = stats.RequestCount
		snap.Errors = stats.ErrorCount
		snap.AvgLatencyMs = stats.AvgLatencyMs
		snap.InFlight = stats.InFlight
		snap.LastRequestAt = stats.LastRequestAt
		snap.LastError = stats.LastError
		snap.LastErrorAt = stats.LastErrorAt
		snap.PID = stats.PID
		snap.StartedAt = stats.StartedAt
		snap.RestartCount = stats.RestartCount
		snap.RecentStderr = p.stdioPool.ServerRecentStderr(name)
		snap.applyInit(p.stdioPool.ServerInitializeResult(name))
		// The stored initialize result outlives a crash; only a completed
		// handshake of the current process counts.
		snap.Initialized = p.stdioPool.IsServerMCPInitialized(name)
	case transportHTTP:
		snap.Transport = "http"
		snap.State, _ = p.httpPool.GetServerState(name)
		snap.applyInit(p.httpPool.ServerInitializeResult(name))
		p.applyRemote(&snap, p.httpPool.connStatusOf(name))
	case transportSSE:
		snap.Transport = "sse"
		snap.State, _ = p.ssePool.GetServerState(name)
		snap.applyInit(p.ssePool.ServerInitializeResult(name))
		p.applyRemote(&snap, p.ssePool.connStatusOf(name))
	default:
		return ServerSnapshot{}, false
	}
	return snap, true
}

func (p *UnifiedPool) applyRemote(snap *ServerSnapshot, conn *connStatus) {
	calls := p.calls.get(snap.Name)
	snap.Requests = calls.requests
	snap.Errors = calls.errors
	snap.InFlight = calls.inFlight
	snap.LastRequestAt = calls.lastRequestAt
	if calls.requests > 0 {
		snap.AvgLatencyMs = float64(calls.totalLatency.Milliseconds()) / float64(calls.requests)
	}
	snap.LastError, snap.LastErrorAt = calls.lastErr, calls.lastErrAt
	if conn != nil {
		connErr, connErrAt, connectedAt := conn.snapshot()
		snap.ConnectedAt = connectedAt
		snap.LastError, snap.LastErrorAt = later(snap.LastError, snap.LastErrorAt, connErr, connErrAt)
	}
	// Only a live connection is "initialized": the stored result is that
	// of the last successful one.
	if snap.State != StateRunning {
		snap.Initialized = false
	}
}

// Snapshots returns the runtime state of every server, sorted by name.
func (p *UnifiedPool) Snapshots() []ServerSnapshot {
	names := p.ListServers()
	sort.Strings(names)
	out := make([]ServerSnapshot, 0, len(names))
	for _, name := range names {
		if snap, ok := p.Snapshot(name); ok {
			out = append(out, snap)
		}
	}
	return out
}

// StartServer adds a server to the pool that owns its transport. Starting
// a name that is already in a pool is an error: remove it first.
func (p *UnifiedPool) StartServer(ctx context.Context, config *migrate.ServerConfig) error {
	if config == nil || config.Name == "" {
		return fmt.Errorf("unified_pool: server name required")
	}
	if p.resolveTransport(config.Name) != transportNone {
		return fmt.Errorf("unified_pool: server %s already exists", config.Name)
	}
	switch config.Transport {
	case registry.TransportStdio:
		if config.Stdio == nil {
			return fmt.Errorf("unified_pool: server %s has no stdio block", config.Name)
		}
		return p.stdioPool.StartServer(ctx, config)
	case registry.TransportHTTP:
		return p.httpPool.StartServer(ctx, config)
	case registry.TransportSSE:
		if p.ssePool == nil {
			return fmt.Errorf("unified_pool: no SSE pool for server %s", config.Name)
		}
		return p.ssePool.StartServer(ctx, config)
	default:
		return fmt.Errorf("unified_pool: server %s has unknown transport %q", config.Name, config.Transport)
	}
}

// RemoveServer stops the named server and forgets it. In-flight calls to
// it fail; later calls get "not found".
func (p *UnifiedPool) RemoveServer(name string) error {
	switch p.resolveTransport(name) {
	case transportStdio:
		return p.stdioPool.RemoveServer(name)
	case transportHTTP:
		p.calls.forget(name)
		return p.httpPool.RemoveServer(name)
	case transportSSE:
		p.calls.forget(name)
		return p.ssePool.RemoveServer(name)
	default:
		return fmt.Errorf("unified_pool: server %s not found", name)
	}
}

// RemoveServer stops the named stdio server and removes it from the pool.
func (p *StdioPool) RemoveServer(name string) error {
	p.mu.Lock()
	server, exists := p.servers[name]
	delete(p.servers, name)
	p.mu.Unlock()
	if !exists {
		return fmt.Errorf("pool: server %s not found", name)
	}
	// Closed first, like Close does: a health- or crash-triggered restart
	// racing the removal must not respawn a process nobody tracks.
	server.closed.Store(true)
	if err := server.stop(); err != nil {
		return fmt.Errorf("pool: stop %s: %w", name, err)
	}
	p.logger.Info("server removed from pool", "name", name)
	return nil
}

// ServerRecentStderr returns the last stderr lines of the named server
// (already redacted), or "" when it wrote none.
func (p *StdioPool) ServerRecentStderr(name string) string {
	p.mu.RLock()
	server, exists := p.servers[name]
	p.mu.RUnlock()
	if !exists || server.stderrLines == nil {
		return ""
	}
	server.stderrLines.mu.Lock()
	defer server.stderrLines.mu.Unlock()
	if len(server.stderrLines.lines) == 0 {
		return ""
	}
	out := ""
	for i, line := range server.stderrLines.lines {
		if i > 0 {
			out += "\n"
		}
		out += line
	}
	return out
}

// RemoveServer closes the named HTTP server's connection and forgets it.
func (p *HTTPClientPool) RemoveServer(name string) error {
	p.mu.Lock()
	server, exists := p.servers[name]
	delete(p.servers, name)
	p.mu.Unlock()
	if !exists {
		return fmt.Errorf("http_pool: server %s not found", name)
	}
	_ = server.Close()
	p.logger.Info("http_pool: server removed", "name", name)
	return nil
}

func (p *HTTPClientPool) connStatusOf(name string) *connStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if server, ok := p.servers[name]; ok {
		return &server.conn
	}
	return nil
}

// RemoveServer closes the named SSE server's connection and forgets it.
func (p *SSEPool) RemoveServer(name string) error {
	p.mu.Lock()
	server, exists := p.servers[name]
	delete(p.servers, name)
	p.mu.Unlock()
	if !exists {
		return fmt.Errorf("sse_pool: server %s not found", name)
	}
	_ = server.Close()
	p.logger.Info("sse_pool: server removed", "name", name)
	return nil
}

func (p *SSEPool) connStatusOf(name string) *connStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if server, ok := p.servers[name]; ok {
		return &server.conn
	}
	return nil
}

// ensureConnected returns the server's live client, connecting first when
// needed, and records the outcome for Snapshot.
func (s *HTTPClientServer) ensureConnected(ctx context.Context) (c *client.Client, err error) {
	c, err = s.connect(ctx)
	s.conn.note(err, s.generation.Load())
	return c, err
}

// ensureConnected returns the server's live client, connecting first when
// needed, and records the outcome for Snapshot.
func (s *SSEServer) ensureConnected(ctx context.Context) (c *client.Client, err error) {
	c, err = s.connect(ctx)
	s.conn.note(err, s.generation.Load())
	return c, err
}
