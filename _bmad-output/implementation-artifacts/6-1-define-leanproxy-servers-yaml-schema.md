# Story 6-1: Define LeanProxy Servers YAML Schema

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 6-1 |
| **Key** | 6-1-define-leanproxy-servers-yaml-schema |
| **Epic** | epic-6 |
| **Title** | Define LeanProxy Servers YAML Schema |

## Story Requirements

### User Story

```
As a developer
I want to define a comprehensive `leanproxy_servers.yaml` schema
So that users can configure MCP servers with transport type, command/args, env vars, and timeouts
```

### Acceptance Criteria (BDD Format)

```gherkin
Feature: LeanProxy Server Configuration Schema

  Scenario: User creates a minimal server configuration
    Given a user configuring their MCP servers
    When they create `~/.config/leanproxy_servers.yaml` with only name and command
    Then defaults are applied for all other settings (enabled: true, timeout: 30s, etc.)

  Scenario: User creates a full stdio transport configuration
    Given a user creating a server entry with stdio transport
    When they specify name, enabled, transport, command, args, and env variables
    Then the configuration includes all specified fields correctly
    And cwd is set for the working directory

  Scenario: User creates an HTTP/SSE transport configuration
    Given a user creating a server entry with http or sse transport
    When they specify name, transport type, and url
    Then they can also specify headers for authentication

  Scenario: User creates a configuration with advanced options
    Given a user creating a server with advanced settings
    When they specify timeout, connect_timeout, cache settings, and summarize settings
    Then all settings are applied correctly

  Scenario: User provides invalid schema (missing required fields)
    Given a server configuration with missing required fields
    When the proxy starts
    Then it reports the validation error
    And exits with a helpful error message

  Scenario: Server entries are properly typed
    Given the YAML schema definition
    Then transport type accepts only: stdio, http, sse
    And timeout values are parsed as durations
    And enabled is a boolean flag
```

## Tasks / Subtasks

- [ ] Task 1: Design and implement leanproxy_servers.yaml schema (AC: 1-5)
  - [ ] Define Go struct for server configuration with proper tags
  - [ ] Implement YAML unmarshaling with validation
  - [ ] Create default values for optional fields
  - [ ] Add documentation comments to schema

- [ ] Task 2: Implement config file discovery and loading (AC: 1-6)
  - [ ] Define config search paths (~/.config/leanproxy_servers.yaml)
  - [ ] Load and parse YAML configuration
  - [ ] Handle missing config file gracefully (start in passthrough mode)
  - [ ] Validate all server entries on load

- [ ] Task 3: Add integration tests (AC: #6)
  - [ ] Test valid minimal configuration
  - [ ] Test valid full configuration with all fields
  - [ ] Test invalid configuration error handling
  - [ ] Test default value application

## Dev Notes

### Architecture Patterns from Existing Stories

- **Project Structure**: Follow `pkg/migrate/config.go` pattern per architecture.md line 134
- **Directory Layout**: `pkg/migrate/` for migration-related code, `pkg/registry/` for server registry
- **Naming Conventions**: camelCase for Go symbols, kebab-case for CLI flags and config keys
- **Error Handling**: `fmt.Errorf("context: %w", err)` pattern exclusively
- **Logging**: Use `log/slog` for structured logging to stderr

### Source Tree Components to Touch

```
pkg/
├── migrate/
│   └── config.go      # NEW - Server config schema and validation
├── registry/
│   └── registry.go   # UPDATE - Integrate with server config schema
cmd/
├── root.go           # UPDATE - Add config path flag
cmd/serve.go         # UPDATE - Load config on startup
```

### Testing Standards Summary

1. **Unit Tests**: Test config parsing, validation, default values
2. **Integration Tests**: Test end-to-end config loading
3. **Use Go's built-in testing package** per architecture.md line 75

### Technical Requirements

1. **Config File Location**: `~/.config/leanproxy_servers.yaml` (primary)
2. **Schema Fields**:
   - `name` (required): Server identifier
   - `enabled` (default: true): Boolean flag
   - `transport` (required): One of stdio, http, sse
   - For stdio: `command`, `args`, `env`, `cwd`
   - For http/sse: `url`, `headers`
   - `timeout` (default: 30s): Request timeout
   - `connect_timeout` (default: 10s): Connection timeout
   - `cache_settings`: Cache configuration
   - `summarize_settings`: Summarization configuration

3. **Validation Rules**:
   - Name must be non-empty
   - Transport must be valid enum value
   - For stdio: command is required
   - For http/sse: url is required

## Project Structure Notes

- Alignment with unified project structure: YES
- **Schema File**: Create `pkg/migrate/config.go` following architecture decisions
- **No conflicts detected** with existing patterns

## References

- [Source: architecture.md#Decision-Config-Schema] - Config schema decisions (lines 134-144)
- [Source: epics.md#Epic-6-Story-6.1] - Story requirements (lines 736-758)
- [Source: architecture.md#Project-Structure] - Project directory structure (lines 196-220)

## Dev Agent Record

### Agent Model Used

{{agent_model_name_version}}

### Debug Log References

N/A - First story in Epic 6

### Completion Notes List

N/A

### File List

- `pkg/migrate/config.go` (NEW)
- `pkg/registry/registry.go` (UPDATE)
- `cmd/root.go` (UPDATE)
- `cmd/serve.go` (UPDATE)
- `pkg/migrate/config_test.go` (NEW - tests)