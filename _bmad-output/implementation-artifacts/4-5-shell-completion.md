# Story 4-5: Implement Shell Completion

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 4-5 |
| **Key** | shell-completion |
| **Epic** | Epic 4 - CLI Installation and Interaction |
| **Title** | Implement Shell Completion |
| **Priority** | Medium |
| **Status** | ready-for-dev |

## Story Requirements

### User Story

As a CLI user, I want shell completion for leanproxy commands and flags so that I can work more efficiently without memorizing all options.

### Acceptance Criteria (BDD Format)

```gherkin
Feature: Shell Completion Support
  Scenario: bash completion generates valid completion script
    Given the leanproxy CLI is installed
    When the user runs "leanproxy completion bash"
    Then a valid bash completion script is output
    And sourcing it enables tab completion for leanproxy

  Scenario: zsh completion generates valid completion script
    Given the leanproxy CLI is installed
    When the user runs "leanproxy completion zsh"
    Then a valid zsh completion script is output
    And sourcing it enables tab completion for leanproxy

  Scenario: bash completion suggests subcommands
    Given bash completion is enabled
    And the user types "leanproxy <TAB>"
    Then "proxy", "registry", "token", "config" are suggested
    And "version", "help" are suggested

  Scenario: bash completion suggests flags
    Given bash completion is enabled
    And the user types "leanproxy proxy <TAB>"
    Then flags like "--help", "--dry-run", "--config" are suggested
    And subcommands like "start", "stop", "status" are suggested

  Scenario: zsh completion provides descriptions
    Given zsh completion is enabled
    And the user types "leanproxy proxy <TAB>"
    Then both options and descriptions are shown
    And descriptions are in English

  Scenario: fish completion generates valid script
    Given the leanproxy CLI is installed
    When the user runs "leanproxy completion fish"
    Then a valid fish completion script is output
    And installing it enables tab completion for leanproxy

  Scenario: PowerShell completion generates valid script
    Given the leanproxy CLI is installed
    When the user runs "leanproxy completion powershell"
    Then a valid PowerShell completion script is output
    And installing it enables tab completion for leanproxy
```

## Developer Context

### Technical Requirements

1. **Cobra Completion Integration**
   - Use `cobra.EnableCompletionGeneration()`
   - Implement `cobra.Command.GenBashCompletionFile()`
   - Implement `cobra.Command.GenZshCompletionFile()`
   - Implement `cobra.Command.GenFishCompletionFile()`
   - Implement `cobra.Command.GenPowerShellCompletionFile()`

2. **Command Structure**
   ```
   leanproxy completion [bash|zsh|fish|powershell]
     --no-desc           Suppress command descriptions
     --description       Custom completion description
     -h, --help          Help for completion command
   ```

3. **Completion Features**
   - Dynamic command completion based on current command tree
   - Flag completion for all supported flag types
   - Positional argument completion where applicable
   - Support for custom completers for specific arguments

4. **Custom Completions**
   - Config file path: complete `.yaml`, `.yml` files
   - Socket path: complete socket files (`.sock`)
   - Log level: complete `debug`, `info`, `warn`, `error`
   - Registry URL: complete valid URL patterns
   - Token URI: complete `api://`, `oidc://`, `oauth://` schemes

5. **Installation Instructions**
   - Output completion script to stdout
   - Document installation in help text
   - Provide one-liner installation commands
   - Support both user-level and system-level installation

### Architecture Compliance

- All Go code uses camelCase for functions and variables
- CLI flags use kebab-case (e.g., `--completion`, `--shell`)
- Error wrapping: `fmt.Errorf("completion: %w", err)`
- Structured logging via `log/slog` to stderr
- Use cobra's built-in completion generation

### File Structure

```
cmd/
  leanproxy/
    main.go                    # Entry point
    completion.go              # Completion command implementation
    completers.go              # Custom completion functions

pkg/
  registry/
    registry.go                # Registry types for completion
```

### Testing Requirements

1. **Unit Tests**
   - Test completion script generation
   - Test custom completer functions
   - Test error handling

2. **Integration Tests**
   - Source completion script and verify it loads without errors
   - Use `complete -p leanproxy` to verify registration
   - Test tab completion behavior with `compgen`

3. **Test Patterns**
   ```bash
   # Verify bash completion script
   source <(leanproxy completion bash)
   complete -p leanproxy
   
   # Test completion for subcommand
   compgen -W "$(leanproxy completion bash | grep -oP '(?<=_command_words=)\S+')" leanproxy
   
   # Verify zsh completion file
   leanproxy completion zsh > ~/.zsh/completion/_leanproxy
   autoload -Uz _leanproxy
   ```

### Implementation Notes

1. Use `cobra.GenCompletionFile()` for file-based generation
2. Use `cobra.GenCompletionCmd()` for the completion subcommand
3. Implement `RegisterFlagCompletionFunc()` for custom completers
4. Handle shell detection with `SHELL` environment variable fallback
5. Provide helpful error messages when completion fails
6. Support `--help` flag on completion command to show installation guide
7. Ensure completion scripts are POSIX-compliant where possible
