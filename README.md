# LeanProxy-MCP

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://golang.org/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Test](https://github.com/mmornati/leanproxy-mcp/actions/workflows/test.yml/badge.svg)](https://github.com/mmornati/leanproxy-mcp/actions/workflows/test.yml)
[![Lint](https://github.com/mmornati/leanproxy-mcp/actions/workflows/lint.yml/badge.svg)](https://github.com/mmornati/leanproxy-mcp/actions/workflows/lint.yml)
[![codecov](https://codecov.io/gh/mmornati/leanproxy-mcp/branch/main/graph/badge.svg)](https://codecov.io/gh/mmornati/leanproxy-mcp)
[![Release](https://img.shields.io/github/v/release/mmornati/leanproxy-mcp?include_prereleases)](https://github.com/mmornati/leanproxy-mcp/releases)

**LeanProxy-MCP** is a lightweight, local CLI proxy that sits between your IDE and MCP servers — acting as a *Token Firewall* that cuts token waste and protects sensitive data before it reaches LLM providers.

In the pay-per-use AI era (May 2026+), every token costs money. LeanProxy slashes your token bill by replacing the "schema tax" of Native MCP with a gateway pattern that loads tool definitions only when needed.

## The Problem: MCP Schema Tax

When you connect multiple MCP servers, each injects tool schemas into **every LLM request** — even when you never use that server. This compounds quickly:

| MCP Servers | Tokens per Request |
|-------------|-------------------|
| 1 (e.g., GitHub) | ~3,000 tokens |
| 5 (GitHub, Slack, K8s, Linear, Postgres) | **~15,000+ tokens** |

In a 20-prompt coding session where GitHub is called only twice, Native MCP wastes **61,654 tokens** (99.7% overhead) on schema descriptions the agent never needed.

## The Solution: LeanProxy Gateway

LeanProxy uses a gateway pattern with Just-In-Time schema loading:

- **Single router schema**: Only 3 tools (`list_servers`, `invoke_tool`, `search_tools`) = **~160 tokens** vs 3,000+ for Native MCP
- **On-demand tool registration**: Backend server schemas load only when actually invoked
- **Session-aware caching**: Tool schemas persist across the session without per-request overhead

### Real Token Savings (20-prompt session, 2 GitHub ops)

| Modality | Tokens | vs LeanProxy |
|----------|--------|--------------|
| Native GitHub MCP | 61,654 | — |
| **LeanProxy Gateway** | **~892** | baseline |
| CLI (raw) | 448 | -50% |

### Monthly Dollar Savings (100 sessions/month)

| Provider | Model | Native MCP | LeanProxy | Monthly Savings |
|----------|-------|------------|-----------|-----------------|
| OpenAI | GPT-4o | $0.77/session | $0.011/session | **$75.90** |
| OpenAI | GPT-5.4 | $0.92/session | $0.013/session | **$90.70** |
| Anthropic | Sonnet 4.6 | $0.33/session | $0.005/session | **$32.50** |
| Anthropic | Opus 4.7 | $0.55/session | $0.008/session | **$54.20** |

*Calculated at 80% input / 20% output token mix with May 2026 pricing.*

## Key Features

| Feature | Description |
|---------|-------------|
| **Token Firewall** | Pre-configured redaction engine that intercepts secrets, API keys, and PII before they reach LLM providers |
| **Shadow Manifesting** | Automatically merges global (`~/.config/mcp.json`) and project-local MCP configurations |
| **JIT Discovery** | On-demand tool registration via signatures to minimize initial context overhead |
| **Dry-Run Mode** | Simulate proxy behavior and generate token savings reports without live execution |
| **POSIX CLI** | Manage MCP servers with simple commands (`server`, `compactor`, `context`) |

## Quick Start

### Installation

```bash
# macOS/Linux via Homebrew
brew tap mmornati/leanproxy-mcp
brew install leanproxy-mcp

# Download binary from releases
curl -fsSL https://github.com/mmornati/leanproxy-mcp/releases/latest/download/leanproxy-mcp -o leanproxy-mcp
chmod +x leanproxy-mcp && sudo mv leanproxy-mcp /usr/local/bin/

# Build from source
git clone https://github.com/mmornati/leanproxy-mcp.git
cd leanproxy-mcp && make build
```

### Basic Usage

```bash
# Start the proxy with a local MCP server
leanproxy-mcp server --stdio "npx @modelcontextprotocol/server-filesystem ./my-project"

# Run in dry-run mode to see potential savings
leanproxy-mcp server --dry-run --stdio "npx @modelcontextprotocol/server-filesystem ./my-project"

# Generate a token savings report
leanproxy-mcp compactor --manifest ./mcp.json
```

### IDE Configuration

LeanProxy can be configured as an MCP server in your IDE. For detailed setup instructions, see the [Installation Guide](docs/installation.md).

#### OpenCode Example

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

Other IDEs (Claude Desktop, Cursor, Windsurf): see [Installation Guide](docs/installation.md).

### Verification

```bash
leanproxy server list
```

## Build from Source

### Prerequisites

- Go 1.25 or later
- Git

### Commands

```bash
make build        # Build all platform binaries to dist/
make build-local  # Build for current platform only
make test         # Run all tests
make lint         # Run linter
make install      # Build and install to $GOPATH/bin
```

## Documentation

For detailed documentation, see:

| Guide | Description |
|-------|-------------|
| [User Documentation](docs/index.md) | Overview, economics, and key concepts |
| [Installation Guide](docs/installation.md) | Download, install, and IDE setup |
| [Quick Start](docs/quickstart.md) | Get up and running in minutes |
| [Commands Reference](docs/commands.md) | Complete CLI command documentation |
| [Configuration](docs/configuration.md) | Customize LeanProxy behavior |
| [Architecture](docs/architecture.md) | Understanding internal design |
| [Troubleshooting](docs/troubleshooting.md) | Common issues and solutions |
| [FAQ](docs/faq.md) | Frequently asked questions |

## License

[MIT License](LICENSE)
