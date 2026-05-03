---
id: 5-2
key: markdown-report-generation
epic: epic-5-reporting-insights
title: Implement Markdown Report Generation
status: ready-for-dev
developer: Amelia
---

# Story 5-2: Implement Markdown Report Generation

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 5-2 |
| **Key** | `markdown-report-generation` |
| **Epic** | Epic 5: Reporting & Insights |
| **Title** | Implement Markdown Report Generation |
| **Status** | `ready-for-dev` |
| **Story Points** | 3 |
| **Implementation Order** | 2 |

---

## Story Requirements

### User Story

**As a** user,
**I want to** generate Markdown-formatted reports on tokens saved and risks intercepted,
**So that** I can share the impact with my team or include it in documentation.

### FR Coverage

- **FR22**: The system can generate Markdown-formatted reports summarizing "Total Tokens Saved" and "Security Risks Intercepted."
- **NFR9**: The system shall output real-time health and savings metrics (tokens saved, secrets redacted) to stderr.

---

## Acceptance Criteria

### BDD Format

```
Scenario: Generate report for completed session
  Given a completed session (or dry-run)
  When the user runs `leanproxy report`
  Then a Markdown-formatted report is output to stdout
  And it includes a summary section with key metrics
  And it includes a detailed breakdown by server

Scenario: Report includes token savings metrics
  Given the report format includes
  When the report is generated
  Then it shows "Total Tokens Saved: X" with percentage reduction
  And it shows individual server breakdown with token counts
  And it shows session duration and request counts

Scenario: Report includes security metrics
  Given the report format includes
  When the report is generated
  Then it shows "Security Risks Intercepted: Y" with risk categories
  And it shows breakdown by redaction type (API keys, secrets, PII)
  And it shows timestamps of security events

Scenario: Report formatted for IDE display
  Given the report is generated
  When it is displayed
  Then it uses proper Markdown formatting
  And code blocks are used for metrics tables
  And headings create a clear document structure
  And it renders nicely in IDE preview panels

Scenario: Report with --json flag
  Given the user runs `leanproxy report --json`
  When the report is generated
  Then JSON output is sent to stdout instead of Markdown
  And all metrics are preserved in structured format

Scenario: Report with no session data
  Given no requests have been processed
  When the user runs `leanproxy report`
  Then a valid Markdown report is generated
  And it shows zero values for all metrics
  And it indicates no session data is available
```

---

## Developer Context

### Technical Requirements

#### Report Generator

1. **ReportGenerator Interface**
   - Located in `pkg/utils/report_generator.go`
   - Method: `GenerateMarkdownReport(sessionData SessionMetrics) string`
   - Method: `GenerateJSONReport(sessionData SessionMetrics) string`

2. **SessionMetrics Struct**
   - Fields:
     - `SessionID string`
     - `SessionStart time.Time`
     - `SessionEnd time.Time`
     - `TotalRequests int`
     - `TokenSavings TokenSavingsSummary`
     - `SecurityEvents []SecurityEvent`
     - `ServerMetrics map[string]ServerMetrics`

3. **TokenSavingsSummary Struct**
   - Fields:
     - `OriginalTokens int64`
     - `OptimizedTokens int64`
     - `SavedTokens int64`
     - `SavingsPercentage float64`
     - `ByServer map[string]ServerTokenSavings`

4. **SecurityEvent Struct**
   - Fields:
     - `Timestamp time.Time`
     - `EventType string` // "api_key", "secret", "pii", "custom_pattern"
     - `PatternMatched string`
     - `ServerName string`
     - `Redacted bool`

5. **ServerMetrics Struct**
   - Fields:
     - `ServerName string`
     - `RequestsHandled int`
     - `Uptime time.Duration`
     - `Errors int`
     - `TokenSavings ServerTokenSavings`

6. **ServerTokenSavings Struct**
   - Fields:
     - `ServerName string`
     - `OriginalTokens int64`
     - `OptimizedTokens int64`
     - `SavedTokens int64`

#### Markdown Report Format

