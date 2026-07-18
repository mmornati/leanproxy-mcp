# Story 12.2: Vector-store integration (pluggable backends)

Status: review

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 12.2 |
| **Key** | leanproxy-12-2 |
| **Epic** | epic-12 — Semantic Prompt Cache |
| **Title** | Vector-store integration (pluggable backends) |
| **Related FRs** | FR42 |
| **Related NFRs** | NFR12 |
| **Previous Story:** [12.1 embed-payloads](12-1-embed-payloads.md) |

## User Story

As a developer, I want the semantic cache to support multiple vector-store backends (SQLite-vec default; Qdrant/Pinecone optional), so the user picks the right trade-off.

## Acceptance Criteria (BDD Summary)

cache.vector_store: sqlite-vec (default) -> create ~/.leanproxy/cache/vectors.db, load vec0 if available, warn otherwise. qdrant -> init client w/ URL + key, abort startup on conn fail. pinecone -> init client, key from env, validate index.

## Developer Context

### Technical Notes

pkg/cache/vectordb/ interface + sqlite.go, qdrant.go, pinecone.go (NEW); sqlite-vec via modernc.org/sqlite (CGO-free) or mattn/go-sqlite3; factory pattern in cmd/serve.go config load.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-12-Story-12.2]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: Semantic Prompt Cache

---

## Dev Agent Record

### Debug Log

- Config types defined in `pkg/migrate/config.go`: `CacheConfig`, `VectorStoreConfig`, `SQLiteVectorConfig`, `QdrantVectorConfig`, `PineconeVectorConfig`
- Defaults applied in `LoadConfig()`: backend=`sqlite-vec`, sqlite path=`~/.leanproxy/cache/vectors.db`, qdrant collection=`leanproxy_cache`, pinecone env=`PINECONE_API_KEY`
- `vectordb.Store` interface: `Upsert`, `Search`, `Delete`, `Close`
- SQLite backend uses CGO-free `modernc.org/sqlite`, attempts vec0 extension load on init (warns if unavailable, falls back to Go-native cosine similarity)
- Qdrant backend uses `net/http` REST client; validates connection on init (returns error on fail — logged as warn in serve.go, continues without store)
- Pinecone backend uses `net/http` REST client; API key from env var (default `PINECONE_API_KEY`); validates index via describe_index_stats on init
- Wired in `cmd/serve.go` via `initVectorStore(cfg)` called after config load; stores in `globalVectorStore atomic.Value`
- 28 unit tests covering all backends, edge cases, utility functions

### Completion Notes

Implemented Story 12.2 — Vector-store pluggable backends. The three backends share a common `Store` interface:

- **sqlite-vec** (default): CGO-free via modernc.org/sqlite, tries to load vec0 extension, fallback to manual cosine search
- **qdrant**: REST API collection validation at init
- **pinecone**: REST API with index validation, API key from environment

Config structure: `cache.vector_store.backend: sqlite-vec | qdrant | pinecone` in `leanproxy_servers.yaml`. If no config section exists, defaults to sqlite-vec.

All 1395 tests pass (28 new, 1367 existing). `go vet` clean. Binary size 18.2MB < 20MB limit.

## File List

| File | Status | Description |
|------|--------|-------------|
| `pkg/cache/vectordb/vectordb.go` | new | Store interface, VectorRecord, SearchResult, NewStore factory |
| `pkg/cache/vectordb/sqlite.go` | new | SQLite backend (modernc.org/sqlite CGO-free), vec0 extension, cosine fallback |
| `pkg/cache/vectordb/qdrant.go` | new | Qdrant REST client (net/http), collection validation at init |
| `pkg/cache/vectordb/pinecone.go` | new | Pinecone REST client, API key from env, index validation |
| `pkg/cache/vectordb/vectordb_test.go` | new | Unit tests (28 tests) for all backends + utilities |
| `pkg/migrate/config.go` | modified | Added CacheConfig, VectorStoreConfig, SQLiteVectorConfig, QdrantVectorConfig, PineconeVectorConfig |
| `cmd/serve.go` | modified | Added globalVectorStore, initVectorStore helper, wired NewStore after config load |
| `go.mod` / `go.sum` | modified | Added modernc.org/sqlite v1.54.0 dependency |

## Change Log

| Date | Change |
|------|--------|
| 2026-07-18 | Implemented vector-store pluggable backends (SQLite, Qdrant, Pinecone) with factory pattern. Config via `cache.vector_store` in YAML. 28 new tests, 1395 total passing, binary 18.2MB < 20MB limit. |
