package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/cache/vectordb"
)

type mockVectorStore struct {
	mu      sync.Mutex
	records map[string]vectordb.VectorRecord
}

func newMockVectorStore() *mockVectorStore {
	return &mockVectorStore{records: make(map[string]vectordb.VectorRecord)}
}

func (m *mockVectorStore) Upsert(_ context.Context, records ...vectordb.VectorRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range records {
		m.records[r.ID] = r
	}
	return nil
}

// Search honors the Store contract: results sorted by score descending and
// limited to k.
func (m *mockVectorStore) Search(_ context.Context, vector []float32, k int) ([]vectordb.SearchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var results []vectordb.SearchResult
	for _, rec := range m.records {
		sim := cosineSim(vector, rec.Vector)
		if sim >= SemanticSimilarityThreshold {
			results = append(results, vectordb.SearchResult{Record: rec, Score: sim})
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if k > 0 && len(results) > k {
		results = results[:k]
	}
	return results, nil
}

func (m *mockVectorStore) Delete(_ context.Context, ids ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		delete(m.records, id)
	}
	return nil
}

func (m *mockVectorStore) Close() error { return nil }

func (m *mockVectorStore) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.records)
}

func cosineSim(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		fa := float64(a[i])
		fb := float64(b[i])
		dot += fa * fb
		na += fa * fa
		nb += fb * fb
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func TestNewSemanticCache(t *testing.T) {
	sc := NewSemanticCache(nil, nil, 0)
	if sc == nil {
		t.Fatal("expected non-nil SemanticCache")
	}
	if sc.ttl != DefaultSemanticTTL {
		t.Errorf("ttl = %v, want %v", sc.ttl, DefaultSemanticTTL)
	}
	if sc.Len() != 0 {
		t.Errorf("Len = %d, want 0", sc.Len())
	}
}

func TestSemanticCache_SetAndGetExactHit(t *testing.T) {
	sc := NewSemanticCache(nil, nil, time.Hour)
	ctx := context.Background()
	response := json.RawMessage(`{"result":"ok"}`)

	if err := sc.Set(ctx, "hello world", response, "test-tool", nil); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	result, err := sc.Get(ctx, "hello world", "test-tool", nil)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if result.HitType != HitExact {
		t.Errorf("HitType = %v, want HitExact", result.HitType)
	}
	if result.Similarity != 1.0 {
		t.Errorf("Similarity = %f, want 1.0", result.Similarity)
	}
	if string(result.Response) != string(response) {
		t.Errorf("Response = %s, want %s", string(result.Response), string(response))
	}
}

func TestSemanticCache_CrossToolIsolation(t *testing.T) {
	sc := NewSemanticCache(nil, nil, time.Hour)
	ctx := context.Background()

	if err := sc.Set(ctx, "status", json.RawMessage(`{"r":"a"}`), "tool-a", nil); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	result, err := sc.Get(ctx, "status", "tool-b", nil)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if result.HitType != HitMiss {
		t.Errorf("same prompt to different tool must miss, got %v", result.HitType)
	}

	// Same prompt, correct tool still hits.
	result, _ = sc.Get(ctx, "status", "tool-a", nil)
	if result.HitType != HitExact {
		t.Errorf("tool-a lookup should hit, got %v", result.HitType)
	}
}

func TestSemanticCache_ExactMiss(t *testing.T) {
	sc := NewSemanticCache(nil, nil, time.Hour)
	result, err := sc.Get(context.Background(), "nonexistent", "test-tool", nil)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if result.HitType != HitMiss {
		t.Errorf("HitType = %v, want HitMiss", result.HitType)
	}
}

func TestSemanticCache_TTLExpiry(t *testing.T) {
	sc := NewSemanticCache(nil, nil, 1*time.Millisecond)
	ctx := context.Background()

	if err := sc.Set(ctx, "hello", json.RawMessage(`{"r":1}`), "tool", nil); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	result, err := sc.Get(ctx, "hello", "tool", nil)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if result.HitType != HitMiss {
		t.Errorf("expected miss after TTL expiry, got %v", result.HitType)
	}
	// Lazy expiry must be accounted.
	if got := sc.Stats().EvictedEntries; got != 1 {
		t.Errorf("EvictedEntries = %d, want 1 after lazy expiry", got)
	}
}

func TestSemanticCache_SetRejectsEmptyResponse(t *testing.T) {
	sc := NewSemanticCache(nil, nil, time.Hour)
	if err := sc.Set(context.Background(), "p", nil, "tool", nil); err == nil {
		t.Error("Set with nil response should return an error")
	}
	if sc.Len() != 0 {
		t.Errorf("Len = %d, want 0 — rejected Set must not store", sc.Len())
	}
}

func TestSemanticCache_PurgeTool(t *testing.T) {
	sc := NewSemanticCache(nil, nil, time.Hour)
	ctx := context.Background()

	sc.Set(ctx, "prompt1", json.RawMessage(`{"r":1}`), "tool-a", nil)
	sc.Set(ctx, "prompt2", json.RawMessage(`{"r":2}`), "tool-a", nil)
	sc.Set(ctx, "prompt3", json.RawMessage(`{"r":3}`), "tool-b", nil)

	if sc.Len() != 3 {
		t.Fatalf("Len = %d, want 3", sc.Len())
	}

	if count := sc.PurgeTool("tool-a"); count != 2 {
		t.Errorf("PurgeTool count = %d, want 2", count)
	}
	if sc.Len() != 1 {
		t.Errorf("Len after purge = %d, want 1", sc.Len())
	}

	result, _ := sc.Get(ctx, "prompt3", "tool-b", nil)
	if result.HitType != HitExact {
		t.Error("tool-b entries should still exist after purging tool-a")
	}
}

func TestSemanticCache_PurgeAll(t *testing.T) {
	sc := NewSemanticCache(nil, nil, time.Hour)
	ctx := context.Background()

	sc.Set(ctx, "a", json.RawMessage(`{"r":1}`), "t1", nil)
	sc.Set(ctx, "b", json.RawMessage(`{"r":2}`), "t2", nil)

	if count := sc.PurgeAll(); count != 2 {
		t.Errorf("PurgeAll count = %d, want 2", count)
	}
	if sc.Len() != 0 {
		t.Errorf("Len after PurgeAll = %d, want 0", sc.Len())
	}
}

func TestSemanticCache_Stats(t *testing.T) {
	sc := NewSemanticCache(nil, nil, time.Hour)
	ctx := context.Background()

	stats := sc.Stats()
	if stats.TotalRequests != 0 {
		t.Errorf("TotalRequests = %d, want 0", stats.TotalRequests)
	}

	sc.Set(ctx, "p1", json.RawMessage(`{"r":1}`), "t1", nil)
	sc.Get(ctx, "p1", "t1", nil)
	sc.Get(ctx, "p1", "t1", nil)
	sc.Get(ctx, "missing", "t1", nil)

	stats = sc.Stats()
	if stats.TotalRequests != 3 {
		t.Errorf("TotalRequests = %d, want 3", stats.TotalRequests)
	}
	if stats.ExactHits != 2 {
		t.Errorf("ExactHits = %d, want 2", stats.ExactHits)
	}
	if stats.Misses != 1 {
		t.Errorf("Misses = %d, want 1", stats.Misses)
	}
}

func TestSemanticCache_SemanticHit(t *testing.T) {
	mock := newMockVectorStore()
	sc := NewSemanticCache(mock, nil, time.Hour)
	ctx := context.Background()

	embed1 := []float32{0.1, 0.2, 0.3, 0.4}
	embed2 := []float32{0.11, 0.21, 0.29, 0.41}

	if err := sc.Set(ctx, "original prompt", json.RawMessage(`{"result":"cached"}`), "test-tool", embed1); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	result, err := sc.Get(ctx, "similar prompt", "test-tool", embed2)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if result.HitType != HitSemantic {
		t.Errorf("HitType = %v, want HitSemantic (similarity=%.4f)", result.HitType, result.Similarity)
	}
	if string(result.Response) != `{"result":"cached"}` {
		t.Errorf("Response = %s, want cached response", string(result.Response))
	}
}

func TestSemanticCache_SemanticHitCrossToolRejected(t *testing.T) {
	mock := newMockVectorStore()
	sc := NewSemanticCache(mock, nil, time.Hour)
	ctx := context.Background()

	embed := []float32{0.1, 0.2, 0.3, 0.4}

	// Entry belongs to tool-a; a similar lookup for tool-b must not serve it.
	if err := sc.Set(ctx, "deploy prod", json.RawMessage(`{"r":"a"}`), "tool-a", embed); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	result, err := sc.Get(ctx, "deploy production", "tool-b", embed)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if result.HitType == HitSemantic {
		t.Error("semantic candidate from another tool must never be served")
	}
}

func TestSemanticCache_SemanticMissLowSimilarity(t *testing.T) {
	mock := newMockVectorStore()
	sc := NewSemanticCache(mock, nil, time.Hour)
	ctx := context.Background()

	sc.Set(ctx, "hello world", json.RawMessage(`{"r":1}`), "test-tool", []float32{1.0, 0.0, 0.0, 0.0})

	result, err := sc.Get(ctx, "goodbye world", "test-tool", []float32{0.0, 1.0, 0.0, 0.0})
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if result.HitType != HitMiss {
		t.Errorf("HitType = %v, want HitMiss for low similarity", result.HitType)
	}
}

func TestSemanticCache_ThreadSafety(t *testing.T) {
	sc := NewSemanticCache(nil, nil, time.Hour)
	ctx := context.Background()

	var wg sync.WaitGroup
	n := 50

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			prompt := fmt.Sprintf("prompt-%d", id)
			sc.Set(ctx, prompt, json.RawMessage(`{"id":`+fmt.Sprintf("%d", id)+`}`), "tool", nil)
		}(i)
	}
	wg.Wait()

	if sc.Len() != n {
		t.Errorf("Len = %d, want %d", sc.Len(), n)
	}

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			prompt := fmt.Sprintf("prompt-%d", id)
			result, _ := sc.Get(ctx, prompt, "tool", nil)
			if result.HitType != HitExact {
				t.Errorf("prompt-%d: expected hit, got %v", id, result.HitType)
			}
		}(i)
	}
	wg.Wait()

	if got := sc.Stats().ExactHits; got != int64(n) {
		t.Errorf("ExactHits = %d, want %d", got, n)
	}
}

