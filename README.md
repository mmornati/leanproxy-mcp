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

- **Single router schema**: Only 2 tools (`invoke_tool`, `search_tools`) = **~110 tokens** vs 3,000+ for Native MCP
- **On-demand tool registration**: Backend server schemas load only when actually invoked
- **Session-aware caching**: Tool schemas persist across the session without per-request overhead

### Real Token Savings (20-prompt session, 2 GitHub ops)

| Modality | Tokens | vs LeanProxy |
|----------|--------|--------------|
| Native GitHub MCP | 61,654 | — |
| **LeanProxy Gateway** | **~892** | baseline |
| CLI (raw) | 448 | -50% |

### The Cache Read Cost Fallacy

**Providers advertise prompt caching as "free" or "90% savings" — but cache reads aren't free.**

When a prompt cache hit occurs, you still pay for reading from cache:
- **OpenAI**: Cache reads at **0.25x** input token price
- **Anthropic**: Cache reads at **0.25x** input token price  
- **DeepSeek**: Cache reads at **0.25x** input token price
- **Google Gemini**: Cache reads at ~**0.25x** input token price

This means **100% cache hit doesn't mean 100% free**. A 30,000-token MCP schema at 100% cache hit still costs:
```
30,000 tokens × 0.25x = 7,500 "effective" tokens worth of money
```

#### Real Comparison: Native MCP vs LeanProxy

| MCP Servers | Native MCP (100% cache hit) | LeanProxy | Savings |
|-------------|----------------------------|----------|---------|
| 1 | 750 effective tokens | 27.5 | **96.3%** |
| 2 | 1,500 effective tokens | 27.5 | **98.2%** |
| 3 | 2,250 effective tokens | 27.5 | **98.8%** |
| 4 | 3,000 effective tokens | 27.5 | **99.1%** |

*Native MCP sends ~3,000 tokens/server × 0.25x cache read. LeanProxy sends ~110 tokens regardless of backend servers.*

**The key insight**: With Native MCP + caching, you pay for every tool schema on every request (at 0.25x). LeanProxy sends only the router schema — the backend tool schemas only load when actually invoked.

### Monthly Total Token Savings (100 sessions/month)

Native MCP sends tool schemas every request (at 0.25x cache read). LeanProxy only sends router schema.

| Servers | GPT-4o-mini ($0.0375/M) | Anthropic Sonnet ($0.40/M) |
|---------|--------------------------|----------------------------|
| 1 | $0.75 → **$0.74 saved** | $8.00 → **$7.89 saved** |
| 3 | $2.25 → **$2.24 saved** | $24.00 → **$23.89 saved** |
| 5 | $3.75 → **$3.74 saved** | $40.00 → **$39.89 saved** |

*Formula: 3,000 tokens/server × servers × 20 prompts × 100 sessions × 0.25x cache read*

### Should You Use Caching with MCP?

| Scenario | Cache Hit | Recommendation |
|----------|----------|----------------|
| MCP tool schemas (100% same) | 100% | ❌ Still costs 0.25x — use LeanProxy |
| Conversation history (growing) | 90%+ | ✅ Caching saves money |
| Codebase/RAG context | 80%+ | ✅ Caching saves money |
| MCP schemas in short session | 100% | ❌ Cache read cost > savings |

**Key insight**: For MCP tool schemas that are **identical every request**, caching only reduces cost by 75% — you're still paying for the read. LeanProxy eliminates the overhead entirely.

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

LeanProxy can be configured as an MCP server in your IDE. For detailed setup instructions, see the [Installation Guide](https://mmornati.github.io/leanproxy-mcp/installation/).

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

Other IDEs (Claude Desktop, Cursor, Windsurf): see [Installation Guide](https://mmornati.github.io/leanproxy-mcp/installation/).

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
| [User Documentation](https://mmornati.github.io/leanproxy-mcp/) | Overview, economics, and key concepts |
| [Installation Guide](https://mmornati.github.io/leanproxy-mcp/installation/) | Download, install, and IDE setup |
| [Quick Start](https://mmornati.github.io/leanproxy-mcp/quickstart/) | Get up and running in minutes |
| [Commands Reference](https://mmornati.github.io/leanproxy-mcp/commands/) | Complete CLI command documentation |
| [Configuration](https://mmornati.github.io/leanproxy-mcp/configuration/) | Customize LeanProxy behavior |
| [Architecture](https://mmornati.github.io/leanproxy-mcp/architecture/) | Understanding internal design |
| [Troubleshooting](https://mmornati.github.io/leanproxy-mcp/troubleshooting/) | Common issues and solutions |
| [FAQ](https://mmornati.github.io/leanproxy-mcp/faq/) | Frequently asked questions |

## License

[MIT License](LICENSE)
