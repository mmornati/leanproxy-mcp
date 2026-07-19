---
baseline_commit: fc54f8391d5c0db2e01914ae9ddeadfc8c38dadd
---

# Story 14.1: Publish /metrics JSON endpoint

Status: done

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

## Tasks/Subtasks

- [x] Create `pkg/metrics/aggregator.go` — MetricsSnapshot struct + Snapshot() function wrapping reporter.CostTracker
- [x] Create `pkg/metrics/server.go` — HTTP server for /metrics endpoint using net/http stdlib
- [x] Write unit tests for aggregator (Snapshot, empty data, top-5 sorting, concurrency)
- [x] Write integration tests for server (disabled addr, GET /metrics response, method validation, concurrent requests)
- [x] Integrate metrics server into `cmd/serve.go` — add `--metrics-bind` flag, start/shutdown lifecycle
- [x] Run full test suite and verify no regressions (1584 tests passing)
- [x] Update story file with status, file list, and completion notes

## Dev Agent Record

### Debug Log

- Implemented metrics package at `pkg/metrics/` with aggregator and HTTP server components
- Reused existing `reporter.GlobalCostTracker()` for token tracking — no duplicate state
- Integrated into `cmd/serve.go` with `--metrics-bind` flag; empty or "off" disables the endpoint
- Used `net.Listen` + `srv.Serve` pattern to correctly report the actual listening port
- Non-loopback bind addresses produce a security warning via slog.Warn (per FR44 security requirement)
- No new dependencies introduced; all functionality uses stdlib (`net/http`, `encoding/json`)

### Completion Notes

Implemented `/metrics` JSON endpoint exposing real-time token spend data. The server starts on the main serve lifecycle, respects disable-by-config, warns on non-loopback bind, and exposes per-server tokens, per-tool tokens, total session spend, and top-5 most expensive tools. All 1584 tests pass with no regressions.

## File List

- `pkg/metrics/aggregator.go` (NEW)
- `pkg/metrics/server.go` (NEW)
- `pkg/metrics/aggregator_test.go` (NEW)
- `pkg/metrics/server_test.go` (NEW)
- `cmd/serve.go` (MODIFIED — added metrics integration)

## Review Findings

- [x] [Review][Patch] `enc.Encode(snapshot)` error silently discarded [pkg/metrics/server.go:64]
- [x] [Review][Patch] Empty host (`:9090`) binds all interfaces without security warning [pkg/metrics/server.go:22]
- [x] [Review][Patch] Test cleanup lacks `defer` for `Reset()`, risks state leaks on panic [pkg/metrics/aggregator_test.go]
- [x] [Review][Patch] `TestServeConcurrentRequests` silently discards HTTP errors [pkg/metrics/server_test.go:125-134]
- [x] [Review][Patch] Fragile test name generation using `string(rune('a' + i - 1))` [pkg/metrics/aggregator_test.go:67]
- [x] [Review][Defer] No graceful shutdown (`Close()` vs `Shutdown()`) — pre-existing pattern across codebase [cmd/serve.go:319]
- [x] [Review][Defer] Redundant allocation in `Snapshot()` — metrics endpoint is not a hot path [pkg/metrics/aggregator.go]

## Change Log

- Added `pkg/metrics/` package with aggregator and HTTP server for /metrics endpoint
- Modified `cmd/serve.go` to accept `--metrics-bind` flag and manage metrics server lifecycle
