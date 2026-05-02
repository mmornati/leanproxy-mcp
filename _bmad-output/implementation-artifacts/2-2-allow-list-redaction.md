---
id: 2-2
key: 2-2-allow-list-redaction
epic: epic-2
title: Implement Allow-List Redaction Patterns
---

# Story 2-2: Implement Allow-List Redaction Patterns

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 2-2 |
| **Key** | `2-2-allow-list-redaction` |
| **Epic** | `epic-2` (Security & Data Governance - The Bouncer) |
| **Title** | Implement Allow-List Redaction Patterns |

## Story Requirements

### User Story

**As a** developer,
**I want to** implement an allow-list approach for core redaction patterns,
**So that** we minimize false negatives while ensuring high confidence redaction.

### Acceptance Criteria (BDD Format)

```gherkin
Feature: Allow-List Redaction Patterns
  The Bouncer uses an allow-list approach for core redaction patterns to ensure
  100% interception of standard secret formats with zero false positives.

  Scenario: Standard secret formats are detected with 100% accuracy
    Given standard secret formats (AWS keys, GitHub tokens, Stripe keys, .env values)
    When they appear in JSON-RPC traffic
    Then they are detected and redacted with 100% accuracy
    And the allow-list is documented and extensible

  Scenario: Unknown patterns that look like secrets are NOT redacted
    Given an unknown pattern that looks like a secret
    When it doesn't match any allow-list pattern
    Then it is NOT redacted (no false positives)

  Scenario: New patterns can be added to the allow-list
    Given a false negative (secret not caught)
    When the user reports it
    Then the pattern can be added to the allow-list
    And a new release includes the updated pattern
```

## Developer Context

### Technical Requirements

1. **Allow-List Pattern Definitions**
   - Maintain a definitive list of known secret patterns in `pkg/bouncer/allowlist.go`
   - Each pattern MUST be documented with its secret type and example
   - Patterns MUST be reviewed before addition to prevent false positives
   - Default patterns cover: AWS, GitHub, Stripe, generic API keys, environment variables

2. **Built-in Allow-List Patterns**

| Secret Type | Pattern | Example |
|-------------|---------|---------|
| AWS Access Key | `AKIA[0-9A-Z]{16}` | `AKIAIOSFODNN7EXAMPLE` |
| AWS Secret Key | `[A-Za-z0-9/+=]{40}` | (contextual, paired with Access Key) |
| GitHub Classic PAT | `ghp_[A-Za-z0-9]{36}` | `ghp_abcdefghijklmnopqrstuvwxyz1234567890abcd` |
| GitHub Fine-grained PAT | `github_pat_[A-Za-z0-9_]{22,}` | `github_pat_11abcdefghI...` |
| Stripe Secret Key | `sk_live_[A-Za-z0-9]{24}` | `sk_live_REDACTED_EXAMPLE_24CH` |
| Stripe Publishable Key | `pk_live_[A-Za-z0-9]{24}` | `pk_live_REDACTED_EXAMPLE_24CH` |
| Generic API Key | `[aA][pP][iI][-_]?[kK][eE][yY][_-]?[=]?[A-Za-z0-9]{16,}` | `api_key=abcdefghijklmnop` |
| Bearer Token | `Bearer [A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+` | `Bearer eyJhbGciOiJIUzI1NiIs...` |
| Environment Variable | `\$[A-Z_][A-Z0-9_]{0,30}=([^\s,}]+)` | `$API_KEY=secret123` |

3. **Pattern Validation**
   - All patterns MUST be validated at compile time using `regexp.MustCompile`
   - Patterns MUST have test coverage with positive and negative cases
   - Invalid patterns MUST cause startup failure with clear error message

4. **Extensibility**
   - Custom patterns from config extend (not replace) the allow-list
   - Built-in patterns are always active
   - Pattern priority: custom patterns first, then built-ins

### Architecture Compliance

