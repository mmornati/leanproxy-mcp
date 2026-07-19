package metrics

import (
	"sort"

	"github.com/mmornati/leanproxy-mcp/pkg/reporter"
)

type MetricsSnapshot struct {
	ByTool             []ToolMetric  `json:"by_tool"`
	ByServer           []ServerMetric `json:"by_server"`
	TotalSpend         int64         `json:"total_spend"`
	Top5ExpensiveTools []ToolMetric  `json:"top_5_expensive_tools"`
}

type ToolMetric struct {
	ToolName   string `json:"tool_name"`
	TokenCount int64  `json:"token_count"`
}

type ServerMetric struct {
	ServerName string `json:"server_name"`
	TokenCount int64  `json:"token_count"`
}

func Snapshot() MetricsSnapshot {
	tracker := reporter.GlobalCostTracker()
	breakdown := tracker.GetBreakdown()

	byTool := make([]ToolMetric, len(breakdown.ByTool))
	for i, tc := range breakdown.ByTool {
		byTool[i] = ToolMetric{ToolName: tc.ToolName, TokenCount: tc.TokenCount}
	}

	byServer := make([]ServerMetric, len(breakdown.ByServer))
	for i, sc := range breakdown.ByServer {
		byServer[i] = ServerMetric{ServerName: sc.ServerName, TokenCount: sc.TokenCount}
	}

	top5 := make([]ToolMetric, 0, len(byTool))
	top5 = append(top5, byTool...)
	sort.Slice(top5, func(i, j int) bool {
		return top5[i].TokenCount > top5[j].TokenCount
	})
	if len(top5) > 5 {
		top5 = top5[:5]
	}

	return MetricsSnapshot{
		ByTool:             byTool,
		ByServer:           byServer,
		TotalSpend:         breakdown.Total,
		Top5ExpensiveTools: top5,
	}
}
