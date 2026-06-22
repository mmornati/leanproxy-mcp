# Story 17.1: Per-team and per-project budget configuration

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 17.1 |
| **Key** | leanproxy-17-1 |
| **Epic** | epic-17 — Token Budget Governor |
| **Title** | Per-team and per-project budget configuration |
| **Related FRs** | FR48 |
| **Related NFRs** | NFR11 |

## User Story

As a user, I want to set daily/monthly token budgets for teams and projects in config, so spend is governed centrally.

## Acceptance Criteria (BDD Summary)

budgets.teams.<team>.daily: 100000 + request from team -> tokens deducted, in-memory cumulative updated. Sub-budget budgets.teams.<team>.projects.<project>.monthly > 80% -> stderr warn + webhook (if configured). No budget configured -> governor disabled, no overhead (NFR11).

## Developer Context

### Technical Notes

pkg/budget/ governor.go + store.go (NEW): in-memory token buckets keyed by team[/project]; webhook dispatcher pkg/webhook/; config schema leanproxy.yaml budgets: section.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-17-Story-17.1]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: Token Budget Governor

## File List

- See Technical Notes above
