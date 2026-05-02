---
id: 2-5
key: 2-5-redaction-alerts
epic: epic-2
title: Implement Redaction Alerts via stderr
---

# Story 2-5: Implement Redaction Alerts via stderr

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 2-5 |
| **Key** | `2-5-redaction-alerts` |
| **Epic** | `epic-2` (Security & Data Governance - The Bouncer) |
| **Title** | Implement Redaction Alerts via stderr |

## Story Requirements

### User Story

**As a** user,
**I want to** be alerted via stderr when redaction occurs,
**So that** I know my sensitive data was protected without polling logs.

### Acceptance Criteria (BDD Format)

```gherkin
Feature: Redaction Alerts via stderr
  Users receive immediate notification when sensitive data is redacted,
  allowing them to understand security events in real-time.

  Scenario: Single redaction triggers stderr alert
    Given a redaction event occurring during JSON-RPC processing
    When the Bouncer redacts a secret
    Then a message is written to stderr (not stdout)
    And the message includes the pattern that was matched
    And the message does NOT include the actual secret value

  Scenario: Multiple redactions produce a summary
    Given multiple redactions in a single request
    When processing completes
    Then a summary is written to stderr
    And it shows the count of redactions by type

  Scenario: Verbose mode provides additional context
    Given verbose mode is enabled (`--verbose`)
    When redaction occurs
    Then additional context is provided in the stderr message
    And the original message structure is hinted at (without exposing secrets)
```

## Developer Context

### Technical Requirements

1. **Alert Output Channel**
   - All redaction alerts MUST be written to stderr, NOT stdout
   - Stdout is reserved for JSON-RPC protocol traffic only
   - Use `log/slog` with default handler which outputs to stderr

2. **Alert Content**
   - Alert MUST include: timestamp (ISO 8601), pattern name, redaction count
   - Alert MUST NOT include: actual secret values, unredacted content
   - Alert format: `level=INFO msg="redaction_event" pattern=<name> count=<n> timestamp=<iso8601>`

3. **Alert Levels**
   - `INFO`: Normal redaction events (expected in production)
   - `WARN`: High number of redactions (>10 in single message) - potential scanning
   - `DEBUG`: Detailed per-match information (only with `--verbose`)

4. **Summary Reporting**
   - After each JSON-RPC request completes, emit a summary if redactions occurred
   - Summary format:
     ```
     [REDACTION SUMMARY] Request completed
       Patterns matched: <count>
       Secret type breakdown:
         - aws-access-key: 2
         - github-classic-pat: 1
         - generic-api-key: 3
     ```

5. **Verbose Mode**
   - CLI flag `--verbose` or env var `LEANPROXY_VERBOSE=true`
   - When enabled, include message structure hints in alerts
   - Example: `msg_id=<id> method=<method> has_secrets=true secret_fields=["api_key","token"]`

### Architecture Compliance

| Requirement | Implementation |
|-------------|----------------|
| Go with cobra CLI | `--verbose` flag on root command |
| `pkg/bouncer/` for redaction | Alert logic in `pkg/bouncer/alerts.go` |
| camelCase for Go symbols | Use `camelCase` for functions/variables |
| kebab-case for CLI flags | `--verbose` flag |
| `fmt.Errorf("context: %w", err)` | Use for error wrapping |
| `log/slog` for logging | All alerts via `slog.Info`, `slog.Warn`, `slog.Debug` |
| stderr for alerts | slog default outputs to stderr |

### File Structure

```
pkg/bouncer/
├── redactor.go          # Core streaming redaction engine
├── alerts.go            # Alert formatting and emission
├── alerts_test.go       # Alert tests
├── patterns.go          # Pattern types and helpers
└── redactor_test.go     # Redaction engine tests

cmd/leanproxy/
├── main.go              # CLI entry point with --verbose flag
└── bouncer.go           # Bouncer subcommands
```

### Package Implementation

