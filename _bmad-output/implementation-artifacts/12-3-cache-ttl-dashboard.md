---
baseline_commit: 2fcc14f825f9b8e3994d5193beb29e0d1acee433
---

# Story 12.3: TTL, invalidation, and hit/miss dashboard

Status: done

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 12.3 |
| **Key** | leanproxy-12-3 |
| **Epic** | epic-12 — Semantic Prompt Cache |
| **Title** | TTL, invalidation, and hit/miss dashboard |
| **Related FRs** | FR42 |
| **Related NFRs** | NFR9 |
| **Previous Story:** [12.2 vector-store-pluggable](12-2-vector-store-pluggable.md) |

## User Story

As a user, I want cache entries to expire, schema changes to invalidate affected entries, and a hit-rate dashboard, so the cache is correct and observable.

## Acceptance Criteria (BDD Summary)

Entry > TTL (default 24h) -> treat as miss, write fresh after response. Tool schema change (registry refresh) -> purge all entries for tool + log count. leanproxy cache --semantic -> table: total, exact hits, semantic hits, misses, hit rate %, avg similarity. Cache hit -> log 'cache=semantic similarity=X' to stderr (NFR9), increment counter.

## Developer Context

### Technical Notes

Extend pkg/cache/cache.go with TTL eviction goroutine; hook pkg/registry schema refresh; extend cmd/cache.go with --semantic flag; pkg/reporter for Markdown table.

### File Structure

New files listed in technical notes; modify existing files only where required.

### Architecture Compliance

- camelCase Go, kebab-case CLI flags
- log/slog to stderr; errors wrapped with fmt.Errorf %w
- Static binary <20MB; Homebrew + curl|sh install preserved
- Backward compatibility: existing endpoints and flags unchanged

### Testing Requirements

- Unit tests for all new exported functions
- Integration tests for any HTTP/MCP wire changes
- Benchmark for any new hot path (target <1ms p95 overhead unless otherwise stated)
- gosec clean for any new server code (Epic 16)

## References

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-12-Story-12.3]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: Semantic Prompt Cache

## File List

- `pkg/cache/semantic_cache.go` (new) — Semantic cache: tool-scoped keys, TTL eviction, exact/semantic hits, ctx-aware I/O, lifecycle (Start/Stop), tracked background deletes, stats accounting
- `pkg/cache/semantic_persist.go` (new) — Stats snapshot persistence to `~/.leanproxy/cache/semantic-stats.json` (atomic write, 30s interval + on Stop) for cross-process CLI dashboard
- `pkg/cache/semantic_cache_test.go` (new) — 20 tests + 2 benchmarks (Get exact ~1.06µs/op)
- `pkg/cache/semantic_persist_test.go` (new) — Snapshot round-trip, missing-file, Stop-persists-final
- `cmd/cache.go` (modified) — `--semantic` flag reads persisted snapshot; JSON-valid in all branches; mutual exclusion with other action flags
- `cmd/cache_test.go` (modified) — `--semantic` flag, markdown/JSON/unavailable dashboard tests
- `pkg/registry/feed.go` (modified) — `OnSync` hook fires only on real content change (SHA-256 of entries); panic-isolated invocation
- `pkg/registry/feed_test.go` (modified) — OnSync change-detection and panic-recovery tests
- `cmd/serve.go` (modified) — Semantic cache init with server ctx; lookup/store wired into all four request handlers; Stop on shutdown before vector-store Close; feed-sync purge on real change

## Change Log

- Added `SemanticCache` type with configurable TTL (default 24h), background eviction goroutine, exact-match fast path, and vector-similarity semantic matching
- Added `SemanticCacheStats` with total/exact/semantic/miss counts, hit rate %, and average similarity — supports `FormatMarkdown()` and `FormatJSON()` output
- Added `GlobalSemanticCache()` / `SetGlobalSemanticCache()` singleton for cross-package access
- Added `PurgeTool()` and `PurgeAll()` methods for cache invalidation
- Added `OnSync` hook to `FeedFetcher` — called after successful registry sync with parsed entries
- Added `--semantic` flag to `leanproxy cache` command — displays semantic cache hit/miss dashboard
- Added cache hit logging to stderr: `cache=semantic similarity=X` with hit type and tool name
- Wired semantic cache purge into registry feed refresh in `serve.go`

## Dev Agent Record

### Implementation Notes

All four acceptance criteria are satisfied:

1. **TTL eviction**: Entries exceeding TTL (default 24h) are treated as misses in `Get()` and cleaned up by a background eviction goroutine running every hour
2. **Registry schema invalidation**: `FeedFetcher.OnSync` callback purges all semantic cache entries when registry feed refreshes, with logged count
3. **`leanproxy cache --semantic` dashboard**: Shows table with Total Requests, Exact Hits, Semantic Hits, Misses, Hit Rate %, Avg Similarity, Evicted Entries
4. **Cache hit logging**: On exact hit: `cache=semantic similarity=1.000`; on semantic hit: `cache=semantic similarity=<score>` — both logged to stderr via slog

