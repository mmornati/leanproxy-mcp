package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/mmornati/leanproxy-mcp/internal/logx"
	"github.com/mmornati/leanproxy-mcp/internal/version"
	"github.com/mmornati/leanproxy-mcp/pkg/errors"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp/exposure"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp/governor"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/toolsearch"
	"github.com/mmornati/leanproxy-mcp/pkg/toolstore"
)

type ParamInfo struct {
	Name        string
	Type        string
	IsRequired  bool
	Description string
}

type ToolCache struct {
	mu    sync.RWMutex
	tools map[string][]Tool
}

// CachedTools returns a snapshot of the tools known per server name. It is
// used by the serve listener to register backend tools with the router so
// tool calls can be routed to the owning server.
func (h *Handler) CachedTools() map[string][]Tool {
	h.toolCache.mu.RLock()
	defer h.toolCache.mu.RUnlock()
	out := make(map[string][]Tool, len(h.toolCache.tools))
	for name, tools := range h.toolCache.tools {
		snapshot := make([]Tool, len(tools))
		copy(snapshot, tools)
		out[name] = snapshot
	}
	return out
}

type Handler struct {
	pool           pool.ServerSource
	logger         *slog.Logger
	timeout        time.Duration
	timeouts       map[string]time.Duration
	toolCache      *ToolCache
	toolStore      toolstore.Cache
	manifest       *AggregatedManifest
	cacheRefreshes atomic.Uint64
	cacheFailures  atomic.Uint64

	// Background per-server tool refresh (see toolrefresh.go).
	refreshMu      sync.Mutex
	refreshing     map[string]*refreshCall
	refreshErrs    map[string]error
	bgCtx          context.Context
	retryInterval  time.Duration
	toolsListeners []func(server string, tools []Tool)

	// search is the ranked cross-server tool index behind search_tools,
	// kept in step with toolCache (see toolrefresh.go).
	search atomic.Pointer[toolsearch.Index]

	// pipelineMu guards middlewares; pipeline holds the composed chain
	// (middlewares around dispatch) so HandleRequest can load it lock-free.
	pipelineMu  sync.Mutex
	middlewares []Middleware
	pipeline    atomic.Pointer[Next]

	// Client sessions (see protocol.go). defaultSession serves requests
	// whose context carries none.
	sessionsMu        sync.Mutex
	sessions          map[*ClientSession]struct{}
	defaultSession    *ClientSession
	sessionCloseHooks []func(*ClientSession)

	// resourceOwners maps an upstream resource URI (as the upstream lists
	// it) to its server, from the last resources/list fan-out, so a raw URI
	// (e.g. from a resource_link) can still be routed (see aggregate.go).
	ownersMu       sync.RWMutex
	resourceOwners map[string]string

	// relay routes what the upstreams initiate (server-to-client requests,
	// progress, resource updates) to the right client (see relay.go).
	relay relayState

	// pins is the tool pinning state (see toolpins.go); nil: disabled.
	pins atomic.Pointer[ToolPins]

	// policy is the per-tool policy (see middleware_policy.go); nil: none.
	policy atomic.Pointer[Policy]

	// exposure decides each client's exposure mode (see exposure.go); nil:
	// router for every client.
	exposure      atomic.Pointer[exposure.Resolver]
	exposureState exposureState
}

type AggregatedManifest struct {
	Tools     []Tool
	Resources []Resource
	Prompts   []Prompt
}

func NewHandler(p pool.ServerSource, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	h := &Handler{
		pool:    p,
		logger:  logger,
		timeout: 30 * time.Second,
		toolCache: &ToolCache{
			tools: make(map[string][]Tool),
		},
		refreshing:     make(map[string]*refreshCall),
		refreshErrs:    make(map[string]error),
		bgCtx:          context.Background(),
		retryInterval:  DefaultToolRefreshRetryInterval,
		defaultSession: &ClientSession{},
	}
	h.search.Store(toolsearch.New(toolsearch.Options{Logger: logger}))
	return h
}

func NewHandlerWithToolStore(p pool.ServerSource, logger *slog.Logger, store toolstore.Cache) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	h := NewHandler(p, logger)
	h.toolStore = store
	return h
}

// SetTimeout registers a per-server request timeout. The handler falls back
// to its default timeout (30s unless changed via SetDefaultTimeout) for any
// server that has no explicit entry. A zero duration clears the entry.
func (h *Handler) SetTimeout(serverName string, timeout time.Duration) {
	if h.timeouts == nil {
		h.timeouts = make(map[string]time.Duration)
	}
	if timeout <= 0 {
		delete(h.timeouts, serverName)
		return
	}
	h.timeouts[serverName] = timeout
}

// SetDefaultTimeout overrides the fallback timeout used when a server has no
// explicit per-server entry. A zero or negative value is ignored (the 30s
// default remains in effect).
func (h *Handler) SetDefaultTimeout(timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	h.timeout = timeout
}

