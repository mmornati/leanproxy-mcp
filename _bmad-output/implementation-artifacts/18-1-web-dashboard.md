# Story 18.1: Web dashboard served from LeanProxy

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 18.1 |
| **Key** | leanproxy-18-1 |
| **Epic** | epic-18 — Cost Attribution Web Dashboard |
| **Title** | Web dashboard served from LeanProxy |
| **Related FRs** | FR49 |
| **Related NFRs** | NFR13 |

## User Story

As a user, I want to open http://localhost:9090 and see a real-time cost dashboard, so I don't need a separate tool to visualize spend.

## Acceptance Criteria (BDD Summary)

Dashboard enabled (default on) + open URL -> HTML page loads <500ms; summary card: today's spend, WTD, top server, top tool. Non-loopback access -> bearer token required (dashboard.token), 401 without. Disabled in config -> no HTTP listener bound. Reads only aggregated metrics from in-memory store, no prompt content rendered (NFR13).

## Developer Context

### Technical Notes

pkg/dashboard/ server.go + assets/ (NEW): net/http stdlib; embed.HTMX for interactivity (no SPA framework); reuses pkg/metrics from 14.1; bearer-token middleware in pkg/dashboard/auth.go.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-18-Story-18.1]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: Cost Attribution Web Dashboard

## File List

- See Technical Notes above
