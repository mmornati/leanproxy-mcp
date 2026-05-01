---
id: 2-1
key: 2-1-core-redaction-engine
epic: epic-2
title: Implement Core Redaction Engine
---

# Story 2-1: Implement Core Redaction Engine

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 2-1 |
| **Key** | `2-1-core-redaction-engine` |
| **Epic** | `epic-2` (Security & Data Governance - The Bouncer) |
| **Title** | Implement Core Redaction Engine |

## Story Requirements

### User Story

**As a** developer,
**I want to** implement a streaming regex-based redaction engine,
**So that** sensitive data is intercepted and redacted in real-time before leaving the machine.

### Acceptance Criteria (BDD Format)

```gherkin
Feature: Core Redaction Engine
  The Bouncer must intercept and redact sensitive data patterns in real-time
  with minimal latency impact on JSON-RPC traffic.

  Scenario: API keys and secrets are redacted in transit
    Given outgoing JSON-RPC traffic containing sensitive patterns
    When the traffic passes through the Bouncer
    Then API keys matching known patterns are replaced with `[SECRET_REDACTED]`
    And environment variable values are replaced with `[SECRET_REDACTED]`
    And the redaction happens inline without buffering entire messages
    And the processing adds less than 50ms overhead

  Scenario: Multiple secrets in a single message are all redacted
    Given a JSON-RPC message with multiple secrets
    When the Bouncer processes it
    Then all matching secrets are redacted
    And the message structure remains valid JSON
    And the redacted message length is approximately the same as the original

  Scenario: Messages without secrets pass through unchanged
    Given a message with no secrets
    When the Bouncer processes it
    Then the message passes through unchanged
    And no false positives are introduced
```

## Developer Context

### Technical Requirements

1. **Streaming Regex Engine**
   - Implement a streaming regex processor in `pkg/bouncer/redactor.go`
   - Use `regexp.MustCompile` for pattern compilation at startup
   - Process input via `io.Reader` and `io.Writer` interfaces for streaming
   - Maintain message structure (JSON validity) after redaction

2. **Core Patterns (Built-in Allow-list)**
   - AWS Access Keys: `AKIA[0-9A-Z]{16}`
   - AWS Secret Keys: `[A-Za-z0-9/+=]{40}` (contextual)
   - GitHub Tokens: `ghp_[A-Za-z0-9]{36}`, `github_pat_[A-Za-z0-9_]{22,}`
   - Stripe Keys: `sk_live_[A-Za-z0-9]{24}`, `pk_live_[A-Za-z0-9]{24}`
   - Generic API Keys: `[aA][pP][iI][-_]?[kK][eE][yY][_-]?[=]?[A-Za-z0-9]{16,}`
   - Environment Variables: `\$[A-Z_][A-Z0-9_]{0,30}` with value capture
   - Bearer Tokens: `Bearer [A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+`

3. **Redaction Replacement**
   - All redactions MUST use the placeholder: `[SECRET_REDACTED]`
   - Replacement string length should approximate the original for minimal downstream impact

4. **Performance Requirements**
   - Sub-50ms processing overhead per JSON-RPC message (NFR1)
   - Streaming processing (no full message buffering)
   - Handle payloads up to 50MB without memory issues (NFR2)

### Architecture Compliance

| Requirement | Implementation |
|-------------|----------------|
| Go with cobra CLI | Use `cobra` for CLI integration |
| `pkg/bouncer/` for redaction | Implement in `pkg/bouncer/redactor.go` |
| camelCase for Go symbols | Use `camelCase` for functions/variables |
| kebab-case for CLI flags | N/A for this story |
| `fmt.Errorf("context: %w", err)` | Use for error wrapping |
| `log/slog` for logging | Use `slog.Info`, `slog.Warn`, `slog.Error` to stderr |
| Allow-list approach | Core patterns defined as allow-list, not block-list |
| Streaming regex redaction | Use `io.Reader`/`io.Writer` streaming |
| In-memory only | No disk persistence; streaming through memory |