**`pkg/bouncer/alerts.go`**:
```go
package bouncer

import (
    "encoding/json"
    "log/slog"
    "sync"
    "time"
)

type AlertManager struct {
    verbose       bool
    enabled       bool
    mu            sync.Mutex
    currentCounts map[string]int
}

func NewAlertManager(verbose bool) *AlertManager {
    return &AlertManager{
        verbose:       verbose,
        enabled:       true,
        currentCounts: make(map[string]int),
    }
}

type RedactionEvent struct {
    PatternName string
    Count       int
    Timestamp   time.Time
    MessageID   string
    Method      string
}

func (am *AlertManager) RecordRedaction(event RedactionEvent) {
    if !am.enabled {
        return
    }

    am.mu.Lock()
    am.currentCounts[event.PatternName] += event.Count
    am.mu.Unlock()

    am.emitAlert(event)
}

func (am *AlertManager) emitAlert(event RedactionEvent) {
    attrs := []slog.Attr{
        slog.String("pattern", event.PatternName),
        slog.Int("count", event.Count),
        slog.String("timestamp", event.Timestamp.Format(time.RFC3339)),
    }

    if am.verbose && event.MessageID != "" {
        attrs = append(attrs, slog.String("msg_id", event.MessageID))
        attrs = append(attrs, slog.String("method", event.Method))
    }

    if am.verbose {
        slog.Debug("redaction_match", attrs...)
    } else {
        slog.Info("redaction_event",
            slog.String("pattern", event.PatternName),
            slog.Int("count", event.Count))
    }
}

func (am *AlertManager) EmitSummary(messageID string, method string) {
    am.mu.Lock()
    defer am.mu.Unlock()

    if len(am.currentCounts) == 0 {
        return
    }

    total := 0
    for _, count := range am.currentCounts {
        total += count
    }

    if total == 0 {
        return
    }

    slog.Info("redaction_summary",
        slog.String("msg_id", messageID),
        slog.String("method", method),
        slog.Int("total_redactions", total),
        slog.Any("breakdown", am.currentCounts))

    if am.verbose {
        am.emitVerboseSummary(messageID, method)
    }

    am.currentCounts = make(map[string]int)
}

func (am *AlertManager) emitVerboseSummary(messageID, method string) {
    summary := map[string]interface{}{
        "event":         "redaction_summary",
        "message_id":    messageID,
        "method":        method,
        "patterns":      am.currentCounts,
        "has_secrets":   true,
        "secret_fields": detectSecretFields(method),
    }

    data, _ := json.Marshal(summary)
    slog.Debug("redaction_detail", slog.String("detail", string(data)))
}

func detectSecretFields(method string) []string {
    knownMethods := map[string][]string{
        "tools/call":      {"arguments", "input"},
        "resources/read": {"uri", "contents"},
    }
    if fields, ok := knownMethods[method]; ok {
        return fields
    }
    return []string{"payload"}
}

func (am *AlertManager) SetVerbose(verbose bool) {
    am.verbose = verbose
}

func (am *AlertManager) SetEnabled(enabled bool) {
    am.enabled = enabled
}
```

**`pkg/bouncer/redactor.go` integration**:
```go
package bouncer

import (
    "fmt"
    "io"
    "log/slog"
)

type Redactor struct {
    patterns      []*regexp.Regexp
    alertManager  *AlertManager
    bufferSize    int
}

func NewRedactor(patterns []*regexp.Regexp, alertManager *AlertManager) *Redactor {
    return &Redactor{
        patterns:     patterns,
        alertManager: alertManager,
        bufferSize:   4096,
    }
}

func (r *Redactor) RedactStream(reader io.Reader, writer io.Writer, meta *RedactionMeta) error {
    // ... streaming redaction logic ...

    for _, match := range matches {
        r.alertManager.RecordRedaction(RedactionEvent{
            PatternName: match.PatternName,
            Count:       match.Count,
            Timestamp:   time.Now(),
            MessageID:   meta.MessageID,
            Method:      meta.Method,
        })
    }

    r.alertManager.EmitSummary(meta.MessageID, meta.Method)

    return nil
}

type RedactionMeta struct {
    MessageID string
    Method    string
}
```

### Testing Requirements

1. **Alert Tests** (`pkg/bouncer/alerts_test.go`):
   - Test alert manager records redaction counts correctly
   - Test summary is emitted after processing
   - Test verbose mode includes additional context
   - Test disabled alert manager doesn't emit

2. **Output Tests**:
   - Test alerts are written to stderr (not stdout)
   - Test secret values are never in alert output
   - Test pattern names are correctly reported

3. **Integration Tests**:
   - Test end-to-end alert emission
   - Test multiple redactions aggregate correctly
   - Test summary includes all pattern types

