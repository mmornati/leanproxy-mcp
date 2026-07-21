package metrics

import (
	"sort"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/reporter"
)

type ToolDrillDown struct {
	ToolName      string    `json:"tool_name"`
	CallCount     int64     `json:"call_count"`
	TokenCount    int64     `json:"token_count"`
	AvgTokensCall float64   `json:"avg_tokens_call"`
	LastInvoked   time.Time `json:"last_invoked"`
}

type ServerDrillDown struct {
	ServerName  string          `json:"server_name"`
	Tools       []ToolDrillDown `json:"tools"`
	TotalTokens int64           `json:"total_tokens"`
}

type ToolServerDrillDown struct {
	ToolName    string          `json:"tool_name"`
	Servers     []ToolDrillDown `json:"servers"`
	TotalTokens int64           `json:"total_tokens"`
}

func ServerDrilldown(serverName string, since time.Time) ServerDrillDown {
	toolMap := make(map[string]*ToolDrillDown)
	var total int64

	for _, e := range reporter.GetEntries(since) {
		if e.ServerName != serverName {
			continue
		}
		td, ok := toolMap[e.ToolName]
		if !ok {
			toolMap[e.ToolName] = &ToolDrillDown{ToolName: e.ToolName}
			td = toolMap[e.ToolName]
		}
		td.CallCount++
		td.TokenCount += e.TokenCount
		if e.Timestamp.After(td.LastInvoked) {
			td.LastInvoked = e.Timestamp
		}
		total += e.TokenCount
	}

	tools := make([]ToolDrillDown, 0, len(toolMap))
	for _, td := range toolMap {
		if td.CallCount > 0 {
			td.AvgTokensCall = float64(td.TokenCount) / float64(td.CallCount)
		}
		tools = append(tools, *td)
	}
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].TokenCount > tools[j].TokenCount
	})

	return ServerDrillDown{
		ServerName:  serverName,
		Tools:       tools,
		TotalTokens: total,
	}
}

func ToolDrilldown(toolName string, since time.Time) ToolServerDrillDown {
	serverMap := make(map[string]*ToolDrillDown)
	var total int64

	for _, e := range reporter.GetEntries(since) {
		if e.ToolName != toolName {
			continue
		}
		td, ok := serverMap[e.ServerName]
		if !ok {
			serverMap[e.ServerName] = &ToolDrillDown{ToolName: e.ServerName}
			td = serverMap[e.ServerName]
		}
		td.CallCount++
		td.TokenCount += e.TokenCount
		if e.Timestamp.After(td.LastInvoked) {
			td.LastInvoked = e.Timestamp
		}
		total += e.TokenCount
	}

	servers := make([]ToolDrillDown, 0, len(serverMap))
	for _, td := range serverMap {
		if td.CallCount > 0 {
			td.AvgTokensCall = float64(td.TokenCount) / float64(td.CallCount)
		}
		servers = append(servers, *td)
	}
	sort.Slice(servers, func(i, j int) bool {
		return servers[i].TokenCount > servers[j].TokenCount
	})

	return ToolServerDrillDown{
		ToolName:    toolName,
		Servers:     servers,
		TotalTokens: total,
	}
}
