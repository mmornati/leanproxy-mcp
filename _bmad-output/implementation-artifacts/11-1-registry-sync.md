# Story 11.1: Subscribe to the MCP Registry feed

Status: done

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 11.1 |
| **Key** | leanproxy-11-1 |
| **Epic** | epic-11 — MCP Registry Mirror & Discovery |
| **Title** | Subscribe to the MCP Registry feed |
| **Related FRs** | FR41 |
| **Related NFRs** | NFR11 |

## User Story

As a developer, I want LeanProxy to fetch the public MCP Registry index and cache it locally, so the user has an up-to-date catalog offline.

## Acceptance Criteria (BDD Summary)

leanproxy marketplace sync -> download to ~/.leanproxy/registry/index.json + record timestamp. Network failure -> preserve cache, error with retry guidance. Cache >24h on start -> stderr notice offering sync. Async sync; startup not blocked (NFR11).

## Developer Context

### Technical Notes

pkg/registry/feed.go (NEW): HTTP GET + JSONL parse; store under UserConfigDir; periodic refresh goroutine; cmd/marketplace_sync.go (NEW).

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-11-Story-11.1]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: MCP Registry Mirror & Discovery

## File List

- See Technical Notes above