// sendUpstream wraps h.pool.SendRequestToServer with a CLIENT span (issue
// #317: "child spans ... for the upstream call, kind CLIENT"), named
// "<method> <server>" per the OTel RPC client convention, carrying
// mcp.server.name and mcp.method.name. It never adds request or response
// payload content to the span: only the outcome (error.type on failure).
func (h *Handler) sendUpstream(ctx context.Context, serverName, method string, params []byte, timeout time.Duration) (*pool.Response, error) {
	if !telemetryActive.Load() {
		return h.pool.SendRequestToServer(ctx, serverName, method, params, timeout)
	}
	ctx, span := tracer().Start(ctx, method+" "+serverName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(McpMethodNameAttr(method), UpstreamServerNameAttr(serverName)))
	defer span.End()

	resp, err := h.pool.SendRequestToServer(ctx, serverName, method, params, timeout)
	switch {
	case err != nil:
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	case resp != nil && resp.Error != nil:
		span.SetStatus(codes.Error, resp.Error.Message)
	default:
		span.SetStatus(codes.Ok, "")
	}
	return resp, err
}

func (h *Handler) timeoutFor(serverName string) time.Duration {
	if d, ok := h.timeouts[serverName]; ok && d > 0 {
		return d
	}
	return h.timeout
}

// HandleRequest runs req through the middleware pipeline registered with Use
// and, innermost, the handler's own method dispatch.
func (h *Handler) HandleRequest(ctx context.Context, req *Request) (*Response, error) {
	if chain := h.pipeline.Load(); chain != nil {
		resp, err := (*chain)(ctx, req)
		return guardResponse(resp), err
	}
	resp, err := h.dispatch(ctx, req)
	return guardResponse(resp), err
}

// dispatch routes a request to its method handler. It is the innermost Next
// of the pipeline.
func (h *Handler) dispatch(ctx context.Context, req *Request) (*Response, error) {
	h.logger.Debug("handling mcp request", "method", req.Method, "id", req.ID)

	if err := errors.ValidateContext(ctx); err != nil {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeInternalError, err.Error()),
			ID:      req.ID,
		}, nil
	}

	switch req.Method {
	case MethodInitialize:
		return h.handleInitialize(ctx, req)
	case MethodInitialized:
		h.logger.Info("received initialized notification from client")
		return nil, nil
	case NotificationRootsListChanged:
		h.forwardRootsListChanged()
		return nil, nil
	case MethodResourcesList:
		return h.handleResourcesList(ctx, req)
	case MethodResourcesTemplatesList:
		return h.handleResourceTemplatesList(ctx, req)
	case MethodResourcesRead:
		return h.handleResourcesRead(ctx, req)
	case MethodResourcesSubscribe, MethodResourcesUnsubscribe:
		return h.handleResourcesSubscription(ctx, req)
	case MethodPromptsList:
		return h.handlePromptsList(ctx, req)
	case MethodPromptsGet:
		return h.handlePromptsGet(ctx, req)
	case MethodToolsList:
		return h.handleToolsList(ctx, req)
	case MethodToolsCall:
		return h.handleToolsCall(ctx, req)
	case MethodPing:
		return h.handlePing(ctx, req)
	case MethodShutdown:
		return h.handleShutdown(ctx, req)
	default:
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeMethodNotFound, fmt.Sprintf("method not found: %s", req.Method)),
			ID:      req.ID,
		}, nil
	}
}

func (h *Handler) handleInitialize(ctx context.Context, req *Request) (*Response, error) {
	var params InitializeParams
	var caps struct {
		Capabilities json.RawMessage `json:"capabilities"`
	}
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &Response{
				JSONRPC: JSONRPCVersion,
				Error:   NewError(ErrCodeInvalidParams, fmt.Sprintf("invalid params: %v", err)),
				ID:      req.ID,
			}, nil
		}
		_ = json.Unmarshal(req.Params, &caps)
	}

	negotiated := NegotiateProtocolVersion(params.ProtocolVersion)
	upstream := h.upstreamFeatures(ctx)
	// Exposure mode (#322): passthrough and hybrid list the upstream tools,
	// whose list changes with the upstreams (and the pins), hence
	// listChanged; router's discovery tools never change.
	mode, decidedBy := h.exposureModeFor(params.ClientInfo)

	result := InitializeResult{
		ProtocolVersion: negotiated,
		Capabilities: ServerCapabilities{
			Tools: &ToolsCapability{ListChanged: mode.ListsUpstreamTools()},
		},
		ServerInfo: ServerInfo{
			Name:    "leanproxy-mcp",
			Version: version.Get().Version,
		},
	}
	// Resources and prompts are only advertised when an upstream serves
	// them; the aggregated lists change with the upstreams, hence
	// listChanged.
	if upstream.resources {
		// subscribe: resources/subscribe is routed to the owning server
		// and its notifications/resources/updated relayed (#308).
		result.Capabilities.Resources = &ResourcesCapability{ListChanged: true, Subscribe: upstream.subscribe}
	}
	if upstream.prompts {
		result.Capabilities.Prompts = &PromptsCapability{ListChanged: true}
	}
	if ProtocolAtLeast(negotiated, ProtocolVersion20250618) {
		result.ServerInfo.Title = "LeanProxy"
	}

	resultBytes, err := json.Marshal(result)
	if err != nil {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeInternalError, fmt.Sprintf("marshal result: %v", err)),
			ID:      req.ID,
		}, nil
	}

	if s := h.sessionFor(ctx); s != nil {
		s.setInitialized(negotiated, params, caps.Capabilities, mode)
	}
	h.logger.Info("initialized leanproxy-mcp", "client", params.ClientInfo.Name, "version", params.ClientInfo.Version,
		"requested_protocol", params.ProtocolVersion, "protocol", negotiated,
		"exposure", string(mode), "exposure_decided_by", decidedBy,
		"resources", upstream.resources, "prompts", upstream.prompts)

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}, nil
}

