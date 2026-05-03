# LeanProxy-MCP Documentation

Welcome to the LeanProxy-MCP user documentation. This documentation is intended for developers and technical users who want to understand and use LeanProxy-MCP.

## What is LeanProxy-MCP?

**LeanProxy-MCP** is a lightweight, local CLI proxy designed to sit between your IDE and MCP (Model Context Protocol) servers. It acts as a "Token Firewall" — reducing token consumption and redacting sensitive data before it reaches LLM providers.

## Target Audience

This documentation is designed for:
- **Developers** who use IDEs with MCP support (Claude Desktop, Cursor, OpenCode, Windsurf)
- **Technical users** who want to optimize token usage and protect sensitive data
- **DevOps engineers** who need to manage MCP server configurations

## Quick Links

| Guide | Description |
|-------|-------------|
| [Installation](./installation.md) | Download and install LeanProxy-MCP |
| [Quick Start](./quickstart.md) | Get up and running in minutes |
| [Commands Reference](./commands.md) | Complete CLI command documentation |
| [Configuration](./configuration.md) | Customize LeanProxy-MCP behavior |
| [Architecture](./architecture.md) | Understanding the internal design |
| [Troubleshooting](./troubleshooting.md) | Common issues and solutions |
| [FAQ](./faq.md) | Frequently asked questions |

## The Economics of MCP: Why LeanProxy Saves Money

The AI provider market has shifted from monthly forfaits to **pay-per-use** pricing (May 2026). Every token sent to an LLM now costs real money. This makes token efficiency critical.

### The MCP Schema Tax

When you run multiple MCP servers, each adds tool schemas to every LLM request. A single GitHub MCP server contributes roughly **3,000+ tokens** of tool definitions to every prompt—even if you never use GitHub that turn.

This "schema tax" compounds quickly:
- 1 MCP server: ~3,000 tokens/request
- 5 MCP servers (GitHub, Slack, Kubernetes, Linear, Postgres): **~15,000+ tokens/request**

For a 20-prompt coding session where GitHub is used only twice, Native MCP wastes **61,000+ tokens** (99.7% of the cost) on schema overhead.

### Real Example: GitHub MCP in a Coding Session

Based on [data-driven analysis](https://blog.mornati.net/the-future-of-agentic-tooling-mcp-servers-vs-cli-a-data-driven-comparison):

| Modality | Tokens per 20-prompt session (2 GitHub ops) |
|----------|---------------------------------------------|
| Native GitHub MCP | **61,654** tokens |
| LeanProxy Gateway | **~892** tokens |
| CLI (raw) | **448** tokens |

**LeanProxy saves ~60,762 tokens per session (98.5% reduction)**

### Monthly Dollar Savings (100 sessions/month)

| Provider | Model | Native MCP | LeanProxy | **Monthly Savings** |
|----------|-------|------------|-----------|----------------------|
| **OpenAI** | GPT-4o | $0.77/session | $0.011/session | **$75.90/month** |
| **OpenAI** | GPT-5.4 | $0.92/session | $0.013/session | **$90.70/month** |
| **Anthropic** | Sonnet 4.6 | $0.33/session | $0.005/session | **$32.50/month** |
| **Anthropic** | Opus 4.7 | $0.55/session | $0.008/session | **$54.20/month** |
| **Anthropic** | Haiku 4.5 | $0.13/session | $0.002/session | **$12.80/month** |

*Calculated at 80% input / 20% output token mix with May 2026 pricing.*

### How LeanProxy Achieves This

LeanProxy uses a **gateway pattern** with JIT (Just-In-Time) schema loading:

1. **Single router schema**: Only 2 tools (`invoke_tool`, `search_tools`) = **~110 tokens** vs 3,000+ for Native MCP
2. **On-demand tool registration**: Backend server schemas only load when actually needed
3. **Session-aware caching**: Tool schemas persist across the session without per-request overhead

### Decision Framework

| Service Usage (G/N ratio) | Recommendation |
|--------------------------|----------------|
| > 40% (every prompt) | Native MCP justified |
| 5-40% (regular use) | **LeanProxy Gateway** |
| < 5% (rare use) | CLI or on-demand skill |

For most developers, GitHub has G/N ≈ 5-10% (fetch issue + create PR), making LeanProxy the cost-efficient choice.

## Key Features

| Feature | Description |
|---------|-------------|
| **Token Firewall** | Pre-configured redaction engine that intercepts secrets, API keys, and PII |
| **Shadow Manifesting** | Merges global and project-local MCP configurations |
| **JIT Discovery** | On-demand tool registration to minimize context overhead |
| **Dry-Run Mode** | Simulate proxy behavior without live execution |
| **POSIX CLI** | Simple commands for server management |

## Getting Started

New to LeanProxy-MCP? Start here:

1. [Installation Guide](./installation.md) - Download and install
2. [Quick Start](./quickstart.md) - Basic usage
3. [Commands Reference](./commands.md) - Full command documentation

## Need Help?

- Check the [FAQ](./faq.md)
- Review the [Troubleshooting Guide](./troubleshooting.md)
- See [Commands Reference](./commands.md) for detailed command documentation