package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

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

	hits := h.searchIndex().Search(ctx, toolsearch.Query{Text: args.Query, K: k, Server: args.Server})
	lines := make([]string, 0, len(hits)+1)
	for _, hit := range hits {
		lines = append(lines, formatTool(Tool{Name: hit.Tool.Name, Description: hit.Tool.Description, InputSchema: hit.Tool.InputSchema}, hit.Tool.Server, searchDescChars))
	}
	if len(lines) == 0 {
		lines = append(lines, fmt.Sprintf("No tools match %q. Try other words, or browse with list_servers and list_tools.", args.Query))
	}
	if len(unknown) > 0 {
		lines = append(lines, fmt.Sprintf("(tools of %s not known yet: unreachable or still starting)", strings.Join(unknown, ", ")))
	}
	h.logger.Info("search_tools completed", "results", len(hits), "k", k, "server", args.Server)
	return textResult(req.ID, strings.Join(lines, "\n")), nil
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