func (h *Handler) handleToolsList(ctx context.Context, req *Request) (*Response, error) {
	sess := h.sessionFor(ctx)
	mode := h.ExposureMode(ctx)

	var tools []Tool
	switch mode {
	case exposure.ModePassthrough, exposure.ModeHybrid:
		// Every upstream tool the security layers let the client see
		// (#322); hybrid adds search_tools (read_result is added by the
		// response governor while it is on, in every mode).
		if mode == exposure.ModeHybrid {
			if def := GetToolDefinition("search_tools"); def != nil {
				search := *def
				search.Description = hybridSearchDescription
				tools = append(tools, gatewayTool(search, sess))
			}
		}
		tools = append(tools, h.passthroughTools(ctx)...)
	default:
		// Router: the gateway tools only. Annotations exist since
		// 2025-03-26: an older client gets exactly the fields it knows.
		for _, def := range GetAllToolDefinitions() {
			tools = append(tools, gatewayTool(def, sess))
		}
	}
	if tools == nil {
		tools = []Tool{}
	}

	resultBytes, _ := json.Marshal(ToolsListResult{Tools: tools})
	h.logger.Info("tools/list sent to client", "exposure", string(mode), "count", len(tools))

	// Schema savings accounting (issue #324): schema_sent_tokens is exactly
	// what was marshaled above; schema_native_tokens is what the client
	// would have received listing every upstream tool the security layers
	// allow (h.passthroughTools), i.e. the same real payload passthrough
	// mode sends. In passthrough/hybrid mode these are computed from the
	// same tool set, so the measured saving is legitimately zero: the
	// router's compaction is what creates the gap.
	// exposedToolsSnapshot (never awaits a cold/unpinned server) so this
	// accounting never changes tools/list's own latency or blocks on a
	// server that is still starting.
	nativeTools := tools
	if mode == exposure.ModeRouter {
		nativeTools = h.exposedToolsSnapshot(ctx)
	}
	nativeBytes, _ := json.Marshal(ToolsListResult{Tools: nativeTools})
	RecordSchemaListing(ctx, int64(governor.Tokens(len(nativeBytes))), int64(governor.Tokens(len(resultBytes))))

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}, nil
}

func (h *Handler) collectTools(ctx context.Context) (*AggregatedManifest, error) {
	return &AggregatedManifest{
		Tools:     make([]Tool, 0),
		Resources: make([]Resource, 0),
		Prompts:   make([]Prompt, 0),
	}, nil
}

func (h *Handler) handleToolsCall(ctx context.Context, req *Request) (*Response, error) {
	h.logger.Debug("handleToolsCall called", logx.Payload("params", req.Params))

	var params ToolsCallParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			h.logger.Warn("failed to unmarshal tools/call params", "error", err)
			return &Response{
				JSONRPC: JSONRPCVersion,
				Error:   NewError(ErrCodeInvalidParams, fmt.Sprintf("invalid params: %v", err)),
				ID:      req.ID,
			}, nil
		}
	}

	h.logger.Debug("tools/call request", "name", params.Name)

	if params.Name == "" {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeInvalidParams, "tool name is required"),
			ID:      req.ID,
		}, nil
	}

	if GetToolDefinition(params.Name) != nil {
		return h.handleLeanproxyTool(ctx, req, params)
	}

	serverName, toolName, err := h.parseToolName(params.Name)
	if err != nil {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeInvalidParams, err.Error()),
			ID:      req.ID,
		}, nil
	}

	// The pool performs the MCP handshake with the server (once per
	// process generation / connection); the handler just sends requests.
	newParams := ToolsCallParams{
		Name:      toolName,
		Arguments: params.Arguments,
	}
	paramsBytes, _ := json.Marshal(newParams)
	// Progress token and client routing of the upstream's own requests
	// (#308).
	paramsBytes, endCall := h.BeginUpstreamCall(ctx, serverName, req.Params, paramsBytes)
	defer endCall()

	resp, err := h.sendUpstream(ctx, serverName, MethodToolsCall, paramsBytes, h.timeoutFor(serverName))
	if err != nil {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeServerError, fmt.Sprintf("tool call failed: %v", err)),
			ID:      req.ID,
		}, nil
	}

	if resp != nil && resp.Error != nil {
		// Forward the upstream JSON-RPC error instead of an empty response.
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   &Error{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data},
			ID:      req.ID,
		}, nil
	}

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resp.Result,
		ID:      req.ID,
	}, nil
}

