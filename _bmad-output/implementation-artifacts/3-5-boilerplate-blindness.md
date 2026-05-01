# Story 3-5: Boilerplate Blindness

## Header

| Field | Value |
|-------|-------|
| ID | 3-5 |
| Key | boilerplate-blindness |
| Epic | Epic 3: Context Optimization (JIT Discovery & Compactor) |
| Title | Implement Boilerplate Blindness |
| Status | backlog |
| Estimated Points | 3 |

## User Story

**As a** developer,
**I want to** prune redundant imports and boilerplate from file-read results,
**So that** large file reads don't consume excessive tokens.

## Acceptance Criteria (BDD Format)

### AC1: Import Statement Pruning

**Given** a file read result containing import statements
**When** the result passes through the proxy
**Then** common import blocks are identified
**And** replaced with a compact `[IMPORTS_REDACTED]` marker
**And** the actual file content is preserved

### AC2: Copyright Header Pruning

**Given** a file read result containing copyright headers
**When** the result passes through the proxy
**Then** standard copyright blocks are identified
**And** replaced with `[LICENSE_REDACTED]`
**And** the actual code content is preserved

### AC3: No-Op for Clean Files

**Given** a file read result with no boilerplate
**When** it passes through the proxy
**Then** it passes through unchanged

### AC4: Configurable Disable

**Given** boilerplate blindness is disabled in config
**When** file read results pass through
**Then** no modifications are made

## Developer Context

### Technical Requirements

1. **Boilerplate Detection Patterns**
   - Go imports: `^import\s+\([\s\S]*?\)|import\s+"[^"]+"`
   - Python imports: `^from\s+[\w.]+\s+import|^import\s+[\w.,\s]+`
   - JavaScript/TypeScript imports: `^import\s+.*from\s+['"][^'"]+['"]`
   - Copyright: `/\*[\s\S]*?Copyright[\s\S]*?\*/|^#.*Copyright.*$`
   - License blocks: Apache, MIT, GPL license headers

2. **Streaming Processing**
   - Process file content line by line or in chunks
   - Identify and replace boilerplate regions
   - Preserve actual code content exactly
   - Handle files up to 50MB without memory issues

3. **Replacement Markers**
   - Imports: `[IMPORTS_REDACTED:{count} lines]`
   - Copyright: `[LICENSE_REDACTED]`
   - Generic boilerplate: `[BOILERPLATE_REDACTED:{type}]`

4. **Content Type Detection**
   - Detect language from file extension or shebang
   - Apply language-specific patterns
   - Fall back to generic patterns

5. **Configuration**
   - Add `boilerplate.enabled` config option (default: true)
   - Add `boilerplate.redact-imports` config option (default: true)
   - Add `boilerplate.redact-licenses` config option (default: true)
   - Add `boilerplate.custom-patterns` for user-defined patterns

### Architecture Compliance

- **Naming**: `camelCase` for Go functions/variables, `kebab-case` for CLI flags
- **Error Handling**: `fmt.Errorf("context: %w", err)` for error wrapping
- **Logging**: `log/slog` for structured logging to stderr
- **Project Structure**: `pkg/bouncer/` for content processing, `pkg/utils/` for patterns

### File Structure

```
pkg/
├── bouncer/
│   ├── bouncer.go            # Main bouncer orchestration
│   ├── redaction.go          # Secret redaction (existing)
│   └── boilerplate.go        # NEW: Boilerplate blindness logic
│   └── boilerplate_test.go   # Unit tests
└── utils/
    └── patterns.go           # Shared regex patterns
```

### Testing Requirements

1. **Unit Tests**
   - Test each import pattern detection
   - Test copyright pattern detection
   - Test replacement markers are correct
   - Test edge cases (empty file, no boilerplate)

2. **Integration Tests**
   - Test with sample Go, Python, JS files
   - Verify token reduction calculation
   - Verify content integrity (code still works)

3. **Performance Tests**
   - Test 50MB file processing completes in <200ms
   - Verify memory usage stays bounded

## Implementation Notes

### Boilerplate Processor Interface

```go
// pkg/bouncer/boilerplate.go
type BoilerplateProcessor interface {
    Process(content []byte, language string) ([]byte, BoilerplateReport, error)
}

type BoilerplateReport struct {
    ImportsRedacted  int
    LicensesRedacted int
    OriginalSize    int
    ProcessedSize   int
    TokenSavings    int
}
```

### Pattern Definitions

```go
// pkg/utils/patterns.go
var (
    GoImportPattern = regexp.MustCompile(`(?m)^import\s+\([\s\S]*?^\)`)
    PyImportPattern = regexp.MustCompile(`(?m)^(from\s+[\w.]+\s+import\s+|import\s+[\w.,\s]+)$`)
    JSEditImportPattern = regexp.MustCompile(`(?m)^import\s+.*from\s+['"][^'"]+['"];?$`)
    
    CopyrightPattern = regexp.MustCompile(`(?si)/\*.{0,500}?[Cc]opyright.{0,200}?\*/`)
    LicensePattern = regexp.MustCompile(`(?si)/(?:Apache|MIT|GPL)[- ]?[Ll]icense.{0,500}?\*/`)
)
```

### Processing Logic

```go
func (p *BoilerplatePruner) Process(content []byte, language string) ([]byte, *Report, error) {
    report := &Report{OriginalSize: len(content)}
    
    // Apply language-specific patterns
    patterns := p.getPatterns(language)
    
    result := content
    for _, pattern := range patterns {
        result = pattern.ReplaceAllFunc(result, func(match []byte) []byte {
            return []byte(fmt.Sprintf("[%s_REDACTED]", pattern.Name))
        })
        report.AddRedaction(pattern.Name, len(match))
    }
    
    report.ProcessedSize = len(result)
    report.TokenSavings = (report.OriginalSize - report.ProcessedSize) * 4 / 3 // rough token estimate
    
    return result, report, nil
}
```

### Configuration Schema

```yaml
boilerplate:
  enabled: true
  redact-imports: true
  redact-licenses: true
  custom-patterns:
    - name: "my-copyright"
      regex: "My Company Inc\\..*?\\n"
```
