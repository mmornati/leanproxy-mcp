# Story 1-1: Initialize Go Project with CLI Structure

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 1-1 |
| **Key** | tokengate-mcp-1-1 |
| **Epic** | tokengate-mcp-epic-1 |
| **Title** | Initialize Go Project with CLI Structure |

## Story Requirements

### User Story

```
As a developer
I want to have a properly initialized Go project with a clean CLI structure
So that I can build the tokengate-mcp tool with proper separation of concerns
```

### Acceptance Criteria (BDD Format)

```gherkin
Feature: Go Project Initialization

  Scenario: Project structure is correctly organized
    Given I have initialized the Go module
    When I examine the project structure
    Then I should see cmd/ directory for CLI entrypoints
    And I should see pkg/ directory for shared libraries
    And I should see pkg/proxy, pkg/bouncer, pkg/registry, pkg/utils subdirectories

  Scenario: CLI application can be built
    Given the project has been initialized
    When I run `go build -o tokengate-mcp ./cmd/`
    Then the build should succeed
    And the binary should be under 20MB

  Scenario: CLI displays help information
    Given the tokengate-mcp binary exists
    When I run `./tokengate-mcp --help`
    Then I should see usage information
    And I should see available commands listed

  Scenario: Code follows naming conventions
    Given the project structure exists
    Then all Go functions and variables should use camelCase
    And all CLI flags should use kebab-case
    And error wrapping should use fmt.Errorf("context: %w", err) pattern
```

## Developer Context

### Technical Requirements

1. **Go Module Initialization**
   - Module name: `github.com/mmornati/tokengate-mcp`
   - Go version: 1.21+
   - Dependencies: cobra for CLI, slog for logging

2. **CLI Structure (cmd/)**
   - `cmd/root.go` - Root command with global flags
   - `cmd/serve.go` - Serve command to start the proxy
   - `cmd/version.go` - Version command

3. **Package Structure (pkg/)**
   - `pkg/proxy/` - JSON-RPC streaming proxy core
   - `pkg/bouncer/` - Token validation and auth
   - `pkg/registry/` - MCP server registry
   - `pkg/utils/` - Shared utilities

4. **Required Dependencies**
   ```go
   import (
       "github.com/spf13/cobra"
       "log/slog"
       "fmt"
   )
   ```

### Architecture Compliance

- **Directory Layout**: `cmd/` for CLI, `pkg/` for libraries
- **Naming Conventions**: camelCase for Go symbols, kebab-case for flags
- **Error Handling**: `fmt.Errorf("context: %w", err)` pattern exclusively
- **Logging**: `log/slog` for structured logging to stderr
- **Performance**: No I/O in init(); lazy initialization pattern

### File Structure

```
tokengate-mcp/
├── cmd/
│   ├── root.go           # Root command setup
│   ├── serve.go          # serve subcommand
│   └── version.go        # version subcommand
├── pkg/
│   ├── proxy/
│   │   └── proxy.go      # Streaming proxy interface
│   ├── bouncer/
│   │   └── bouncer.go    # Token validation interface
│   ├── registry/
│   │   └── registry.go   # Server registry interface
│   └── utils/
│       └── utils.go      # Shared utilities
├── go.mod
├── go.sum
└── main.go               # Entry point
```

### Testing Requirements

1. **Unit Tests**
   - Test each package has valid Go syntax
   - Test that imports resolve correctly
   - Test CLI help command works

2. **Build Verification**
   ```bash
   go build -o tokengate-mcp ./cmd/
   size tokengate-mcp  # Should be < 20MB
   ./tokengate-mcp --help
   ```

3. **No Test Framework Required** for this story
   - Focus is on project structure and build system

### Implementation Checklist

- [x] Initialize Go module with `go mod init`
- [x] Create directory structure (cmd/, pkg/*)
- [x] Create main.go entry point
- [x] Create cmd/root.go with Cobra root command
- [x] Create cmd/serve.go with serve command
- [x] Create cmd/version.go with version command
- [x] Create placeholder pkg/* files with package declarations
- [x] Add required dependencies
- [x] Verify build succeeds
- [x] Verify binary size < 20MB
- [x] Verify --help works
- [x] Run go vet and golint

### Notes

- Keep implementation minimal for this story - focus on structure
- No actual proxy logic needed yet
- Placeholder implementations acceptable for pkg/* files
- Ensure all logging goes to stderr via slog

## Dev Agent Record

### Debug Log

- Fixed export issue: changed `rootCmd` to `RootCmd` to make it accessible from main.go
- Fixed duplicate command registration: removed explicit AddCommand calls from root.go init() since serve.go and version.go already register themselves in their own init()
- Renamed project from tokengate-mcp to leanproxy-mcp throughout all files
- Binary size: 4.7MB (well under 20MB limit)

### Completion Notes

Successfully initialized the Go project with:
- Go module: github.com/mmornati/leanproxy-mcp
- CLI structure with Cobra framework
- Commands: root, serve, version
- Package structure: pkg/proxy, pkg/bouncer, pkg/registry, pkg/utils
- Proper error handling with fmt.Errorf pattern
- Structured logging with log/slog

### File List

- go.mod (new)
- go.sum (new)
- main.go (new)
- cmd/root.go (new)
- cmd/serve.go (new)
- cmd/version.go (new)
- pkg/proxy/proxy.go (new)
- pkg/bouncer/bouncer.go (new)
- pkg/registry/registry.go (new)
- pkg/utils/utils.go (new)
- leanproxy (compiled binary)

### Change Log

- Initial project structure setup (Date: 2026-05-01)

### Status

review
