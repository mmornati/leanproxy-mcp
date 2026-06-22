# Story 10.2: Auto-inject cache_control: ephemeral breakpoints

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 10.2 |
| **Key** | leanproxy-10-2 |
| **Epic** | epic-10 — Anthropic Prompt Caching Bridge |
| **Title** | Auto-inject cache_control: ephemeral breakpoints |
| **Related FRs** | balanced |
| **Related NFRs** | aggressive).|FR40|NFR11 |
| **Previous Story:** [10.1 detect-anthropic-calls](10-1-detect-anthropic-calls.md) |

## User Story

As a developer, I want LeanProxy to identify stable segments (system prompt, tool definitions) and inject Anthropic cache breakpoints, so the upstream cache hits on subsequent requests.

## Acceptance Criteria (BDD Summary)

Anthropic request w/ system + tools -> append cache_control:{type:ephemeral} to last tool and last system block. User-supplied cache_control -> skip + log debug. Strategy=off -> no injection. Strategy=aggressive (default) -> both; balanced -> largest block only. <1ms p95 overhead (NFR11).

## Developer Context

### Technical Notes

pkg/cache/breakpoint_injector.go (NEW): post-parse JSON transformer preserving user blocks; config-driven strategy enum (off

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-10-Story-10.2]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: Anthropic Prompt Caching Bridge

## File List

- See Technical Notes above
