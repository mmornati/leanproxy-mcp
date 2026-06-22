# Story 10.3: Report cache hit-rate via 'leanproxy cache' command

Status: ready-for-dev

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

## File List

- See Technical Notes above
