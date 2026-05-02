# Story 6-3: Validate Imported Server Configurations

Status: review

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 6-3 |
| **Key** | 6-3-validate-imported-server-configs |
| **Epic** | epic-6 |
| **Title** | Validate Imported Server Configurations |

## Story Requirements

### User Story

```
As a user
I want to see validation errors during migration
So that I know which servers might not work and why
```

### Acceptance Criteria (BDD Format)

```gherkin
Feature: Server Configuration Validation

  Scenario: Validate imported server with missing executable
    Given an imported server with a missing executable command
    When the migration validates the config
    Then an error is reported: "Server 'github': command 'npx' not found in PATH"

  Scenario: Validate imported server with invalid transport type
    Given an imported server with invalid transport type
    When the migration validates the config
    Then an error is reported: "Server 'myserver': invalid transport 'ftp'. Must be stdio, http, or sse"

  Scenario: Validate imported server with missing required field
    Given an imported server with missing required field
    When the migration validates the config
    Then an error is reported with the specific field missing

  Scenario: Migration completes with warnings but continues
    Given validation errors occur during migration
    When the import completes
    Then the summary shows: "Imported X servers, Y warnings"
    And warnings are displayed but don't block import

  Scenario: User runs validate-only mode
    Given the user runs `leanproxy migrate --validate-only`
    When the command executes
    Then only validation runs without importing
    And all validation errors are reported
    And no changes are made to leanproxy_servers.yaml

  Scenario: Successful validation shows no errors
    Given all imported servers are valid
    When validation completes
    Then a success message is displayed
    And migration can proceed normally

  Scenario: Validation checks command executables in PATH
    Given a server with command "npx"
    When validation runs
    Then it checks if "npx" exists in system PATH
    And it reports missing executables clearly
```

## Tasks / Subtasks

- [x] Task 1: Implement validation engine (AC: 1-6)
  - [x] Create validator interface in pkg/migrate/
  - [x] Implement executable PATH check for stdio transport
  - [x] Implement transport type validation
  - [x] Implement required field validation
  - [x] Collect and format validation errors

- [x] Task 2: Integrate validation into migration flow (AC: 1-4, 6)
  - [x] Run validation after scanning, before import
  - [x] Display validation errors with helpful messages
  - [x] Continue migration on non-critical errors (warnings)
  - [x] Block import only on critical errors if configured

- [x] Task 3: Add --validate-only flag (AC: 5)
  - [x] Add flag to migrate command
  - [x] Implement validation-only mode that skips import
  - [x] Return appropriate exit codes (0 if valid, 1 if errors)

- [x] Task 4: Add tests for validation scenarios (AC: 1-6)
  - [x] Test missing executable detection
  - [x] Test invalid transport type detection
  - [x] Test missing field detection
  - [x] Test validate-only mode

## Dev Notes

### Architecture Patterns from Epic 6 Stories

- **Package Location**: `pkg/migrate/` per architecture.md line 146
- **Validation Output**: "Server 'name': command 'cmd' not found in PATH" style errors per architecture.md line 151
- **Error Handling**: `fmt.Errorf("context: %w", err)` pattern
- **Logging**: Use `log/slog` for structured logging to stderr

### Source Tree Components to Touch

```
pkg/
├── migrate/
│   ├── validator.go     # NEW - Validation engine
│   ├── migrate.go       # UPDATE - Integrate validation
│   └── config.go        # UPDATE - May need validation helpers
cmd/
└── migrate.go           # UPDATE - Add --validate-only flag
```

### Testing Standards Summary

1. **Unit Tests**: Test each validation rule independently
2. **Integration Tests**: Test validation in migration context
3. **Use Go's built-in testing package**

### Technical Requirements

1. **Validation Rules**:
   - Command exists in system PATH (for stdio transport)
   - Transport type is valid enum (stdio, http, sse)
   - Required fields are present (name, command/url depending on transport)
   - URL is valid format (for http/sse transport)

2. **Error Message Format**:
   - "Server 'name': command 'cmd' not found in PATH"
   - "Server 'name': invalid transport 'type'. Must be stdio, http, or sse"
   - "Server 'name': missing required field 'field'"

3. **Exit Codes**:
   - 0: Validation passed or --validate-only with no errors
   - 1: Validation failed

## Project Structure Notes

- Alignment with unified project structure: YES
- Follows existing migration patterns from story 6-2
- No conflicts detected

## References

- [Source: architecture.md#Decision-Migration-Engine] - Migration validation phase (lines 148-151)
- [Source: epics.md#Epic-6-Story-6.3] - Story requirements (lines 795-823)
- [Source: Story 6-2] - Scanner and migration flow (dependencies)
- [Source: Story 6-1] - Config schema (dependencies)

## Dev Agent Record

### Agent Model Used

openrouter/minimax/minimax-m2.7

### Debug Log References

N/A

### Completion Notes List

- Created `pkg/migrate/validator.go` with full validation engine supporting:
  - Executable PATH checking for stdio transport
  - Transport type validation (stdio, http, sse)
  - Required field validation per transport type
  - URL format validation for http/sse
  - ValidationResult with Errors and Warnings collections
- Updated `pkg/migrate/migrate.go`:
  - Added Validate() method to Migrator
  - Added Validation field to ImportResult
  - Validation runs before import with results stored in ImportResult
- Updated `cmd/migrate.go`:
  - Added --validate-only flag
  - Validation-only mode shows errors/warnings without importing
  - Returns exit code 1 on validation errors
  - Shows success message when all servers pass validation
- Created comprehensive tests in `pkg/migrate/validator_test.go`:
  - 63 tests covering all validation scenarios
  - Tests for missing executables, invalid transport, missing fields
  - Tests for valid stdio/http/sse configurations

### File List

- `pkg/migrate/validator.go` (NEW)
- `pkg/migrate/validator_test.go` (NEW)
- `pkg/migrate/migrate.go` (UPDATE)
- `cmd/migrate.go` (UPDATE)

### Change Log

- 2026-05-02: Implemented validation engine with PATH checking, transport validation, and required field validation. Added --validate-only flag to migrate command. All 63 tests pass.

### File List

- `pkg/migrate/validator.go` (NEW)
- `pkg/migrate/migrate.go` (UPDATE)
- `cmd/migrate.go` (UPDATE)
- `pkg/migrate/validator_test.go` (NEW - tests)