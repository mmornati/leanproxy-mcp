# Story 12.2: Vector-store integration (pluggable backends)

Status: ready-for-dev

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

## File List

- See Technical Notes above
