package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/mmornati/leanproxy-mcp/pkg/errors"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
)

// Relay of server-initiated messages (issue #308).
//
// Upstream → client requests (the pool hands them to HandleServerRequest):
//
//   - roots/list: answered from servers[].roots when configured, otherwise
//     relayed;
//   - sampling/createMessage: refused (-32601) unless servers[].allow_sampling
//     is true; every relayed request is logged;
//   - elicitation/create: relayed, with "[<server>] " prepended to the
//     message so the user always knows which server is asking;
//   - anything else: -32601 (ping is answered by the pool itself).
//
// A request is only relayed to a client that declared the matching
// capability in initialize and whose front end can carry server-to-client
// requests (ClientSession.EnableRequests); otherwise the upstream gets
// -32601 at once. The client is chosen as follows:
//
//  1. the session carried by ctx (an HTTP upstream that sends its request
//     on the response stream of the call that triggered it: ctx is that
//     call's context);
//  2. otherwise the most recent session with a call in flight to that
//     server (a stdio upstream, or an HTTP upstream using its GET stream,
//     cannot say which call a request belongs to);
//  3. otherwise, when no call to that server is in flight at all, the only
//     open session that can take the request. When several could, none is
//     picked: a request must never reach a client that did not start it.
//
// The request goes to the client under an id of the session's own (see
// ClientSession.Request); the pool keeps the upstream's id for the answer.
// Params are secret-redacted (and, for sampling and elicitation, checked
// by the prompt-injection guard) before they reach the client; the
// client's answer is redacted before it reaches the upstream.
//
// Upstream → client notifications (HandleServerNotification):
//
//   - notifications/progress: routed to the client whose call carried the
//     token, with its original token (BeginUpstreamCall gives every call a
//     proxy token, unique across servers and clients);
//   - notifications/resources/updated: namespaced and sent to the sessions
//     subscribed to that resource;
//   - notifications/elicitation/complete: sent to the client that received
//     the URL-mode elicitation;
//   - anything else is dropped.
//
// A cancellation is handled where the request lives: the pool cancels a
// relayed request's context when its upstream cancels it (the session then
// sends the MCP cancel notification to the client), and a client cancel
// ends the handler's context, which makes the pool send the cancel
// notification upstream.

const (
	methodSamplingCreateMessage = pool.MethodSamplingCreateMessage
	methodElicitationCreate     = pool.MethodElicitationCreate
	methodRootsList             = pool.MethodRootsList

	// capability names a client declares for each relayed request.
	capabilitySampling    = "sampling"
	capabilityElicitation = "elicitation"
	capabilityRoots       = "roots"

	// progressTokenPrefix starts every progress token the proxy gives an
	// upstream call.
	progressTokenPrefix = "lp-progress-"

	// unsubscribeTimeout bounds the best-effort resources/unsubscribe sent
	// when the last subscriber of a resource disconnects.
	unsubscribeTimeout = 5 * time.Second
)

// Root is one entry of a roots/list result.
type Root struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

// RelayPolicy is the per-server policy for server-to-client requests.
type RelayPolicy struct {
	// AllowSampling relays sampling/createMessage (servers[].allow_sampling).
	AllowSampling bool
	// Roots, when non-nil, answers roots/list without asking the client
	// (servers[].roots).
	Roots []Root
}

// upstreamCall is one call in flight to an upstream on behalf of a client.
type upstreamCall struct {
	session *ClientSession
	// route is the response route of the client request that made the
	// call (WithResponseRoute), or nil.
	route any
}

// progressRoute maps a proxy progress token back to its client.
type progressRoute struct {
	server  string
	session *ClientSession
	token   json.RawMessage
}

// resourceKey identifies one upstream resource.
type resourceKey struct {
	server, uri string
}

// relayState is the handler's relay bookkeeping.
type relayState struct {
	mu       sync.Mutex
	policies map[string]RelayPolicy
	firewall *Firewall
	// calls lists the in-flight calls per server, oldest first.
	calls map[string][]*upstreamCall
	// progress maps a proxy progress token to its client.
	progress map[string]progressRoute
	// elicitations maps "<server>\x00<elicitationId>" of a URL-mode
	// elicitation to the session that received it.
	elicitations map[string]*ClientSession
	// subscriptions maps a subscribed upstream resource to its
	// subscribers and the URI each one used.
	subscriptions map[resourceKey]map[*ClientSession]string
	seq           atomic.Uint64
}

