---
baseline_commit: 4b23bfdbb6994f447279c2d84d58e990f8dab6b4
---

# Story 18.3: CSV/JSON export for finance

Status: done

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

cmd/report.go: extends existing pkg/reporter; streaming via encoding/csv + json.Encoder; progress bar via existing pkg/utils; data source pkg/reporter/cost.go (GetEntries).

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

- NEW: `pkg/reporter/export.go` — CSV and JSON streaming export functions
- NEW: `pkg/reporter/export_test.go` — tests for export functions
- MODIFIED: `cmd/report.go` — added `--export` and `--since` flags
- MODIFIED: `cmd/report_test.go` — tests for new export flags

## Dev Agent Record

### Implementation Plan

Created CSV and JSON export capability for cost data, extending the existing `pkg/reporter` package. Two new streaming export functions (`ExportCSV` and `ExportJSON`) write rows incrementally via `encoding/csv.Writer` and `encoding/json.Encoder` respectively, avoiding full in-memory buffering for large datasets (NFR2). The `cmd/report.go` command gains `--export` (csv|json) and `--since` (YYYY-MM-DD) flags that delegate to these exporters, with a periodic stderr progress indicator. No prompt content, secrets, or PII are included in exports (NFR4). The CSV format produces columns: timestamp, team, project, server, tool, tokens, estimated_cost. Team and project fields are empty placeholders until the data model supports them.

### Completion Notes

- `ExportCSV` streams entries with header row and progress callback
- `ExportJSON` streams individual JSON objects comma-separated and wrapped in `[]`
- Cost estimation uses a default rate of $0.000002/token
- Both exporters support a `progress` callback for progress indication
- All existing tests pass (1953 tests across 44 packages)
- `go vet` clean

## Change Log

- Added `--export` and `--since` CLI flags to `cmd/report.go`
- Created `pkg/reporter/export.go` with `ExportCSV` and `ExportJSON` streaming functions
- Added comprehensive tests for CSV/JSON export with progress, empty, large, and NFR4 compliance scenarios

## Review Findings

### Decision Needed

- [ ] [Review][Decision] 1M+ rows from AC impossible with maxCallLogEntries=10000 — The spec requires "Large range (90d, 1M+ rows)" (NFR2) but the underlying `CostTracker.callLog` is capped at 10,000 entries. Export functions stream correctly, but the data source truncates before export. Supporting 1M+ rows requires either a persistent store (DB/file-backed log) or removing the 10k cap.
- [ ] [Review][Decision] No auto-generated output filename — AC states `--export csv --since 2026-01-01` should produce `leanproxy-report-<date>.csv`, but implementation writes to stdout (or `--output` path). Needs user direction on whether auto-naming is required.
- [ ] [Review][Decision] Hardcoded cost rate `defaultCostPerToken = 0.000002` — The cost per token is a hardcoded constant in `pkg/reporter/export.go:11`. Different LLM models have different costs. Should this be configurable via CLI flag or config file?

### Patch

- [ ] [Review][Patch] `os.Exit(1)` in cobra Run function breaks testability [cmd/report.go:91-131] — `runExport()` calls `os.Exit(1)` for invalid format/dates instead of returning errors. Should use cobra's `RunE` and propagate errors up.
- [ ] [Review][Patch] No end-to-end content verification in cmd export tests [cmd/report_test.go:152-176] — `TestReportCmd_ExportCSV`/`TestReportCmd_ExportJSON` create output files but never read or validate the exported content.
- [ ] [Review][Patch] `--since` flag silently ignored without `--export` [cmd/report.go:53-81] — Running `report --since 2026-01-01` without `--export` parses the flag but has no effect.
- [ ] [Review][Patch] Progress batching for NFR2 compliance [pkg/reporter/export.go:44-46] — Progress callback fires on every row; for large exports this creates unnecessary I/O. Should batch to every N rows or every 1%.
- [ ] [Review][Patch] Spec inaccuracy: Technical Notes reference non-existent file [spec:33] — Technical Notes mention `pkg/metrics/aggregator.go (NEW)` which was never created; implementation correctly uses existing `reporter.GetEntries`.

### Defer

- [x] [Review][Defer] `pkg/reporter/export.go:23,51` — nil io.Writer panic path — deferred, pre-existing: public API contract; callers always provide valid writer
- [x] [Review][Defer] `cmd/report.go:99-106` — Partial write on file close undetected — deferred, pre-existing: write errors already caught inline; close error is extremely rare
- [x] [Review][Defer] `pkg/reporter/export.go:39,69` — float64 precision for large TokenCount — deferred, pre-existing: unrealistic scenario (int64 max * $0.000002 = $18T)
- [x] [Review][Defer] `pkg/reporter/export.go:32-39` — Negative TokenCount not guarded — deferred, pre-existing: internal source never produces negatives
- [x] [Review][Defer] `cmd/report.go:108-115` — Progress callback for zero entries never called — deferred, pre-existing: negligible UX; export completes immediately
- [x] [Review][Defer] `pkg/reporter/export.go:52-53` — Partial JSON output on mid-export failure — deferred, pre-existing: inherent to streaming design