func TestSemanticCache_StartStopLifecycle(t *testing.T) {
	sc := NewSemanticCache(nil, nil, time.Hour,
		WithEvictionInterval(10*time.Millisecond),
		WithStatsPersistPath(""),
	)

	sc.Start(context.Background())
	sc.Start(context.Background()) // double-start must be a no-op

	done := make(chan struct{})
	go func() {
		sc.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return promptly — evict loop not responding to shutdown")
	}

	sc.Stop() // double-stop must be a no-op
}

func TestSemanticCache_EvictExpired(t *testing.T) {
	sc := NewSemanticCache(nil, nil, 50*time.Millisecond)
	ctx := context.Background()

	sc.Set(ctx, "old-entry", json.RawMessage(`{"r":1}`), "tool", nil)
	sc.Set(ctx, "another-old", json.RawMessage(`{"r":2}`), "tool", nil)

	time.Sleep(60 * time.Millisecond)

	sc.Set(ctx, "fresh-entry", json.RawMessage(`{"r":3}`), "tool", nil)

	sc.evictExpired()

	if sc.Len() != 1 {
		t.Errorf("Len after eviction = %d, want 1 (only fresh)", sc.Len())
	}

	if got := sc.Stats().EvictedEntries; got != 2 {
		t.Errorf("EvictedEntries = %d, want exactly 2", got)
	}
}

