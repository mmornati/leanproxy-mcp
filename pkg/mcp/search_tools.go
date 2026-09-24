package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/toolpin"
	"github.com/mmornati/leanproxy-mcp/pkg/toolsearch"
)

// searchDescChars caps each hit's description in search_tools output.
const searchDescChars = 200

// searchColdWait bounds how long search_tools waits for servers whose tool
// list has never been fetched (proxy just started). Servers whose refresh
// already failed are not waited for.
const searchColdWait = 5 * time.Second

// ConfigureToolSearch replaces the search_tools index with one built from
// opts and seeds it from the current tool cache. Call it before serving;
// the returned index can then be switched to hybrid mode with
// EnableHybrid.
func (h *Handler) ConfigureToolSearch(opts toolsearch.Options) *toolsearch.Index {
	if opts.Logger == nil {
		opts.Logger = h.logger
	}
	ix := toolsearch.New(opts)
	for name, tools := range h.CachedTools() {
		ix.SetServerTools(name, toSearchTools(tools))
	}
	h.search.Store(ix)
	return ix
}

// SearchIndex returns the search_tools index.
func (h *Handler) SearchIndex() *toolsearch.Index { return h.searchIndex() }

func (h *Handler) searchIndex() *toolsearch.Index { return h.search.Load() }

// toSearchTools converts cached tools to index documents.
func toSearchTools(tools []Tool) []toolsearch.Tool {
	out := make([]toolsearch.Tool, len(tools))
	for i, t := range tools {
		out[i] = toolsearch.Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}
	}
	return out
}

// searchToolsArgs are the search_tools arguments.
type searchToolsArgs struct {
	Query  string   `json:"query"`
	K      *float64 `json:"k"`
	Server string   `json:"server"`
}

// handleSearchTools implements the search_tools gateway tool: a ranked
// (BM25, optionally hybrid) search over the cached tools of every server.
// The answer is one text block, one line per hit in the list_tools format
// (server_tool: description [required] {optional}), best first.
func (h *Handler) handleSearchTools(ctx context.Context, req *Request, params ToolsCallParams) (*Response, error) {
	var args searchToolsArgs
	if len(params.Arguments) > 0 {
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			return &Response{JSONRPC: JSONRPCVersion, Error: NewError(ErrCodeInvalidParams, fmt.Sprintf("invalid params: %v", err)), ID: req.ID}, nil
		}
	}
	args.Query = strings.TrimSpace(args.Query)
	args.Server = strings.TrimSpace(args.Server)
	if args.Query == "" {
		return &Response{JSONRPC: JSONRPCVersion, Error: NewError(ErrCodeInvalidParams, "query is required: describe the task, e.g. \"open a pull request\"."), ID: req.ID}, nil
	}
	k := toolsearch.DefaultK
	if args.K != nil {
		k = int(*args.K)
		if k < 1 {
			k = toolsearch.DefaultK
		}
		if k > toolsearch.MaxK {
			k = toolsearch.MaxK
		}
	}

	servers := h.pool.ListServers()
	sort.Strings(servers)
	if args.Server != "" {
		found := false
		for _, s := range servers {
			if s == args.Server {
				found = true
				break
			}
		}
		if !found {
			return textResult(req.ID, fmt.Sprintf("Server '%s' not found. Available servers: %s.", args.Server, strings.Join(servers, ", "))), nil
		}
		servers = []string{args.Server}
	}

	unknown := h.awaitColdServers(ctx, servers)
	h.awaitPinned(ctx, servers)

	// Tool pinning (#310): blocked tools are never ranked (block mode);
	// neither are the tools the per-tool policy (#314) denies.
	pinExclude, policyExclude := h.pinExclude(), h.policyExclude()
	exclude := pinExclude
	switch {
	case pinExclude != nil && policyExclude != nil:
		exclude = func(server, name string) bool { return pinExclude(server, name) || policyExclude(server, name) }
	case policyExclude != nil:
		exclude = policyExclude
	}
	hits := h.searchIndex().Search(ctx, toolsearch.Query{Text: args.Query, K: k, Server: args.Server, Exclude: exclude})
	lines := make([]string, 0, len(hits)+2)
	if w := h.pinSearchWarning(hits); w != "" {
		lines = append(lines, w)
	}
	var structured []StructuredTool
	if h.sessionFor(ctx).AtLeast(ProtocolVersion20250618) {
		structured = make([]StructuredTool, 0, len(hits))
	}
	for _, hit := range hits {
		// The index only keeps what ranking needs; the cache has the full
		// tool (annotations, outputSchema, ...).
		tool, ok := h.cachedTool(hit.Tool.Server, hit.Tool.Name)
		if !ok {
			tool = Tool{Name: hit.Tool.Name, Description: hit.Tool.Description, InputSchema: hit.Tool.InputSchema}
		}
		confirm := h.policyConfirm(hit.Tool.Server, tool)
		lines = append(lines, formatToolMarkedAs(tool, h.exposedToolName(ctx, hit.Tool.Server, hit.Tool.Name), searchDescChars, confirm))
		if structured != nil {
			structured = append(structured, StructuredTool{Server: hit.Tool.Server, Tool: tool, Policy: policyMark(confirm)})
		}
	}
	if len(hits) == 0 {
		lines = append(lines, fmt.Sprintf("No tools match %q. Try other words, or browse with list_servers and list_tools.", args.Query))
	}
	if exclude != nil {
		// How many of the unfiltered top k were hidden, and by what.
		pinned, denied := 0, 0
		for _, hit := range h.searchIndex().Search(ctx, toolsearch.Query{Text: args.Query, K: k, Server: args.Server}) {
			switch {
			case pinExclude != nil && pinExclude(hit.Tool.Server, hit.Tool.Name):
				pinned++
			case policyExclude != nil && policyExclude(hit.Tool.Server, hit.Tool.Name):
				denied++
			}
		}
		if pinned > 0 {
			lines = append(lines, fmt.Sprintf("(%d matching tool(s) hidden by tool pinning until approved; see `leanproxy-mcp tools pins list`)", pinned))
		}
		if denied > 0 {
			lines = append(lines, fmt.Sprintf("(%d matching tool(s) hidden by the leanproxy-mcp policy; see `leanproxy-mcp doctor security`)", denied))
		}
	}
	if len(unknown) > 0 {
		lines = append(lines, fmt.Sprintf("(tools of %s not known yet: unreachable or still starting)", strings.Join(unknown, ", ")))
	}
	h.logger.Info("search_tools completed", "results", len(hits), "k", k, "server", args.Server)
	return toolListingResult(req.ID, strings.Join(lines, "\n"), structured), nil
}

