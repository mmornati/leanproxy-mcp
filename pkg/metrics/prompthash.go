package metrics

import (
	"sort"

	"github.com/mmornati/leanproxy-mcp/pkg/reporter"
)

type PromptHashEntry struct {
	Hash      string `json:"hash"`
	TokenCost int64  `json:"token_cost"`
	Count     int64  `json:"count"`
}

type PromptHashResult struct {
	Hashes []PromptHashEntry `json:"hashes"`
	Total  int64             `json:"total"`
}

func ServerToolPromptHashes(serverName, toolName string) PromptHashResult {
	tracker := reporter.GlobalCostTracker()
	hashes := tracker.GetPromptHashesForServerTool(serverName, toolName)

	entries := make([]PromptHashEntry, 0, len(hashes))
	var total int64
	for h, cost := range hashes {
		entries = append(entries, PromptHashEntry{
			Hash:      h,
			TokenCost: cost,
			Count:     1,
		})
		total += cost
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].TokenCost > entries[j].TokenCost
	})

	return PromptHashResult{
		Hashes: entries,
		Total:  total,
	}
}
