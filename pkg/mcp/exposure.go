package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp/exposure"
	"github.com/mmornati/leanproxy-mcp/pkg/toolpin"
)

// Exposure modes (issue #322): how the upstream tools reach one client.
//
//   - router (the default for unknown clients): tools/list returns the
//     discovery tools (search_tools, list_servers, list_tools, invoke_tool),
//     exactly as before #322;
//   - passthrough: tools/list returns every upstream tool the security
//     layers let the client see, namespaced "<server>__<tool>" (shortened
//     with a hash suffix when needed, see exposure.ToolName), with its full
//     metadata for the negotiated protocol revision. A client with native
//     tool search (Claude Code, ...) defers and discovers them itself;
//   - hybrid: passthrough plus search_tools (and read_result while the
//     response governor is on).
//
// The mode is decided once per session, at initialize, from the
// clientInfo.name the client sends (exposure.Resolver). MCP has no client
// capability that announces native tool search, so the declared
// capabilities cannot tell; clientInfo.name is the only signal.
//
// Security: passthrough is a discovery format, not a bypass. The listing
// applies the same tool pinning (#310, block mode hides pending tools,
// invisible characters are stripped when the cache is filled) and per-tool
// policy (#314, denied tools hidden, confirm tools marked) views as
// list_tools and search_tools. A call on a namespaced name is rewritten by
// ExposureMiddleware, the outermost stage, into the canonical
// "<server>.<tool>" form every later stage (telemetry, governor, pinning,
// policy, response cache, redaction, injection guard) and the router of
// `serve` already understand, so each of them sees and decides the call
// exactly as for the other forms.

// exposureWatchInterval is how often the exposed list is compared with the
// last one sent, to catch changes no tools/list refresh reports (a tool
// approved with `tools pins approve` in another process).
var exposureWatchInterval = 2 * time.Second

// exposureDebounce coalesces the changes of several servers (the startup
// refresh) into one notifications/tools/list_changed.
var exposureDebounce = 200 * time.Millisecond

// NotificationToolsListChanged tells a client to fetch tools/list again.
const NotificationToolsListChanged = "notifications/tools/list_changed"

// exposureState is the handler's list_changed bookkeeping.
type exposureState struct {
	mu      sync.Mutex
	lastSig [32]byte
	hasSig  bool
	pending bool
	force   bool
}

// SetExposure installs the exposure resolver (nil: every client gets
// router, as before #322). Call it before serving.
func (h *Handler) SetExposure(r *exposure.Resolver) {
	h.exposure.Store(r)
}

// Exposure returns the installed resolver (nil when none).
func (h *Handler) Exposure() *exposure.Resolver { return h.exposure.Load() }

// ExposureMode is the mode of the request's client session: the one
// decided at its initialize, or the mode of a client without a name before
// that.
func (h *Handler) ExposureMode(ctx context.Context) exposure.Mode {
	if s := h.sessionFor(ctx); s != nil && s.Initialized() {
		if m := s.ExposureMode(); m != "" {
			return m
		}
	}
	m, _ := h.exposure.Load().ModeFor("")
	return m
}

// exposureModeFor decides the mode of a client at initialize.
func (h *Handler) exposureModeFor(info ClientInfo) (exposure.Mode, string) {
	return h.exposure.Load().ModeFor(info.Name)
}

// ExposureMiddleware rewrites a tools/call on a namespaced name
// ("<server>__<tool>", or its shortened form) into the canonical
// "<server>.<tool>" form, so every later stage sees the upstream server and
// tool. It must be the outermost stage. Any other request passes unchanged;
// so does a name that is not an exposed tool (the handler answers it as
// before).
func (h *Handler) ExposureMiddleware() Middleware {
	return func(next Next) Next {
		return func(ctx context.Context, req *Request) (*Response, error) {
			if req == nil || req.Method != MethodToolsCall || len(req.Params) == 0 {
				return next(ctx, req)
			}
			var params map[string]json.RawMessage
			if json.Unmarshal(req.Params, &params) != nil {
				return next(ctx, req)
			}
			var name string
			if json.Unmarshal(params["name"], &name) != nil || name == "" || GetToolDefinition(name) != nil || name == ReadResultToolName {
				return next(ctx, req)
			}
			server, tool, ok := h.resolveExposedName(name)
			if !ok {
				return next(ctx, req)
			}
			canonical, ok := canonicalToolRef(server, tool, h.pool.ListServers())
			if !ok {
				h.logger.Warn("exposure: namespaced tool has no unambiguous server.tool form", "name", name, "server", server, "tool", tool)
				return next(ctx, req)
			}
			encoded, err := json.Marshal(canonical)
			if err != nil {
				return next(ctx, req)
			}
			params["name"] = encoded
			body, err := json.Marshal(params)
			if err != nil {
				return next(ctx, req)
			}
			rewritten := *req
			rewritten.Params = body
			return next(ctx, &rewritten)
		}
	}
}

