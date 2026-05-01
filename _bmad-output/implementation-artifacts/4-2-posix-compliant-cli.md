# Story 4-2: Implement POSIX-Compliant CLI

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 4-2 |
| **Key** | posix-compliant-cli |
| **Epic** | Epic 4 - CLI Installation and Interaction |
| **Title** | Implement POSIX-Compliant CLI |
| **Priority** | High |
| **Status** | ready-for-dev |

## Story Requirements

### User Story

As a CLI user, I want a POSIX-compliant command-line interface so that I can use familiar patterns like short flags, flag grouping, and standard exit codes.

### Acceptance Criteria (BDD Format)

```gherkin
Feature: POSIX-Compliant CLI Interface
  Scenario: Short flags can be combined
    Given the tokengate CLI is installed
    When the user runs "tokengate -hvr proxy start"
    Then the system displays help output
    And version information
    And verbose logging is enabled
    And no error occurs

  Scenario: Long flags accept equals syntax
    Given the tokengate CLI is installed
    When the user runs "tokengate --config=/etc/tokengate.yaml validate"
    Then the system uses /etc/tokengate.yaml as config file
    And validation runs successfully

  Scenario: Flags can appear before and after positional arguments
    Given the tokengate CLI is installed
    When the user runs "tokengate --verbose proxy start --port=8080"
    Then verbose mode is enabled
    And the proxy starts on port 8080

  Scenario: Standard exit codes are used
    Given the tokengate CLI is installed
    When the user runs various commands
    Then exit code 0 indicates success
    And exit code 1 indicates general error
    And exit code 2 indicates misuse
    And exit code 3 indicates configuration error
    And exit code 4 indicates token resolution failure

  Scenario: Help output follows POSIX conventions
    Given the tokengate CLI is installed
    When the user runs "tokengate --help"
    Then the output uses standard POSIX format
    And OPTIONS section lists all flags
    And USAGE line shows command syntax
    And EXIT STATUS section documents codes

  Scenario: Commands accept stdin input where appropriate
    Given the tokengate CLI is installed
    When the user runs "cat config.yaml | tokengate config validate -"
    Then the system reads config from stdin
    And validates successfully
```

## Developer Context

### Technical Requirements

1. **Flag Behavior**
   - Short flags: single character, preceded by single dash (e.g., `-h`, `-v`)
   - Long flags: full word, preceded by double dash (e.g., `--help`, `--config`)
   - Flag grouping: short flags combine ( `-hvr` = `-h -v -r`)
   - Equals syntax: `--config=value` and `--config value` equivalent
   - POSIX mandates that flags can appear anywhere in command line

2. **Exit Codes**
   - `0`: Success
   - `1`: General errors (runtime failures)
   - `2`: Misuse (invalid flags, wrong argument count)
   - `3`: Configuration error (invalid config file)
   - `4`: Token resolution failure
   - `125`: Reserved for upstream/network errors
   - Use `stdlib os.Exit(code)` for all exits

3. **Help Text Format**
   ```
   Usage: tokengate [OPTIONS] COMMAND [ARGUMENTS]
   
   Options:
     -h, --help        Show help
     -v, --verbose     Enable verbose output
     -c, --config=FILE Configuration file path
   
   Commands:
     proxy    Manage proxy server
     registry Manage token registry
     token    Token operations
   
   Exit Status:
     0      Success
     1      General error
     2      Misuse
     3      Configuration error
   ```

4. **Stdin Support**
   - Accept `-` as filename meaning stdin
   - Config commands should read from stdin if no file specified
   - Use `os.Stdin` directly, not `flag.NArg()` parsing

5. **Error Output**
   - All errors go to stderr via `log/slog`
   - Error messages do not need localization (English only)
   - Error format: `tokengate: error: descriptive message` for user errors

### Architecture Compliance

- All Go code uses camelCase for functions and variables
- CLI flags use kebab-case (e.g., `--config-file`, `--dry-run`)
- Error wrapping: `fmt.Errorf("posix: %w", err)` or context-specific
- Structured logging via `log/slog` to stderr
- All cobra commands return `error` from `Execute()` for proper exit handling

### File Structure

```
cmd/
  tokengate/
    main.go                    # Entry point, exit code handling
    root.go                    # Root command with flag definitions
    error.go                    # Exit code constants and error types
    proxy.go                   # Proxy subcommand
    registry.go                # Registry subcommand
    token.go                   # Token subcommand
    config.go                  # Config subcommand
    help.go                    # Custom help command

pkg/
  utils/
    exit/
      exit.go                  # Exit code constants
      exit_test.go             # Exit code tests
```

### Testing Requirements

1. **Unit Tests**
   - `pkg/utils/exit/exit_test.go`: Test all exit codes
   - Test flag parsing behavior in each command

2. **Integration Tests**
   - Test flag grouping: `tokengate -hvr` equivalent to `-h -v -r`
   - Test equals syntax: `--config=value` works
   - Test flag position: flags before/after args work
   - Test stdin with `-`: `cmd -` reads stdin
   - Test all exit codes are correct for each scenario

3. **Test Patterns**
   ```bash
   # Test flag grouping
   tokengate -hvr 2>&1 | grep -q "verbose" && echo "PASS"
   
   # Test exit codes
   tokengate --invalid-flag 2>/dev/null; [ $? -eq 2 ] && echo "PASS"
   
   # Test stdin
   echo "test" | tokengate config validate - && echo "PASS"
   ```

### Implementation Notes

1. Use `cobra.EnableQuoteDetection()` for better error messages
2. Implement custom `Execute()` wrapper that handles exit codes
3. Use `pflag` instead of standard `flag` for POSIX compliance
4. Set `pflag.CommandLine.SortFlags = true` for consistent help output
5. Implement `--` delimiter support for separating args from flags
