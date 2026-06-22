# Story 14.3: JetBrains plugin (Kotlin) - parity with VS Code

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 14.3 |
| **Key** | leanproxy-14-3 |
| **Epic** | epic-14 — IDE Plugin (VS Code / JetBrains) + Live Cost Sidebar |
| **Title** | JetBrains plugin (Kotlin) - parity with VS Code |
| **Related FRs** | FR44 |
| **Related NFRs** | NFR13 |
| **Previous Story:** [14.2 vscode-extension](14-2-vscode-extension.md) |

## User Story

As a JetBrains user (IntelliJ, PyCharm, GoLand), I want the same live-cost experience as VS Code, so my team has consistent observability across IDEs.

## Acceptance Criteria (BDD Summary)

Plugin installed + IDE open + LeanProxy running -> status-bar widget shows session cost + tool window 'LeanProxy' with per-tool table. Open view -> polls /metrics, configurable refresh interval. Published on JetBrains Marketplace with >=4.5 star rating in first 90 days.

## Developer Context

### Technical Notes

plugins/jetbrains/ (NEW repo or subdir): build.gradle.kts, src/main/kotlin/...; Gradle IntelliJ plugin; uses same /metrics contract as 14.2; JetBrains Marketplace publish workflow.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-14-Story-14.3]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: IDE Plugin (VS Code / JetBrains) + Live Cost Sidebar

## File List

- See Technical Notes above