// SetRelayPolicy sets the server-to-client request policy of one server.
func (h *Handler) SetRelayPolicy(server string, p RelayPolicy) {
	h.relay.mu.Lock()
	defer h.relay.mu.Unlock()
	if h.relay.policies == nil {
		h.relay.policies = make(map[string]RelayPolicy)
	}
	h.relay.policies[server] = p
}

// ConfigureRelay sets every server's policy from its config
// (allow_sampling, roots).
func (h *Handler) ConfigureRelay(servers []*migrate.ServerConfig) {
	for _, srv := range servers {
		if srv == nil {
			continue
		}
		p := RelayPolicy{AllowSampling: srv.AllowSampling}
		if len(srv.Roots) > 0 {
			p.Roots = make([]Root, 0, len(srv.Roots))
			for _, r := range srv.Roots {
				p.Roots = append(p.Roots, Root{URI: r.URI, Name: r.Name})
			}
		}
		h.SetRelayPolicy(srv.Name, p)
	}
}

// SetRelayFirewall installs the firewall applied to relayed traffic: its
// redaction on everything relayed in both directions, its injection guard
// on sampling and elicitation requests.
func (h *Handler) SetRelayFirewall(fw *Firewall) {
	h.relay.mu.Lock()
	defer h.relay.mu.Unlock()
	h.relay.firewall = fw
}

// AttachUpstreamRelay makes the handler receive what the upstreams initiate
// (server-to-client requests, progress, resource updates) until ctx ends.
func (h *Handler) AttachUpstreamRelay(ctx context.Context) {
	src, ok := h.pool.(pool.ServerMessageSource)
	if !ok {
		return
	}
	src.SetServerMessageHandler(h)
	context.AfterFunc(ctx, func() { src.SetServerMessageHandler(nil) })
}

var _ pool.ServerMessageHandler = (*Handler)(nil)

func (h *Handler) relayPolicy(server string) RelayPolicy {
	h.relay.mu.Lock()
	defer h.relay.mu.Unlock()
	return h.relay.policies[server]
}

func (h *Handler) relayFirewall() *Firewall {
	h.relay.mu.Lock()
	defer h.relay.mu.Unlock()
	return h.relay.firewall
}

// BeginUpstreamCall registers a call to server made on behalf of the client
// session in ctx, so the server's requests can be routed to that client,
// and gives it a progress token of the proxy's own: when clientParams (the
// client's request params) carry _meta.progressToken, the returned params
// are upstreamParams with _meta.progressToken replaced by a token unique
// across servers and clients, and the server's progress notifications for
// it reach this client only, with its original token. The returned func
// ends the call; it must be called once the upstream answered.
func (h *Handler) BeginUpstreamCall(ctx context.Context, server string, clientParams, upstreamParams json.RawMessage) (json.RawMessage, func()) {
	session := ClientSessionFrom(ctx)
	if session == nil {
		return upstreamParams, func() {}
	}
	call := &upstreamCall{session: session, route: ResponseRouteFrom(ctx)}
	var proxyToken string
	if token := progressToken(clientParams); token != nil {
		proxyToken = progressTokenPrefix + strconv.FormatUint(h.relay.seq.Add(1), 10)
		if out, ok := withProgressToken(upstreamParams, proxyToken); ok {
			upstreamParams = out
		} else {
			proxyToken = ""
		}
		if proxyToken != "" {
			h.relay.mu.Lock()
			if h.relay.progress == nil {
				h.relay.progress = make(map[string]progressRoute)
			}
			h.relay.progress[proxyToken] = progressRoute{server: server, session: session, token: token}
			h.relay.mu.Unlock()
		}
	}

	h.relay.mu.Lock()
	if h.relay.calls == nil {
		h.relay.calls = make(map[string][]*upstreamCall)
	}
	h.relay.calls[server] = append(h.relay.calls[server], call)
	h.relay.mu.Unlock()

	var once sync.Once
	return upstreamParams, func() {
		once.Do(func() {
			if proxyToken != "" {
				// The upstream wrote its last progress before its
				// answer: let it reach the client before the response.
				session.flushQueued()
			}
			h.relay.mu.Lock()
			defer h.relay.mu.Unlock()
			calls := h.relay.calls[server]
			for i, c := range calls {
				if c == call {
					calls = append(calls[:i:i], calls[i+1:]...)
					break
				}
			}
			if len(calls) == 0 {
				delete(h.relay.calls, server)
			} else {
				h.relay.calls[server] = calls
			}
			if proxyToken != "" {
				delete(h.relay.progress, proxyToken)
			}
		})
	}
}