// canonicalToolRef is the "server.tool" (else "server_tool") reference
// that SplitToolName maps back to exactly server and tool.
func canonicalToolRef(server, tool string, servers []string) (string, bool) {
	for _, sep := range toolNameSeparators {
		ref := server + string(sep) + tool
		if s, t, err := SplitToolName(ref, servers); err == nil && s == server && t == tool {
			return ref, true
		}
	}
	return "", false
}

// resolveExposedName maps a namespaced name back to its server and tool.
// A name with the shape of a shortened one is first looked up among the
// cached tools (a shortened name is itself a well-formed "<server>__<rest>"
// and must not be taken for the plain name of a tool "<rest>"); a plain
// "<server>__<tool>" then resolves without the tool list, so a call to a
// tool the cache does not know yet still reaches the policy's
// unknown_tools check.
func (h *Handler) resolveExposedName(name string) (server, tool string, ok bool) {
	maxLen := h.exposure.Load().MaxNameLength()
	if exposure.Hashed(name) {
		h.toolCache.mu.RLock()
		for s, tools := range h.toolCache.tools {
			for _, t := range tools {
				if exposure.ToolName(s, t.Name, maxLen) == name {
					h.toolCache.mu.RUnlock()
					return s, t.Name, true
				}
			}
		}
		h.toolCache.mu.RUnlock()
	}
	if s, t, plain := exposure.SplitPlain(name); plain && h.knownServer(s) && exposure.ToolName(s, t, maxLen) == name {
		return s, t, true
	}
	return "", "", false
}

// exposedTool is one upstream tool as passthrough lists it.
type exposedTool struct {
	server  string
	tool    Tool // as cached (sanitized), original name
	name    string
	confirm bool // per-tool policy: confirm before each call
	pinWarn bool // tool pinning (warn mode): changed since approval
}

// exposedTools is the passthrough view of the cached tools, in server then
// upstream order: tool pinning and the per-tool policy applied as for
// list_tools. It never waits for a server.
func (h *Handler) exposedTools() []exposedTool {
	servers := h.pool.ListServers()
	sort.Strings(servers)
	maxLen := h.exposure.Load().MaxNameLength()
	p := h.pinner()
	seen := make(map[string]string)
	var out []exposedTool
	for _, server := range servers {
		h.toolCache.mu.RLock()
		tools, known := h.toolCache.tools[server]
		h.toolCache.mu.RUnlock()
		if !known {
			continue
		}
		tools, _ = h.pinView(server, tools)
		tools, confirm, _ := h.policyView(server, tools)
		var warned map[string]bool
		identity := false
		if p != nil && p.Mode() == toolpin.ModeWarn {
			pending, id := p.Unapproved(server)
			identity = id
			for _, u := range pending {
				if warned == nil {
					warned = make(map[string]bool)
				}
				warned[u.Tool] = true
			}
		}
		for _, t := range tools {
			name := exposure.ToolName(server, t.Name, maxLen)
			if prev, dup := seen[name]; dup {
				h.logger.Warn("exposure: two tools map to the same namespaced name, keeping the first", "name", name, "first", prev, "dropped", server+"."+t.Name)
				continue
			}
			seen[name] = server + "." + t.Name
			out = append(out, exposedTool{server: server, tool: t, name: name, confirm: confirm[t.Name], pinWarn: identity || warned[t.Name]})
		}
	}
	return out
}

// Description markers of passthrough entries: the model sees them where
// list_tools shows its tags.
const (
	confirmMarker = "[confirm] "
	pinWarnMarker = "[WARNING tool pinning: changed since approval] "
)

// Vendor _meta keys of passthrough entries. Claude Code documents
// "anthropic/alwaysLoad" (load this tool upfront instead of deferring it
// behind its tool search); no MCP revision nor any other vendor defines a
// "defer this tool" hint, and deferral is those clients' default, so none
// is invented (see docs/configuration.md, "Deferred-loading hints").
const metaAlwaysLoad = "anthropic/alwaysLoad"

// passthroughTools builds the tools/list entries of a passthrough or hybrid
// session, waiting (bounded) for servers whose tools were never fetched
// and, in pinning block mode, for the first comparison with the pins.
func (h *Handler) passthroughTools(ctx context.Context) []Tool {
	servers := h.pool.ListServers()
	h.awaitColdServers(ctx, servers)
	h.awaitPinned(ctx, servers)
	sess := h.sessionFor(ctx)
	r := h.exposure.Load()
	view := h.exposedTools()
	out := make([]Tool, 0, len(view))
	for _, e := range view {
		t := e.tool
		t.Name = e.name
		switch {
		case e.pinWarn && e.confirm:
			t.Description = pinWarnMarker + confirmMarker + t.Description
		case e.pinWarn:
			t.Description = pinWarnMarker + t.Description
		case e.confirm:
			t.Description = confirmMarker + t.Description
		}
		t.Meta = exposedMeta(t.Meta, r.AlwaysLoad(e.server, e.tool.Name))
		out = append(out, toolForProtocol(t, sess))
	}
	return out
}

