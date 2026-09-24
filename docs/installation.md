# Installation

LeanProxy-MCP ships pre-built binaries for macOS and Linux on amd64 and arm64.
On other platforms, [build from source](#build-from-source).

## Prerequisites

- **macOS or Linux** (amd64 or arm64)
- **IDE with MCP support** (Claude Desktop, Cursor, OpenCode, Windsurf)
- Optionally: **Go 1.25+** (for building from source)

## Install Script (macOS/Linux)

The install script downloads the release archive for your OS and architecture,
verifies it against the release's `checksums.txt`, and installs the
`leanproxy-mcp` binary. It installs nothing else: no config files and no shell
completions (see [Shell Completions](#shell-completions)).

```bash
curl -fsSL https://raw.githubusercontent.com/mmornati/leanproxy-mcp/main/install/install.sh | sh
```

It honours two environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `VERSION` | `latest` | Release to install, e.g. `v0.11` or `0.11` |
| `INSTALL_DIR` | `/usr/local/bin` | Destination directory; `sudo` is used if it is not writable |

For example, to install a specific version into your home directory:

```bash
curl -fsSL https://raw.githubusercontent.com/mmornati/leanproxy-mcp/main/install/install.sh | VERSION=v0.11 INSTALL_DIR="$HOME/.local/bin" sh
```

## Install via Homebrew (macOS/Linux)

```bash
# Add custom tap (point to this repository)
brew tap mmornati/leanproxy-mcp https://github.com/mmornati/leanproxy-mcp

# Install
brew install leanproxy-mcp
```

## Manual Download

Download `leanproxy-mcp_<version>_<os>_<arch>.tar.gz` and `checksums.txt` from
the [Releases page](https://github.com/mmornati/leanproxy-mcp/releases), where
`<os>` is `darwin` or `linux` and `<arch>` is `amd64` or `arm64`. The version
in the file name has no `v` prefix. For example, for v0.11 on Apple Silicon:

```bash
curl -fsSLO https://github.com/mmornati/leanproxy-mcp/releases/download/v0.11/leanproxy-mcp_0.11_darwin_arm64.tar.gz
curl -fsSLO https://github.com/mmornati/leanproxy-mcp/releases/download/v0.11/checksums.txt

# Verify (on Linux use: sha256sum --check --ignore-missing checksums.txt)
shasum -a 256 --check --ignore-missing checksums.txt

tar -xzf leanproxy-mcp_0.11_darwin_arm64.tar.gz leanproxy-mcp
sudo install -m 0755 leanproxy-mcp /usr/local/bin/leanproxy-mcp
```

## Build from Source

```bash
# Clone repository
git clone https://github.com/mmornati/leanproxy-mcp.git
cd leanproxy-mcp

# Build
go build -o leanproxy-mcp .

# Install
sudo mv leanproxy-mcp /usr/local/bin/
```

Or use the Makefile:

```bash
make build
sudo make install
```

## Verify Installation

```bash
leanproxy-mcp version
```

Expected output:
```
leanproxy-mcp version 0.11
build date: 2026-09-24T04:56:34Z
platform: darwin/arm64
go: go1.25.14
```

## IDE Configuration

After installation, configure your IDE to use LeanProxy-MCP as an MCP server proxy. LeanProxy proxies existing MCP server configurations from your IDE.

### Step 1: Migrate Existing MCP Servers

First, import your existing MCP server configurations from your IDE:

```bash
# Scan all IDEs at once (finds OpenCode, Claude Desktop, Cursor, VS Code)
leanproxy-mcp migrate
```

This scans all supported IDEs and imports any found MCP server configurations into `~/.config/leanproxy_servers.yaml`.

Example output:
```
Found 4 MCP server(s) from 1 source(s):

  OpenCode: 4 server(s)

  [1] nexus-dev (opencode) - /usr/bin/env
  [2] nexus-dev-test (opencode) - /usr/bin/env
  [3] garmin (opencode) - uvx
  [4] Intervals.icu (opencode) - /usr/bin/env

Import to ~/.config/leanproxy_servers.yaml? [y/N]:
```

Confirm to import the servers.

### Step 2: Configure LeanProxy in Your IDE

Configure LeanProxy as an MCP server in your IDE. LeanProxy runs as a daemon and proxies all your existing MCP servers through a single connection.

#### OpenCode

Add to your `~/.config/opencode/opencode.json`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "leanproxy": {
      "type": "local",
      "command": ["leanproxy-mcp", "server", "run", "--stdio"],
      "enabled": true
    }
  }
}
```

#### Cursor

Add to your `~/.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "leanproxy": {
      "command": "leanproxy-mcp",
      "args": ["serve"]
    }
  }
}
```

#### VS Code

Add to your `~/.vscode/mcp.json` (create if it doesn't exist):

```json
{
  "mcpServers": {
    "leanproxy": {
      "command": "leanproxy-mcp",
      "args": ["serve"]
    }
  }
}
```

> **Note:** When configured as an MCP server, LeanProxy automatically starts when your IDE connects. No need to run `leanproxy-mcp serve` manually.

## Shell Completions

The Homebrew formula installs bash, zsh, and fish completions automatically
(starting with the first release after v0.11). Otherwise, generate them with
`leanproxy-mcp completion`:

```bash
# Bash (requires the bash-completion package)
leanproxy-mcp completion bash | sudo tee /etc/bash_completion.d/leanproxy-mcp > /dev/null

# Zsh (~/.zsh/completions must be on your $fpath)
leanproxy-mcp completion zsh > ~/.zsh/completions/_leanproxy-mcp

# Fish
leanproxy-mcp completion fish > ~/.config/fish/completions/leanproxy-mcp.fish
```

Add `--no-desc` to omit command descriptions from the completion candidates.

## Next Steps

- [Quick Start Guide](./quickstart.md)
- [Configuration](./configuration.md)
- [Commands Reference](./commands.md)