### Design Decisions

- Semantic cache lives in `pkg/cache/` as part of the existing cache package
- Uses SHA-256 prompt hashing for O(1) exact-match lookup
- Vector similarity search delegates to `vectordb.Store` (cosine similarity, threshold 0.92)
- Thread-safe with `sync.RWMutex` for concurrent access
- Global singleton mirrors existing patterns (`GlobalCacheStatsTracker`, `globalCostTracker`, etc.)

### Verification

- `go build ./...` — success
- `go vet ./...` — no issues
- `go test ./...` — 1416 tests passing across 26 packages (14 new semantic cache tests)

## Review Findings

### Decision Needed (resolved 2026-07-18)

- [x] [Review][Decision] Cache never populated — **RESOLVED: wired into request path**. `semanticCacheLookup`/`semanticCacheStore` hook all four proxy handlers (`handleSingleRequest`, `handleSingleRequestAsync`, both batch handlers) in cmd/serve.go; embeddings come from the embedder pool with a 2s timeout; lifecycle/listing methods are denylisted. [cmd/serve.go]
- [x] [Review][Decision] Dashboard unreachable cross-process — **RESOLVED: persist stats to file**. Server writes `~/.leanproxy/cache/semantic-stats.json` every 30s + on shutdown; CLI reads the snapshot. [pkg/cache/semantic_persist.go, cmd/cache.go]
- [x] [Review][Decision] Hourly purge defeated TTL — **RESOLVED: purge only on real change**. FeedFetcher hashes feed entries and fires `OnSync` only when content differs from the previous sync. [pkg/registry/feed.go]

### Patch (all applied 2026-07-18)

- [x] [Review][Patch] Cross-tool cache poisoning — cache key now `sha256(toolName + "\x00" + prompt)`; hits re-verify ToolName; cross-tool semantic candidates rejected [pkg/cache/semantic_cache.go]
- [x] [Review][Patch] slog `%.3f` literal — message built with fmt.Sprintf before logging [pkg/cache/semantic_cache.go]
- [x] [Review][Patch] `Stop()` 1h hang — done-channel select in loop; Start idempotent; Stop waits loop+jobs, persists final snapshot; wired into SIGINT/SIGTERM handler before vector-store Close [pkg/cache/semantic_cache.go, cmd/serve.go]
- [x] [Review][Patch] `context.Background()` on I/O — Get/Set take ctx; vector deletes use bounded 10s timeout; request-path embedding bounded to 2s [pkg/cache/semantic_cache.go, cmd/serve.go]
- [x] [Review][Patch] onSync panic kills process — recover wrapper in invokeOnSync [pkg/registry/feed.go]
- [x] [Review][Patch] Untracked delete goroutines — tracked via jobsWg, suppressed after Stop, waited in Stop [pkg/cache/semantic_cache.go]
- [x] [Review][Patch] TOCTOU in semantic path — entry presence/tool/TTL re-validated under single lock hold (trySemanticHit); dead delete removed [pkg/cache/semantic_cache.go]
- [x] [Review][Patch] `Set()` always-nil error — vector upsert failure now returned (wrapped, %w) while in-memory entry is kept [pkg/cache/semantic_cache.go]
- [x] [Review][Patch] EvictedEntries accounting — lazy Get-path deletions now counted via removeEntryLocked [pkg/cache/semantic_cache.go]
- [x] [Review][Patch] Inconsistent sync/async vector deletes — all deletes route through tracked asyncDeleteVector [pkg/cache/semantic_cache.go]
- [x] [Review][Patch] Avg Similarity row conditional — row always emitted (0.000 when no semantic hits) [pkg/cache/semantic_cache.go]
- [x] [Review][Patch] `--semantic --json` plain-text branches — all branches emit valid JSON [cmd/cache.go]
- [x] [Review][Patch] `--semantic` silent precedence — MarkFlagsMutuallyExclusive with clear/list/search/location and server [cmd/cache.go]
- [x] [Review][Patch] nil response accepted — Set rejects empty response with error [pkg/cache/semantic_cache.go]
- [x] [Review][Patch] Mock Store contract — Search sorts by score desc and honors k [pkg/cache/semantic_cache_test.go]
- [x] [Review][Patch] Global state pollution — TestGlobalSemanticCache saves/restores via t.Cleanup [pkg/cache/semantic_cache_test.go]
- [x] [Review][Patch] Weak eviction assertion — now exact `== 2` [pkg/cache/semantic_cache_test.go]
- [x] [Review][Patch] Missing tests/benchmark — added Start/Stop lifecycle (prompt-stop assertion), double-start/stop, HitType.String, HitRate, cross-tool isolation (exact+semantic), nil-response rejection, snapshot round-trip, Stop-persists-final, FeedFetcher OnSync change-detection + panic-recovery, --semantic markdown/JSON/unavailable cmd tests, BenchmarkSemanticCacheGetExact (~1.06µs/op) + BenchmarkSemanticCacheSet [pkg/cache, pkg/registry, cmd]
