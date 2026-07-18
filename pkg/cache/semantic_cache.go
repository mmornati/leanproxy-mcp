package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/cache/vectordb"
)

const (
	DefaultSemanticTTL          = 24 * time.Hour
	DefaultEvictionInterval     = 1 * time.Hour
	DefaultStatsPersistInterval = 30 * time.Second
	SemanticSimilarityThreshold = 0.92
	semanticSearchCandidates    = 5
	vectorDeleteTimeout         = 10 * time.Second
)

type HitType int

const (
	HitMiss HitType = iota
	HitExact
	HitSemantic
)

func (h HitType) String() string {
	switch h {
	case HitExact:
		return "exact"
	case HitSemantic:
		return "semantic"
	default:
		return "miss"
	}
}

type SemanticCacheEntry struct {
	Key        string
	Prompt     string
	ToolName   string
	Response   json.RawMessage
	CreatedAt  time.Time
	AccessedAt time.Time
}

type SemanticCacheResult struct {
	Response   json.RawMessage
	HitType    HitType
	Similarity float64
}

type SemanticCacheStats struct {
	TotalRequests  int64   `json:"total_requests"`
	ExactHits      int64   `json:"exact_hits"`
	SemanticHits   int64   `json:"semantic_hits"`
	Misses         int64   `json:"misses"`
	AvgSimilarity  float64 `json:"avg_similarity"`
	EvictedEntries int64   `json:"evicted_entries"`
}

func (s SemanticCacheStats) HitRate() float64 {
	if s.TotalRequests == 0 {
		return 0.0
	}
	hits := s.ExactHits + s.SemanticHits
	return float64(hits) / float64(s.TotalRequests) * 100
}

func (s SemanticCacheStats) FormatMarkdown() string {
	var b strings.Builder
	b.WriteString("### Semantic Prompt Cache Stats\n\n")
	b.WriteString("| Metric | Value |\n")
	b.WriteString("|--------|-------|\n")
	b.WriteString(fmt.Sprintf("| Total Requests | %d |\n", s.TotalRequests))
	b.WriteString(fmt.Sprintf("| Exact Hits | %d |\n", s.ExactHits))
	b.WriteString(fmt.Sprintf("| Semantic Hits | %d |\n", s.SemanticHits))
	b.WriteString(fmt.Sprintf("| Misses | %d |\n", s.Misses))
	b.WriteString(fmt.Sprintf("| Hit Rate | %.2f%% |\n", s.HitRate()))
	b.WriteString(fmt.Sprintf("| Avg Similarity | %.3f |\n", s.AvgSimilarity))
	b.WriteString(fmt.Sprintf("| Evicted Entries | %d |\n", s.EvictedEntries))
	return b.String()
}

func (s SemanticCacheStats) FormatJSON() string {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		errData, _ := json.Marshal(map[string]string{"error": err.Error()})
		return string(errData)
	}
	return string(data)
}

// SemanticCache is a tool-scoped prompt cache with exact-match and
// vector-similarity (semantic) lookup, TTL eviction, and periodic stats
// persistence. It is safe for concurrent use.
//
// Cache-aside contract: Get/Set never fail the caller's operation. Vector
// store errors are logged and surfaced as return values where useful, but a
// degraded (or absent) vector store simply means exact-match-only behavior.
type SemanticCache struct {
	mu       sync.RWMutex
	entries  map[string]*SemanticCacheEntry
	stats    SemanticCacheStats
	ttl      time.Duration
	vectorDB vectordb.Store
	logger   *slog.Logger

	evictInterval   time.Duration
	persistPath     string
	persistInterval time.Duration

	done    chan struct{}
	started atomic.Bool
	stopped atomic.Bool
	loopWg  sync.WaitGroup // evict/persist loop
	jobsWg  sync.WaitGroup // async vector deletes
}

var globalSemanticCache atomic.Pointer[SemanticCache]

func GlobalSemanticCache() *SemanticCache {
	return globalSemanticCache.Load()
}

func SetGlobalSemanticCache(sc *SemanticCache) {
	globalSemanticCache.Store(sc)
}

// cacheKey scopes cache entries by tool so identical prompts to different
// tools never share an entry.
func cacheKey(toolName, prompt string) string {
	h := sha256.Sum256([]byte(toolName + "\x00" + prompt))
	return hex.EncodeToString(h[:])
}

type SemanticCacheOption func(*SemanticCache)

func WithEvictionInterval(d time.Duration) SemanticCacheOption {
	return func(sc *SemanticCache) {
		if d > 0 {
			sc.evictInterval = d
		}
	}
}

func WithStatsPersistPath(path string) SemanticCacheOption {
	return func(sc *SemanticCache) {
		sc.persistPath = path
	}
}

func WithStatsPersistInterval(d time.Duration) SemanticCacheOption {
	return func(sc *SemanticCache) {
		if d > 0 {
			sc.persistInterval = d
		}
	}
}

