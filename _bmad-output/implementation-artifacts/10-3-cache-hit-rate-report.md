# Story 10.3: Report cache hit-rate via 'leanproxy cache' command

Status: review

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 10.3 |
| **Key** | leanproxy-10-3 |
| **Epic** | epic-10 — Anthropic Prompt Caching Bridge |
| **Title** | Report cache hit-rate via 'leanproxy cache' command |
| **Related FRs** | FR40 |
| **Related NFRs** | — |
| **Previous Story:** [10.2 inject-cache-breakpoints](10-2-inject-cache-breakpoints.md) |

## User Story

As a user, I want a CLI command that shows Anthropic cache hit rate, tokens saved, and dollar savings, so I can verify the feature is working and quantify impact.

## Acceptance Criteria (BDD Summary)

leanproxy cache -> Markdown table: total requests, cache hits, hit rate %, tokens saved, $ saved (Anthropic pricing). No traffic -> 'No Anthropic traffic observed', exit 0. --json -> JSON to stdout.

## Developer Context

### Technical Notes

cmd/cache.go (NEW): reads from in-memory cache stats; pricing table pkg/cache/pricing.go (NEW); use pkg/reporter for Markdown formatting.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-10-Story-10.3]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: Anthropic Prompt Caching Bridge

## Tasks / Subtasks

- [x] Implement in-memory cache stats tracker (`pkg/cache/stats.go`)
- [x] Implement Anthropic pricing table (`pkg/cache/pricing.go`)
- [x] Add `leanproxy cache stats` CLI subcommand
- [x] Wire up stats tracking in proxy request flow
- [x] Write unit tests for stats tracker
- [x] Write unit tests for pricing table
- [x] Write CLI tests for `cache stats` subcommand
- [x] Verify all tests pass with no regressions

## Dev Agent Record

### Implementation Plan

**Architecture:**
- `pkg/cache/stats.go`: Thread-safe `CacheStatsTracker` with global singleton (parallels `pkg/reporter/cost.go` pattern)
- `pkg/cache/pricing.go`: Anthropic model pricing table with `ModelCost()` and `CalculateTokenSavingsCost()` 
- `cmd/cache.go`: Added `stats` subcommand with `--json` and `--model` flags
- `cmd/serve.go`: Wired `GlobalCacheStatsTracker().RecordRequest()` into `injectBreakpoints()` flow

**Key Decisions:**
- Stats stored in-memory (no persistence), matching NFR4 (in-memory only)
- Pricing: 5 Anthropic models with $/Mtok pricing; default is claude-sonnet-4-20250514
- Cache hit tracking: `RecordRequest()` captures all Anthropic requests with breakpoint status; `RecordCacheHit()` separately tracks known hits
- Hit rate formula: CacheHits / AnthropicRequests (clamped to 1.0)
- Token estimation: len(params) / 4 (1 token ≈ 4 chars heuristic)

### Acceptance Criteria Coverage

| AC | Status | Evidence |
|----|--------|----------|
| `leanproxy cache stats`→Markdown table with requests/hits/rate/tokens/$ | ✅ | `FormatMarkdown()` in stats.go with full metrics table |
| No Anthropic traffic→"No Anthropic traffic observed", exit 0 | ✅ | `HasTraffic()` check in `runCacheStats()` |
| `--json`→JSON to stdout | ✅ | `FormatJSON()` and `--json` flag in stats subcommand |

### Completion Notes

- 29 new tests added across `pkg/cache/` and `cmd/` packages
- All 1200 tests pass (1171 prior + 29 new)
- Backward compatible: existing `leanproxy cache` commands (--list, --server, etc.) unchanged
- New usage: `leanproxy cache stats`, `leanproxy cache stats --json`, `leanproxy cache stats --model claude-3-5-sonnet-20241022`

## File List

- `pkg/cache/stats.go` (NEW) — CacheStatsTracker
- `pkg/cache/pricing.go` (NEW) — Anthropic pricing table
- `pkg/cache/stats_test.go` (NEW) — Stats tracker tests
- `pkg/cache/pricing_test.go` (NEW) — Pricing tests
- `cmd/cache.go` (MODIFIED) — Added stats subcommand
- `cmd/serve.go` (MODIFIED) — Wired up stats tracking
- `cmd/cache_test.go` (MODIFIED) — Added stats subcommand tests

## Change Log

| Date | Change |
|------|--------|
| 2026-06-23 | Initial implementation: stats tracker, pricing, CLI command, serve integration
