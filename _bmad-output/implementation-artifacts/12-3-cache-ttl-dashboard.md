# Story 12.3: TTL, invalidation, and hit/miss dashboard

Status: ready-for-dev

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

- See Technical Notes above
