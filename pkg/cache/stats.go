package cache

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

type CacheStats struct {
	TotalRequests      int64   `json:"total_requests"`
	AnthropicRequests  int64   `json:"anthropic_requests"`
	CacheableRequests  int64   `json:"cacheable_requests"`
	CacheHits          int64   `json:"cache_hits"`
	CacheMisses        int64   `json:"cache_misses"`
	InputTokens        int64   `json:"input_tokens"`
	CachedInputTokens  int64   `json:"cached_input_tokens"`
	TokensSaved int64 `json:"tokens_saved"`
}

func (s *CacheStats) HitRate() float64 {
	if s.AnthropicRequests == 0 {
		return 0.0
	}
	rate := float64(s.CacheHits) / float64(s.AnthropicRequests)
	if rate > 1.0 {
		rate = 1.0
	}
	return rate
}

func (s *CacheStats) EstimatedDollarSavings(model string) float64 {
	return CalculateTokenSavingsCost(model, s.TokensSaved)
}

func (s *CacheStats) HasTraffic() bool {
	return s.AnthropicRequests > 0
}

func (s *CacheStats) FormatMarkdown(model string) string {
	var b strings.Builder

	dollarSavings := s.EstimatedDollarSavings(model)

	b.WriteString("### Anthropic Prompt Cache Stats\n\n")
	b.WriteString("| Metric | Value |\n")
	b.WriteString("|--------|-------|\n")
	b.WriteString(fmt.Sprintf("| Total Requests | %d |\n", s.TotalRequests))
	b.WriteString(fmt.Sprintf("| Anthropic Requests | %d |\n", s.AnthropicRequests))
	b.WriteString(fmt.Sprintf("| Cacheable Requests | %d |\n", s.CacheableRequests))
	b.WriteString(fmt.Sprintf("| Cache Hits | %d |\n", s.CacheHits))
	b.WriteString(fmt.Sprintf("| Cache Hit Rate | %.2f%% |\n", s.HitRate()*100))
	b.WriteString(fmt.Sprintf("| Tokens Saved | %d |\n", s.TokensSaved))
	b.WriteString(fmt.Sprintf("| Est. Dollar Savings | $%.4f |\n", dollarSavings))
	b.WriteString(fmt.Sprintf("| Model | %s |\n", model))

	return b.String()
}

func (s *CacheStats) FormatJSON() string {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		errData, _ := json.Marshal(map[string]string{"error": err.Error()})
		return string(errData)
	}
	return string(data)
}

type CacheStatsTracker struct {
	mu sync.RWMutex

	totalRequests     int64
	anthropicRequests int64
	cacheableRequests int64
	cacheHits         int64
	cacheMisses       int64
	inputTokens       int64
	cachedInputTokens int64
	tokensSaved       int64
}

func NewCacheStatsTracker() *CacheStatsTracker {
	return &CacheStatsTracker{}
}

var globalCacheStatsTracker = NewCacheStatsTracker()

func GlobalCacheStatsTracker() *CacheStatsTracker {
	return globalCacheStatsTracker
}

func (t *CacheStatsTracker) RecordRequest(provider Provider, hasBreakpoint bool, inputTokenEstimate int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.totalRequests++

	if provider != ProviderAnthropic {
		return
	}

	t.anthropicRequests++
	t.inputTokens += inputTokenEstimate

	if hasBreakpoint {
		t.cacheableRequests++
		t.cachedInputTokens += inputTokenEstimate
	}
}

func (t *CacheStatsTracker) RecordCacheHit(tokensSaved int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.cacheHits++
	t.tokensSaved += tokensSaved
}

func (t *CacheStatsTracker) RecordCacheMiss() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.cacheMisses++
}

func (t *CacheStatsTracker) GetStats() CacheStats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return CacheStats{
		TotalRequests:     t.totalRequests,
		AnthropicRequests: t.anthropicRequests,
		CacheableRequests: t.cacheableRequests,
		CacheHits:         t.cacheHits,
		CacheMisses:       t.cacheMisses,
		InputTokens:       t.inputTokens,
		CachedInputTokens: t.cachedInputTokens,
		TokensSaved:       t.tokensSaved,
	}
}

type anthropicUsage struct {
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

type anthropicResponseMeta struct {
	Usage *anthropicUsage `json:"usage"`
}

func ProcessResponse(result json.RawMessage) {
	if len(result) == 0 {
		return
	}
	var meta anthropicResponseMeta
	if err := json.Unmarshal(result, &meta); err != nil || meta.Usage == nil {
		return
	}
	if meta.Usage.CacheReadInputTokens > 0 {
		GlobalCacheStatsTracker().RecordCacheHit(meta.Usage.CacheReadInputTokens)
	} else if meta.Usage.CacheCreationInputTokens > 0 {
		GlobalCacheStatsTracker().RecordCacheMiss()
	}
}

func (t *CacheStatsTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.totalRequests = 0
	t.anthropicRequests = 0
	t.cacheableRequests = 0
	t.cacheHits = 0
	t.cacheMisses = 0
	t.inputTokens = 0
	t.cachedInputTokens = 0
	t.tokensSaved = 0
}