func TestHitType_String(t *testing.T) {
	cases := map[HitType]string{
		HitMiss:     "miss",
		HitExact:    "exact",
		HitSemantic: "semantic",
		HitType(99): "miss",
	}
	for ht, want := range cases {
		if got := ht.String(); got != want {
			t.Errorf("HitType(%d).String() = %q, want %q", int(ht), got, want)
		}
	}
}

func TestSemanticCacheStats_HitRate(t *testing.T) {
	s := SemanticCacheStats{}
	if s.HitRate() != 0.0 {
		t.Errorf("HitRate with no requests = %.2f, want 0", s.HitRate())
	}
	s = SemanticCacheStats{TotalRequests: 10, ExactHits: 5, SemanticHits: 2}
	if got := s.HitRate(); got != 70.0 {
		t.Errorf("HitRate = %.2f, want 70.00", got)
	}
}

func TestSemanticCacheStats_FormatMarkdown(t *testing.T) {
	stats := SemanticCacheStats{
		TotalRequests:  100,
		ExactHits:      50,
		SemanticHits:   30,
		Misses:         20,
		AvgSimilarity:  0.95,
		EvictedEntries: 5,
	}

	md := stats.FormatMarkdown()
	for _, want := range []string{"Total Requests", "80.00%", "Avg Similarity", "0.950", "Evicted Entries"} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown missing %q:\n%s", want, md)
		}
	}

	// Avg Similarity row must always be present, even with zero semantic hits.
	empty := SemanticCacheStats{TotalRequests: 4, Misses: 4}
	if !strings.Contains(empty.FormatMarkdown(), "Avg Similarity") {
		t.Error("Avg Similarity row must be emitted even when SemanticHits == 0")
	}
}

func TestSemanticCacheStats_FormatJSON(t *testing.T) {
	stats := SemanticCacheStats{TotalRequests: 10, ExactHits: 5}

	jsonStr := stats.FormatJSON()
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed["total_requests"] != float64(10) {
		t.Errorf("total_requests = %v, want 10", parsed["total_requests"])
	}
}

func TestGlobalSemanticCache(t *testing.T) {
	prev := GlobalSemanticCache()
	t.Cleanup(func() { SetGlobalSemanticCache(prev) })

	sc := NewSemanticCache(nil, nil, time.Hour)
	SetGlobalSemanticCache(sc)

	if got := GlobalSemanticCache(); got != sc {
		t.Error("GlobalSemanticCache should return the same instance")
	}
}

func BenchmarkSemanticCacheGetExact(b *testing.B) {
	sc := NewSemanticCache(nil, nil, time.Hour)
	ctx := context.Background()
	sc.Set(ctx, "bench prompt", json.RawMessage(`{"result":"ok"}`), "tool", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := sc.Get(ctx, "bench prompt", "tool", nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSemanticCacheSet(b *testing.B) {
	sc := NewSemanticCache(nil, nil, time.Hour)
	ctx := context.Background()
	resp := json.RawMessage(`{"result":"ok"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := sc.Set(ctx, "bench prompt", resp, "tool", nil); err != nil {
			b.Fatal(err)
		}
	}
}
