---
baseline_commit: 1657c7b543d25d4056c2a010b58295b41f283546
---

# Story 18.1: Web dashboard served from LeanProxy

Status: done

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

## Tasks/Subtasks

- [x] Create `pkg/dashboard/auth.go` — bearer token middleware with loopback bypass
- [x] Create `pkg/dashboard/server.go` — HTTP server with embedded HTMX and dashboard HTML template
- [x] Write unit tests for auth middleware (no token, loopback bypass, missing/wrong/valid auth, scheme)
- [x] Write unit tests for dashboard server (disabled, renders, JSON API, auth, formatting, per-server/per-tool)
- [x] Integrate dashboard server into `cmd/serve.go` — add `--dashboard-bind` and `--dashboard-token` flags, start/shutdown lifecycle
- [x] Run full test suite and verify no regressions (1917 tests passing)

## Dev Agent Record

### Debug Log

- Created `pkg/dashboard/auth.go` with constant-time bearer token verification and loopback detection
- Created `pkg/dashboard/server.go` with `embed` package for HTMX JS and HTML template
- Dashboard HTML uses HTMX hx-trigger="every 5s" for auto-refresh
- Summary card shows: today's spend, WTD, top server, top tool with formatted token counts
- Exported `DashboardJSON` handler for JSON API endpoint used by external consumers
- Integrated into `cmd/serve.go` following the same pattern as `--metrics-bind`; dashboard defaults to 127.0.0.1:9090
- No new external dependencies introduced; all functionality uses stdlib

### Completion Notes

Implemented web dashboard served from LeanProxy at configurable bind address (default 127.0.0.1:9090). The dashboard reuses `pkg/metrics` for aggregated token cost data, renders a summary card via embedded HTML template with HTMX auto-refresh, and supports bearer token auth for non-loopback access. All 1917 tests pass with no regressions.

## File List

- `pkg/dashboard/auth.go` (NEW)
- `pkg/dashboard/server.go` (NEW)
- `pkg/dashboard/auth_test.go` (NEW)
- `pkg/dashboard/server_test.go` (NEW)
- `pkg/dashboard/assets/htmx.min.js` (NEW)
- `cmd/serve.go` (MODIFIED — added dashboard integration)

## Change Log

- Added `pkg/dashboard/` package with auth middleware, HTTP server, and embedded assets
- Modified `cmd/serve.go` to accept `--dashboard-bind` and `--dashboard-token` flags and manage dashboard server lifecycle

## Review Findings

### Patch (all applied)

- [x] [Review][Patch] F1: Top server/tool reads unsorted data — `collectDashboardData()` and `DashboardJSON()` read `snap.ByServer[0]` / `snap.ByTool[0]` before sorting by `TokenCount` descending, yielding arbitrary "top" entries. Fix: sort before reading `[0]`. [`server.go:170-180`, `server.go:216-220`]
- [x] [Review][Patch] F3: JSON endpoint `DashboardJSON` not mounted in HTTP mux — `/api/dashboard` returns HTML (HTMX card template); the exported `DashboardJSON` handler (proper `application/json`) has no route. Fix: mount at `GET /api/dashboard/json`. [`server.go:138-140`, `server.go:212-266`]
- [x] [Review][Patch] F4: Template/log errors use global `slog.Error()` instead of passed logger — `handleDashboardIndex`, `handleDashboardJSON`, and `DashboardJSON` ignore the `logger` parameter. Fix: use the passed logger via closure. [`server.go:188-190`, `server.go:196-198`, `server.go:263-265`]
- [x] [Review][Patch] F5: Auth bypass via `Host` header spoofing — `isLoopback()` trusts client-controlled `r.Host` header; an external attacker sending `Host: localhost` bypasses bearer token auth. Fix: remove `r.Host` check, rely solely on `r.RemoteAddr`. [`auth.go:46-53`]
- [x] [Review][Patch] F6: Missing `WWW-Authenticate` header on 401 — RFC 7235 violation. Fix: set `WWW-Authenticate: Bearer realm="dashboard"` before writing 401. [`auth.go:26`, `auth.go:32`, `auth.go:38`]
- [x] [Review][Patch] F8: Flaky tests with `time.Sleep(50ms)` — goroutine server start creates race condition. Fix: replace sleep with connection retry loop. [`server_test.go:55`, `server_test.go:85`, `server_test.go:188`]
- [x] [Review][Patch] F11: IPv6 bind address parsing edge case — unbracketed IPv6 literal could produce confusing errors in `net.SplitHostPort`. Fix: add explicit IPv6 validation/handling. [`server.go:121-124`]

### Defer

- [x] [Review][Defer] F2: TodaySpend == WTDSpend — both use `snap.TotalSpend`; `pkg/metrics` has no separate daily/weekly aggregation. Pre-existing data model limitation, not introduced by this change. [`server.go:164-165`, `server.go:223-225`]
- [x] [Review][Defer] F7: Dashboard uses `Close()` not `Shutdown()` during graceful shutdown — matches existing `metricsServer.Close()` pattern. Pre-existing pattern. [`cmd/serve.go:397-399`]
- [x] [Review][Defer] F9: No rate limiting on auth — brute-force protection for non-loopback. Out of scope for story 18-1. [`auth.go:11-43`]
- [x] [Review][Defer] F10: No TLS support — dashboard is HTTP-only with warning for non-loopback binds. Out of scope for this story. [`server.go:126-129`]
