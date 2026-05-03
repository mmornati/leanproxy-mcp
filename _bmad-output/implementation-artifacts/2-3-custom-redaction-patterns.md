---
id: 2-3
key: 2-3-custom-redaction-patterns
epic: epic-2
title: Implement Custom Redaction Patterns
---

# Story 2-3: Implement Custom Redaction Patterns

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 2-3 |
| **Key** | `2-3-custom-redaction-patterns` |
| **Epic** | `epic-2` (Security & Data Governance - The Bouncer) |
| **Title** | Implement Custom Redaction Patterns |

## Story Requirements

### User Story

**As a** user,
**I want to** define custom regex patterns for redaction in my local config,
**So that** I can redact project-specific sensitive data beyond the built-in patterns.

### Acceptance Criteria (BDD Format)

```gherkin
Feature: Custom Redaction Patterns
  Users can define custom regex patterns in their local configuration
  to redact project-specific sensitive data that the built-in patterns don't cover.

  Scenario: Custom patterns are loaded from local configuration
    Given a local `leanproxy.yaml` with custom redaction patterns
    When the proxy starts
    Then it loads the custom patterns from the config
    And applies them in addition to built-in patterns

  Scenario: Custom pattern redacts matching secrets
    Given a custom pattern `my-company-key-[A-Z0-9]{20}`
    When a message containing `my-company-key-ABC123XYZ789012345678` is processed
    Then the key is redacted to `[SECRET_REDACTED]`
    And the user is notified via stderr

  Scenario: Invalid regex patterns are handled gracefully
    Given an invalid regex pattern in the config
    When the proxy starts
    Then it logs a warning about the invalid pattern
    And continues startup with only valid patterns

  Scenario: Custom patterns extend built-in patterns
    Given built-in patterns for AWS and GitHub
    And custom patterns for company-specific secrets
    When traffic is processed
    Then both built-in and custom patterns are applied
```

## Developer Context

### Technical Requirements

1. **Configuration Structure**
   - Custom patterns defined in `leanproxy.yaml` under `bouncer.custom_patterns`
   - Each pattern requires: `name` (string), `pattern` (regex string)
   - Patterns are loaded at startup and merged with built-in allow-list

2. **YAML Configuration Schema**:
```yaml
bouncer:
  enabled: true
  custom_patterns:
    - name: "company-api-key"
      pattern: "my-company-key-[A-Z0-9]{20}"
    - name: "internal-token"
      pattern: "int_token_[a-f0-9]{32}"
```

3. **Pattern Compilation**
   - Custom regex patterns MUST be validated using `regexp.Compile` (not `MustCompile`)
   - Invalid patterns MUST generate a warning log but NOT block startup
   - Valid patterns are appended to the built-in pattern list

4. **Redaction Behavior**
   - Custom patterns use the same `[SECRET_REDACTED]` placeholder
   - Custom patterns are checked alongside built-in patterns
   - Order of application: custom patterns first, then built-ins

5. **Integration with CLI**
   - Patterns loaded when bouncer is initialized
   - CLI command `leanproxy bouncer validate-patterns` to test pattern validity
   - CLI command `leanproxy bouncer list-patterns` to see all active patterns

### Architecture Compliance

| Requirement | Implementation |
|-------------|----------------|
| Go with cobra CLI | Add `bouncer` subcommand with `validate-patterns` and `list-patterns` |
| `pkg/bouncer/` for redaction | Custom patterns loaded and merged in `pkg/bouncer/config.go` |
| camelCase for Go symbols | Use `camelCase` for functions/variables |
| kebab-case for CLI flags | `validate-patterns`, `list-patterns` |
| `fmt.Errorf("context: %w", err)` | Use for error wrapping |
| `log/slog` for logging | Use `slog.Warn` for invalid patterns, `slog.Info` for loading |
| Allow-list approach | Custom patterns extend allow-list, never replace |
| Streaming regex redaction | Custom patterns applied via streaming redactor |

### File Structure

```
pkg/bouncer/
├── redactor.go          # Core streaming redaction engine
├── allowlist.go         # Built-in allow-list patterns
├── config.go            # Configuration loading and pattern merging
├── config_test.go       # Config loading tests
├── patterns.go          # Pattern types and helpers
└── redactor_test.go      # Redaction engine tests

cmd/leanproxy/
├── main.go              # CLI entry point
└── bouncer.go           # Bouncer subcommands (validate-patterns, list-patterns)
```

### Package Implementation