### File Structure

```
pkg/bouncer/
├── redactor.go          # Core streaming redaction engine
├── patterns.go          # Built-in allow-list patterns
├── patterns_test.go     # Pattern unit tests
└── redactor_test.go     # Redaction engine tests

cmd/leanproxy/
└── main.go              # CLI entry point (no changes needed for this story)
```

### Package Implementation

**`pkg/bouncer/redactor.go`**:
```go
package bouncer

import (
    "encoding/json"
    "fmt"
    "io"
    "log/slog"
    "regexp"
    "strings"
)

type Redactor struct {
    patterns   []*regexp.Regexp
    bufferSize int
}

func NewRedactor(patterns []*regexp.Regexp) *Redactor {
    return &Redactor{
        patterns:   patterns,
        bufferSize: 4096,
    }
}

func (r *Redactor) RedactStream(reader io.Reader, writer io.Writer) error {
    // Streaming regex replacement implementation
    // Process input in chunks, apply all patterns, write to output
    // Return error only on actual I/O failures, not on redaction matches
}

func (r *Redactor) RedactJSON(data []byte) ([]byte, error) {
    // JSON-aware redaction that preserves structure
    // Uses json.Encoder for streaming output
}
```

**`pkg/bouncer/patterns.go`**:
```go
package bouncer

import "regexp"

var BuiltInPatterns = []*regexp.Regexp{
    regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                                    // AWS Access Key
    regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`),                                  // GitHub Personal Token
    regexp.MustCompile(`github_pat_[A-Za-z0-9_]{22,}`),                         // GitHub Fine-grained PAT
    regexp.MustCompile(`sk_live_[A-Za-z0-9]{24}`),                              // Stripe Live Secret Key
    regexp.MustCompile(`pk_live_[A-Za-z0-9]{24}`),                              // Stripe Live Publishable Key
    regexp.MustCompile(`(?i)(api[_-]?key)[_-]?[=]?[A-Za-z0-9]{16,}`),          // Generic API Key
    regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+`), // Bearer Token
}
```

### Testing Requirements

1. **Unit Tests** (`pkg/bouncer/redactor_test.go`):
   - Test AWS key redaction
   - Test GitHub token redaction
   - Test Stripe key redaction
   - Test multiple secrets in single message
   - Test message with no secrets (pass-through)
   - Test JSON structure preservation
   - Test large payload handling (simulate 50MB)

2. **Benchmark Tests**:
   - Measure latency overhead per message
   - Verify sub-50ms requirement (NFR1)
   - Test memory usage under large payload

3. **Test Patterns**:
```go
func TestRedactAWSKey(t *testing.T) {
    input := `{"api_key": "AKIAIOSFODNN7EXAMPLE"}`
    expected := `{"api_key": "[SECRET_REDACTED]"}`
    // ...
}

func TestRedactMultipleSecrets(t *testing.T) {
    input := `{"aws": "AKIAIOSFODNN7EXAMPLE", "github": "ghp_abcdefghijklmnopqrstuvwxyz1234567890abcd"}`
    // Verify both are redacted, structure intact
}

func BenchmarkRedactSmallMessage(b *testing.B) {
    // Measure < 50ms requirement
}
```

### Error Handling

- Redaction failures (pattern errors) must be logged but MUST NOT block processing
- I/O errors from reader/writer MUST be returned with wrapped context
- Invalid JSON input MUST be passed through unchanged (not crashed on)
- Use `fmt.Errorf("bouncer redact: %w", err)` for error wrapping

### Logging Requirements

Use `log/slog` for all logging:
- `slog.Debug("redacting message", "size", len(data))` - verbose redaction
- `slog.Info("redaction complete", "secrets_found", count)` - summary
- `slog.Warn("pattern_error", "pattern", patternName, "error", err)` - invalid pattern
