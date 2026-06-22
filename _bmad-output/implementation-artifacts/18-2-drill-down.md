# Story 18.2: Per-server / per-tool drill-down

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 18.2 |
| **Key** | leanproxy-18-2 |
| **Epic** | epic-18 — Cost Attribution Web Dashboard |
| **Title** | Per-server / per-tool drill-down |
| **Related FRs** | FR49 |
| **Related NFRs** | NFR13 |
| **Previous Story:** [18.1 web-dashboard](18-1-web-dashboard.md) |

## User Story

As a user, I want to click a server in the dashboard and see its tool-level breakdown, so I can identify which tools drive the most cost.

## Acceptance Criteria (BDD Summary)

Dashboard loaded + click server row -> drill-down: tool name, call count, token count, avg tokens/call, last invoked; sorted by tokens desc default. Date filter (last 7 days) -> all charts/tables update; URL query param reflects filter. 'Show prompts' (opt-in) -> list of prompt hashes + cost; no prompt content, only hashes for privacy.

## Developer Context

### Technical Notes

pkg/dashboard/views/drilldown.html (NEW): HTMX partials for server/tool views; aggregation in pkg/metrics drilldown.go; hash-only prompt view pkg/metrics/prompthash.go.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-18-Story-18.2]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: Cost Attribution Web Dashboard

## File List

- See Technical Notes above