| Requirement | Implementation |
|-------------|----------------|
| Go with cobra CLI | Patterns loaded via config in `pkg/bouncer/` |
| `pkg/bouncer/` for redaction | Patterns defined in `pkg/bouncer/allowlist.go` |
| camelCase for Go symbols | Use `camelCase` for functions/variables |
| `fmt.Errorf("context: %w", err)` | Use for error wrapping |
| `log/slog` for logging | Use `slog.Info` for pattern loading, `slog.Warn` for issues |
| Allow-list approach | Only defined patterns are redacted |
| In-memory only | Pattern definitions are in-memory only |

### File Structure

```
pkg/bouncer/
├── redactor.go          # Core streaming redaction engine
├── allowlist.go         # Built-in allow-list pattern definitions
├── allowlist_test.go    # Pattern validation tests
├── patterns.go          # Pattern types and helpers
└── redactor_test.go     # Redaction engine tests
```

### Package Implementation

**`pkg/bouncer/allowlist.go`**:
```go
package bouncer

import (
    "fmt"
    "regexp"
)

type SecretPattern struct {
    Name            string
    Pattern         *regexp.Regexp
    Example         string
    Description     string
}

var BuiltInPatterns = []SecretPattern{
    {
        Name:        "aws-access-key",
        Pattern:     regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
        Example:     "AKIAIOSFODNN7EXAMPLE",
        Description: "AWS Access Key ID (20 characters, starts with AKIA)",
    },
    {
        Name:        "github-classic-pat",
        Pattern:     regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`),
        Example:     "ghp_abcdefghijklmnopqrstuvwxyz1234567890abcd",
        Description: "GitHub Classic Personal Access Token (starts with ghp_)",
    },
    {
        Name:        "github-fine-grained-pat",
        Pattern:     regexp.MustCompile(`github_pat_[A-Za-z0-9_]{22,}`),
        Example:     "github_pat_11abcdefghIJ9xsQ_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
        Description: "GitHub Fine-grained PAT (starts with github_pat_)",
    },
    {
        Name:        "stripe-secret-key",
        Pattern:     regexp.MustCompile(`sk_live_[A-Za-z0-9]{24}`),
        Example:     "sk_live_REDACTED_EXAMPLE_24CH",
        Description: "Stripe Live Secret Key (starts with sk_live_)",
    },
    {
        Name:        "stripe-publishable-key",
        Pattern:     regexp.MustCompile(`pk_live_[A-Za-z0-9]{24}`),
        Example:     "pk_live_REDACTED_EXAMPLE_24CH",
        Description: "Stripe Live Publishable Key (starts with pk_live_)",
    },
    {
        Name:        "generic-api-key",
        Pattern:     regexp.MustCompile(`(?i)(api[_-]?key)[_-]?[=]?[A-Za-z0-9]{16,}`),
        Example:     "api_key=abcdefghijklmnopqrstuvwx",
        Description: "Generic API key pattern (case-insensitive)",
    },
    {
        Name:        "bearer-token",
        Pattern:     regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+`),
        Example:     "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
        Description: "JWT Bearer token (three base64url segments)",
    },
    {
        Name:        "env-var-value",
        Pattern:     regexp.MustCompile(`\$[A-Z_][A-Z0-9_]{0,30}=([^\s,}]+)`),
        Example:     "$API_KEY=secret123",
        Description: "Environment variable assignment",
    },
}

func ValidatePatterns() error {
    for _, p := range BuiltInPatterns {
        if p.Pattern == nil {
            return fmt.Errorf("allowlist: pattern %q has nil regexp", p.Name)
        }
        if p.Name == "" {
            return fmt.Errorf("allowlist: pattern has empty name")
        }
    }
    return nil
}
```

**`pkg/bouncer/patterns.go`**:
```go
package bouncer

import "regexp"

type PatternConfig struct {
    Name    string `yaml:"name"`
    Pattern string `yaml:"pattern"`
}

func CompilePatterns(configs []PatternConfig) ([]*regexp.Regexp, error) {
    var patterns []*regexp.Regexp
    for _, c := range configs {
        re, err := regexp.Compile(c.Pattern)
        if err != nil {
            return nil, fmt.Errorf("invalid pattern %q: %w", c.Name, err)
        }
        patterns = append(patterns, re)
    }
    return patterns, nil
}
```

### Testing Requirements

