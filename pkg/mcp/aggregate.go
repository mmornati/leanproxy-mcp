package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/pool"
)

// Resources and prompts aggregation (issue #307): the upstream servers'
// resources, resource templates and prompts are merged into one namespaced
// view, and reads/gets are routed back to the owning server.
//
// Namespacing:
//
//   - a resource URI becomes leanproxy://<server>/<original URI>. The
//     original URI is appended verbatim (not escaped), so the mapping
//     round-trips for any URI and a resource template keeps its {variables}:
//     the client expands leanproxy://<server>/file:///{path} and the
//     expansion still starts with the prefix. The server name is
//     path-escaped, so it can never contain the '/' that ends it;
//   - a prompt name becomes <server>.<original name>.

const (
	// ResourceURIScheme is the scheme of namespaced resource URIs.
	ResourceURIScheme = "leanproxy"
	resourceURIPrefix = ResourceURIScheme + "://"

	// maxListPages caps how many pages of one upstream list (tools,
	// resources, templates, prompts) are followed through nextCursor.
	maxListPages = 20

	// initializeCapabilityWait bounds how long initialize waits for
	// upstream sessions whose capabilities are not known yet. A server
	// slower than that is not reflected in the advertised capabilities
	// (they cannot change after initialize), but initialize never waits
	// for a hung server.
	initializeCapabilityWait = time.Second

	// ErrCodeResourceNotFound is the MCP error code for an unknown resource.
	ErrCodeResourceNotFound = -32002
)

// ResourceURI returns the namespaced form of an upstream resource URI (or
// URI template).
func ResourceURI(server, uri string) string {
	return resourceURIPrefix + url.PathEscape(server) + "/" + uri
}

// ParseResourceURI splits a namespaced resource URI into its server and the
// upstream URI. ok is false when u is not a leanproxy:// URI.
func ParseResourceURI(u string) (server, uri string, ok bool) {
	rest, found := strings.CutPrefix(u, resourceURIPrefix)
	if !found {
		return "", "", false
	}
	i := strings.IndexByte(rest, '/')
	if i <= 0 {
		return "", "", false
	}
	server, err := url.PathUnescape(rest[:i])
	if err != nil || server == "" {
		return "", "", false
	}
	return server, rest[i+1:], true
}

// PromptName returns the namespaced name of an upstream prompt.
func PromptName(server, name string) string { return server + "." + name }

// serverCaps is what LeanProxy needs to know about an upstream's
// capabilities.
type serverCaps struct {
	resources bool
	prompts   bool
	// subscribe: the server supports resources/subscribe.
	subscribe bool
}

// parseServerCaps reads the resources/prompts capabilities of an upstream
// InitializeResult.
func parseServerCaps(raw json.RawMessage) serverCaps {
	var caps serverCaps
	var c map[string]json.RawMessage
	if json.Unmarshal(raw, &c) != nil {
		return caps
	}
	present := func(key string) bool {
		v, ok := c[key]
		return ok && len(v) > 0 && string(v) != "null"
	}
	caps.resources = present("resources")
	caps.prompts = present("prompts")
	if caps.resources {
		var r struct {
			Subscribe bool `json:"subscribe"`
		}
		caps.subscribe = json.Unmarshal(c["resources"], &r) == nil && r.Subscribe
	}
	return caps
}

// upstreamCaps returns a server's capabilities from its stored session, or
// asks the pool for the session (which performs the handshake when needed),
// bounded by ctx and the server's timeout. A stdio server that is down is
// not restarted just to learn its capabilities.
func (h *Handler) upstreamCaps(ctx context.Context, name string) serverCaps {
	if res := h.serverInitializeResult(name); res != nil {
		return parseServerCaps(res.Capabilities)
	}
	if state, err := h.pool.GetServerState(name); err == nil && (state == pool.StateError || state == pool.StateStopped) {
		return serverCaps{}
	}
	timeout := h.timeoutFor(name)
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := h.pool.SendRequestToServer(pctx, name, MethodInitialize, nil, timeout)
	if err != nil || resp == nil || resp.Error != nil {
		return serverCaps{}
	}
	if res := h.serverInitializeResult(name); res != nil {
		return parseServerCaps(res.Capabilities)
	}
	var r struct {
		Capabilities json.RawMessage `json:"capabilities"`
	}
	if json.Unmarshal(resp.Result, &r) != nil {
		return serverCaps{}
	}
	return parseServerCaps(r.Capabilities)
}