// progressToken returns params._meta.progressToken, or nil.
func progressToken(params json.RawMessage) json.RawMessage {
	if len(params) == 0 {
		return nil
	}
	var p struct {
		Meta struct {
			ProgressToken json.RawMessage `json:"progressToken"`
		} `json:"_meta"`
	}
	if json.Unmarshal(params, &p) != nil {
		return nil
	}
	t := p.Meta.ProgressToken
	if len(t) == 0 || string(t) == "null" {
		return nil
	}
	return t
}

// withProgressToken returns params with _meta.progressToken set to token
// (other _meta members and params members are kept).
func withProgressToken(params json.RawMessage, token string) (json.RawMessage, bool) {
	members := map[string]json.RawMessage{}
	if len(params) > 0 && string(params) != "null" {
		if json.Unmarshal(params, &members) != nil || members == nil {
			return nil, false
		}
	}
	meta := map[string]json.RawMessage{}
	if raw, ok := members["_meta"]; ok && string(raw) != "null" {
		if json.Unmarshal(raw, &meta) != nil || meta == nil {
			return nil, false
		}
	}
	encoded, err := json.Marshal(token)
	if err != nil {
		return nil, false
	}
	meta["progressToken"] = encoded
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return nil, false
	}
	members["_meta"] = metaJSON
	out, err := json.Marshal(members)
	if err != nil {
		return nil, false
	}
	return out, true
}

// pickSession chooses the client of a request from server that needs
// capability (see the relay comment at the top of this file).
func (h *Handler) pickSession(ctx context.Context, server, capability string) *ClientSession {
	s, _ := h.pickSessionRoute(ctx, server, capability)
	return s
}

// pickSessionRoute is pickSession that also returns the response route of
// the client request the upstream request belongs to (nil when unknown).
func (h *Handler) pickSessionRoute(ctx context.Context, server, capability string) (*ClientSession, any) {
	can := func(s *ClientSession) bool {
		return s != nil && s.Initialized() && s.AcceptsRequests() && s.HasCapability(capability)
	}
	if s := ClientSessionFrom(ctx); s != nil {
		if can(s) {
			return s, ResponseRouteFrom(ctx)
		}
		return nil, nil
	}

	h.relay.mu.Lock()
	calls := append([]*upstreamCall(nil), h.relay.calls[server]...)
	h.relay.mu.Unlock()
	if len(calls) > 0 {
		for i := len(calls) - 1; i >= 0; i-- {
			if can(calls[i].session) {
				return calls[i].session, calls[i].route
			}
		}
		return nil, nil
	}

	h.sessionsMu.Lock()
	defer h.sessionsMu.Unlock()
	var only *ClientSession
	for s := range h.sessions {
		if !can(s) {
			continue
		}
		if only != nil {
			return nil, nil
		}
		only = s
	}
	return only, nil
}

// HandleServerRequest answers a server-to-client request from an upstream
// (pool.ServerMessageHandler).
func (h *Handler) HandleServerRequest(ctx context.Context, server, method string, params json.RawMessage) (json.RawMessage, *errors.JSONRPCError) {
	if !telemetryActive.Load() {
		return h.handleServerRequest(ctx, server, method, params)
	}
	ctx, span := tracer().Start(ctx, method+" "+server,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(McpMethodNameAttr(method), UpstreamServerNameAttr(server)))
	defer span.End()
	result, rpcErr := h.handleServerRequest(ctx, server, method, params)
	if rpcErr != nil {
		span.SetStatus(codes.Error, rpcErr.Message)
	} else {
		span.SetStatus(codes.Ok, "")
	}
	return result, rpcErr
}