```markdown
# LeanProxy Session Report

## Summary

| Metric | Value |
|--------|-------|
| Session ID | `<session-id>` |
| Duration | `<duration>` |
| Total Requests | `<count>` |
| **Total Tokens Saved** | **<saved> (<pct>%)** |
| Security Risks Intercepted | `<count>` |

## Token Savings

### By Server

| Server | Original | Optimized | Saved | Savings % |
|--------|----------|-----------|-------|-----------|
| `<name>` | `<tokens>` | `<tokens>` | `<tokens>` | `<pct>%` |

### Optimization Breakdown

| Technique | Tokens Saved |
|-----------|---------------|
| Discovery Signatures | `<n>` |
| JIT Schema Injection | `<n>` |
| Boilerplate Pruning | `<n>` |
| Manifest Compaction | `<n>` |

## Security Events

| Timestamp | Type | Server | Pattern |
|-----------|------|--------|---------|
| `<time>` | `<type>` | `<server>` | `<pattern>` |

### Risk Summary

- API Keys Redacted: `<count>`
- Secrets Redacted: `<count>`
- PII Detected: `<count>`
- Custom Patterns: `<count>`

---
*Report generated by LeanProxy at <timestamp>*
```

#### CLI Integration

- New command: `leanproxy report` (subcommand of root)
  - Flags:
    - `--session-id` (string): Generate report for specific session (default: current)
    - `--output` (string): Output file path (default: stdout)
    - `--json` (bool): Output JSON instead of Markdown
    - `--no-security` (bool): Exclude security events from report

#### Output Destinations

- Default: stdout (Markdown format)
- With `--json`: stdout (JSON format)
- With `--output <file>`: Write to specified file

### Architecture Compliance

| Requirement | Implementation |
|-------------|----------------|
| Go with cobra CLI | CLI commands in `cmd/leanproxy/` |
| camelCase for functions/variables | `generateMarkdownReport`, `sessionMetrics`, `tokenSavingsSummary` |
| kebab-case for CLI flags | `--session-id`, `--output`, `--json`, `--no-security` |
| `fmt.Errorf("context: %w", err)` | Used for all error wrapping |
| `log/slog` for structured logging | Progress logged to stderr via `slog.Info` |
| Markdown-formatted reports | Full Markdown output per format specification |
| pkg/ structure | `pkg/utils/report_generator.go` |

### File Structure

```
leanproxy-mcp/
├── cmd/
│   └── leanproxy/
│       ├── main.go
│       └── report.go          # New: report CLI command
├── pkg/
│   ├── utils/
│   │   └── report_generator.go # New: report generation logic
│   └── ... (existing files)
```

### Integration Points

1. **With `pkg/utils/savings_tracker.go`**: Retrieve token savings data
2. **With `pkg/bouncer/`**: Retrieve security event history
3. **With `pkg/proxy/`**: Retrieve server metrics and request counts
4. **With `cmd/leanproxy/savings.go`**: Use report generation for `savings` command output

### Testing Requirements

#### Unit Tests

- `pkg/utils/report_generator_test.go`
  - Test Markdown formatting with all sections
  - Test JSON output format
  - Test empty session handling
  - Test report with no security events
  - Test report with no token savings
  - Test server breakdown formatting

#### Integration Tests

- Test report generation end-to-end with mock session data
- Verify Markdown renders correctly in preview panels
- Verify JSON is valid and parseable

### Error Handling

- If session data is corrupted, generate report with available data and log warning
- If report generation fails entirely, return error with exit code 1
- All errors wrapped with `fmt.Errorf("report generation: context: %w", err)`

### Edge Cases

1. **No session data**: Generate report with zero values and "No session data" message
2. **Very long session**: Report remains performant; don't load all events into memory at once
3. **Special characters in server names**: Properly escape for Markdown tables
4. **Unicode in content**: Support full Unicode in report output
5. **Empty server list**: Show "No servers processed" in appropriate section

---

## Definition of Done

- [ ] SessionMetrics struct defined with all required fields
- [ ] ReportGenerator interface implemented
- [ ] `GenerateMarkdownReport()` produces valid Markdown per format spec
- [ ] `GenerateJSONReport()` produces valid JSON with all metrics
- [ ] `leanproxy report` CLI command functional with all flags
- [ ] Report includes token savings summary with percentage
- [ ] Report includes security events breakdown
- [ ] Report includes per-server breakdown
- [ ] Unit tests pass with >80% coverage
- [ ] Integration tests verify end-to-end report generation
- [ ] Architecture compliance verified (naming, error handling, logging)
