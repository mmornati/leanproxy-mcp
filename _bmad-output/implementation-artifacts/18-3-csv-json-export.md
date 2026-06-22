# Story 18.3: CSV/JSON export for finance

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 18.3 |
| **Key** | leanproxy-18-3 |
| **Epic** | epic-18 — Cost Attribution Web Dashboard |
| **Title** | CSV/JSON export for finance |
| **Related FRs** | FR49 |
| **Related NFRs** | NFR2,NFR4 |
| **Previous Story:** [18.2 drill-down](18-2-drill-down.md) |

## User Story

As a user, I want to export cost data as CSV or JSON, so my finance team can include it in monthly reports.

## Acceptance Criteria (BDD Summary)

leanproxy report --export csv --since 2026-01-01 -> leanproxy-report-<date>.csv: timestamp, team, project, server, tool, tokens, estimated_cost. --export json -> JSON array. Large range (90d, 1M+ rows) -> streamed, no full buffering (NFR2), progress indicator. Only aggregated metrics; no PII, secrets, or prompt content (NFR4).

## Developer Context

### Technical Notes

cmd/report.go (NEW): extends existing pkg/reporter; streaming via encoding/csv + json.Encoder; progress bar via existing pkg/utils; data source pkg/metrics/aggregator.go (NEW).

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-18-Story-18.3]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: Cost Attribution Web Dashboard

## File List

- See Technical Notes above
