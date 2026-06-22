# Story 10.1: Detect Anthropic API calls in the proxy stream

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 10.1 |
| **Key** | leanproxy-10-1 |
| **Epic** | epic-10 — Anthropic Prompt Caching Bridge |
| **Title** | Detect Anthropic API calls in the proxy stream |
| **Related FRs** | FR40 |
| **Related NFRs** | NFR9,NFR11 |

## User Story

As a developer, I want LeanProxy to detect when an outgoing request is bound for the Anthropic API, so caching logic is only applied where supported.

## Acceptance Criteria (BDD Summary)

Given an outbound URL matching an Anthropic endpoint -> tag provider=anthropic and log to stderr (NFR9). Non-Anthropic -> tag provider=other, skip caching, no overhead. Multi-provider config -> matcher loads from leanproxy.yaml and reloads on SIGHUP.

## Developer Context

### Technical Notes

pkg/cache/provider_detector.go (NEW): pattern matcher per provider; slog debug; SIGHUP hot-reload via existing cmd/serve.go signal handling.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-10-Story-10.1]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: Anthropic Prompt Caching Bridge

## File List

- See Technical Notes above