func (h *Handler) handleLeanproxyTool(ctx context.Context, req *Request, params ToolsCallParams) (*Response, error) {
	switch params.Name {
	case "search_tools":
		return h.handleSearchTools(ctx, req, params)
	case "list_servers":
		return h.handleListServers(ctx, req)
	case "list_tools":
		return h.handleListTools(ctx, req, params)
	case "invoke_tool":
		return h.handleInvokeTool(ctx, req, params)
	default:
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeMethodNotFound, fmt.Sprintf("unknown gateway tool: %s", params.Name)),
			ID:      req.ID,
		}, nil
	}
}

func (h *Handler) handleListTools(ctx context.Context, req *Request, params ToolsCallParams) (*Response, error) {
	var serverName string
	var maxDescChars int
	if params.Arguments != nil {
		var args map[string]interface{}
		if err := json.Unmarshal(params.Arguments, &args); err == nil {
			args = ApplyDefaults("list_tools", args)
			if s, ok := args["server_name"].(string); ok {
				serverName = s
			}
			if m, ok := args["max_description_chars"].(float64); ok {
				maxDescChars = int(m)
			}
		}
	}

	if serverName == "" {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeInvalidParams, "server_name parameter is required. Use list_servers to get available server names, then use list_tools to see tools on a specific server."),
			ID:      req.ID,
		}, nil
	}

	if valid, msg := ValidateParam("list_tools", "max_description_chars", float64(maxDescChars)); !valid {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeInvalidParams, fmt.Sprintf("max_description_chars %s", msg)),
			ID:      req.ID,
		}, nil
	}

	if maxDescChars == 0 {
		maxDescChars = 200
	}

	h.logger.Info("list_tools called", "server_name", serverName, "max_desc_chars", maxDescChars)

	servers := h.pool.ListServers()
	serverFound := false
	for _, s := range servers {
		if s == serverName {
			serverFound = true
			break
		}
	}

	if !serverFound {
		serversList := strings.Join(servers, ", ")
		result := map[string]interface{}{
			"content": []map[string]string{
				{"type": "text", "text": fmt.Sprintf("Server '%s' not found. Available servers: %s. Use list_servers to see all available servers.", serverName, serversList)},
			},
		}
		resultBytes, _ := json.Marshal(result)
		return &Response{
			JSONRPC: JSONRPCVersion,
			Result:  resultBytes,
			ID:      req.ID,
		}, nil
	}

	h.toolCache.mu.RLock()
	tools, exists := h.toolCache.tools[serverName]
	h.toolCache.mu.RUnlock()

	var refreshErr error
	if !exists || len(tools) == 0 {
		// Refresh this server only, waiting at most its own timeout; the
		// other servers are never touched.
		refreshErr = h.RefreshServerTools(ctx, serverName)
		h.toolCache.mu.RLock()
		tools = h.toolCache.tools[serverName]
		h.toolCache.mu.RUnlock()
	}

	// Tool pinning (#310): block mode hides tools awaiting approval (after
	// comparing the server's current list, if this process has not yet),
	// warn mode adds a one-line warning.
	h.awaitPinned(ctx, []string{serverName})
	h.toolCache.mu.RLock()
	if fresh, ok := h.toolCache.tools[serverName]; ok {
		tools = fresh
	}
	h.toolCache.mu.RUnlock()
	tools, pinNotice := h.pinView(serverName, tools)
	// Per-tool policy (#314): denied tools are hidden, the ones that need
	// confirmation are marked [confirm].
	tools, confirm, policyHidden := h.policyView(serverName, tools)
	if policyHidden > 0 {
		note := policyHiddenNote(policyHidden)
		if pinNotice != "" {
			pinNotice += "\n" + note
		} else {
			pinNotice = note
		}
	}

	if len(tools) == 0 {
		text := fmt.Sprintf("No tools available on server '%s'. The server may be unavailable or have no tools.", serverName)
		if pinNotice != "" {
			text = pinNotice
		}
		if refreshErr != nil {
			text = fmt.Sprintf("No tools available on server '%s': listing its tools failed: %v", serverName, refreshErr)
		}
		result := map[string]interface{}{
			"content": []map[string]string{
				{"type": "text", "text": text},
			},
		}
		resultBytes, _ := json.Marshal(result)
		return &Response{
			JSONRPC: JSONRPCVersion,
			Result:  resultBytes,
			ID:      req.ID,
		}, nil
	}

	formattedTools := make([]string, 0, len(tools))
	for _, tool := range tools {
		formatted := formatToolMarkedAs(tool, h.exposedToolName(ctx, serverName, tool.Name), maxDescChars, confirm[tool.Name])
		formattedTools = append(formattedTools, formatted)
	}

	h.logger.Info("list_tools completed", "server", serverName, "results", len(formattedTools))

	text := fmt.Sprintf("%s tools (%d):\n%s", serverName, len(tools), strings.Join(formattedTools, "\n"))
	if pinNotice != "" {
		text = pinNotice + "\n" + text
	}
	var structured []StructuredTool
	if h.sessionFor(ctx).AtLeast(ProtocolVersion20250618) {
		structured = make([]StructuredTool, 0, len(tools))
		for _, tool := range tools {
			structured = append(structured, StructuredTool{Server: serverName, Tool: tool, Policy: policyMark(confirm[tool.Name])})
		}
	}
	return toolListingResult(ctx, req.ID, text, structured), nil
}