// upstreamFeatures reports whether at least one upstream serves resources
// and prompts, waiting at most initializeCapabilityWait for sessions not
// established yet (and returning as soon as both are found).
func (h *Handler) upstreamFeatures(ctx context.Context) serverCaps {
	var out serverCaps
	servers := h.pool.ListServers()
	if len(servers) == 0 {
		return out
	}
	wctx, cancel := context.WithTimeout(ctx, initializeCapabilityWait)
	defer cancel()
	results := make(chan serverCaps, len(servers))
	for _, name := range servers {
		go func(name string) { results <- h.upstreamCaps(wctx, name) }(name)
	}
	for range servers {
		select {
		case c := <-results:
			out.resources = out.resources || c.resources
			out.prompts = out.prompts || c.prompts
			out.subscribe = out.subscribe || c.subscribe
			if out.resources && out.prompts && out.subscribe {
				return out
			}
		case <-wctx.Done():
			return out
		}
	}
	return out
}

// listUpstream fetches every page of an upstream list (method answering
// {<key>: [...], nextCursor}), following nextCursor up to maxListPages. The
// whole walk is bounded by the server's timeout. Items are returned raw.
func (h *Handler) listUpstream(ctx context.Context, server, method, key string) ([]json.RawMessage, error) {
	timeout := h.timeoutFor(server)
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var items []json.RawMessage
	cursor := ""
	for page := 0; page < maxListPages; page++ {
		var params json.RawMessage
		if cursor != "" {
			params, _ = json.Marshal(map[string]string{"cursor": cursor})
		}
		resp, err := h.pool.SendRequestToServer(cctx, server, method, params, timeout)
		if err == nil && resp == nil {
			err = fmt.Errorf("no response")
		}
		if err != nil {
			return items, err
		}
		if resp.Error != nil {
			return items, fmt.Errorf("server error: %s", resp.Error.Message)
		}
		if len(resp.Result) == 0 || string(resp.Result) == "null" {
			return items, fmt.Errorf("empty %s result", method)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(resp.Result, &body); err != nil {
			return items, fmt.Errorf("invalid %s result: %w", method, err)
		}
		var list []json.RawMessage
		if raw, ok := body[key]; ok && string(raw) != "null" {
			if err := json.Unmarshal(raw, &list); err != nil {
				return items, fmt.Errorf("invalid %s result: %w", method, err)
			}
		}
		items = append(items, list...)
		var next string
		if raw, ok := body["nextCursor"]; ok {
			_ = json.Unmarshal(raw, &next)
		}
		if next == "" || next == cursor {
			return items, nil
		}
		cursor = next
	}
	h.logger.Warn("upstream list truncated at the page cap", "server", server, "method", method, "pages", maxListPages, "items", len(items))
	return items, nil
}

// serverItems is one upstream's contribution to an aggregated list.
type serverItems struct {
	server string
	items  []json.RawMessage
}

// fanOut lists method on every server whose capabilities pass want, in
// parallel (each bounded by its own timeout), and returns the results in
// server-name order. A failing server contributes what it returned before
// failing; the failure is logged, never returned to the client.
func (h *Handler) fanOut(ctx context.Context, want func(serverCaps) bool, method, key string) []serverItems {
	servers := h.pool.ListServers()
	sort.Strings(servers)
	out := make([]serverItems, len(servers))
	var wg sync.WaitGroup
	for i, name := range servers {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			out[i].server = name
			if !want(h.upstreamCaps(ctx, name)) {
				return
			}
			items, err := h.listUpstream(ctx, name, method, key)
			if err != nil {
				h.logger.Warn("aggregated list: upstream failed", "server", name, "method", method, "error", err)
			}
			out[i].items = items
		}(i, name)
	}
	wg.Wait()
	return out
}