func (h *Handler) handleServerRequest(ctx context.Context, server, method string, params json.RawMessage) (json.RawMessage, *errors.JSONRPCError) {
	policy := h.relayPolicy(server)
	switch method {
	case methodRootsList:
		if policy.Roots != nil {
			result, err := json.Marshal(map[string][]Root{"roots": policy.Roots})
			if err != nil {
				return nil, rpcError(NewError(ErrCodeInternalError, err.Error()))
			}
			return result, nil
		}
		return h.relayToClient(ctx, server, method, params, capabilityRoots)
	case methodSamplingCreateMessage:
		if !policy.AllowSampling {
			h.logger.Warn("refused sampling request: sampling is disabled for this server (set allow_sampling: true to relay it)", "server", server)
			return nil, rpcError(NewError(ErrCodeMethodNotFound, fmt.Sprintf("sampling/createMessage is disabled for server %s (allow_sampling: false)", server)))
		}
		return h.relayToClient(ctx, server, method, params, capabilitySampling)
	case methodElicitationCreate:
		return h.relayToClient(ctx, server, method, params, capabilityElicitation)
	default:
		return nil, rpcError(NewError(ErrCodeMethodNotFound, fmt.Sprintf("Method not found: %s (not supported by leanproxy)", method)))
	}
}

// relayToClient sends a server request to the chosen client and returns its
// (redacted) answer.
func (h *Handler) relayToClient(ctx context.Context, server, method string, params json.RawMessage, capability string) (json.RawMessage, *errors.JSONRPCError) {
	if string(params) == "null" {
		params = nil
	}
	session, route := h.pickSessionRoute(ctx, server, capability)
	if session == nil {
		h.logger.Info("server-to-client request not relayed: no client that declared the capability", "server", server, "method", method, "capability", capability)
		return nil, rpcError(NewError(ErrCodeMethodNotFound, fmt.Sprintf("%s is not supported by the client (no %s capability)", method, capability)))
	}

	var elicitationID string
	if method == methodElicitationCreate {
		var p struct {
			Mode          string `json:"mode"`
			ElicitationID string `json:"elicitationId"`
		}
		_ = json.Unmarshal(params, &p)
		if p.Mode == "url" {
			if !session.HasSubCapability(capabilityElicitation, "url") {
				return nil, rpcError(NewError(ErrCodeMethodNotFound, "URL-mode elicitation is not supported by the client"))
			}
			elicitationID = p.ElicitationID
		}
	}

	fw := h.relayFirewall()
	if fw != nil && (method == methodSamplingCreateMessage || method == methodElicitationCreate) {
		checked, blocked := fw.Injection.CheckServerRequest(ctx, server, method, params)
		if blocked != nil {
			return nil, rpcError(blocked)
		}
		params = checked
	}
	if method == methodElicitationCreate {
		// Spoofing protection: the user always sees which server asks.
		if out, ok := prefixElicitationMessage(params, server); ok {
			params = out
		}
	}
	if fw != nil {
		req := &Request{Params: params}
		if err := fw.Redaction.RedactRequestContext(ctx, req); err != nil {
			h.logger.Warn("server-to-client request could not be redacted, not relaying it", "server", server, "method", method, "error", err)
			return nil, rpcError(NewError(ErrCodeInternalError, RedactionFailedMessage))
		}
		params = req.Params
	}

	client := session.ClientInfo()
	if method == methodSamplingCreateMessage {
		h.logger.Warn("relaying sampling request: the server is using the client's LLM", "server", server, "client", client.Name)
	} else {
		h.logger.Info("relaying server-to-client request", "server", server, "method", method, "client", client.Name)
	}

	if elicitationID != "" {
		h.trackElicitation(server, elicitationID, session)
	}
	reqCtx := ctx
	if route != nil && ResponseRouteFrom(ctx) == nil {
		reqCtx = WithResponseRoute(ctx, route)
	}
	result, rpcErr := session.Request(reqCtx, method, params)
	if elicitationID != "" && (rpcErr != nil || !elicitationAccepted(result)) {
		h.untrackElicitation(server, elicitationID)
	}

	if fw != nil {
		resp := &Response{Result: result, Error: rpcErr}
		if err := fw.Redaction.RedactResponseContext(ctx, resp); err != nil {
			h.logger.Warn("client answer could not be redacted, withholding it", "server", server, "method", method, "error", err)
			return nil, rpcError(NewError(ErrCodeInternalError, ResponseRedactionFailedMessage))
		}
		result, rpcErr = resp.Result, resp.Error
	}
	if rpcErr != nil {
		return nil, rpcError(rpcErr)
	}
	return result, nil
}

// elicitationAccepted reports whether an elicitation result's action is
// "accept" (a URL-mode elicitation then completes later).
func elicitationAccepted(result json.RawMessage) bool {
	var r struct {
		Action string `json:"action"`
	}
	return json.Unmarshal(result, &r) == nil && r.Action == "accept"
}