// StructuredTool is one entry of the structuredContent list_tools and
// search_tools return to clients that negotiated 2025-06-18 or newer: the
// full upstream tool object (outputSchema, annotations, icons, _meta, ...)
// and the server that owns it.
type StructuredTool struct {
	Server string `json:"server"`
	Tool   Tool   `json:"tool"`
	// Policy is "confirm" when the per-tool policy (#314) asks the user
	// before each call to this tool.
	Policy string `json:"policy,omitempty"`
}

// policyMark is StructuredTool.Policy for a tool that needs confirmation.
func policyMark(confirm bool) string {
	if confirm {
		return "confirm"
	}
	return ""
}

// toolListingResult is a tools/call result carrying the compact text
// listing and, when structured is non-nil, the full tool objects as
// structuredContent ({"tools": [...]}).
// toolListingResult builds the response for the discovery tools
// (list_tools, list_servers, search_tools) and records its size as
// discovery tokens (issue #324's "discovery via search_tools" category):
// these are tokens the client spends reading a discovery result instead of
// a fixed schema, so the savings report can show them as a labelled,
// separate line rather than folding them into schema savings.
func toolListingResult(ctx context.Context, id interface{}, text string, structured []StructuredTool) *Response {
	result := map[string]interface{}{
		"content": []map[string]string{{"type": "text", "text": text}},
	}
	if structured != nil {
		result["structuredContent"] = map[string]interface{}{"tools": structured}
	}
	resultBytes, _ := json.Marshal(result)
	RecordDiscovery(ctx, int64(governor.Tokens(len(resultBytes))))
	return &Response{JSONRPC: JSONRPCVersion, Result: resultBytes, ID: id}
}

// listServersInstructionsChars caps the server instructions list_servers
// shows per server.
const listServersInstructionsChars = 120

// handleListServers implements the list_servers gateway tool: one compact
// line per configured server with its transport, state and cached tool
// count (kept current by the background tool refresh), plus the serverInfo
// and the first 120 characters of the instructions the server returned in
// its InitializeResult, when known. It takes no parameters and never forces
// a backend round-trip, so it stays cheap to call before every session.
func (h *Handler) handleListServers(ctx context.Context, req *Request) (*Response, error) {
	servers := h.pool.ListServers()
	sort.Strings(servers)

	lines := make([]string, 0, len(servers))
	for _, name := range servers {
		state, err := h.pool.GetServerState(name)
		if err != nil {
			state = pool.StateUnknown
		}

		transport, err := h.pool.GetServerTransport(name)
		if err != nil {
			transport = "unknown"
		}

		count := h.toolCountFor(name)
		line := fmt.Sprintf("%s (%s, %s, %d tools)", name, transport, healthLabel(state), count)
		if info := h.serverInitializeResult(name); info != nil {
			line += sessionSummary(info)
		}
		lines = append(lines, line)
	}

	text := "No servers configured."
	if len(lines) > 0 {
		text = strings.Join(lines, "\n")
	}

	h.logger.Info("list_servers completed", "count", len(servers))

	result := map[string]interface{}{
		"content": []map[string]string{
			{"type": "text", "text": text},
		},
	}
	resultBytes, _ := json.Marshal(result)

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}, nil
}

// serverInitializeResult returns the pool's stored InitializeResult for a
// server, or nil when the pool does not keep one (or has none yet).
func (h *Handler) serverInitializeResult(name string) *pool.InitializeResult {
	provider, ok := h.pool.(pool.SessionInfoProvider)
	if !ok {
		return nil
	}
	res, ok := provider.ServerInitializeResult(name)
	if !ok {
		return nil
	}
	return res
}

