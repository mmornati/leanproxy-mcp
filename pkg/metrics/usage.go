package metrics

import (
	"sort"
	"time"
)

// UsageSummary is the usage store's (pkg/usage) view of the tokens every
// front end on this machine recorded, over two windows: today and
// week-to-date. It is computed by pkg/usage (which imports this package,
// so the types live here) and served by /metrics under "usage" and by the
// dashboard. Every number is measured by the proxy and counted with
// Estimator; nothing here is modeled or priced.
type UsageSummary struct {
	// Estimator names the token-counting heuristic of every *_tokens
	// field ("chars/4"), the same one `leanproxy-mcp report` uses.
	Estimator string `json:"estimator"`
	// Today starts at 00:00 UTC of the current day.
	Today UsageWindow `json:"today"`
	// Week starts at 00:00 UTC of the current ISO week's Monday.
	Week UsageWindow `json:"week"`
}

// UsageWindow is the tokens recorded between Since and now, summed over
// every session (front-end process) that recorded something in it. A
// session that started before Since only contributes what it accumulated
// after Since (its latest record minus its last record before Since), so
// today and week-to-date are genuinely different windows.
type UsageWindow struct {
	Since    time.Time `json:"since"`
	Sessions int       `json:"sessions"`

	// OriginalTokens, SavedTokens and SavedPercent are `report`'s
	// total_original_tokens / total_saved_tokens / total_saved_percent for
	// this window: schema savings plus the response governor's.
	OriginalTokens int64   `json:"original_tokens"`
	SavedTokens    int64   `json:"saved_tokens"`
	SavedPercent   float64 `json:"saved_percent"`

	// DiscoveryCalls and DiscoveryTokens are the search_tools / list_tools
	// / list_servers cost (not a saving; not in SavedTokens).
	DiscoveryCalls  int64 `json:"discovery_calls"`
	DiscoveryTokens int64 `json:"discovery_tokens"`

	// ToolCalls is the number of tool results the response governor saw.
	ToolCalls int64 `json:"tool_calls"`

	// TopServer and TopTool are the largest entries of ByServer and ByTool
	// (by response size, OriginalTokens); empty when there are none.
	TopServer string `json:"top_server,omitempty"`
	TopTool   string `json:"top_tool,omitempty"`

	// ByServer and ByTool break tool results down by upstream server and
	// tool, largest response size first. They come from the response
	// governor's per-tool accounting, so they stay empty unless
	// `response.enabled: true` (the same data as `report --by tool|server`).
	ByServer []UsageServer `json:"by_server"`
	ByTool   []UsageTool   `json:"by_tool"`
}

// UsageTool is one tool's results in a window.
type UsageTool struct {
	// Server is the upstream server; empty for a tool the governor did
	// not attribute to a server (and for its "other" overflow bucket).
	Server         string `json:"server"`
	Tool           string `json:"tool"`
	Calls          int64  `json:"calls"`
	OriginalTokens int64  `json:"original_tokens"`
	ReturnedTokens int64  `json:"returned_tokens"`
	SavedTokens    int64  `json:"saved_tokens"`
}

// AvgTokensPerCall is OriginalTokens / Calls (0 without calls).
func (t UsageTool) AvgTokensPerCall() float64 {
	if t.Calls <= 0 {
		return 0
	}
	return float64(t.OriginalTokens) / float64(t.Calls)
}

// UsageServer is one upstream server's tool results in a window.
type UsageServer struct {
	Server         string `json:"server"`
	Tools          int    `json:"tools"`
	Calls          int64  `json:"calls"`
	OriginalTokens int64  `json:"original_tokens"`
	ReturnedTokens int64  `json:"returned_tokens"`
	SavedTokens    int64  `json:"saved_tokens"`
}

// ServerTools returns the window's tools of server, largest first.
func (w UsageWindow) ServerTools(server string) []UsageTool {
	tools := make([]UsageTool, 0)
	for _, t := range w.ByTool {
		if t.Server == server {
			tools = append(tools, t)
		}
	}
	return tools
}

// SetTools sets ByTool from tools and derives ByServer, TopServer and
// TopTool from it, all sorted by OriginalTokens descending (then name, so
// the output is deterministic).
func (w *UsageWindow) SetTools(tools []UsageTool) {
	sort.Slice(tools, func(i, j int) bool {
		if tools[i].OriginalTokens != tools[j].OriginalTokens {
			return tools[i].OriginalTokens > tools[j].OriginalTokens
		}
		if tools[i].Server != tools[j].Server {
			return tools[i].Server < tools[j].Server
		}
		return tools[i].Tool < tools[j].Tool
	})
	byServer := map[string]*UsageServer{}
	for _, t := range tools {
		s, ok := byServer[t.Server]
		if !ok {
			s = &UsageServer{Server: t.Server}
			byServer[t.Server] = s
		}
		s.Tools++
		s.Calls += t.Calls
		s.OriginalTokens += t.OriginalTokens
		s.ReturnedTokens += t.ReturnedTokens
		s.SavedTokens += t.SavedTokens
	}
	servers := make([]UsageServer, 0, len(byServer))
	for _, s := range byServer {
		servers = append(servers, *s)
	}
	sort.Slice(servers, func(i, j int) bool {
		if servers[i].OriginalTokens != servers[j].OriginalTokens {
			return servers[i].OriginalTokens > servers[j].OriginalTokens
		}
		return servers[i].Server < servers[j].Server
	})

	w.ByTool = tools
	w.ByServer = servers
	w.TopTool, w.TopServer = "", ""
	if len(tools) > 0 {
		w.TopTool = tools[0].Tool
		if tools[0].Server != "" {
			w.TopTool = tools[0].Server + "." + tools[0].Tool
		}
	}
	if len(servers) > 0 {
		w.TopServer = servers[0].Server
	}
}
