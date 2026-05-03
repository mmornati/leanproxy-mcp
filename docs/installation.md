# Installation

LeanProxy-MCP can be installed on macOS, Linux, and Windows.

## Prerequisites

- **macOS, Linux, or Windows**
- **IDE with MCP support** (Claude Desktop, Cursor, OpenCode, Windsurf)
- Optionally: **Go 1.21+** (for building from source)

## Download Binary (v0.2.1)

Download the pre-built binary for your platform from the GitHub Releases page:

### macOS

```bash
# Apple Silicon (M1/M2/M3/M4)
curl -fsSL https://github.com/mmornati/leanproxy-mcp/releases/download/v0.2.1/leanproxy-mcp_0.2.1_darwin_arm64.tar.gz -o leanproxy-mcp.tar.gz
tar -xzf leanproxy-mcp.tar.gz
chmod +x leanproxy-mcp
sudo mv leanproxy-mcp /usr/local/bin/
rm leanproxy-mcp.tar.gz

# Intel (x86_64)
curl -fsSL https://github.com/mmornati/leanproxy-mcp/releases/download/v0.2.1/leanproxy-mcp_0.2.1_darwin_amd64.tar.gz -o leanproxy-mcp.tar.gz
tar -xzf leanproxy-mcp.tar.gz
chmod +x leanproxy-mcp
sudo mv leanproxy-mcp /usr/local/bin/
rm leanproxy-mcp.tar.gz
```

### Linux

```bash
# x86_64
curl -fsSL https://github.com/mmornati/leanproxy-mcp/releases/download/v0.2.1/leanproxy-mcp_0.2.1_linux_amd64.tar.gz -o leanproxy-mcp.tar.gz
tar -xzf leanproxy-mcp.tar.gz
chmod +x leanproxy-mcp
sudo mv leanproxy-mcp /usr/local/bin/
rm leanproxy-mcp.tar.gz

# ARM64
curl -fsSL https://github.com/mmornati/leanproxy-mcp/releases/download/v0.2.1/leanproxy-mcp_0.2.1_linux_arm64.tar.gz -o leanproxy-mcp.tar.gz
tar -xzf leanproxy-mcp.tar.gz
chmod +x leanproxy-mcp
sudo mv leanproxy-mcp /usr/local/bin/
rm leanproxy-mcp.tar.gz
```

Or download directly from: https://github.com/mmornati/leanproxy-mcp/releases/tag/v0.2.1

## Install via Homebrew (macOS/Linux)

```bash
# Add custom tap
brew tap mmornati/leanproxy-mcp

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
 leanproxy-mcp version v0.2.1
 build date: 2026-05-03
 platform: darwin/arm64
 go: go1.26.2
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

### Step 2: Start LeanProxy

Start the LeanProxy server in a terminal:

```bash
leanproxy-mcp serve
```

### Step 3: Configure Your IDE

Connect your IDE to LeanProxy instead of individual MCP servers.

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