// sessionSummary renders the serverInfo and (truncated, single-line)
// instructions part of a list_servers line.
func sessionSummary(info *pool.InitializeResult) string {
	var sb strings.Builder
	if info.ServerInfo.Name != "" {
		sb.WriteString(" [")
		sb.WriteString(info.ServerInfo.Name)
		if info.ServerInfo.Version != "" {
			sb.WriteString(" ")
			sb.WriteString(info.ServerInfo.Version)
		}
		sb.WriteString("]")
	}
	if instr := strings.Join(strings.Fields(info.Instructions), " "); instr != "" {
		if r := []rune(instr); len(r) > listServersInstructionsChars {
			instr = string(r[:listServersInstructionsChars-3]) + "..."
		}
		sb.WriteString(" instructions: ")
		sb.WriteString(instr)
	}
	return sb.String()
}

// healthLabel maps a pool.ServerState to the compact word list_servers
// reports: "healthy" for a server that can serve requests right now, and
// "unreachable" for anything else (stopped, errored, disconnected,
// unknown, ...).
func healthLabel(state pool.ServerState) string {
	switch state {
	case pool.StateIdle, pool.StateRunning, pool.StateBusy:
		return "healthy"
	default:
		return "unreachable"
	}
}

// toolCountFor returns the number of cached tools for a server, without
// forcing a cache refresh (list_servers must stay cheap).
func (h *Handler) toolCountFor(serverName string) int {
	h.toolCache.mu.RLock()
	defer h.toolCache.mu.RUnlock()
	return len(h.toolCache.tools[serverName])
}

// invokeToolEnvelope is the invoke_tool gateway tool's params, decoded
// without going through map[string]interface{} so that Arguments travels to
// the upstream server byte-for-byte (no float64 round-trip that would lose
// precision on large integers, and no risk of silently dropping a
// non-object payload).
type invokeToolEnvelope struct {
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

func (h *Handler) handleInvokeTool(ctx context.Context, req *Request, params ToolsCallParams) (*Response, error) {
	var envelope invokeToolEnvelope
	if params.Arguments != nil {
		if err := json.Unmarshal(params.Arguments, &envelope); err != nil {
			h.logger.Warn("failed to unmarshal invoke_tool params", "error", err)
			return &Response{
				JSONRPC: JSONRPCVersion,
				Error:   NewError(ErrCodeInvalidParams, fmt.Sprintf("invalid params: %v", err)),
				ID:      req.ID,
			}, nil
		}
	}
	// ApplyDefaults("invoke_tool", ...) is a documented no-op today (see
	// tool_defaults.go): invoke_tool has no defaultable proxy-owned keys.
	// It is intentionally not called here so it can never reach into
	// (and mutate) the upstream `arguments` payload.

	serverName := envelope.Server
	toolName := envelope.Tool
	arguments := envelope.Arguments

	if len(arguments) > 0 && string(arguments) != "null" {
		trimmed := strings.TrimSpace(string(arguments))
		if !strings.HasPrefix(trimmed, "{") {
			return &Response{
				JSONRPC: JSONRPCVersion,
				Error:   NewError(ErrCodeInvalidParams, "arguments must be a JSON object"),
				ID:      req.ID,
			}, nil
		}
	} else {
		arguments = nil
	}

	if serverName == "" || toolName == "" {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   NewError(ErrCodeInvalidParams, "server and tool are required. Use search_tools to find a tool and its server."),
			ID:      req.ID,
		}, nil
	}

	// Tolerate a model that repeats the server prefix ("github_create_issue"
	// on server "github"), but never strip it from a tool that really is
	// named that way: Slack's tools are called "slack_post_message" on a
	// server usually named "slack".
	if strings.HasPrefix(toolName, serverName+"_") {
		found, known := h.hasCachedTool(serverName, toolName)
		if !known {
			// Only this ambiguous case waits for the server's tool list
			// (at most its own timeout); a failed refresh keeps the old
			// strip-the-prefix behavior.
			_ = h.RefreshServerTools(ctx, serverName)
			found, _ = h.hasCachedTool(serverName, toolName)
		}
		if !found {
			toolName = strings.TrimPrefix(toolName, serverName+"_")
		}
	}

	h.logger.Info("invoke_tool called", "server", serverName, "tool", toolName)

	// No handler-level restart or handshake: the pool restarts an
	// unhealthy stdio server on use (and its crash-restart loop runs in the
	// background) and performs the MCP handshake before the call.
	newParams := ToolsCallParams{
		Name:      toolName,
		Arguments: arguments,
	}
	paramsBytes, _ := json.Marshal(newParams)
	// The gateway call's own _meta.progressToken is forwarded (remapped)
	// to the upstream tool call (#308).
	paramsBytes, endCall := h.BeginUpstreamCall(ctx, serverName, req.Params, paramsBytes)
	defer endCall()

	resp, err := h.sendUpstream(ctx, serverName, MethodToolsCall, paramsBytes, h.timeoutFor(serverName))
	if err != nil {
		// A transport/proxy-level failure (timeout, connection error, server
		// not reachable) rather than a structured upstream JSON-RPC error:
		// keep the enriched, hint-carrying ErrCodeServerError.
		h.logger.Error("invoke_tool failed", "server", serverName, "tool", toolName, "error", err)
		schema := h.lookupToolSchema(serverName, toolName)
		enrichedError := FormatErrorWithHint(fmt.Sprintf("tool invocation failed: %v", err), serverName, toolName)
		errResp := NewError(ErrCodeServerError, enrichedError)
		if schema != nil {
			dataBytes, _ := json.Marshal(map[string]interface{}{
				"tool":   toolName,
				"schema": json.RawMessage(schema),
			})
			errResp.Data = dataBytes
		}
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   errResp,
			ID:      req.ID,
		}, nil
	}

	if resp != nil && resp.Error != nil {
		// Forward the upstream JSON-RPC error unchanged (same code and
		// message the upstream server sent, redacted downstream by the
		// firewall middleware). This used to be dropped entirely, leaving
		// the client with a response that had neither result nor error.
		h.logger.Error("invoke_tool: upstream error", "server", serverName, "tool", toolName, "code", resp.Error.Code, "message", resp.Error.Message)
		errResp := &Error{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}
		if schema := h.lookupToolSchema(serverName, toolName); schema != nil {
			dataBytes, marshalErr := json.Marshal(map[string]interface{}{
				"tool":          toolName,
				"schema":        json.RawMessage(schema),
				"upstream_data": rawOrNull(resp.Error.Data),
			})
			if marshalErr == nil {
				errResp.Data = dataBytes
			}
		}
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   errResp,
			ID:      req.ID,
		}, nil
	}

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resp.Result,
		ID:      req.ID,
	}, nil
}

