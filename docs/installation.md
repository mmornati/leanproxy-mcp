# Installation

LeanProxy-MCP can be installed on macOS, Linux, and Windows.

## Prerequisites

- **macOS, Linux, or Windows**
- **IDE with MCP support** (Claude Desktop, Cursor, OpenCode, Windsurf)
- Optionally: **Go 1.21+** (for building from source)

## Download Binary

Download the pre-built binary for your platform from the GitHub Releases page:

### Automatic (All Platforms)

This single command works on macOS and Linux, automatically detecting your architecture:

```bash
# Download latest version (auto-detects OS/arch)
VERSION=${VERSION:-$(curl -sL https://api.github.com/repos/mmornati/leanproxy-mcp/releases/latest | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')}
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
[ "$ARCH" = "x86_64" ] && ARCH="amd64"
[ "$ARCH" = "arm64" ] && ARCH="arm64"
curl -fsSL "https://github.com/mmornati/leanproxy-mcp/releases/download/${VERSION}/leanproxy-mcp_${VERSION#v}_${OS}_${ARCH}.tar.gz" -o leanproxy-mcp.tar.gz
tar -xzf leanproxy-mcp.tar.gz
chmod +x leanproxy-mcp
sudo mv leanproxy-mcp /usr/local/bin/
rm leanproxy-mcp.tar.gz

# Override version: VERSION=v0.2.0 ... (run the full command above with VERSION set)
```

### Manual Download

If you prefer, download manually from: https://github.com/mmornati/leanproxy-mcp/releases

## Install via Homebrew (macOS/Linux)

```bash
# Add custom tap (point to this repository)
brew tap mmornati/leanproxy-mcp https://github.com/mmornati/leanproxy-mcp

# Install
brew install leanproxy-mcp
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
 leanproxy-mcp version 0.5.2
 build date: 2026-05-04
 platform: darwin/arm64
 go: go1.25.5
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

Generate shell completions for your shell:

```bash
# Bash
leanproxy-mcp completion bash > /etc/bash_completion.d/leanproxy-mcp

# Zsh
leanproxy-mcp completion zsh > ~/.zsh/completions/_leanproxy-mcp

# Fish
leanproxy-mcp completion fish > ~/.config/fish/completions/leanproxy-mcp.fish
```

## Next Steps

- [Quick Start Guide](./quickstart.md)
- [Configuration](./configuration.md)
- [Commands Reference](./commands.md)