// pinSearchWarning is warn mode's one-line warning about hits that are
// awaiting approval ("" when none).
func (h *Handler) pinSearchWarning(hits []toolsearch.Hit) string {
	p := h.pinner()
	if p == nil || p.Mode() != toolpin.ModeWarn {
		return ""
	}
	parts := make([]string, 0, len(hits))
	servers := map[string]bool{}
	for _, hit := range hits {
		st, _ := p.Check(hit.Tool.Server, hit.Tool.Name)
		identity := p.IdentityPending(hit.Tool.Server)
		if st != toolpin.StatusChanged && st != toolpin.StatusNew && !identity {
			continue
		}
		label := string(st)
		if identity {
			label = "server identity changed"
		}
		parts = append(parts, hit.Tool.Server+"_"+hit.Tool.Name+" ("+label+")")
		servers[hit.Tool.Server] = true
	}
	if len(parts) == 0 {
		return ""
	}
	review := "leanproxy-mcp tools pins diff"
	if len(servers) == 1 {
		for s := range servers {
			review += " " + s
		}
	}
	return fmt.Sprintf("WARNING (tool pinning): not approved since they changed: %s. Review with `%s`.", strings.Join(parts, ", "), review)
}

// cachedTool returns the cached definition of one tool of server.
func (h *Handler) cachedTool(server, name string) (Tool, bool) {
	h.toolCache.mu.RLock()
	defer h.toolCache.mu.RUnlock()
	for _, t := range h.toolCache.tools[server] {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

// awaitColdServers refreshes, in parallel, the servers whose tool list has
// never been fetched and whose refresh has not failed yet, waiting at most
// searchColdWait. It returns the servers whose tools are still unknown.
func (h *Handler) awaitColdServers(ctx context.Context, servers []string) []string {
	var cold []string
	for _, name := range servers {
		if !h.toolsKnown(name) && !h.refreshFailed(name) {
			cold = append(cold, name)
		}
	}
	if len(cold) > 0 {
		wctx, cancel := context.WithTimeout(ctx, searchColdWait)
		var wg sync.WaitGroup
		for _, name := range cold {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				_ = h.RefreshServerTools(wctx, name)
			}(name)
		}
		wg.Wait()
		cancel()
	}
	var unknown []string
	for _, name := range servers {
		if !h.toolsKnown(name) {
			unknown = append(unknown, name)
		}
	}
	return unknown
}

// toolsKnown reports whether the tool cache holds a tool list (possibly
// empty) for server.
func (h *Handler) toolsKnown(server string) bool {
	h.toolCache.mu.RLock()
	defer h.toolCache.mu.RUnlock()
	_, ok := h.toolCache.tools[server]
	return ok
}

// refreshFailed reports whether the last tool refresh of server failed.
func (h *Handler) refreshFailed(server string) bool {
	h.refreshMu.Lock()
	defer h.refreshMu.Unlock()
	return h.refreshErrs[server] != nil
}

// textResult is a tools/call result with one text content block.
func textResult(id interface{}, text string) *Response {
	result, _ := json.Marshal(map[string]interface{}{
		"content": []map[string]string{{"type": "text", "text": text}},
	})
	return &Response{JSONRPC: JSONRPCVersion, Result: result, ID: id}
}