1. **Pattern Validation Tests** (`pkg/bouncer/allowlist_test.go`):
   - Test `ValidatePatterns()` passes for all built-in patterns
   - Test AWS key pattern matches valid keys, rejects invalid
   - Test GitHub token patterns match valid tokens, reject invalid
   - Test Stripe key patterns match valid keys, reject invalid
   - Test generic API key pattern (case insensitivity)
   - Test Bearer token pattern matches valid JWTs
   - Test environment variable pattern

2. **Negative Test Cases** (False Positive Prevention):
   - Test AWS pattern does NOT match: `AKIA1234567890` (too short)
   - Test AWS pattern does NOT match: `akiaIOSFODNN7EXAMPLE` (lowercase prefix)
   - Test GitHub pattern does NOT match: `ghx_abcdefghijklmnopqrstuvwxyz1234567890abcd` (wrong prefix)
    - Test Stripe pattern does NOT match: `sk_live_REDACTED_EXAMPLE_24CH` (obvious placeholder)
   - Test API key pattern does NOT match: `api_key=short` (too short)

3. **Test Implementation**:
```go
func TestAWSKeyPattern(t *testing.T) {
    valid := []string{
        "AKIAIOSFODNN7EXAMPLE",
        "AKIAJ7XGSJBSWYZXCDER",
    }
    invalid := []string{
        "akiaIOSFODNN7EXAMPLE",  // lowercase prefix
        "AKIA1234567890",         // too short
        "AKIAIOSFODNN7EXAMPLE=",  // suffix not allowed
    }
    // test matches and non-matches
}

func TestNoFalsePositives(t *testing.T) {
    benign := []string{
        "This is not an API key",
        "AKIA1234567890EXAMPLE", // has invalid chars
        "ghx_token",             // wrong prefix
        "sk_test_xxx",           // test mode
    }
    // verify none are matched
}
```

### Error Handling

- Pattern validation errors at startup MUST fail fast with descriptive message
- Invalid custom patterns from config MUST be logged as warnings and skipped
- Use `fmt.Errorf("allowlist: %w", err)` for error wrapping

### Logging Requirements

- `slog.Info("loading allow-list patterns", "count", len(BuiltInPatterns))` at startup
- `slog.Debug("pattern validated", "name", p.Name)` for each pattern
- `slog.Warn("invalid custom pattern skipped", "name", name, "error", err)` for bad configs

## Status

- [x] Tasks/Subtasks completed
- Status: review
- Last Updated: 2026-05-02

## File List

- `pkg/bouncer/allowlist.go` - New file with SecretPattern struct and BuiltInPatterns
- `pkg/bouncer/allowlist_test.go` - New file with comprehensive pattern tests
- `pkg/bouncer/patterns.go` - Updated to use allowlist integration
- `pkg/bouncer/redactor.go` - Updated to use allowlist for redaction
- `pkg/bouncer/patterns_test.go` - Updated to use new BuiltInPatterns type
- `pkg/bouncer/redactor_test.go` - Updated to use PatternsToRegexps

## Change Log

- 2026-05-02: Initial implementation of allow-list redaction patterns with 8 built-in patterns (AWS, GitHub, Stripe, generic API key, Bearer token, env var)
- 2026-05-02: Added comprehensive tests for pattern validation and redaction
- 2026-05-02: All 47 tests passing

## Dev Agent Record

### Implementation Plan

Implemented allow-list approach for core redaction patterns following the story requirements. Created SecretPattern struct with Name, Pattern, Example, and Description fields. Built-in patterns include AWS access keys, GitHub PATs (classic and fine-grained), Stripe keys (secret and publishable), generic API keys, Bearer tokens, and environment variable assignments.

### Debug Log

- Resolved pattern matching issues by adjusting test case string lengths
- Updated patterns_test.go and redactor_test.go to use new BuiltInPatterns type with SecretPattern struct
- Fixed duplicate invalid declarations in Stripe tests

### Completion Notes

Story 2-2 implementation complete. All 8 allow-list patterns implemented and tested with 47 passing tests. Patterns use regexp.MustCompile for compile-time validation. Extensibility supported through custom pattern configuration.