func NewSemanticCache(vectorDB vectordb.Store, logger *slog.Logger, ttl time.Duration, opts ...SemanticCacheOption) *SemanticCache {
	if logger == nil {
		logger = slog.Default()
	}
	if ttl <= 0 {
		ttl = DefaultSemanticTTL
	}
	sc := &SemanticCache{
		entries:         make(map[string]*SemanticCacheEntry),
		ttl:             ttl,
		vectorDB:        vectorDB,
		logger:          logger,
		evictInterval:   DefaultEvictionInterval,
		persistPath:     DefaultSemanticStatsPath(),
		persistInterval: DefaultStatsPersistInterval,
		done:            make(chan struct{}),
	}
	for _, opt := range opts {
		opt(sc)
	}
	return sc
}

// Start launches the background eviction/persistence loop. It is idempotent:
// calling Start more than once is a no-op.
func (sc *SemanticCache) Start(ctx context.Context) {
	if !sc.started.CompareAndSwap(false, true) {
		return
	}
	sc.loopWg.Add(1)
	go sc.runLoop(ctx)
	sc.logger.Debug("semantic cache loop started", "ttl", sc.ttl)
}

// Stop shuts the cache down: it blocks new background work, stops the loop,
// waits for in-flight vector deletes, and writes a final stats snapshot.
// Get/Set remain usable after Stop (the loop simply no longer runs).
func (sc *SemanticCache) Stop() {
	if !sc.started.CompareAndSwap(true, false) {
		return
	}
	sc.mu.Lock()
	sc.stopped.Store(true)
	close(sc.done)
	sc.mu.Unlock()

	sc.loopWg.Wait()
	sc.jobsWg.Wait()
	sc.persistStats()
	sc.logger.Debug("semantic cache stopped")
}

func (sc *SemanticCache) runLoop(ctx context.Context) {
	defer sc.loopWg.Done()
	evictTicker := time.NewTicker(sc.evictInterval)
	persistTicker := time.NewTicker(sc.persistInterval)
	defer evictTicker.Stop()
	defer persistTicker.Stop()

	for {
		select {
		case <-sc.done:
			return
		case <-ctx.Done():
			return
		case <-evictTicker.C:
			sc.evictExpired()
		case <-persistTicker.C:
			sc.persistStats()
		}
	}
}

// Get looks up a cached response for (toolName, prompt). It checks the exact
// key first, then falls back to vector similarity when an embedding is
// available. Lookups never fail the caller: errors degrade to a miss.
func (sc *SemanticCache) Get(ctx context.Context, prompt, toolName string, embedding []float32) (*SemanticCacheResult, error) {
	key := cacheKey(toolName, prompt)
	miss := &SemanticCacheResult{HitType: HitMiss}

	sc.mu.Lock()
	sc.stats.TotalRequests++
	if entry, ok := sc.entries[key]; ok {
		if sc.entryUsable(entry, toolName) {
			entry.AccessedAt = time.Now()
			sc.stats.ExactHits++
			resp := entry.Response
			sc.mu.Unlock()
			sc.logger.Info("cache=semantic similarity=1.000",
				"hit_type", "exact",
				"tool", toolName,
				"prompt_hash", key[:12])
			return &SemanticCacheResult{Response: resp, HitType: HitExact, Similarity: 1.0}, nil
		}
		sc.removeEntryLocked(key)
		sc.stats.Misses++
		sc.mu.Unlock()
		sc.asyncDeleteVector(key)
		return miss, nil
	}
	sc.mu.Unlock()

	if sc.vectorDB == nil || len(embedding) == 0 {
		sc.recordMiss()
		return miss, nil
	}

	results, err := sc.vectorDB.Search(ctx, embedding, semanticSearchCandidates)
	if err != nil {
		sc.logger.Warn("semantic cache: vector search failed", "error", err)
		sc.recordMiss()
		return miss, nil
	}

	for _, cand := range results {
		if cand.Score < SemanticSimilarityThreshold {
			continue
		}
		if result, ok := sc.trySemanticHit(cand, toolName); ok {
			return result, nil
		}
	}

	sc.recordMiss()
	return miss, nil
}

// trySemanticHit validates a vector candidate against the in-memory entry
// (presence, tool scope, TTL) under a single lock hold. Returns the result
// and true only when the candidate is fully valid.
func (sc *SemanticCache) trySemanticHit(cand vectordb.SearchResult, toolName string) (*SemanticCacheResult, bool) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	entry, ok := sc.entries[cand.Record.ID]
	if !ok {
		// Stale vector: present in the store, absent in memory.
		sc.stats.Misses++
		go sc.asyncDeleteVector(cand.Record.ID)
		return nil, false
	}
	if entry.ToolName != toolName {
		// Similar prompt belonging to a different tool — never serve.
		return nil, false
	}
	if !sc.entryUsable(entry, toolName) {
		sc.removeEntryLocked(cand.Record.ID)
		sc.stats.Misses++
		go sc.asyncDeleteVector(cand.Record.ID)
		return nil, false
	}

	sc.stats.SemanticHits++
	n := float64(sc.stats.SemanticHits)
	sc.stats.AvgSimilarity = (sc.stats.AvgSimilarity*(n-1) + cand.Score) / n
	entry.AccessedAt = time.Now()
	resp := entry.Response

	sc.logger.Info(fmt.Sprintf("cache=semantic similarity=%.3f", cand.Score),
		"hit_type", "semantic",
		"tool", toolName,
		"similarity", cand.Score,
		"prompt_hash", cand.Record.ID[:12])

	return &SemanticCacheResult{Response: resp, HitType: HitSemantic, Similarity: cand.Score}, true
}

