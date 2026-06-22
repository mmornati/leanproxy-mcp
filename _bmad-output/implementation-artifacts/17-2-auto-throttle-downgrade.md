# Story 17.2: Auto-throttle and downgrade at threshold

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 17.2 |
| **Key** | leanproxy-17-2 |
| **Epic** | epic-17 — Token Budget Governor |
| **Title** | Auto-throttle and downgrade at threshold |
| **Related FRs** | soft; integrate with pkg/modelrouter from 15.1; CLI flag in cmd/serve.go; HTTP header parsing in pkg/proxy/proxy.go middleware chain. |
| **Related NFRs** | FR48|NFR13 |
| **Previous Story:** [17.1 budget-config](17-1-budget-config.md) |

## User Story

As a user, I want the governor to throttle or downgrade to a cheaper model when budget is hit, so I never go over budget without consent.

## Acceptance Criteria (BDD Summary)

Team daily 100% consumed + next request -> rejected w/ structured budget_exceeded error, exit 1 (CLI) / JSON-RPC error (gateway). 90% consumed -> allowed but routed to 'budget' provider + stderr notice. hard_cap: true -> reject regardless of model. Soft cap (default) -> downgrade but allow; override per-call via --ignore-budget (CLI) or X-Ignore-Budget header. Budget state in-memory only, not persisted (NFR13).

## Developer Context

### Technical Notes

pkg/budget/actions.go (NEW): policy switch hard

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-17-Story-17.2]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: Token Budget Governor

## File List

- See Technical Notes above