// prefixElicitationMessage prepends "[<server>] " to params.message.
func prefixElicitationMessage(params json.RawMessage, server string) (json.RawMessage, bool) {
	var members map[string]json.RawMessage
	if json.Unmarshal(params, &members) != nil || members == nil {
		return nil, false
	}
	var msg string
	if raw, ok := members["message"]; ok {
		_ = json.Unmarshal(raw, &msg)
	}
	encoded, err := json.Marshal("[" + server + "] " + msg)
	if err != nil {
		return nil, false
	}
	members["message"] = encoded
	out, err := json.Marshal(members)
	if err != nil {
		return nil, false
	}
	return out, true
}

// rpcError converts a pipeline error to the pool's error type.
func rpcError(e *Error) *errors.JSONRPCError {
	if e == nil {
		return nil
	}
	return &errors.JSONRPCError{Code: e.Code, Message: e.Message, Data: e.Data}
}

func elicitationKey(server, id string) string { return server + "\x00" + id }

func (h *Handler) trackElicitation(server, id string, s *ClientSession) {
	h.relay.mu.Lock()
	defer h.relay.mu.Unlock()
	if h.relay.elicitations == nil {
		h.relay.elicitations = make(map[string]*ClientSession)
	}
	h.relay.elicitations[elicitationKey(server, id)] = s
}

func (h *Handler) untrackElicitation(server, id string) *ClientSession {
	h.relay.mu.Lock()
	defer h.relay.mu.Unlock()
	key := elicitationKey(server, id)
	s := h.relay.elicitations[key]
	delete(h.relay.elicitations, key)
	return s
}

// HandleServerNotification routes an upstream notification to the client it
// belongs to (pool.ServerMessageHandler).
func (h *Handler) HandleServerNotification(ctx context.Context, server, method string, params json.RawMessage) {
	switch method {
	case NotificationProgress:
		h.relayProgress(ctx, server, params)
	case NotificationResourcesUpdated:
		h.relayResourceUpdated(ctx, server, params)
	case NotificationElicitationComplete:
		var p struct {
			ElicitationID string `json:"elicitationId"`
		}
		if json.Unmarshal(params, &p) != nil || p.ElicitationID == "" {
			return
		}
		if s := h.untrackElicitation(server, p.ElicitationID); s != nil {
			h.sendRelayed(ctx, s, method, params)
		}
	default:
		h.logger.Debug("dropping upstream notification", "server", server, "method", method)
	}
}

// relayProgress sends an upstream progress notification to the client
// whose call carried the token, with the client's own token.
func (h *Handler) relayProgress(ctx context.Context, server string, params json.RawMessage) {
	var members map[string]json.RawMessage
	if json.Unmarshal(params, &members) != nil || members == nil {
		return
	}
	var token string
	if json.Unmarshal(members["progressToken"], &token) != nil {
		return
	}
	h.relay.mu.Lock()
	route, ok := h.relay.progress[token]
	h.relay.mu.Unlock()
	if !ok || route.server != server {
		// Unknown token (the call already ended) or another server's
		// token: never broadcast.
		h.logger.Debug("dropping progress notification with an unknown token", "server", server)
		return
	}
	members["progressToken"] = route.token
	out, err := json.Marshal(members)
	if err != nil {
		return
	}
	h.sendRelayed(ctx, route.session, NotificationProgress, out)
}

// sendRelayed redacts a relayed notification's params and queues it for
// the client (see ClientSession.sendQueued).
func (h *Handler) sendRelayed(ctx context.Context, s *ClientSession, method string, params json.RawMessage) {
	if fw := h.relayFirewall(); fw != nil {
		req := &Request{Params: params}
		if err := fw.Redaction.RedactRequestContext(ctx, req); err != nil {
			h.logger.Warn("relayed notification could not be redacted, dropping it", "method", method, "error", err)
			return
		}
		params = req.Params
	}
	if !s.sendQueued(method, params) {
		h.logger.Debug("relayed notification dropped: client gone or not reading", "method", method)
	}
}

