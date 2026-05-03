# Story 4-4: Implement Universal Installer

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 4-4 |
| **Key** | universal-installer |
| **Epic** | Epic 4 - CLI Installation and Interaction |
| **Title** | Implement Universal Installer |
| **Priority** | High |
| **Status** | ready-for-dev |

## Story Requirements

### User Story

As a new user, I want to install leanproxy with a single command so that I can get started quickly on any supported platform.

### Acceptance Criteria (BDD Format)

```gherkin
Feature: Universal Installer
  Scenario: curl installer works on macOS
    Given the user has curl installed
    And the user is on macOS
    When the user runs the install script
    Then leanproxy is installed to /usr/local/bin/leanproxy
    And the binary has correct permissions (755)
    And shell completion is installed
    And the user can run "leanproxy version"

  Scenario: curl installer works on Linux
    Given the user has curl installed
    And the user is on Linux
    When the user runs the install script
    Then leanproxy is installed to /usr/local/bin/leanproxy
    And the binary has correct permissions (755)
    And the user can run "leanproxy version"

  Scenario: Homebrew installation works on macOS
    Given Homebrew is installed
    When the user runs "brew install leanproxy-mcp/tap/leanproxy"
    Then leanproxy is installed via Homebrew
    And the user can run "leanproxy version"

  Scenario: curl installer verifies checksums
    Given the user runs the install script
    When the download completes
    Then the SHA256 checksum is verified
    And installation fails if checksum mismatches

  Scenario: curl installer supports version selection
    Given the user wants a specific version
    When the user runs with VERSION environment variable
    Then the specified version is installed
    And the user can run "leanproxy version" to confirm

  Scenario: Installer creates config directory
    Given the installer runs successfully
    Then ~/.leanproxy directory is created
    And default config file is placed at ~/.leanproxy/config.yaml

  Scenario: Installer updates existing installation
    Given leanproxy is already installed
    When the user runs the install script
    Then the existing binary is replaced
    And configuration is preserved
```

## Developer Context

### Technical Requirements

1. **curl Installer Script**
   - URL: `https://get.leanproxy.io/install.sh`
   - Alternative: `curl -sSL https://get.leanproxy.io/install.sh | sh`
   - Support `VERSION=x.y.z` for specific version
   - Support `INSTALL_DIR=/path` for custom installation directory
   - Detect OS/ARCH and download appropriate binary
   - Verify SHA256 checksum before installation
   - Create parent directories as needed
   - Set correct file permissions (755)
   - Backup existing installation to `.bak`

2. **Binary Distribution**
   - GitHub Releases at `github.com/leanproxy/leanproxy-mcp/releases`
   - Naming: `leanproxy-{VERSION}-{OS}-{ARCH}.tar.gz`
   - Include binary and LICENSE/README
   - Include shell completion files in archive
   - Provide latest version detection

3. **Homebrew Tap**
   - Repository: `leanproxy-mcp/homebrew-tap`
   - Formula in `Formula/leanproxy.rb`
   - Head, bottle, and source options
   - Post-install hook for shell completion
   - Auto-update support via Homebrew

4. **Shell Completion Installation**
   - Install bash completion to `/etc/bash_completion.d/leanproxy`
   - Install zsh completions to `$(brew --prefix)/share/zsh/site-functions/_leanproxy`
   - Detect current shell automatically
   - Provide completion generation command: `leanproxy completion [bash|zsh]`

5. **Configuration Creation**
   - Create `~/.leanproxy` directory with `0700` permissions
   - Create `~/.leanproxy/config.yaml` with sensible defaults
   - Config should be human-readable with comments

### Architecture Compliance

- All Go code uses camelCase for functions and variables
- CLI flags use kebab-case (e.g., `--install-dir`, `--version`)
- Shell scripts use POSIX-compliant syntax
- Error wrapping: `fmt.Errorf("installer: %w", err)`
- Structured logging via `log/slog` to stderr

### File Structure

```
install/
  install.sh                   # curl installer script
  install.sh.sha256             # Checksum for install script
  build-release.sh              # Release build script

homebrew/
  Formula/
    leanproxy.rb                # Homebrew formula

cmd/
  leanproxy/
    main.go                    # Entry point
    completion.go              # Completion command
```

### Testing Requirements

1. **Shell Script Tests**
   - Test on clean macOS VM
   - Test on clean Linux VM
   - Test upgrade path
   - Test checksum verification

2. **Homebrew Tests**
   - `brew test` for formula
   - Test bottle installation
   - Test head installation

3. **Test Patterns**
   ```bash
   # Test install script
   curl -sSL https://get.leanproxy.io/install.sh | sh -s -- --dry-run
   
   # Test version selection
   VERSION=1.0.0 curl -sSL https://get.leanproxy.io/install.sh | sh
   
   # Verify installation
   leanproxy version
   sha256sum /usr/local/bin/leanproxy
   ```

### Implementation Notes

1. Use `set -euo pipefail` in shell scripts for error handling
2. Support both GNU and BSD sed/awk for cross-platform scripts
3. Use `mktemp` for safe temporary file handling
4. Check for root/admin privileges and warn if installing to system directories
5. Provide uninstall script at `https://get.leanproxy.io/uninstall.sh`
6. Log all installation steps to `/tmp/leanproxy-install.log` for debugging
7. Make install script re-runnable (idempotent)
