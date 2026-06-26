package cache

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestNewCacheStatsTracker(t *testing.T) {
	tr := NewCacheStatsTracker()
	if tr == nil {
		t.Fatal("expected non-nil tracker")
	}
	stats := tr.GetStats()
	if stats.TotalRequests != 0 {
		t.Errorf("TotalRequests = %d, want 0", stats.TotalRequests)
	}
	if stats.CacheHits != 0 {
		t.Errorf("CacheHits = %d, want 0", stats.CacheHits)
	}
	if stats.CacheMisses != 0 {
		t.Errorf("CacheMisses = %d, want 0", stats.CacheMisses)
	}
}

func TestCacheStatsTracker_RecordRequest(t *testing.T) {
	tr := NewCacheStatsTracker()

	tr.RecordRequest(ProviderAnthropic, true, 1000)
	tr.RecordRequest(ProviderAnthropic, false, 500)
	tr.RecordRequest(ProviderOther, false, 200)

	stats := tr.GetStats()
	if stats.TotalRequests != 3 {
		t.Errorf("TotalRequests = %d, want 3", stats.TotalRequests)
	}
	if stats.AnthropicRequests != 2 {
		t.Errorf("AnthropicRequests = %d, want 2", stats.AnthropicRequests)
	}
	if stats.CacheableRequests != 1 {
		t.Errorf("CacheableRequests = %d, want 1", stats.CacheableRequests)
	}
	// 1000+500 input tokens => 1500 (Other is not anthropic so not counted in input)
	if stats.InputTokens != 1500 {
		t.Errorf("InputTokens = %d, want 1500", stats.InputTokens)
	}
	if stats.CachedInputTokens != 1000 {
		t.Errorf("CachedInputTokens = %d, want 1000", stats.CachedInputTokens)
	}
}

func TestCacheStatsTracker_RecordCacheHit(t *testing.T) {
	tr := NewCacheStatsTracker()

	tr.RecordCacheHit(100)
	tr.RecordCacheHit(200)

	stats := tr.GetStats()
	if stats.CacheHits != 2 {
		t.Errorf("CacheHits = %d, want 2", stats.CacheHits)
	}
	if stats.TokensSaved != 300 {
		t.Errorf("TokensSaved = %d, want 300", stats.TokensSaved)
	}
}

func TestCacheStatsTracker_RecordRequestOnlyAnthropic(t *testing.T) {
	tr := NewCacheStatsTracker()

	tr.RecordRequest(ProviderOther, false, 500)
	tr.RecordRequest(ProviderAnthropic, false, 300)

	stats := tr.GetStats()
	if stats.AnthropicRequests != 1 {
		t.Errorf("AnthropicRequests = %d, want 1", stats.AnthropicRequests)
	}
	if stats.InputTokens != 300 {
		t.Errorf("InputTokens = %d, want 300", stats.InputTokens)
	}
}

func TestCacheStatsTracker_HitRate(t *testing.T) {
	tr := NewCacheStatsTracker()

	tr.RecordRequest(ProviderAnthropic, true, 500)
	tr.RecordCacheHit(200)
	tr.RecordRequest(ProviderAnthropic, true, 300)
	tr.RecordCacheHit(150)

	stats := tr.GetStats()
	// 2 cache hits out of 2 Anthropic requests = 100%
	if stats.HitRate() != 1.0 {
		t.Errorf("HitRate() = %.2f, want 1.0", stats.HitRate())
	}

	tr.RecordRequest(ProviderAnthropic, true, 400)
	stats = tr.GetStats()
	// 2 cache hits out of 3 Anthropic requests = 66.67%
	if stats.HitRate() != 2.0/3.0 {
		t.Errorf("HitRate() = %.2f, want %.2f", stats.HitRate(), 2.0/3.0)
	}
}

func TestCacheStatsTracker_HitRateNoRequests(t *testing.T) {
	tr := NewCacheStatsTracker()
	stats := tr.GetStats()
	if stats.HitRate() != 0.0 {
		t.Errorf("HitRate() = %.2f, want 0.0", stats.HitRate())
	}
}

func TestCacheStatsTracker_EstimatedDollarSavings(t *testing.T) {
	tr := NewCacheStatsTracker()

	tr.RecordCacheHit(1000000)
	tr.RecordRequest(ProviderAnthropic, true, 1000000)

	// Use default model (claude-sonnet-4-20250514)
	// $3/M input, $0.30/M cached input
	// Savings = 1M tokens * ($3 - $0.30) / 1M = $2.70
	stats := tr.GetStats()
	savings := stats.EstimatedDollarSavings("claude-sonnet-4-20250514")
	expected := 2.70
	if savings < expected-0.01 || savings > expected+0.01 {
		t.Errorf("EstimatedDollarSavings() = %.4f, want %.2f", savings, expected)
	}
}

func TestCacheStatsTracker_Empty(t *testing.T) {
	tr := NewCacheStatsTracker()
	stats := tr.GetStats()

	if stats.HasTraffic() {
		t.Error("HasTraffic() should be false with no requests")
	}
}

func TestCacheStatsTracker_HasTraffic(t *testing.T) {
	tr := NewCacheStatsTracker()
	tr.RecordRequest(ProviderAnthropic, false, 100)

	stats := tr.GetStats()
	if !stats.HasTraffic() {
		t.Error("HasTraffic() should be true with Anthropic requests")
	}
}