// relayResourceUpdated sends notifications/resources/updated to the
// sessions subscribed to the resource, with the URI each one subscribed to.
func (h *Handler) relayResourceUpdated(ctx context.Context, server string, params json.RawMessage) {
	var members map[string]json.RawMessage
	if json.Unmarshal(params, &members) != nil || members == nil {
		return
	}
	var uri string
	if json.Unmarshal(members["uri"], &uri) != nil || uri == "" {
		return
	}
	h.relay.mu.Lock()
	subs := make(map[*ClientSession]string, len(h.relay.subscriptions[resourceKey{server, uri}]))
	for s, clientURI := range h.relay.subscriptions[resourceKey{server, uri}] {
		subs[s] = clientURI
	}
	h.relay.mu.Unlock()
	for s, clientURI := range subs {
		encoded, err := json.Marshal(clientURI)
		if err != nil {
			continue
		}
		members["uri"] = encoded
		out, err := json.Marshal(members)
		if err != nil {
			continue
		}
		h.sendRelayed(ctx, s, NotificationResourcesUpdated, out)
	}
}

// subscribe records that the session in ctx subscribed to an upstream
// resource under clientURI.
func (h *Handler) subscribe(ctx context.Context, server, upstreamURI, clientURI string) {
	s := ClientSessionFrom(ctx)
	if s == nil {
		return
	}
	h.relay.mu.Lock()
	defer h.relay.mu.Unlock()
	if h.relay.subscriptions == nil {
		h.relay.subscriptions = make(map[resourceKey]map[*ClientSession]string)
	}
	key := resourceKey{server, upstreamURI}
	if h.relay.subscriptions[key] == nil {
		h.relay.subscriptions[key] = make(map[*ClientSession]string)
	}
	h.relay.subscriptions[key][s] = clientURI
}

// unsubscribe removes the subscription of the session in ctx and reports
// whether other sessions still subscribe to the resource (then the
// upstream subscription must stay).
func (h *Handler) unsubscribe(ctx context.Context, server, upstreamURI string) (othersRemain bool) {
	s := ClientSessionFrom(ctx)
	h.relay.mu.Lock()
	defer h.relay.mu.Unlock()
	key := resourceKey{server, upstreamURI}
	subs := h.relay.subscriptions[key]
	if s != nil {
		delete(subs, s)
	}
	if len(subs) == 0 {
		delete(h.relay.subscriptions, key)
		return false
	}
	return true
}

// dropRelaySession forgets a disconnected session: its progress routes,
// URL-mode elicitations and subscriptions. An upstream subscription left
// without subscribers is unsubscribed in the background.
func (h *Handler) dropRelaySession(s *ClientSession) {
	var orphaned []resourceKey
	h.relay.mu.Lock()
	for token, r := range h.relay.progress {
		if r.session == s {
			delete(h.relay.progress, token)
		}
	}
	for key, es := range h.relay.elicitations {
		if es == s {
			delete(h.relay.elicitations, key)
		}
	}
	for key, subs := range h.relay.subscriptions {
		if _, ok := subs[s]; !ok {
			continue
		}
		delete(subs, s)
		if len(subs) == 0 {
			delete(h.relay.subscriptions, key)
			orphaned = append(orphaned, key)
		}
	}
	h.relay.mu.Unlock()

	for _, key := range orphaned {
		go func(key resourceKey) {
			ctx, cancel := context.WithTimeout(h.backgroundContext(), unsubscribeTimeout)
			defer cancel()
			params, err := json.Marshal(map[string]string{"uri": key.uri})
			if err != nil {
				return
			}
			if _, err := h.pool.SendRequestToServer(ctx, key.server, MethodResourcesUnsubscribe, params, unsubscribeTimeout); err != nil {
				h.logger.Debug("unsubscribe after client disconnect failed", "server", key.server, "error", err)
			}
		}(key)
	}
}

// forwardRootsListChanged tells every upstream whose roots/list is relayed
// that the client's roots changed (the notification is dropped for
// servers answered from a static roots list, and for servers without a
// session yet: they will ask when they start).
func (h *Handler) forwardRootsListChanged() {
	for _, server := range h.pool.ListServers() {
		if h.relayPolicy(server).Roots != nil || h.serverInitializeResult(server) == nil {
			continue
		}
		go func(server string) {
			ctx, cancel := context.WithTimeout(h.backgroundContext(), h.timeoutFor(server))
			defer cancel()
			if err := h.pool.SendServerNotification(ctx, server, NotificationRootsListChanged, nil); err != nil {
				h.logger.Debug("forwarding roots/list_changed failed", "server", server, "error", err)
			}
		}(server)
	}
}
