# Story 4-1: Implement Dry-Run Mode

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 4-1 |
| **Key** | dry-run-mode |
| **Epic** | Epic 4 - CLI Installation and Interaction |
| **Title** | Implement Dry-Run Mode |
| **Priority** | High |
| **Status** | ready-for-dev |

## Story Requirements

### User Story

As a CLI user, I want to preview command effects before execution so that I can validate configuration and avoid unintended changes.

### Acceptance Criteria (BDD Format)

```gherkin
Feature: Dry-Run Mode for CLI Commands
  Scenario: User runs tokengate with --dry-run flag
    Given the tokengate CLI is installed
    And a valid configuration file exists at ~/.tokengate/config.yaml
    When the user runs "tokengate --dry-run [command]"
    Then the system displays the actions that would be taken
    And no actual changes are made to the system
    And the command exits with status code 0

  Scenario: Dry-run shows configuration validation errors
    Given the tokengate CLI is installed
    And an invalid configuration file exists at ~/.tokengate/config.yaml
    When the user runs "tokengate --dry-run [command]"
    Then the system displays configuration validation errors
    And no actual changes are made to the system
    And the command exits with status code 1

  Scenario: Dry-run validates but does not connect to registry
    Given the tokengate CLI is installed
    And a valid configuration file exists at ~/.tokengate/config.yaml
    When the user runs "tokengate --dry-run proxy start"
    Then the system displays the proxy configuration that would be used
    And no connection to the registry is attempted
    And no proxy server is started

  Scenario: Dry-run shows token resolution preview
    Given the tokengate CLI is installed
    And a valid configuration file exists at ~/.tokengate/config.yaml
    When the user runs "tokengate --dry-run token resolve api://example"
    Then the system displays the tokens that would be requested
    And no actual token resolution occurs
```

## Developer Context

### Technical Requirements

1. **Flag Implementation**
   - Add `--dry-run` flag to root command and all subcommands that modify state
   - Flag type: boolean, default: false
   - Flag shorthand: `-n` (POSIX convention)
   - Use `cobra.BypassFlagParsing()` where appropriate for compatibility

2. **Dry-Run Execution Mode**
   - Create `pkg/utils/dryrun/dryrun.go` with `DryRunner` interface
   - Implement `ShouldSkip()` method returning bool
   - Implement `Preview()` method returning description of skipped action
   - All state-modifying functions accept `*DryRunner` as first parameter

3. **Output Format**
   - Use `log/slog` with structured JSON output to stderr
   - Log level: INFO for dry-run actions
   - Message format: `{"level":"INFO","msg":"[DRY-RUN] Would execute action","action":"proxy_start","config":{...}}`

4. **Commands Supporting Dry-Run**
   - `tokengate proxy start`
   - `tokengate proxy stop`
   - `tokengate registry register`
   - `tokengate registry unregister`
   - `tokengate token resolve`
   - `tokengate config validate`

5. **Commands Excluded from Dry-Run**
   - `tokengate version`
   - `tokengate help`
   - `tokengate completion`

### Architecture Compliance

- All Go code uses camelCase for functions and variables
- CLI flags use kebab-case (e.g., `--dry-run`, not `--dryRun`)
- Error wrapping: `fmt.Errorf("dry-run: %w", err)`
- Structured logging via `log/slog` to stderr
- POSIX-compliant flag behavior (short flags combine, long flags with =)

### File Structure

```
cmd/
  tokengate/
    main.go                    # Entry point, register dry-run flag
    proxy.go                   # Add dry-run support to proxy commands
    registry.go                # Add dry-run support to registry commands
    token.go                   # Add dry-run support to token commands
    config.go                  # Add dry-run support to config commands

pkg/
  utils/
    dryrun/
      dryrun.go                # DryRunner interface and implementation
      dryrun_test.go           # Unit tests
```

### Testing Requirements

1. **Unit Tests**
   - `pkg/utils/dryrun/dryrun_test.go`: Test DryRunner.ShouldSkip() and Preview()
   - Test flag parsing in each command

2. **Integration Tests**
   - Test `tokengate --dry-run proxy start` exits 0 with no side effects
   - Test `tokengate --dry-run` with invalid config exits 1
   - Test dry-run output contains expected JSON structure

3. **Test Patterns**
   ```go
   func TestDryRunFlag(t *testing.T) {
       cmd := rootCmd()
       cmd.SetArgs([]string{"--dry-run", "proxy", "start"})
       
       // Capture stderr
       var stderr bytes.Buffer
       cmd.SetErr(&stderr)
       
       err := cmd.Execute()
       assert.NoError(t, err)
       assert.Contains(t, stderr.String(), "[DRY-RUN]")
   }
   ```

### Implementation Notes

1. Use `cobra.OnInitialize()` for dry-run flag binding
2. Store dry-run state in package-level variable accessed via `viper.GetBool("dry-run")`
3. Wrap all stateful operations with dry-run check at command Execute() level
4. Ensure dry-run mode does not initialize network connections