// exposedMeta is a passthrough entry's _meta: the upstream's, without its
// own "anthropic/alwaysLoad" (whether a tool is loaded upfront is the
// proxy operator's decision, exposure.always_load), plus that key when
// alwaysLoad. A _meta that is not an object is dropped.
func exposedMeta(meta json.RawMessage, alwaysLoad bool) json.RawMessage {
	var m map[string]json.RawMessage
	if len(meta) > 0 && string(meta) != "null" && json.Unmarshal(meta, &m) != nil {
		return nil
	}
	if _, has := m[metaAlwaysLoad]; !has && !alwaysLoad {
		if len(m) == 0 {
			return nil
		}
		return meta
	}
	delete(m, metaAlwaysLoad)
	if alwaysLoad {
		if m == nil {
			m = make(map[string]json.RawMessage, 1)
		}
		m[metaAlwaysLoad] = json.RawMessage("true")
	}
	if len(m) == 0 {
		return nil
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return out
}

// toolForProtocol drops the tool fields the session's protocol revision
// does not define: annotations (2025-03-26), title, outputSchema and _meta
// (2025-06-18), icons (2025-11-25).
func toolForProtocol(t Tool, s *ClientSession) Tool {
	if !s.AtLeast(ProtocolVersion20250326) {
		t.Annotations = nil
	}
	if !s.AtLeast(ProtocolVersion20250618) {
		t.Title, t.OutputSchema, t.Meta = "", nil, nil
	}
	if !s.AtLeast(ProtocolVersion20251125) {
		t.Icons = nil
	}
	return t
}

// gatewayTool is a gateway tool's tools/list entry for a session.
func gatewayTool(def ToolDefinition, s *ClientSession) Tool {
	tool := Tool{Name: def.Name, Description: def.Description, InputSchema: def.InputSchema}
	if def.ReadOnly && s.AtLeast(ProtocolVersion20250326) {
		readOnly := true
		tool.Annotations = &ToolAnnotations{ReadOnlyHint: &readOnly}
	}
	return tool
}

// hybridSearchDescription is search_tools' description in hybrid mode,
// where the matches are called directly by the name shown.
const hybridSearchDescription = "Find the best tools for a task across all servers. Returns the top matches with their input schema; call them directly by the name shown."

// exposedToolName is the name a session calls server's tool by: the
// namespaced name in passthrough and hybrid, "server_tool" in router.
func (h *Handler) exposedToolName(ctx context.Context, server, tool string) string {
	if h.ExposureMode(ctx).ListsUpstreamTools() {
		return exposure.ToolName(server, tool, h.exposure.Load().MaxNameLength())
	}
	return server + "_" + tool
}

// exposureSignature fingerprints the exposed list (names and markers), to
// tell when it changed.
func (h *Handler) exposureSignature() [32]byte {
	sum := sha256.New()
	for _, e := range h.exposedTools() {
		sum.Write([]byte(e.name))
		flags := []byte{0, '0', '0'}
		if e.confirm {
			flags[1] = '1'
		}
		if e.pinWarn {
			flags[2] = '1'
		}
		sum.Write(flags)
	}
	var out [32]byte
	copy(out[:], sum.Sum(nil))
	return out
}

// listingSessions returns the open, initialized sessions in passthrough or
// hybrid mode.
func (h *Handler) listingSessions() []*ClientSession {
	h.sessionsMu.Lock()
	defer h.sessionsMu.Unlock()
	var out []*ClientSession
	for s := range h.sessions {
		if s.Initialized() && s.ExposureMode().ListsUpstreamTools() {
			out = append(out, s)
		}
	}
	return out
}

// exposureChanged schedules a comparison of the exposed list with the last
// one (debounced). force: a server's tools changed, so the sessions are
// told even when the names and markers are the same (a description or
// schema changed).
func (h *Handler) exposureChanged(force bool) {
	if !h.exposure.Load().MayListUpstreamTools() {
		return
	}
	st := &h.exposureState
	st.mu.Lock()
	if force {
		st.force = true
	}
	if st.pending {
		st.mu.Unlock()
		return
	}
	st.pending = true
	st.mu.Unlock()
	time.AfterFunc(exposureDebounce, h.checkExposure)
}

// checkExposure notifies the passthrough and hybrid sessions when the
// exposed list changed since the last check.
func (h *Handler) checkExposure() {
	st := &h.exposureState
	st.mu.Lock()
	st.pending = false
	force := st.force
	st.force = false
	st.mu.Unlock()

	if h.backgroundContext().Err() != nil {
		return
	}
	sessions := h.listingSessions()
	sig := h.exposureSignature()
	st.mu.Lock()
	changed := force || (st.hasSig && sig != st.lastSig)
	st.lastSig, st.hasSig = sig, true
	st.mu.Unlock()
	if !changed || len(sessions) == 0 {
		return
	}
	h.logger.Debug("exposed tool list changed, notifying clients", "sessions", len(sessions))
	for _, s := range sessions {
		s.send(NotificationToolsListChanged, nil)
	}
}

// watchExposure periodically compares the exposed list with the last one
// (tool pinning approvals and pin file changes made by another process
// report no event). It stops with ctx.
func (h *Handler) watchExposure(ctx context.Context) {
	ticker := time.NewTicker(exposureWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if len(h.listingSessions()) > 0 {
			h.exposureChanged(false)
		}
	}
}