// hasCachedTool reports whether the tool cache lists toolName on
// serverName (found), and whether it holds any tools for that server at all
// (known).
func (h *Handler) hasCachedTool(serverName, toolName string) (found, known bool) {
	h.toolCache.mu.RLock()
	defer h.toolCache.mu.RUnlock()
	tools := h.toolCache.tools[serverName]
	for _, t := range tools {
		if t.Name == toolName {
			return true, true
		}
	}
	return false, len(tools) > 0
}

// rawOrNull returns data unchanged, or a JSON null literal when data is
// empty, so it always marshals to a valid JSON value inside a map.
func rawOrNull(data json.RawMessage) json.RawMessage {
	if len(data) == 0 {
		return json.RawMessage("null")
	}
	return data
}

// parseToolName splits a namespaced tool reference into its server and tool
// parts by matching the longest configured server name that is a prefix of
// fullName, followed by '_' or '.'. See SplitToolName.
func (h *Handler) parseToolName(fullName string) (serverName, toolName string, err error) {
	return SplitToolName(fullName, h.pool.ListServers())
}

func (h *Handler) handlePing(ctx context.Context, req *Request) (*Response, error) {
	result := map[string]string{"status": "ok"}
	resultBytes, _ := json.Marshal(result)

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}, nil
}

// handleShutdown acknowledges a shutdown request. It does not close the
// pool: the front end that received the request owns the shutdown order
// (drain in-flight requests, answer, then close the pools), and closing
// here would kill servers under requests still in flight.
func (h *Handler) handleShutdown(ctx context.Context, req *Request) (*Response, error) {
	result := map[string]string{"status": "shutdown"}
	resultBytes, _ := json.Marshal(result)

	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}, nil
}

func (h *Handler) ResetManifest() {
	h.manifest = nil
}

func parseInputSchema(schema json.RawMessage) (required, optional []ParamInfo) {
	var schemaMap map[string]interface{}
	if err := json.Unmarshal(schema, &schemaMap); err != nil {
		return nil, nil
	}

	properties, ok := schemaMap["properties"].(map[string]interface{})
	if !ok {
		return nil, nil
	}

	var requiredNames []string
	if req, ok := schemaMap["required"].([]interface{}); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				requiredNames = append(requiredNames, s)
			}
		}
	}

	isRequired := make(map[string]bool)
	for _, name := range requiredNames {
		isRequired[name] = true
	}

	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		propMap, ok := properties[name].(map[string]interface{})
		if !ok {
			continue
		}
		typeVal, _ := propMap["type"].(string)
		descVal, _ := propMap["description"].(string)

		param := ParamInfo{
			Name:        name,
			Type:        typeVal,
			IsRequired:  isRequired[name],
			Description: descVal,
		}

		if isRequired[name] {
			required = append(required, param)
		} else {
			optional = append(optional, param)
		}
	}
	return required, optional
}

func formatToolSearchResult(serverName, toolName, description string, required, optional []ParamInfo, maxDescChars int) string {
	return formatToolLine(serverName+"_"+toolName, "", description, required, optional, maxDescChars)
}