func (sc *SemanticCache) entryUsable(entry *SemanticCacheEntry, toolName string) bool {
	return entry.ToolName == toolName && time.Since(entry.CreatedAt) <= sc.ttl
}

// removeEntryLocked deletes an entry and accounts the eviction.
// Caller must hold sc.mu.
func (sc *SemanticCache) removeEntryLocked(key string) {
	delete(sc.entries, key)
	sc.stats.EvictedEntries++
}

func (sc *SemanticCache) recordMiss() {
	sc.mu.Lock()
	sc.stats.Misses++
	sc.mu.Unlock()
}

// Set stores a response under the tool-scoped key. An empty response is
// rejected. A vector upsert failure is returned as an error but the
// in-memory entry is still stored (exact-match remains available).
func (sc *SemanticCache) Set(ctx context.Context, prompt string, response json.RawMessage, toolName string, embedding []float32) error {
	if len(response) == 0 {
		return fmt.Errorf("semantic cache: response must not be empty")
	}

	key := cacheKey(toolName, prompt)
	now := time.Now()
	entry := &SemanticCacheEntry{
		Key:        key,
		Prompt:     prompt,
		ToolName:   toolName,
		Response:   response,
		CreatedAt:  now,
		AccessedAt: now,
	}

	var upsertErr error
	if sc.vectorDB != nil && len(embedding) > 0 {
		rec := vectordb.VectorRecord{
			ID:     key,
			Vector: embedding,
			Metadata: map[string]string{
				"tool_name": toolName,
			},
		}
		if err := sc.vectorDB.Upsert(ctx, rec); err != nil {
			sc.logger.Warn("semantic cache: vector upsert failed", "tool", toolName, "error", err)
			upsertErr = fmt.Errorf("semantic cache: vector upsert: %w", err)
		}
	}

	sc.mu.Lock()
	sc.entries[key] = entry
	sc.mu.Unlock()

	return upsertErr
}

func (sc *SemanticCache) PurgeTool(toolName string) int {
	sc.mu.Lock()
	var ids []string
	for key, entry := range sc.entries {
		if entry.ToolName == toolName {
			ids = append(ids, key)
			delete(sc.entries, key)
		}
	}
	sc.mu.Unlock()

	sc.asyncDeleteVector(ids...)

	if len(ids) > 0 {
		sc.logger.Info("semantic cache purged", "tool", toolName, "count", len(ids))
	}
	return len(ids)
}

func (sc *SemanticCache) PurgeAll() int {
	sc.mu.Lock()
	count := len(sc.entries)
	ids := make([]string, 0, count)
	for key := range sc.entries {
		ids = append(ids, key)
	}
	sc.entries = make(map[string]*SemanticCacheEntry)
	sc.mu.Unlock()

	sc.asyncDeleteVector(ids...)

	if count > 0 {
		sc.logger.Info("semantic cache purged all", "count", count)
	}
	return count
}

func (sc *SemanticCache) Stats() SemanticCacheStats {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.stats
}

func (sc *SemanticCache) Len() int {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return len(sc.entries)
}

func (sc *SemanticCache) evictExpired() {
	sc.mu.Lock()
	now := time.Now()
	var victims []string
	for key, entry := range sc.entries {
		if now.Sub(entry.CreatedAt) > sc.ttl {
			victims = append(victims, key)
			delete(sc.entries, key)
		}
	}
	if len(victims) > 0 {
		sc.stats.EvictedEntries += int64(len(victims))
	}
	sc.mu.Unlock()

	if len(victims) > 0 {
		sc.logger.Debug("semantic cache eviction", "evicted", len(victims))
		sc.asyncDeleteVector(victims...)
	}
}

// asyncDeleteVector deletes vector records in the background. The goroutine
// is tracked so Stop() waits for it, and it is suppressed once the cache is
// stopped to avoid racing vector-store Close during shutdown.
func (sc *SemanticCache) asyncDeleteVector(ids ...string) {
	if sc.vectorDB == nil || len(ids) == 0 {
		return
	}
	sc.mu.Lock()
	if sc.stopped.Load() {
		sc.mu.Unlock()
		return
	}
	sc.jobsWg.Add(1)
	sc.mu.Unlock()

	go func() {
		defer sc.jobsWg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), vectorDeleteTimeout)
		defer cancel()
		if err := sc.vectorDB.Delete(ctx, ids...); err != nil {
			sc.logger.Warn("semantic cache: vector delete failed", "count", len(ids), "error", err)
		}
	}()
}