// rewriteField decodes item as an object, replaces the string field key
// with rewrite(value) and re-encodes it; every other member is kept as
// sent. ok is false when item is not an object with that string field.
func rewriteField(item json.RawMessage, key string, rewrite func(string) string) (out json.RawMessage, original string, ok bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(item, &obj) != nil {
		return nil, "", false
	}
	raw, found := obj[key]
	if !found || json.Unmarshal(raw, &original) != nil || original == "" {
		return nil, "", false
	}
	value, err := json.Marshal(rewrite(original))
	if err != nil {
		return nil, "", false
	}
	obj[key] = value
	out, err = json.Marshal(obj)
	if err != nil {
		return nil, "", false
	}
	return out, original, true
}

func hasResources(c serverCaps) bool { return c.resources }
func hasPrompts(c serverCaps) bool   { return c.prompts }

// listResult marshals an aggregated list result ({key: items}).
func listResult(id interface{}, key string, items []json.RawMessage) *Response {
	if items == nil {
		items = []json.RawMessage{}
	}
	result, _ := json.Marshal(map[string]interface{}{key: items})
	return &Response{JSONRPC: JSONRPCVersion, Result: result, ID: id}
}

// handleResourcesList merges every upstream's resources/list, with URIs
// namespaced.
func (h *Handler) handleResourcesList(ctx context.Context, req *Request) (*Response, error) {
	merged := make([]json.RawMessage, 0)
	owners := make(map[string]string)
	ambiguous := make(map[string]bool)
	for _, r := range h.fanOut(ctx, hasResources, MethodResourcesList, "resources") {
		for _, item := range r.items {
			out, uri, ok := rewriteField(item, "uri", func(u string) string { return ResourceURI(r.server, u) })
			if !ok {
				continue
			}
			if prev, dup := owners[uri]; dup && prev != r.server {
				ambiguous[uri] = true
			}
			owners[uri] = r.server
			merged = append(merged, out)
		}
	}
	for uri := range ambiguous {
		delete(owners, uri)
	}
	h.ownersMu.Lock()
	h.resourceOwners = owners
	h.ownersMu.Unlock()
	h.logger.Info("resources/list aggregated", "count", len(merged))
	return listResult(req.ID, "resources", merged), nil
}

// handleResourceTemplatesList merges every upstream's
// resources/templates/list, with URI templates namespaced.
func (h *Handler) handleResourceTemplatesList(ctx context.Context, req *Request) (*Response, error) {
	merged := make([]json.RawMessage, 0)
	for _, r := range h.fanOut(ctx, hasResources, MethodResourcesTemplatesList, "resourceTemplates") {
		for _, item := range r.items {
			if out, _, ok := rewriteField(item, "uriTemplate", func(u string) string { return ResourceURI(r.server, u) }); ok {
				merged = append(merged, out)
			}
		}
	}
	h.logger.Info("resources/templates/list aggregated", "count", len(merged))
	return listResult(req.ID, "resourceTemplates", merged), nil
}

// handlePromptsList merges every upstream's prompts/list, with names
// prefixed by "<server>.".
func (h *Handler) handlePromptsList(ctx context.Context, req *Request) (*Response, error) {
	merged := make([]json.RawMessage, 0)
	for _, r := range h.fanOut(ctx, hasPrompts, MethodPromptsList, "prompts") {
		for _, item := range r.items {
			if out, _, ok := rewriteField(item, "name", func(n string) string { return PromptName(r.server, n) }); ok {
				merged = append(merged, out)
			}
		}
	}
	h.logger.Info("prompts/list aggregated", "count", len(merged))
	return listResult(req.ID, "prompts", merged), nil
}

// knownServer reports whether name is a configured server.
func (h *Handler) knownServer(name string) bool {
	for _, s := range h.pool.ListServers() {
		if s == name {
			return true
		}
	}
	return false
}