// formatToolLine renders one tool as `name [tags]: description [required]
// {optional}`, name being how the client calls it (server_tool, or the
// namespaced name in passthrough and hybrid). tags is the compact
// annotation marker (annotationTags), or "".
func formatToolLine(name, tags, description string, required, optional []ParamInfo, maxDescChars int) string {
	var sb strings.Builder
	sb.WriteString(name)
	if tags != "" {
		sb.WriteString(" ")
		sb.WriteString(tags)
	}
	sb.WriteString(": ")
	sb.WriteString(truncateDescription(description, maxDescChars))

	if len(required) > 0 {
		sb.WriteString(" [")
		for i, p := range required {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(p.Name)
			sb.WriteString(": ")
			sb.WriteString(p.Type)
		}
		sb.WriteString("]")
	}

	if len(optional) > 0 {
		sb.WriteString(" {")
		for i, p := range optional {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(p.Name)
			sb.WriteString(": ")
			sb.WriteString(p.Type)
		}
		sb.WriteString("}")
	}

	return sb.String()
}

func formatTool(tool Tool, serverName string, maxDescChars int) string {
	return formatToolMarked(tool, serverName, maxDescChars, false)
}

// formatToolMarked is formatTool with the "[confirm]" marker of a tool the
// per-tool policy (#314) asks the user about before each call.
func formatToolMarked(tool Tool, serverName string, maxDescChars int, confirm bool) string {
	return formatToolMarkedAs(tool, serverName+"_"+tool.Name, maxDescChars, confirm)
}

// formatToolMarkedAs is formatToolMarked with the name the client calls
// the tool by.
func formatToolMarkedAs(tool Tool, name string, maxDescChars int, confirm bool) string {
	required, optional := parseInputSchema(tool.InputSchema)
	tags := annotationTags(tool)
	if confirm {
		tags = strings.TrimSpace(tags + " [confirm]")
	}
	return formatToolLine(name, tags, tool.Description, required, optional, maxDescChars)
}

// annotationTags is the compact text form of a tool's behavior hints shown
// by list_tools and search_tools: "[read-only]" or "[destructive]" (only an
// explicit destructiveHint: true counts; the spec's implicit default does
// not, or every unannotated tool would be flagged).
func annotationTags(tool Tool) string {
	switch {
	case tool.ReadOnly():
		return "[read-only]"
	case tool.Destructive():
		return "[destructive]"
	default:
		return ""
	}
}

func truncateDescription(description string, maxChars int) string {
	if maxChars <= 0 || len(description) <= maxChars {
		return description
	}
	if maxChars < 3 {
		return description[:maxChars]
	}
	return description[:maxChars-3] + "..."
}

func (h *Handler) lookupToolSchema(serverName, toolName string) json.RawMessage {
	h.toolCache.mu.RLock()
	defer h.toolCache.mu.RUnlock()

	tools, ok := h.toolCache.tools[serverName]
	if !ok {
		return nil
	}

	for _, tool := range tools {
		if tool.Name == toolName {
			return tool.InputSchema
		}
	}
	return nil
}

// toolsToCachedTools converts tools for the persistent tool cache, keeping
// the MCP 2025 metadata (title, outputSchema, annotations, icons, _meta) so
// a restart serves the same tool objects before the first refresh.
func toolsToCachedTools(tools []Tool) []toolstore.CachedTool {
	result := make([]toolstore.CachedTool, len(tools))
	for i, t := range tools {
		ct := toolstore.CachedTool{
			Name:         t.Name,
			Title:        t.Title,
			Description:  t.Description,
			InputSchema:  t.InputSchema,
			OutputSchema: t.OutputSchema,
			Meta:         t.Meta,
		}
		if t.Annotations != nil {
			ct.Annotations, _ = json.Marshal(t.Annotations)
		}
		if len(t.Icons) > 0 {
			ct.Icons, _ = json.Marshal(t.Icons)
		}
		result[i] = ct
	}
	return result
}

// cachedToolsToTools is the inverse of toolsToCachedTools. Metadata that
// no longer decodes is dropped (the next refresh restores it).
func cachedToolsToTools(cached []toolstore.CachedTool) []Tool {
	tools := make([]Tool, len(cached))
	for i, ct := range cached {
		t := Tool{
			Name:         ct.Name,
			Title:        ct.Title,
			Description:  ct.Description,
			InputSchema:  ct.InputSchema,
			OutputSchema: ct.OutputSchema,
			Meta:         ct.Meta,
		}
		if len(ct.Annotations) > 0 {
			var a ToolAnnotations
			if json.Unmarshal(ct.Annotations, &a) == nil {
				t.Annotations = &a
			}
		}
		if len(ct.Icons) > 0 {
			_ = json.Unmarshal(ct.Icons, &t.Icons)
		}
		tools[i] = t
	}
	return tools
}
