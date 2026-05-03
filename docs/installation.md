# Installation

LeanProxy-MCP can be installed on macOS, Linux, and Windows.

## Prerequisites

- **macOS, Linux, or Windows**
- **IDE with MCP support** (Claude Desktop, Cursor, OpenCode, Windsurf)
- Optionally: **Go 1.21+** (for building from source)

## Download Binary (v0.2.0)

Download the pre-built binary for your platform from the GitHub Releases page:

### macOS

```bash
# Apple Silicon (M1/M2/M3/M4)
curl -fsSL https://github.com/mmornati/leanproxy-mcp/releases/download/v0.2.0/leanproxy-mcp_darwin_arm64 -o leanproxy-mcp

# Intel (x86_64)
curl -fsSL https://github.com/mmornati/leanproxy-mcp/releases/download/v0.2.0/leanproxy-mcp_darwin_amd64 -o leanproxy-mcp

# Install
chmod +x leanproxy-mcp
sudo mv leanproxy-mcp /usr/local/bin/
```

### Linux

```bash
# x86_64
curl -fsSL https://github.com/mmornati/leanproxy-mcp/releases/download/v0.2.0/leanproxy-mcp_linux_amd64 -o leanproxy-mcp

# ARM64
curl -fsSL https://github.com/mmornati/leanproxy-mcp/releases/download/v0.2.0/leanproxy-mcp_linux_arm64 -o leanproxy-mcp

# Install
chmod +x leanproxy-mcp
sudo mv leanproxy-mcp /usr/local/bin/
```

### Windows

```bash
# Using PowerShell
Invoke-WebRequest -Uri https://github.com/mmornati/leanproxy-mcp/releases/download/v0.2.0/leanproxy-mcp_windows_amd64.exe -OutFile leanproxy-mcp.exe

# Move to PATH
Move-Item leanproxy-mcp.exe $env:LOCALAPPDATA\Microsoft\Windows\Tools\
```

Or download directly from: https://github.com/mmornati/leanproxy-mcp/releases/tag/v0.2.0

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
leanproxy version
```

Expected output:
```
 leanproxy-mcp version 0.2.0
 build date: 2026-05-01
```

## IDE Configuration

After installation, configure your IDE to use LeanProxy-MCP as an MCP server.

### Claude Desktop

1. Open `~/Library/Application Support/Claude/claude_desktop_config.json`
2. Add to `mcpServers`:

```json
{
  "mcpServers": {
    "leanproxy": {
      "command": "leanproxy",
      "args": ["server", "add", "my-server", "npx", "-y", "@modelcontextprotocol/server-filesystem", "./"]
    }
  }
}
```

### Cursor / Windsurf

1. Open Settings → MCP Servers
2. Add new server with:

```
Name: leanproxy
Command: leanproxy server add my-server npx -y @modelcontextprotocol/server-filesystem ./
```

### OpenCode

1. Open Settings → MCP Servers
2. Add the LeanProxy server configuration

## Shell Completions

Generate shell completions for your shell:

```bash
# Bash
leanproxy completion bash > /etc/bash_completion.d/leanproxy

# Zsh
leanproxy completion zsh > ~/.zsh/completions/_leanproxy

# Fish
leanproxy completion fish > ~/.config/fish/completions/leanproxy.fish
```

## Next Steps

- [Quick Start Guide](./quickstart.md)
- [Configuration](./configuration.md)
- [Commands Reference](./commands.md)