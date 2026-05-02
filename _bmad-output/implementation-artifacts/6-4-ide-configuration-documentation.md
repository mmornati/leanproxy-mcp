# Story 6-4: Add IDE Configuration Documentation

Status: review

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 6-4 |
| **Key** | 6-4-ide-configuration-documentation |
| **Epic** | epic-6 |
| **Title** | Add IDE Configuration Documentation |

## Story Requirements

### User Story

```
As a user
I want to configure LeanProxy-MCP as an MCP server in my IDE
So that I can use it with Claude Desktop, Cursor, OpenCode, or Windsurf
```

### Acceptance Criteria (BDD Format)

```gherkin
Feature: IDE Configuration Documentation

  Scenario: User configures LeanProxy-MCP for Claude Desktop
    Given a user reading the README documentation
    When they navigate to the IDE configuration section
    Then they find instructions for Claude Desktop
    And they see how to add `leanproxy` to `mcpServers` in `~/Library/Application Support/Claude/claude_desktop_config.json`
    And they see the transport set to stdio
    And they see the command path pointing to leanproxy binary

  Scenario: User configures LeanProxy-MCP for Cursor
    Given a user reading the README documentation
    When they navigate to the IDE configuration section
    Then they find instructions for Cursor
    And they see how to add to `~/.cursor/mcp.json`
    And they see the configuration format for Cursor

  Scenario: User configures LeanProxy-MCP for OpenCode
    Given a user reading the README documentation
    When they navigate to the IDE configuration section
    Then they find instructions for OpenCode
    And they see how to add to `~/.config/opencode/mcp.json`

  Scenario: User configures LeanProxy-MCP for Windsurf
    Given a user reading the README documentation
    When they navigate to the IDE configuration section
    Then they find instructions for Windsurf
    And they see how to add to `~/.windsurf/mcp.json`

  Scenario: User verifies the connection works
    Given the documentation for each IDE
    When the user follows the configuration steps
    Then they see how to verify the connection works
    And they see what success looks like

  Scenario: User migrates from another MCP tool
    Given a user who has been using another MCP tool
    When they use the leanproxy migrate command
    Then the resulting config is immediately usable by their IDE
    And no manual editing of IDE config files is required

  Scenario: Documentation is accessible from CLI
    Given leanproxy is installed
    When the user runs `leanproxy help`
    Then they see a quick reference to the README for full documentation
    When they run `leanproxy migrate --help`
    Then they see usage instructions

  Scenario: Configuration examples are copy-paste ready
    Given a user reading the documentation
    When they look at IDE configuration examples
    Then each example is complete and ready to copy
    And placeholders are clearly marked (e.g., /path/to/leanproxy)
```

## Tasks / Subtasks

- [x] Task 1: Document Claude Desktop configuration (AC: 1, 5-6, 8)
  - [x] Document config file location
  - [x] Provide JSON configuration template
  - [x] Document verification steps
  - [x] Ensure copy-paste ready example

- [x] Task 2: Document Cursor configuration (AC: 2, 5-6, 8)
  - [x] Document config file location
  - [x] Provide JSON configuration template
  - [x] Document verification steps
  - [x] Ensure copy-paste ready example

- [x] Task 3: Document OpenCode configuration (AC: 3, 5-6, 8)
  - [x] Document config file location
  - [x] Provide JSON configuration template
  - [x] Document verification steps
  - [x] Ensure copy-paste ready example

- [x] Task 4: Document Windsurf configuration (AC: 4, 5-6, 8)
  - [x] Document config file location
  - [x] Provide JSON configuration template
  - [x] Document verification steps
  - [x] Ensure copy-paste ready example

- [x] Task 5: Add CLI help text with documentation references (AC: 7)
  - [x] Update root command help to reference README
  - [x] Update migrate command help with usage
  - [x] Ensure man pages or equivalent if applicable

- [x] Task 6: Review and verify all examples (AC: 8)
  - [x] Test each configuration example
  - [x] Verify syntax is correct JSON
  - [x] Ensure placeholders are clearly marked

## Dev Notes

### Architecture Patterns from Epic 6 Stories

- **Documentation Location**: README.md at project root
- **CLI Help**: Inline help text referencing full documentation
- **Configuration Format**: Follows each IDE's native format

### Source Tree Components to Touch

```
README.md               # UPDATE - Add IDE configuration section
cmd/
├── root.go            # UPDATE - Add documentation reference in help
```

### Testing Standards Summary

1. **Documentation Review**: Manual verification of all examples
2. **JSON Syntax**: Validate all JSON examples
3. **No Test Framework Required**: This is documentation-only story

### Technical Requirements

1. **IDE Configuration Formats**:

   **Claude Desktop** (`~/Library/Application Support/Claude/claude_desktop_config.json`):
   ```json
   {
     "mcpServers": {
       "leanproxy": {
         "command": "/path/to/leanproxy",
         "args": ["serve"]
       }
     }
   }
   ```

   **Cursor** (`~/.cursor/mcp.json`):
   ```json
   {
     "mcpServers": {
       "leanproxy": {
         "command": "/path/to/leanproxy",
         "args": ["serve"]
       }
     }
   }
   ```

   **OpenCode** (`~/.config/opencode/mcp.json`):
   ```json
   {
     "mcpServers": {
       "leanproxy": {
         "command": "/path/to/leanproxy",
         "args": ["serve"]
       }
     }
   }
   ```

   **Windsurf** (`~/.windsurf/mcp.json`):
   ```json
   {
     "mcpServers": {
       "leanproxy": {
         "command": "/path/to/leanproxy",
         "args": ["serve"]
       }
     }
   }
   ```

2. **Common Settings**:
   - Transport: stdio
   - Command: path to leanproxy binary
   - Args: serve (to start in server mode)

## Project Structure Notes

- Alignment with unified project structure: YES
- Follows standard Go project README conventions
- No conflicts detected

## References

- [Source: architecture.md#Decision-IDE-Socket-API] - IDE socket API decisions (lines 153-161)
- [Source: epics.md#Epic-6-Story-6.4] - Story requirements (lines 825-848)
- [Source: Story 6-1] - Config schema (context)

## Dev Agent Record

### Agent Model Used

{{agent_model_name_version}}

### Debug Log References

N/A

### Completion Notes List

- Added comprehensive IDE configuration documentation section to README.md covering Claude Desktop, Cursor, OpenCode, and Windsurf
- Each IDE section includes config file location, JSON template, reload/restart steps, and verification instructions
- Migration section documents `leanproxy migrate` command for users switching from other MCP tools
- Updated root command help text to reference full README documentation
- All JSON examples use `/path/to/leanproxy` as clearly marked placeholder

## File List

- `README.md` (UPDATE - Added IDE Configuration section)
- `cmd/root.go` (UPDATE - Added documentation reference in help text)