**`pkg/bouncer/config.go`**:
```go
package bouncer

import (
    "fmt"
    "io"
    "log/slog"
    "regexp"

    "gopkg.in/yaml.v3"
)

type Config struct {
    Enabled         bool           `yaml:"enabled"`
    CustomPatterns  []PatternDef   `yaml:"custom_patterns"`
}

type PatternDef struct {
    Name    string `yaml:"name"`
    Pattern string `yaml:"pattern"`
}

type LoadedPatterns struct {
    BuiltIn  []SecretPattern
    Custom   []SecretPattern
    All      []*regexp.Regexp
}

func LoadConfig(r io.Reader) (*Config, error) {
    var cfg Config
    if err := yaml.NewDecoder(r).Decode(&cfg); err != nil {
        return nil, fmt.Errorf("bouncer config: %w", err)
    }
    return &cfg, nil
}

func (c *Config) CompilePatterns() (*LoadedPatterns, error) {
    loaded := &LoadedPatterns{
        BuiltIn: BuiltInPatterns,
    }

    for _, p := range c.CustomPatterns {
        re, err := regexp.Compile(p.Pattern)
        if err != nil {
            slog.Warn("invalid custom pattern, skipping",
                "name", p.Name,
                "pattern", p.Pattern,
                "error", err)
            continue
        }
        loaded.Custom = append(loaded.Custom, SecretPattern{
            Name:    p.Name,
            Pattern: re,
        })
        loaded.All = append(loaded.All, re)
    }

    for _, p := range BuiltInPatterns {
        loaded.All = append(loaded.All, p.Pattern)
    }

    slog.Info("patterns compiled",
        "custom_count", len(loaded.Custom),
        "builtin_count", len(loaded.BuiltIn),
        "total_count", len(loaded.All))

    return loaded, nil
}
```

**`cmd/leanproxy/bouncer.go`**:
```go
package main

import (
    "fmt"
    "log/slog"
    "os"

    "github.com/mmornati/leanproxy-mcp/pkg/bouncer"
    "github.com/spf13/cobra"
)

var bouncerConfigPath string

var bouncerCmd = &cobra.Command{
    Use:   "bouncer",
    Short: "Manage Bouncer redaction settings",
}

var validatePatternsCmd = &cobra.Command{
    Use:   "validate-patterns",
    Short: "Validate custom redaction patterns from config",
    Run: func(cmd *cobra.Command, args []string) {
        cfg, err := bouncer.LoadConfigFile(bouncerConfigPath)
        if err != nil {
            slog.Error("failed to load config", "error", err)
            os.Exit(1)
        }
        loaded, err := cfg.CompilePatterns()
        if err != nil {
            slog.Error("failed to compile patterns", "error", err)
            os.Exit(1)
        }
        fmt.Printf("Valid patterns: %d (custom: %d, built-in: %d)\n",
            len(loaded.All), len(loaded.Custom), len(loaded.BuiltIn))
    },
}

var listPatternsCmd = &cobra.Command{
    Use:   "list-patterns",
    Short: "List all active redaction patterns",
    Run: func(cmd *cobra.Command, args []string) {
        loaded := bouncer.GetBuiltInPatterns()
        fmt.Println("# Built-in Patterns")
        for _, p := range loaded {
            fmt.Printf("  - %s: %s\n", p.Name, p.Description)
        }
    },
}

func init() {
    bouncerCmd.PersistentFlags().StringVar(&bouncerConfigPath, "config", "leanproxy.yaml", "path to config file")

    bouncerCmd.AddCommand(validatePatternsCmd)
    bouncerCmd.AddCommand(listPatternsCmd)
    rootCmd.AddCommand(bouncerCmd)
}
```

### Testing Requirements

1. **Config Loading Tests** (`pkg/bouncer/config_test.go`):
   - Test valid YAML config is parsed correctly
   - Test missing optional fields use defaults
   - Test invalid YAML returns error
   - Test empty custom patterns list is valid

2. **Pattern Compilation Tests**:
   - Test valid custom patterns are compiled successfully
   - Test invalid regex patterns generate warnings (not errors)
   - Test compiled patterns include both custom and built-in
   - Test pattern order (custom first, then built-in)

3. **Integration Tests**:
   - Test end-to-end with custom pattern redaction
   - Test custom pattern + built-in pattern both apply
   - Test invalid pattern in config doesn't block startup

4. **Test Implementation**:
```go
func TestCustomPatternRedaction(t *testing.T) {
    cfg := &Config{
        CustomPatterns: []PatternDef{
            {Name: "company-key", Pattern: "my-company-key-[A-Z0-9]{20}"},
        },
    }
    loaded, err := cfg.CompilePatterns()
    require.NoError(t, err)
    assert.Len(t, loaded.All, len(BuiltInPatterns)+1)

    input := `{"key": "my-company-key-ABC123XYZ789012345678"}`
    redacted := RedactString(input, loaded.All)
    assert.Contains(t, redacted, "[SECRET_REDACTED]")
    assert.NotContains(t, redacted, "ABC123XYZ789012345678")
}

func TestInvalidPatternWarning(t *testing.T) {
    cfg := &Config{
        CustomPatterns: []PatternDef{
            {Name: "invalid", Pattern: "[invalid(regex"},
        },
    }
    loaded, err := cfg.CompilePatterns()
    require.NoError(t, err)
    assert.Len(t, loaded.Custom, 0) // invalid pattern skipped
}
```

### Error Handling

- Invalid custom regex: log warning, skip pattern, continue startup
- Config file not found: use defaults, log info message
- Config parse error: return error, block startup
- Use `fmt.Errorf("bouncer config: %w", err)` for config errors
- Use `fmt.Errorf("bouncer pattern %s: %w", name, err)` for pattern errors

### Logging Requirements

- `slog.Info("loading bouncer config", "path", path)` at startup
- `slog.Warn("invalid custom pattern, skipping", "name", name, "error", err)` for bad patterns
- `slog.Info("patterns compiled", "custom_count", n, "builtin_count", m, "total_count", total)`
- `slog.Debug("pattern added", "name", name)` for each successful custom pattern
