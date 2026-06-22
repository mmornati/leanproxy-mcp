# Story 15.1: Per-tool model assignment via manifest

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 15.1 |
| **Key** | leanproxy-15-1 |
| **Epic** | epic-15 — Per-Tool Model Router & Local LLM Sidecar |
| **Title** | Per-tool model assignment via manifest |
| **Related FRs** | FR45 |
| **Related NFRs** | NFR12 |

## User Story

As a user, I want to declare a complexity_tier per tool in leanproxy_servers.yaml, so LeanProxy automatically routes the call to the right model.

## Acceptance Criteria (BDD Summary)

Tool entry complexity_tier=low -> route to 'cheap' provider (Haiku, GPT-4o-mini), response returned unchanged. complexity_tier=high -> 'premium' provider. No tier -> default 'medium' (configurable global), debug log records. Disable-able; disabled mode uses single provider, behaves like current proxy (NFR12).

## Developer Context

### Technical Notes

pkg/router/router.go extended w/ tier lookup; leanproxy_servers.yaml schema adds complexity_tier + provider mapping; env-driven provider API keys; new pkg/modelrouter/ package for separation of concerns.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-15-Story-15.1]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: Per-Tool Model Router & Local LLM Sidecar

## File List

- See Technical Notes above
