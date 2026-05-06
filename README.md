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

| MCP Servers | Tools | Tokens per Request |
|-------------|-------|-------------------|
| 1 (GitHub) | 41 | ~4,100 tokens |
| 2 (GitHub + Stitch) | 53 | ~5,300 tokens |
| 4 (Garmin + Intervals.icu + GitHub + Stitch) | 163 | **~16,300+ tokens** |

In a 7-prompt mixed session where all 4 MCP servers are configured but GitHub/Stitch used only at session start/end, Native MCP wastes **~16,300 tokens** on schema descriptions for servers never invoked.

> **Real data**: These tool counts come from live MCP servers queried via LeanProxy. See the methodology in our [data-driven analysis](https://blog.mornati.net/the-future-of-agentic-tooling-mcp-servers-vs-cli-a-data-driven-comparison).

## The Solution: LeanProxy Gateway

LeanProxy uses a gateway pattern with Just-In-Time schema loading:

- **Single router schema**: Only 2 tools (`invoke_tool`, `list_tools`) = **~110 tokens** vs 16,300+ for Native MCP
- **On-demand tool registration**: Backend server schemas load only when actually invoked
- **Session-aware caching**: Tool schemas persist across the session without per-request overhead

### Real Token Savings (7-prompt session, 4 MCP servers)

| Modality | Tokens | vs LeanProxy |
|----------|--------|--------------|
| Native MCP (4 servers) | ~16,300 | — |
| **LeanProxy Gateway** | **~2,000** | baseline |
| CLI (raw) | 448 | -77% |

### Real-World Working Sessions

These numbers come from actual tool invocations across your MCP servers:

#### Session A: Morning Sport Check (Garmin + Intervals.icu, 4 prompts)

| Prompt | Tool Invoked | Native MCP | LeanProxy |
|--------|-------------|------------|----------|
| 1 | `garmin_get_stats` | 10,000 | ~500 |
| 2 | `intervals_get_events` | 11,000 | ~500 |
| 3 | `intervals_get_activity_intervals` | cached | ~500 |
| 4 | `intervals_add_or_update_event` | cached | ~500 |
| **Total** | | **~21,000** | **~2,000** |

#### Session B: Dev Session (GitHub + Stitch, 5 prompts)

| Prompt | Tool Invoked | Native MCP | LeanProxy |
|--------|-------------|------------|----------|
| 1 | `github_search_repositories` | 4,100 | ~600 |
| 2 | `github_get_file_contents` | cached | cached |
| 3 | `stitch_list_projects` | 5,300 | ~600 |
| 4 | `stitch_generate_screen_from_text` | cached | ~600 |
| 5 | `github_create_pull_request` | cached | ~600 |
| **Total** | | **~10,600** | **~2,400** |

#### Session C: Full Day Workflow (All 4 MCP servers, 7 prompts)

| Prompt | Tool Invoked | Native MCP | LeanProxy |
|--------|-------------|------------|----------|
| 1 | `garmin_get_training_readiness` | 10,000 | ~500 |
| 2 | `intervals_get_events` | 11,000 | ~500 |
| 3 | `stitch_list_projects` | 12,300 | ~500 |
| 4 | `github_get_file_contents` | 16,300 | ~500 |
| 5 | `stitch_generate_screen_from_text` | cached | ~500 |
| 6 | `garmin_log_food` | cached | ~500 |
| 7 | `github_push_files` | cached | ~500 |
| **Total** | | **~49,600** | **~3,500** |

> **Key insight**: You don't need every server on every prompt. With LeanProxy, each tool loads JIT (~500 tokens) only when actually called, slashing the ~16,300 token tax to ~500 per invocation.

### The Cache Read Cost Fallacy

**Providers advertise prompt caching as "free" or "90% savings" — but cache reads aren't free.**

When a prompt cache hit occurs, you still pay for reading from cache:
- **OpenAI**: Cache reads at **0.25x** input token price
- **Anthropic**: Cache reads at **0.25x** input token price  
- **DeepSeek**: Cache reads at **0.25x** input token price
- **Google Gemini**: Cache reads at ~**0.25x** input token price

This means **100% cache hit doesn't mean 100% free**. A 16,300-token MCP schema at 100% cache hit still costs:
```
16,300 tokens × 0.25x = 4,075 "effective" tokens worth of money
```

#### Real Comparison: Native MCP vs LeanProxy (Live MCP Data)

| MCP Servers | Tools | Native MCP (100% cache hit) | LeanProxy | Savings |
|-------------|-------|----------------------------|----------|---------|
| 1 (GitHub) | 41 | 1,025 tokens | 27.5 | **97.3%** |
| 2 (GitHub + Stitch) | 53 | 1,325 tokens | 27.5 | **97.9%** |
| 3 (+ Intervals.icu) | 63 | 1,575 tokens | 27.5 | **98.2%** |
| 4 (all) | 163 | 4,075 tokens | 27.5 | **99.3%** |

*Native MCP sends tool schemas every prompt at 0.25x cache read. LeanProxy sends only ~110 router tokens regardless of backend servers.*

**The key insight**: With Native MCP + caching, you pay for every tool schema on every request (at 0.25x). LeanProxy sends only the router schema — the backend tool schemas only load when actually invoked.

### Provider Caching on "Same Input Context"

For MCP tool schemas that are **identical every request**, caching only reduces cost by 75% — you're still paying for the read. The "same input context" scenario:

| Scenario | Input Tokens | Cache Rate | Cache Cost (0.25x) | LeanProxy | Savings |
|----------|-------------|-----------|-------------------|----------|---------|
| 1 server (GitHub) | 4,100 | 100% hit | 1,025 | **27.5** | 97% |
| 2 servers | 5,300 | 100% hit | 1,325 | 27.5 | 98% |
| 3 servers | 15,200 | 100% hit | 3,800 | 27.5 | 99% |
| **4 servers (all)** | **16,300** | 100% hit | **4,075** | **27.5** | **99.3%** |

> **Critical insight**: With "same input context" caching, 100% cache hit STILL costs at 0.25x. LeanProxy sends only ~110 tokens, making cache read cost negligible (27.5 tokens). This is the real advantage.

### Monthly Total Token Savings (100 sessions/month)

Native MCP sends tool schemas every request (at 0.25x cache read). LeanProxy only sends router schema.

| Servers | Tools | GPT-4o-mini ($0.0375/M) | Anthropic Sonnet ($0.40/M) |
|---------|-------|--------------------------|----------------------------|
| 1 | 41 | $1.03 → **$1.02 saved** | $10.93 → **$10.90 saved** |
| 2 | 53 | $1.33 → **$1.32 saved** | $14.13 → **$14.10 saved** |
| 4 | 163 | $4.08 → **$4.07 saved** | $43.47 → **$43.44 saved** |

*Formula: 16,300 tokens × 100 sessions × 0.25x cache read / 1M (GPT-4o-mini) or / 1M (Sonnet)*

### Should You Use Caching with MCP?

| Scenario | Cache Hit | Recommendation |
|----------|----------|----------------|
| MCP tool schemas (100% same) | 100% | ❌ Still costs 0.25x — use LeanProxy |
| Conversation history (growing) | 90%+ | ✅ Caching saves money |
| Codebase/RAG context | 80%+ | ✅ Caching saves money |
| MCP schemas in short session | 100% | ❌ Cache read cost > savings |

**Key insight**: For MCP tool schemas that are **identical every request**, caching only reduces cost by 75% — you're still paying for the read. LeanProxy eliminates the overhead entirely. See "Provider Caching on Same Input Context" above for the math.

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
brew tap mmornati/leanproxy-mcp https://github.com/mmornati/leanproxy-mcp
brew install leanproxy-mcp

# Download binary (auto-detects OS/arch)
VERSION=${VERSION:-$(curl -sL https://api.github.com/repos/mmornati/leanproxy-mcp/releases/latest | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')}
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
[ "$ARCH" = "x86_64" ] && ARCH="amd64"
[ "$ARCH" = "arm64" ] && ARCH="arm64"
curl -fsSL "https://github.com/mmornati/leanproxy-mcp/releases/download/${VERSION}/leanproxy-mcp_${VERSION#v}_${OS}_${ARCH}.tar.gz" -o leanproxy-mcp.tar.gz
tar -xzf leanproxy-mcp.tar.gz
chmod +x leanproxy-mcp && sudo mv leanproxy-mcp /usr/local/bin/
rm leanproxy-mcp.tar.gz

# Override version: VERSION=v0.5.2 ...

# Build from source
git clone https://github.com/mmornati/leanproxy-mcp.git
cd leanproxy-mcp && make build
```

### Basic Usage

```bash
# Start the proxy with a local MCP server
leanproxy-mcp server run --stdio

# Run in dry-run mode to see potential savings
leanproxy-mcp server run --dry-run --stdio

# Generate a token savings report
leanproxy-mcp report --output report.md
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
leanproxy-mcp server list
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