func TestCacheStatsTracker_HasTrafficOtherOnly(t *testing.T) {
	tr := NewCacheStatsTracker()
	tr.RecordRequest(ProviderOther, false, 100)

	stats := tr.GetStats()
	if stats.HasTraffic() {
		t.Error("HasTraffic() should be false with only Other requests")
	}
}

func TestCacheStatsTracker_Reset(t *testing.T) {
	tr := NewCacheStatsTracker()

	tr.RecordRequest(ProviderAnthropic, true, 100)
	tr.RecordCacheHit(50)
	tr.Reset()

	stats := tr.GetStats()
	if stats.TotalRequests != 0 {
		t.Errorf("TotalRequests after Reset = %d, want 0", stats.TotalRequests)
	}
	if stats.CacheHits != 0 {
		t.Errorf("CacheHits after Reset = %d, want 0", stats.CacheHits)
	}
}

func TestCacheStatsTracker_FormatMarkdown(t *testing.T) {
	tr := NewCacheStatsTracker()

	tr.RecordRequest(ProviderAnthropic, true, 10000)
	tr.RecordRequest(ProviderAnthropic, false, 5000)
	tr.RecordRequest(ProviderAnthropic, true, 8000)
	tr.RecordCacheHit(10000)
	tr.RecordCacheHit(8000)

	stats := tr.GetStats()
	md := stats.FormatMarkdown("claude-sonnet-4-20250514")

	if !strings.Contains(md, "Total Requests") {
		t.Error("Markdown should contain 'Total Requests'")
	}
	if !strings.Contains(md, "3") {
		t.Error("Markdown should show 3 total requests")
	}
	if !strings.Contains(md, "66.67%") {
		t.Errorf("Markdown should show 66.67%% hit rate")
	}
}

func TestCacheStatsTracker_FormatJSON(t *testing.T) {
	tr := NewCacheStatsTracker()

	tr.RecordRequest(ProviderAnthropic, true, 1000)
	tr.RecordCacheHit(500)

	stats := tr.GetStats()
	jsonStr := stats.FormatJSON()

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if parsed["total_requests"] != float64(1) {
		t.Errorf("total_requests = %v, want 1", parsed["total_requests"])
	}
	if parsed["cache_hits"] != float64(1) {
		t.Errorf("cache_hits = %v, want 1", parsed["cache_hits"])
	}
}

func TestCacheStatsTracker_ThreadSafety(t *testing.T) {
	tr := NewCacheStatsTracker()

	var wg sync.WaitGroup
	n := 100

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if id%2 == 0 {
				tr.RecordRequest(ProviderAnthropic, true, 100)
			} else {
				tr.RecordRequest(ProviderAnthropic, false, 50)
			}
		}(i)
	}

	for i := 0; i < n/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.RecordCacheHit(100)
		}()
	}

	wg.Wait()

	stats := tr.GetStats()
	if stats.TotalRequests != int64(n) {
		t.Errorf("TotalRequests = %d, want %d", stats.TotalRequests, n)
	}
}

func TestGlobalCacheStatsTracker(t *testing.T) {
	tr := GlobalCacheStatsTracker()
	if tr == nil {
		t.Fatal("expected non-nil global tracker")
	}
	tr2 := GlobalCacheStatsTracker()
	if tr != tr2 {
		t.Error("GlobalCacheStatsTracker should return the same instance")
	}
}

func TestProcessResponseFor_OnlyAnthropic(t *testing.T) {
	GlobalCacheStatsTracker().Reset()

	result := json.RawMessage(`{"usage":{"cache_read_input_tokens":1500}}`)
	ProcessResponseFor(ProviderOther, result)
	if got := GlobalCacheStatsTracker().GetStats().CacheHits; got != 0 {
		t.Errorf("non-Anthropic response should not record a cache hit, got %d", got)
	}

	ProcessResponseFor(ProviderAnthropic, result)
	if got := GlobalCacheStatsTracker().GetStats().CacheHits; got != 1 {
		t.Errorf("Anthropic response should record exactly one cache hit, got %d", got)
	}
}

func TestProcessResponseFor_BothReadAndCreationCountsOnlyHit(t *testing.T) {
	GlobalCacheStatsTracker().Reset()

	result := json.RawMessage(`{"usage":{"cache_read_input_tokens":80,"cache_creation_input_tokens":20}}`)
	ProcessResponseFor(ProviderAnthropic, result)
	stats := GlobalCacheStatsTracker().GetStats()
	if stats.CacheHits != 1 {
		t.Errorf("expected 1 cache hit when both fields present, got %d", stats.CacheHits)
	}
	if stats.CacheMisses != 0 {
		t.Errorf("cache_creation should not also count as a miss when read is present, got %d misses", stats.CacheMisses)
	}
}

func TestProcessResponseFor_EmptyResult(t *testing.T) {
	GlobalCacheStatsTracker().Reset()
	ProcessResponseFor(ProviderAnthropic, nil)
	ProcessResponseFor(ProviderAnthropic, json.RawMessage(``))
	stats := GlobalCacheStatsTracker().GetStats()
	if stats.CacheHits != 0 || stats.CacheMisses != 0 {
		t.Errorf("empty result should be a no-op, got %+v", stats)
	}
}