// resolveResource maps a client-side resource URI to its server and the
// upstream URI: a leanproxy:// URI names its server; any other URI is
// looked up among the resources the last resources/list returned (a
// resource_link from a tool result carries the upstream's own URI).
func (h *Handler) resolveResource(uri string) (server, upstreamURI string, ok bool) {
	if s, u, found := ParseResourceURI(uri); found {
		if h.knownServer(s) {
			return s, u, true
		}
		return "", "", false
	}
	h.ownersMu.RLock()
	s, found := h.resourceOwners[uri]
	h.ownersMu.RUnlock()
	if found && h.knownServer(s) {
		return s, uri, true
	}
	return "", "", false
}

// decodeParams decodes a request's params as an object (empty params give
// an empty object).
func decodeParams(req *Request) (map[string]json.RawMessage, *Response) {
	params := make(map[string]json.RawMessage)
	if len(req.Params) > 0 && string(req.Params) != "null" {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, errorResponse(req, ErrCodeInvalidParams, fmt.Sprintf("invalid params: %v", err))
		}
	}
	return params, nil
}

// stringParam returns params[key] as a string ("" when absent or not a
// string).
func stringParam(params map[string]json.RawMessage, key string) string {
	var s string
	if raw, ok := params[key]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// forwardRouted sends method with params (key replaced by value) to server
// and returns the upstream's result or JSON-RPC error.
func (h *Handler) forwardRouted(ctx context.Context, req *Request, server string, params map[string]json.RawMessage, key, value string) (*Response, json.RawMessage) {
	encoded, _ := json.Marshal(value)
	params[key] = encoded
	body, err := json.Marshal(params)
	if err != nil {
		return errorResponse(req, ErrCodeInternalError, fmt.Sprintf("encode params: %v", err)), nil
	}
	// Route the server's own requests (and progress, when the client
	// asked for it) during the call to this client.
	body, endCall := h.BeginUpstreamCall(ctx, server, req.Params, body)
	defer endCall()
	resp, err := h.pool.SendRequestToServer(ctx, server, req.Method, body, h.timeoutFor(server))
	if err == nil && resp == nil {
		err = fmt.Errorf("no response")
	}
	if err != nil {
		h.logger.Warn("routed request failed", "method", req.Method, "server", server, "error", err)
		return errorResponse(req, ErrCodeServerError, fmt.Sprintf("%s failed on server %s: %v", req.Method, server, err)), nil
	}
	if resp.Error != nil {
		return &Response{
			JSONRPC: JSONRPCVersion,
			Error:   &Error{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data},
			ID:      req.ID,
		}, nil
	}
	return nil, resp.Result
}

// handleResourcesRead routes resources/read to the owning server and
// namespaces the URIs of the returned contents.
func (h *Handler) handleResourcesRead(ctx context.Context, req *Request) (*Response, error) {
	params, errResp := decodeParams(req)
	if errResp != nil {
		return errResp, nil
	}
	uri := stringParam(params, "uri")
	if uri == "" {
		return errorResponse(req, ErrCodeInvalidParams, "uri is required"), nil
	}
	server, upstreamURI, ok := h.resolveResource(uri)
	if !ok {
		return resourceNotFound(req, uri), nil
	}
	resp, result := h.forwardRouted(ctx, req, server, params, "uri", upstreamURI)
	if resp != nil {
		return resp, nil
	}
	return &Response{JSONRPC: JSONRPCVersion, Result: namespaceContents(result, server), ID: req.ID}, nil
}

// resourceNotFound is the MCP "resource not found" error.
func resourceNotFound(req *Request, uri string) *Response {
	resp := errorResponse(req, ErrCodeResourceNotFound, "Resource not found")
	resp.Error.Data, _ = json.Marshal(map[string]string{"uri": uri})
	return resp
}

// namespaceContents rewrites contents[].uri of a resources/read result to
// the namespaced form. A result it cannot parse is returned unchanged.
func namespaceContents(result json.RawMessage, server string) json.RawMessage {
	var body map[string]json.RawMessage
	if json.Unmarshal(result, &body) != nil {
		return result
	}
	var contents []json.RawMessage
	if json.Unmarshal(body["contents"], &contents) != nil {
		return result
	}
	for i, c := range contents {
		if out, _, ok := rewriteField(c, "uri", func(u string) string { return ResourceURI(server, u) }); ok {
			contents[i] = out
		}
	}
	encoded, err := json.Marshal(contents)
	if err != nil {
		return result
	}
	body["contents"] = encoded
	out, err := json.Marshal(body)
	if err != nil {
		return result
	}
	return out
}

// handleResourcesSubscription routes resources/subscribe and
// resources/unsubscribe to the owning server, and records the client's
// subscription so the server's notifications/resources/updated reach it
// (#308). Clients share one upstream subscription: an unsubscribe is only
// forwarded when no other client still subscribes to the resource.
func (h *Handler) handleResourcesSubscription(ctx context.Context, req *Request) (*Response, error) {
	params, errResp := decodeParams(req)
	if errResp != nil {
		return errResp, nil
	}
	uri := stringParam(params, "uri")
	if uri == "" {
		return errorResponse(req, ErrCodeInvalidParams, "uri is required"), nil
	}
	server, upstreamURI, ok := h.resolveResource(uri)
	if !ok {
		return resourceNotFound(req, uri), nil
	}
	if req.Method == MethodResourcesUnsubscribe && h.unsubscribe(ctx, server, upstreamURI) {
		return &Response{JSONRPC: JSONRPCVersion, Result: json.RawMessage(`{}`), ID: req.ID}, nil
	}
	resp, result := h.forwardRouted(ctx, req, server, params, "uri", upstreamURI)
	if resp != nil {
		return resp, nil
	}
	if req.Method == MethodResourcesSubscribe {
		h.subscribe(ctx, server, upstreamURI, uri)
	}
	return &Response{JSONRPC: JSONRPCVersion, Result: result, ID: req.ID}, nil
}

// splitPromptName splits "<server>.<prompt>" at the longest configured
// server name followed by '.'.
func (h *Handler) splitPromptName(name string) (server, prompt string, ok bool) {
	servers := h.pool.ListServers()
	sort.Slice(servers, func(i, j int) bool { return len(servers[i]) > len(servers[j]) })
	for _, s := range servers {
		if rest, found := strings.CutPrefix(name, s+"."); found && rest != "" {
			return s, rest, true
		}
	}
	return "", "", false
}

// handlePromptsGet routes prompts/get to the server named by the prompt's
// prefix, with the upstream's own prompt name.
func (h *Handler) handlePromptsGet(ctx context.Context, req *Request) (*Response, error) {
	params, errResp := decodeParams(req)
	if errResp != nil {
		return errResp, nil
	}
	name := stringParam(params, "name")
	if name == "" {
		return errorResponse(req, ErrCodeInvalidParams, "name is required"), nil
	}
	server, prompt, ok := h.splitPromptName(name)
	if !ok {
		return errorResponse(req, ErrCodeInvalidParams, fmt.Sprintf("unknown prompt %q: prompt names are <server>.<prompt>, see prompts/list", name)), nil
	}
	resp, result := h.forwardRouted(ctx, req, server, params, "name", prompt)
	if resp != nil {
		return resp, nil
	}
	return &Response{JSONRPC: JSONRPCVersion, Result: result, ID: req.ID}, nil
}

// notifyListChangedFor tells every client session that the aggregated
// resource and/or prompt list changed because server's session did (a
// restart or reconnect), for the features that server serves.
func (h *Handler) notifyListChangedFor(server string) {
	res := h.serverInitializeResult(server)
	if res == nil {
		return
	}
	caps := parseServerCaps(res.Capabilities)
	if caps.resources {
		h.notifySessions(NotificationResourcesListChanged)
	}
	if caps.prompts {
		h.notifySessions(NotificationPromptsListChanged)
	}
}
