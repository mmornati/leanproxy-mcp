# Story 6-2: Implement Auto-Detection and Migration

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 6-2 |
| **Key** | 6-2-auto-detect-migration |
| **Epic** | epic-6 |
| **Title** | Implement Auto-Detection and Migration |

## Story Requirements

### User Story

```
As a user
I want to run `leanproxy migrate` to auto-detect and import all MCP configs
So that I can move from OpenCode, Claude Code, VS Code, or Cursor without manual setup
```

### Acceptance Criteria (BDD Format)

```gherkin
Feature: MCP Configuration Auto-Detection and Migration

  Scenario: User runs migrate command with existing MCP configs
    Given existing MCP configurations on the system
    When the user runs `leanproxy migrate`
    Then the system scans known locations:
      - `~/.config/opencode/mcp.json`
      - `~/.claude.json` and `~/.config/claude/mcp_config.json`
      - VS Code settings.json (MCP extensions section)
      - `~/.cursor/mcp.json`
      - `~/.config/mcp.json`
    And displays a summary of found configs

  Scenario: User sees migration summary before import
    Given multiple MCP configs are found
    When the scan completes
    Then a summary is displayed showing:
      - Number of configs found
      - Servers to be imported per tool
      - Total server count

  Scenario: User confirms and completes migration
    Given the user has reviewed the migration summary
    When they confirm the import
    Then servers are merged into `leanproxy_servers.yaml`
    And duplicate server names are handled with suffix (_opencode, _claude, etc.)
    And a success message shows imported servers

  Scenario: User runs migrate but no configs found
    Given no MCP configs exist on the system
    When the migrate command runs
    Then a message explains no configs were found
    And suggests manual server addition

  Scenario: System scans OpenCode MCP config
    Given OpenCode is installed with MCP servers configured
    When migration runs
    Then it reads `~/.config/opencode/mcp.json`
    And extracts server name, command, and arguments
    And adds suffix _opencode to duplicate names

  Scenario: System scans Claude Code MCP config
    Given Claude Code is installed with MCP servers configured
    When migration runs
    Then it reads `~/.claude.json` and `~/.config/claude/mcp_config.json`
    And extracts server configurations

  Scenario: System scans VS Code MCP extensions
    Given VS Code is installed with MCP extensions
    When migration runs
    Then it reads VS Code settings.json
    And extracts MCP server configurations from extensions

  Scenario: System scans Cursor MCP config
    Given Cursor is installed with MCP servers configured
    When migration runs
    Then it reads `~/.cursor/mcp.json`
    And extracts server configurations
```

## Tasks / Subtasks

- [ ] Task 1: Implement config file scanner (AC: 1, 4-7)
  - [ ] Create scanner interface for different IDE configs
  - [ ] Implement OpenCode config reader (~/.config/opencode/mcp.json)
  - [ ] Implement Claude Code config reader (~/.claude.json, ~/.config/claude/mcp_config.json)
  - [ ] Implement VS Code settings reader (settings.json MCP section)
  - [ ] Implement Cursor config reader (~/.cursor/mcp.json)
  - [ ] Implement generic mcp.json reader (~/.config/mcp.json)

- [ ] Task 2: Implement migration summary and confirmation (AC: 2-3)
  - [ ] Create summary display with found servers
  - [ ] Implement interactive confirmation flow
  - [ ] Implement non-interactive mode with --yes flag

- [ ] Task 3: Implement server import with conflict resolution (AC: 3)
  - [ ] Merge discovered servers into leanproxy_servers.yaml
  - [ ] Handle duplicate names with suffix (_opencode, _claude, etc.)
  - [ ] Save merged configuration

- [ ] Task 4: Add CLI command (AC: 1-3)
  - [ ] Create `leanproxy migrate` command
  - [ ] Add --yes flag for non-interactive mode
  - [ ] Add --dry-run flag to preview without importing

## Dev Notes

### Architecture Patterns from Epic 6 Stories

- **Package Location**: All migration code in `pkg/migrate/` per architecture.md line 146
- **Migration Engine Phases**: Scan → Validate → Import per architecture.md lines 148-151
- **Discovery Locations**: Listed in architecture.md lines 137-142
- **Conflict Resolution**: Local config wins; imported servers get `_opencode`, `_claude` suffixes per architecture.md line 144
- **Naming Conventions**: camelCase for Go symbols
- **Error Handling**: `fmt.Errorf("context: %w", err)` pattern

### Source Tree Components to Touch

```
pkg/
├── migrate/
│   ├── scanner.go      # NEW - Config file scanner interface
│   ├── opencode.go     # NEW - OpenCode config reader
│   ├── claude.go       # NEW - Claude Code config reader
│   ├── vscode.go       # NEW - VS Code settings reader
│   ├── cursor.go       # NEW - Cursor config reader
│   ├── generic.go      # NEW - Generic mcp.json reader
│   ├── migrate.go      # NEW - Main migration orchestrator
│   └── config.go       # UPDATE - May need updates for import logic
cmd/
├── migrate.go          # NEW - migrate subcommand
```

### Testing Standards Summary

1. **Unit Tests**: Test each scanner for specific IDE config format
2. **Integration Tests**: Test full migration flow with mock config files
3. **Use Go's built-in testing package**

### Technical Requirements

1. **Supported IDEs**:
   - OpenCode: `~/.config/opencode/mcp.json`
   - Claude Code: `~/.claude.json`, `~/.config/claude/mcp_config.json`
   - VS Code: settings.json (MCP extensions section)
   - Cursor: `~/.cursor/mcp.json`
   - Generic: `~/.config/mcp.json`

2. **Migration Flow**:
   - Phase 1 - Scan: Detect all known config file locations
   - Phase 2 - Validate: Check executables in PATH, validate transport types
   - Phase 3 - Import: Merge into leanproxy_servers.yaml

3. **Duplicate Handling**: Add suffix based on source tool

## Project Structure Notes

- Alignment with unified project structure: YES
- Migration components follow architecture.md decisions
- No conflicts detected with existing patterns

## References

- [Source: architecture.md#Decision-Migration-Engine] - Migration engine decisions (lines 146-151)
- [Source: architecture.md#Decision-Config-Schema] - Config schema and discovery locations (lines 134-144)
- [Source: epics.md#Epic-6-Story-6.2] - Story requirements (lines 760-793)
- [Source: Story 6-1] - Schema definitions (dependencies)

## Dev Agent Record

### Agent Model Used

{{agent_model_name_version}}

### Debug Log References

N/A

### Completion Notes List

N/A

### File List

- `pkg/migrate/scanner.go` (NEW)
- `pkg/migrate/opencode.go` (NEW)
- `pkg/migrate/claude.go` (NEW)
- `pkg/migrate/vscode.go` (NEW)
- `pkg/migrate/cursor.go` (NEW)
- `pkg/migrate/generic.go` (NEW)
- `pkg/migrate/migrate.go` (NEW)
- `pkg/migrate/config.go` (UPDATE)
- `cmd/migrate.go` (NEW)
- `pkg/migrate/*_test.go` (NEW - tests)