# Story 1-4: Implement Shadow Manifesting (Config Merging)

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 1-4 |
| **Key** | leanproxy-mcp-1-4 |
| **Epic** | leanproxy-mcp-epic-1 |
| **Title** | Implement Shadow Manifesting (Config Merging) |

## Story Requirements

### User Story

```
As a developer
I want to merge multiple MCP server configurations
So that I can override base configs with user-specific settings
```

### Acceptance Criteria (BDD Format)

```gherkin
Feature: Shadow Manifesting (Config Merging)

  Scenario: Merge base config with user override
    Given a base server config with all fields set
    And a user override config with only some fields set
    When I merge the configs
    Then the result should have user values for overridden fields
    And the result should retain base values for non-overridden fields

  Scenario: Deep merge nested objects
    Given a base config with nested auth settings
    And a user override with different auth token
    When I merge the configs
    Then auth section should use user token
    And other nested sections should retain base values

  Scenario: Arrays are replaced, not concatenated
    Given a base config with allowedServers list
    And a user override with different allowedServers list
    When I merge the configs
    Then the result should have user's allowedServers list
    And base allowedServers should be completely replaced

  Scenario: Null values remove inherited fields
    Given a base config with an optional field set
    And a user override with null value for that field
    When I merge the configs
    Then the result should not include that field

  Scenario: Multiple config layers
    Given a base config, user config, and runtime config
    When configs are merged in order (base -> user -> runtime)
    Then later configs should override earlier ones
    And the result should reflect all three layers

  Scenario: Validation after merge
    Given a merged config
    Then the merged config should be valid
    And all required fields should be present
    And all field values should pass validation rules

  Scenario: Preserve metadata during merge
    Given a merged config
    When I inspect the result
    Then I should know which layer each value came from
    And I should be able to trace the merge history
```

## Developer Context

### Technical Requirements

1. **Merge Strategy**
   - Deep merge for nested maps/structs
   - Replace for arrays/slices
   - Null sentinel to remove fields
   - Ordered layer precedence

2. **Configuration Schema**
   ```go
   type Config struct {
       Version   string            `json:"version"`
       Name      string            `json:"name"`
       Servers   []ServerConfig    `json:"servers"`
       Auth      *AuthConfig       `json:"auth,omitempty"`
       Limits    *LimitsConfig     `json:"limits,omitempty"`
       Logging   *LoggingConfig    `json:"logging,omitempty"`
   }

   type ServerConfig struct {
       ID       string   `json:"id"`
       Command  []string `json:"command"`
       Env      []string `json:"env,omitempty"`
       Port     int      `json:"port"`
       Metadata Metadata `json:"_meta,omitempty"`
   }
   ```

3. **Layer Tracking**
   - Annotate merged values with source
   - Support merge conflict detection
   - Provide diff output

4. **File Formats**
   - Support JSON and YAML input
   - Auto-detect format from extension
   - Validate schema before merge

### Architecture Compliance

- **Package**: `pkg/utils/manifest.go`
- **Interface**: `ManifestMerger` struct with `Merge` method
- **Naming**: camelCase for all exported symbols
- **Error Wrapping**: `fmt.Errorf("manifest: context: %w", err)`
- **Logging**: All logs via `log/slog` to stderr
- **Performance**: Merge < 10ms for typical configs

### File Structure

```
pkg/utils/
├── manifest.go        # Config merging implementation
├── manifest_test.go   # Unit tests
└── doc.go            # Package documentation
```

### API Design

```go
// ManifestMerger handles multi-layer config merging
type ManifestMerger struct {
    logger *slog.Logger
}

// NewManifestMerger creates a new merger instance
func NewManifestMerger(logger *slog.Logger) *ManifestMerger

// Merge combines multiple configs into one
// Later configs override earlier ones
func (m *ManifestMerger) Merge(ctx context.Context, configs ...*Config) (*Config, error)

// MergeFiles reads and merges config files
// Formats: .json, .yaml, .yml
func (m *ManifestMerger) MergeFiles(ctx context.Context, paths ...string) (*Config, error)

// Layer represents a config layer with source info
type Layer struct {
    Source string
    Config *Config
}

// MergeWithLayers preserves layer information
func (m *ManifestMerger) MergeWithLayers(ctx context.Context, layers ...Layer) (*MergedConfig, error)

// MergedConfig contains result with merge metadata
type MergedConfig struct {
    Config    *Config
    Sources   map[string][]string  // field -> [source files]
    Timestamp time.Time
}
```

### Testing Requirements

1. **Unit Tests**
   - Test simple field override
   - Test nested object merge
   - Test array replacement
   - Test null field removal
   - Test multi-layer merge
   - Test validation

2. **Integration Tests**
   - Test real JSON/YAML files
   - Test with actual manifest files

3. **Edge Case Tests**
   - Test conflicting required fields
   - Test invalid field types after merge
   - Test circular references
   - Test empty configs

### Implementation Checklist

- [x] Create Config and related types
- [x] Create ManifestMerger struct
- [x] Implement deep merge for nested structs
- [x] Implement array replacement logic
- [x] Implement null field handling
- [x] Implement layer tracking
- [x] Implement file reading with format detection
- [x] Add validation after merge
- [x] Add unit tests
- [x] Test with real config files

### Edge Cases

- Merge with completely empty config
- Override required field with empty value
- Merge results in invalid enum value
- Circular references in nested structs
- Very deeply nested configs (> 10 levels)
- Large arrays (> 1000 elements)
- Conflicting types for same field
- Missing required fields after override

### Notes

- Use reflection or code generation for deep merge
- Consider using deep.Equal for testing
- Keep merge deterministic (no random ordering)
- Document merge order clearly
- Consider JSON Merge Patch RFC 7396

## Dev Agent Record

### Debug Log

### Completion Notes

Implemented shadow manifesting (config merging) for MCP server configurations:
- Created `Config` struct with nested types (`ServerConfig`, `AuthConfig`, `LimitsConfig`, `LoggingConfig`, `Metadata`)
- Implemented `ManifestMerger` struct with `Merge`, `MergeFiles`, `MergeWithLayers` methods
- Deep merge for nested structs (preserves base values for non-overridden fields)
- Array replacement (not concatenation) - later configs completely replace arrays
- Layer tracking via `MergedConfig.Sources` map
- File reading with auto-detection of JSON/YAML format
- Validation after merge via `ManifestMerger.Validate`
- All 16 unit tests passing

## File List

- `pkg/utils/manifest.go` - Main implementation with Config types and ManifestMerger
- `pkg/utils/manifest_test.go` - Comprehensive unit tests
- `pkg/utils/doc.go` - Package documentation
- `go.mod` - Added gopkg.in/yaml.v3 dependency

## Change Log

- 2026-05-01: Initial implementation of shadow manifesting (config merging) with deep merge, array replacement, layer tracking, file reading, and validation

## Status

| Field | Value |
|-------|-------|
| **Status** | review |
| **Last Updated** | 2026-05-01 |
