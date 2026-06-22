# Story 14.2: VS Code extension (TypeScript) with status bar + webview

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 14.2 |
| **Key** | leanproxy-14-2 |
| **Epic** | epic-14 — IDE Plugin (VS Code / JetBrains) + Live Cost Sidebar |
| **Title** | VS Code extension (TypeScript) with status bar + webview |
| **Related FRs** | FR44 |
| **Related NFRs** | NFR13 |
| **Previous Story:** [14.1 metrics-endpoint](14-1-metrics-endpoint.md) |

## User Story

As a VS Code user, I want a status-bar item showing current session token cost, and a webview panel with per-tool breakdown, so I see AI cost as I work.

## Acceptance Criteria (BDD Summary)

Extension installed + LeanProxy reachable -> status bar shows /tmp/leanproxy_create_stories.sh.00, updates <1s after each call. Click -> webview: server, tool, calls, tokens, est cost; polls /metrics every 2s. LeanProxy down -> 'disconnected' tooltip + 'proxy offline' empty state. Installs from marketplace; first-run <60s.

## Developer Context

### Technical Notes

extensions/vscode/ (NEW repo or subdir): package.json, src/statusBar.ts, src/webview/index.html + breakdown.ts; npm publish workflow via .github/workflows/publish-vscode.yml; reuses /metrics from 14.1.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-14-Story-14.2]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: IDE Plugin (VS Code / JetBrains) + Live Cost Sidebar

## File List

- See Technical Notes above
