package reporter

import (
	"crypto/sha256"
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

func GetEntries(since time.Time) []CallLogEntry {
	return globalCostTracker.GetEntries(since)
}

func TrackCostFromStrings(toolName, serverName, requestJSON, responseJSON string) {
	estimator := &tokenEstimator{charsPerToken: 4}
	inputTokens := estimator.EstimateTokens(requestJSON)
	outputTokens := estimator.EstimateTokens(responseJSON)
	totalTokens := inputTokens + outputTokens
	if totalTokens > 0 {
		hash := promptHash(requestJSON, responseJSON)
		globalCostTracker.TrackWithPromptHash(toolName, serverName, int64(totalTokens), hash, defaultClock)
	}
}

func promptHash(requestJSON, responseJSON string) string {
	h := sha256.Sum256([]byte(requestJSON + "|" + responseJSON))
	return fmt.Sprintf("%x", h[:])
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
	mu           sync.RWMutex
	byTool       map[string]int64
	byServer     map[string]int64
	serverTool   map[ServerToolKey]*ServerToolStat
	promptHashes map[ServerToolKey]map[string]int64
	callLog      []CallLogEntry
	total        int64
	startTime    time.Time
	clock        Clock
}

type ServerToolKey struct {
	ServerName string
	ToolName   string
}

type ServerToolStat struct {
	TokenCount  int64     `json:"token_count"`
	CallCount   int64     `json:"call_count"`
	LastInvoked time.Time `json:"last_invoked"`
}

type CallLogEntry struct {
	ToolName   string
	ServerName string
	TokenCount int64
	Timestamp  time.Time
}

func NewCostTracker() *CostTracker {
	return newCostTracker(defaultClock)
}

func newCostTracker(clock Clock) *CostTracker {
	return &CostTracker{
		byTool:       make(map[string]int64),
		byServer:     make(map[string]int64),
		serverTool:   make(map[ServerToolKey]*ServerToolStat),
		promptHashes: make(map[ServerToolKey]map[string]int64),
		callLog:      make([]CallLogEntry, 0),
		startTime:    clock.Now(),
		clock:        clock,
	}
}

const maxCallLogEntries = 10000

func (c *CostTracker) Track(toolName, serverName string, tokenCount int64) {
	c.TrackAt(toolName, serverName, tokenCount, c.clock.Now())
}

func (c *CostTracker) TrackAt(toolName, serverName string, tokenCount int64, ts time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.trackLocked(toolName, serverName, tokenCount, ts, "")
}

func (c *CostTracker) TrackWithPromptHash(toolName, serverName string, tokenCount int64, hash string, clock Clock) {
	ts := clock.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.trackLocked(toolName, serverName, tokenCount, ts, hash)
}

func (c *CostTracker) trackLocked(toolName, serverName string, tokenCount int64, ts time.Time, hash string) {
	c.byTool[toolName] += tokenCount
	c.byServer[serverName] += tokenCount
	c.total += tokenCount

	key := ServerToolKey{ServerName: serverName, ToolName: toolName}
	stat, ok := c.serverTool[key]
	if !ok {
		stat = &ServerToolStat{}
		c.serverTool[key] = stat
	}
	stat.TokenCount += tokenCount
	stat.CallCount++
	if ts.After(stat.LastInvoked) {
		stat.LastInvoked = ts
	}

	if hash != "" {
		if c.promptHashes[key] == nil {
			c.promptHashes[key] = make(map[string]int64)
		}
		c.promptHashes[key][hash] += tokenCount
	}

	c.callLog = append(c.callLog, CallLogEntry{
		ToolName:   toolName,
		ServerName: serverName,
		TokenCount: tokenCount,
		Timestamp:  ts,
	})
	if len(c.callLog) > maxCallLogEntries {
		c.callLog = c.callLog[len(c.callLog)-maxCallLogEntries:]
	}
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

type NamedServerToolStat struct {
	ToolName    string    `json:"tool_name"`
	ServerName  string    `json:"server_name"`
	TokenCount  int64     `json:"token_count"`
	CallCount   int64     `json:"call_count"`
	LastInvoked time.Time `json:"last_invoked"`
}

func (c *CostTracker) GetServerToolStats(serverName string) []NamedServerToolStat {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var result []NamedServerToolStat
	for key, stat := range c.serverTool {
		if key.ServerName == serverName {
			result = append(result, NamedServerToolStat{
				ToolName:    key.ToolName,
				ServerName:  key.ServerName,
				TokenCount:  stat.TokenCount,
				CallCount:   stat.CallCount,
				LastInvoked: stat.LastInvoked,
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].TokenCount > result[j].TokenCount
	})
	return result
}

func (c *CostTracker) GetToolServerStats(toolName string) []NamedServerToolStat {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var result []NamedServerToolStat
	for key, stat := range c.serverTool {
		if key.ToolName == toolName {
			result = append(result, NamedServerToolStat{
				ToolName:    key.ToolName,
				ServerName:  key.ServerName,
				TokenCount:  stat.TokenCount,
				CallCount:   stat.CallCount,
				LastInvoked: stat.LastInvoked,
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].TokenCount > result[j].TokenCount
	})
	return result
}

func (c *CostTracker) GetPromptHashes() map[string]int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]int64)
	for _, hashes := range c.promptHashes {
		for h, v := range hashes {
			result[h] += v
		}
	}
	return result
}

func (c *CostTracker) GetEntries(since time.Time) []CallLogEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if since.IsZero() {
		result := make([]CallLogEntry, len(c.callLog))
		copy(result, c.callLog)
		return result
	}

	var result []CallLogEntry
	for _, e := range c.callLog {
		if !e.Timestamp.Before(since) {
			result = append(result, e)
		}
	}
	return result
}

func (c *CostTracker) GetPromptHashesForServerTool(server, tool string) map[string]int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := ServerToolKey{ServerName: server, ToolName: tool}
	result := make(map[string]int64, len(c.promptHashes[key]))
	for h, v := range c.promptHashes[key] {
		result[h] = v
	}
	return result
}

func (c *CostTracker) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.byTool = make(map[string]int64)
	c.byServer = make(map[string]int64)
	c.serverTool = make(map[ServerToolKey]*ServerToolStat)
	c.promptHashes = make(map[ServerToolKey]map[string]int64)
	c.callLog = make([]CallLogEntry, 0)
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