4. **Test Implementation**:
```go
func TestAlertManagerRecordsRedactions(t *testing.T) {
    am := NewAlertManager(false)

    am.RecordRedaction(RedactionEvent{
        PatternName: "aws-access-key",
        Count:       1,
        Timestamp:   time.Now(),
    })

    am.mu.Lock()
    assert.Equal(t, 1, am.currentCounts["aws-access-key"])
    am.mu.Unlock()
}

func TestNoSecretsInAlerts(t *testing.T) {
    am := NewAlertManager(false)

    am.RecordRedaction(RedactionEvent{
        PatternName: "aws-access-key",
        Count:       1,
        Timestamp:   time.Now(),
    })

    // Capture slog output
    var stderr bytes.Buffer
    slog.SetOutput(&stderr)

    am.EmitSummary("msg-123", "tools/call")

    output := stderr.String()
    assert.NotContains(t, output, "AKIA")
    assert.NotContains(t, output, "secret")
    assert.Contains(t, output, "aws-access-key")
}
```

### Error Handling

- Alert failures (e.g., stderr closed) MUST NOT block redaction processing
- Alert manager errors should be logged at DEBUG level only
- Use `fmt.Errorf("bouncer alert: %w", err)` for internal errors

### Logging Requirements

| Level | When | Content |
|-------|------|---------|
| INFO | Each redaction event | `pattern=<name> count=<n> timestamp=<iso>` |
| INFO | Request complete | `redaction_summary total=<n> breakdown={...}` |
| WARN | High redaction count (>10) | `potential_scan pattern=<name> count=<n>` |
| DEBUG | Verbose mode only | Full redaction details including method, msg_id |

### CLI Integration

**`cmd/leanproxy/main.go`** modifications:
```go
var verboseFlag bool

func init() {
    rootCmd.PersistentFlags().BoolVar(&verboseFlag, "verbose", false, "enable verbose logging")
}

func preRunE(cmd *cobra.Command, args []string) error {
    if verboseFlag {
        slog.SetLogLoggerLevel(slog.LevelDebug)
    }
    return nil
}
```

**Alert Output Examples**:

Standard mode (stderr):
```
level=INFO msg="redaction_event" pattern=aws-access-key count=1 timestamp=2026-05-01T10:30:00Z
level=INFO msg="redaction_event" pattern=github-classic-pat count=2 timestamp=2026-05-01T10:30:00Z
level=INFO msg="redaction_summary" total_redactions=3 breakdown={"aws-access-key":1,"github-classic-pat":2}
```

Verbose mode (stderr):
```
level=INFO msg="redaction_event" pattern=aws-access-key count=1 timestamp=2026-05-01T10:30:00Z msg_id=abc123 method=tools/call
level=INFO msg="redaction_event" pattern=github-classic-pat count=2 timestamp=2026-05-01T10:30:00Z msg_id=abc123 method=tools/call
level=DEBUG msg="redaction_detail" detail="{\"event\":\"redaction_summary\",\"message_id\":\"abc123\",\"method\":\"tools/call\",\"patterns\":{\"aws-access-key\":1,\"github-classic-pat\":2},\"has_secrets\":true,\"secret_fields\":[\"arguments\"]}"
```

---

## Tasks/Subtasks

- [x] Implement AlertManager struct in `pkg/bouncer/alerts.go`
- [x] Add RecordRedaction and EmitSummary methods
- [x] Integrate with Redactor and StreamingRedactor
- [x] Add `--verbose` flag support
- [x] Write comprehensive tests in `pkg/bouncer/alerts_test.go`
- [x] Verify all tests pass

## Dev Agent Record

### Debug Log

2026-05-02: Initial implementation started

### Completion Notes

Implemented redaction alerts via stderr as specified. Created:
- `pkg/bouncer/alerts.go`: AlertManager with RecordRedaction, EmitSummary, SetVerbose, SetEnabled
- `pkg/bouncer/alerts_test.go`: 12 tests covering all alert scenarios
- Modified `pkg/bouncer/redactor.go` to support optional alert integration via variadic parameter
- Modified `pkg/bouncer/streaming.go` to support optional alert integration via variadic parameter

All tests pass (83 total).

## File List

- `pkg/bouncer/alerts.go` (new)
- `pkg/bouncer/alerts_test.go` (new)
- `pkg/bouncer/redactor.go` (modified)
- `pkg/bouncer/streaming.go` (modified)

## Change Log

- 2026-05-02: Initial implementation of redaction alerts via stderr

## Status

review
