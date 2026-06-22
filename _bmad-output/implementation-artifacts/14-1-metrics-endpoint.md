# Story 14.1: Publish /metrics JSON endpoint

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 14.1 |
| **Key** | leanproxy-14-1 |
| **Epic** | epic-14 — IDE Plugin (VS Code / JetBrains) + Live Cost Sidebar |
| **Title** | Publish /metrics JSON endpoint |
| **Related FRs** | FR44 |
| **Related NFRs** | NFR13 |

## User Story

As a developer, I want LeanProxy to expose a real-time JSON metrics endpoint, so IDE plugins (and other consumers) can read spend data without parsing logs.

## Acceptance Criteria (BDD Summary)

Proxy running + GET http://localhost:<port>/metrics -> JSON: per-server tokens, per-tool tokens, total session spend, top 5 expensive tools. Disabled in config -> listener not bound, no port. metrics.bind: 0.0.0.0:9090 -> bind all interfaces, warn if non-loopback (security). Only aggregated counts - no PII/prompt content (NFR13).

## Developer Context

### Technical Notes

pkg/metrics/ server.go + aggregator.go (NEW); integrate into pkg/serve on existing lifecycle; extend leanproxy.yaml schema; use net/http stdlib (no new deps); token in pkg/proxy.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-14-Story-14.1]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: IDE Plugin (VS Code / JetBrains) + Live Cost Sidebar

## File List

- See Technical Notes above
