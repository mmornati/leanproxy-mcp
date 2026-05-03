# Story 3-4: Manual Re-Distillation Command

## Header

| Field | Value |
|-------|-------|
| ID | 3-4 |
| Key | manual-re-distillation |
| Epic | Epic 3: Context Optimization (JIT Discovery & Compactor) |
| Title | Implement Manual Re-Distillation Command |
| Status | review |
| Estimated Points | 2 |

## User Story

**As a** user,
**I want to** force re-distillation of a server manifest via CLI,
**So that** I can refresh stale discovery signatures when tool descriptions change.

## Acceptance Criteria (BDD Format)

### AC1: Server-Specific Rebuild

**Given** a configured MCP server with an existing distilled manifest
**When** the user runs `leanproxy compactor rebuild <server-name>`
**Then** a fresh distillation is triggered
**And** the new distilled manifest replaces the cached version
**And** a success message is displayed

### AC2: Unknown Server Error

**Given** a server that doesn't exist in the registry
**When** the rebuild command is run
**Then** an appropriate error is returned
**And** the existing manifest is preserved

### AC3: Full Registry Rebuild

**Given** multiple servers with distilled manifests
**When** the user runs `leanproxy compactor rebuild --all`
**Then** all servers are re-distilled
**And** each operation is logged to stderr
**And** a summary of results is displayed

### AC4: Progress Indication

**Given** a server with multiple tools
**When** the rebuild command is run
**Then** the operation can take several seconds (logged to stderr)
**And** progress is shown for each tool distilled

## Developer Context

### Technical Requirements

1. **CLI Command Structure**
   - Add `compactor` subcommand under root `leanproxy`
   - Add `rebuild` subcommand under `compactor`
   - Flags: `--server` (optional), `--all` flag for full rebuild

2. **Rebuild Logic**
   - Clear existing distilled manifest from cache
   - Trigger new distillation via Compactor
   - Update registry with new distilled manifest
   - Persist to cache file

3. **User Feedback**
   - Use slog for structured progress logging to stderr
   - Display "Distilling [tool-name]..." for each tool
   - Display "Done. Reduced from X to Y tokens (Z% reduction)"
   - Exit code 0 on success, 1 on any failure

4. **Error Handling**
   - If distillation fails, preserve original manifest
   - Log error details to stderr
   - Do not modify registry state on failure

### Architecture Compliance

- **Naming**: `camelCase` for Go functions/variables, `kebab-case` for CLI flags
- **Error Handling**: `fmt.Errorf("context: %w", err)` for error wrapping
- **Logging**: `log/slog` for structured logging to stderr
- **Project Structure**: `cmd/leanproxy/` for CLI, `pkg/compactor/` for logic

### File Structure

```
cmd/
└── leanproxy/
    ├── main.go               # Root cobra command setup
    └── compactor.go          # Compactor subcommands

pkg/
└── compactor/
    ├── compactor.go          # Main compactor (existing from 3-3)
    └── cache.go              # Cache operations
```

### Testing Requirements

1. **Unit Tests**
   - Test rebuild command flag parsing
   - Test cache invalidation logic
   - Test error handling when server not found

2. **Integration Tests**
   - Test full rebuild flow with mock LLM
   - Verify cache file is updated
   - Verify registry is updated

3. **CLI Tests**
   - Test `--help` output
   - Test unknown server error message
   - Test `--all` flag behavior

## Implementation Notes

### CLI Command Implementation

```go
// cmd/leanproxy/compactor.go
var compactorRebuildCmd = &cobra.Command{
    Use:   "rebuild [server-name]",
    Short: "Force re-distillation of server manifests",
    Long:  `Force re-distillation of MCP server manifests to refresh stale discovery signatures.`,
    Args:  cobra.RangeArgs(0, 1),
    RunE: func(cmd *cobra.Command, args []string) error {
        all, _ := cmd.Flags().GetBool("all")
        if all {
            return rebuildAllServers()
        }
        if len(args) == 0 {
            return fmt.Errorf("specify server name or use --all")
        }
        return rebuildServer(args[0])
    },
}

func rebuildServer(name string) error {
    slog.Info("starting re-distillation", "server", name)
    
    // Clear cache
    if err := compactor.ClearCache(name); err != nil {
        return fmt.Errorf("clear cache: %w", err)
    }
    
    // Re-distill
    result, err := compactor.DistillServer(name)
    if err != nil {
        return fmt.Errorf("distill: %w", err)
    }
    
    slog.Info("re-distillation complete",
        "server", name,
        "original_tokens", result.OriginalTokens,
        "distilled_tokens", result.DistilledTokens,
        "reduction_percent", result.ReductionPercent)
    
    return nil
}
```

### Flag Registration

```go
// In compactor command init()
rebuildCmd.Flags().Bool("all", false, "Rebuild all servers")
compactorCmd.AddCommand(rebuildCmd)
```

### Output Format

```
$ leanproxy compactor rebuild github
Distilling tools list... done (12 tools)
Distilling: read_file... done
Distilling: write_file... done
...
Done. Reduced from 2,450 to 490 tokens (80% reduction)
```

## Dev Agent Record

### Implementation Plan

1. Created `cmd/compactor.go` with `compactor` and `rebuild` subcommands
2. Implemented `rebuildServer()` for single server re-distillation
3. Implemented `rebuildAllServers()` for full registry rebuild
4. Added `buildRawManifest()` helper for testing
5. Created `cmd/compactor_test.go` with comprehensive unit tests

### Debug Log

- Initial implementation used `userConfigPath` function directly in tests - required switching to `t.Setenv()` for environment variable mocking
- Build and all 402 tests pass

### Completion Notes

Implemented `leanproxy compactor rebuild` command with:
- Single server rebuild: `leanproxy compactor rebuild <server-name>`
- Full rebuild: `leanproxy compactor rebuild --all`
- Cache invalidation before re-distillation
- Structured logging via slog to stderr
- Token reduction reporting
- Error handling for unknown/disabled servers

## File List

- `cmd/compactor.go` - New compactor CLI command implementation
- `cmd/compactor_test.go` - Unit tests for rebuild command

## Change Log

- **2026-05-03**: Initial implementation complete. Created `cmd/compactor.go` with `compactor` subcommand and `rebuild` subcommand. All 402 tests pass. Status updated to "review".
