package reporter

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
	Since(time.Time) time.Duration
}

type RealClock struct{}

func (RealClock) Now() time.Time {
	return time.Now()
}

func (RealClock) Since(t time.Time) time.Duration {
	return time.Since(t)
}

var defaultClock Clock = RealClock{}

var globalCostTracker *CostTracker

func init() {
	globalCostTracker = NewCostTracker()
}

func GlobalCostTracker() *CostTracker {
	return globalCostTracker
}

func TrackCost(toolName, serverName string, tokenCount int64) {
	globalCostTracker.Track(toolName, serverName, tokenCount)
}

func TrackCostFromStrings(toolName, serverName, requestJSON, responseJSON string) {
	estimator := &tokenEstimator{ charsPerToken: 4 }
	inputTokens := estimator.EstimateTokens(requestJSON)
	outputTokens := estimator.EstimateTokens(responseJSON)
	totalTokens := inputTokens + outputTokens
	if totalTokens > 0 {
		globalCostTracker.Track(toolName, serverName, int64(totalTokens))
	}
}

type tokenEstimator struct {
	charsPerToken float64
}

func (e *tokenEstimator) EstimateTokens(content string) int {
	if content == "" {
		return 0
	}
	return int(math.Ceil(float64(len(content)) / e.charsPerToken))
}

type ToolCost struct {
	ToolName   string `json:"tool_name"`
	TokenCount int64  `json:"token_count"`
}

type ServerCost struct {
	ServerName string `json:"server_name"`
	TokenCount int64  `json:"token_count"`
}

type CostBreakdown struct {
	ByTool   []ToolCost    `json:"by_tool"`
	ByServer []ServerCost  `json:"by_server"`
	Total    int64         `json:"total"`
	Duration time.Duration `json:"duration"`
}

type CostTracker struct {
	mu        sync.RWMutex
	byTool    map[string]int64
	byServer  map[string]int64
	total     int64
	startTime time.Time
	clock     Clock
}

func NewCostTracker() *CostTracker {
	return newCostTracker(defaultClock)
}

func newCostTracker(clock Clock) *CostTracker {
	return &CostTracker{
		byTool:    make(map[string]int64),
		byServer:  make(map[string]int64),
		startTime: clock.Now(),
		clock:     clock,
	}
}

func (c *CostTracker) Track(toolName, serverName string, tokenCount int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.byTool[toolName] += tokenCount
	c.byServer[serverName] += tokenCount
	c.total += tokenCount
}

func (c *CostTracker) GetBreakdown() CostBreakdown {
	c.mu.RLock()
	defer c.mu.RUnlock()

	byTool := make([]ToolCost, 0, len(c.byTool))
	for toolName, count := range c.byTool {
		byTool = append(byTool, ToolCost{
			ToolName:   toolName,
			TokenCount: count,
		})
	}
	sort.Slice(byTool, func(i, j int) bool {
		return byTool[i].TokenCount > byTool[j].TokenCount
	})

	byServer := make([]ServerCost, 0, len(c.byServer))
	for serverName, count := range c.byServer {
		byServer = append(byServer, ServerCost{
			ServerName: serverName,
			TokenCount: count,
		})
	}
	sort.Slice(byServer, func(i, j int) bool {
		return byServer[i].TokenCount > byServer[j].TokenCount
	})

	return CostBreakdown{
		ByTool:   byTool,
		ByServer: byServer,
		Total:    c.total,
		Duration: c.clock.Since(c.startTime),
	}
}

func (c *CostTracker) FormatCLI(showByTool, showByServer bool) string {
	breakdown := c.GetBreakdown()

	var output string
	output += "=== Token Cost Summary ===\n"
	output += fmt.Sprintf("Total Session Tokens: %d\n", breakdown.Total)
	output += fmt.Sprintf("Session Duration:     %v\n", breakdown.Duration)

	if showByTool || (!showByTool && !showByServer) {
		if len(breakdown.ByTool) > 0 {
			output += "\n=== Token Cost by Tool ===\n"
			for _, tc := range breakdown.ByTool {
				output += fmt.Sprintf("%s: %d tokens\n", tc.ToolName, tc.TokenCount)
			}
		}
	}

	if showByServer || (!showByTool && !showByServer) {
		if len(breakdown.ByServer) > 0 {
			output += "\n=== Token Cost by Server ===\n"
			for _, sc := range breakdown.ByServer {
				output += fmt.Sprintf("%s: %d tokens\n", sc.ServerName, sc.TokenCount)
			}
		}
	}

	return output
}

func (c *CostTracker) FormatJSON() (string, error) {
	breakdown := c.GetBreakdown()
	data, err := json.MarshalIndent(breakdown, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal cost breakdown: %w", err)
	}
	return string(data), nil
}

func (c *CostTracker) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.byTool = make(map[string]int64)
	c.byServer = make(map[string]int64)
	c.total = 0
	c.startTime = c.clock.Now()
}

func (c *CostTracker) GetByTool() map[string]int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]int64)
	for k, v := range c.byTool {
		result[k] = v
	}
	return result
}

func (c *CostTracker) GetByServer() map[string]int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]int64)
	for k, v := range c.byServer {
		result[k] = v
	}
	return result
}

func (c *CostTracker) GetTotal() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.total
}
