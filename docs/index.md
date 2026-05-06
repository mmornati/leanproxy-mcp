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
| [Security](./security.md) | Security hardening features |
| [Graceful Shutdown](./shutdown.md) | Proper shutdown patterns and best practices |
| [Troubleshooting](./troubleshooting.md) | Common issues and solutions |
| [FAQ](./faq.md) | Frequently asked questions |

## The Economics of MCP: Why LeanProxy Saves Money

The AI provider market has shifted from monthly forfaits to **pay-per-use** pricing (May 2026). Every token sent to an LLM now costs real money. This makes token efficiency critical.

### The MCP Schema Tax

When you run multiple MCP servers, each adds tool schemas to every LLM request. We measured this live with our own MCP configuration:

| MCP Servers | Tools | Tokens per Request |
|-------------|-------|-------------------|
| Garmin | 100 | ~10,000 tokens |
| GitHub | 41 | ~4,100 tokens |
| Stitch | 12 | ~1,200 tokens |
| Intervals.icu | 10 | ~1,000 tokens |
| **All 4 combined** | **163** | **~16,300+ tokens** |

> These tool counts come from live MCP servers queried via LeanProxy. Each tool adds ~100 tokens of schema + arguments. With 163 tools configured, that's the "schema tax" on every prompt.

For a 7-prompt mixed session where all 4 MCP servers are configured but only 2-3 actually invoked, Native MCP wastes **~16,300 tokens** on schemas never used.

### Real Examples: Working Sessions

Based on live MCP tool invocations:

| Session | Description | Prompts | Native MCP | LeanProxy | Savings |
|---------|-------------|--------|------------|----------|---------|
| A | Sport (Garmin + Intervals.icu) | 4 | ~21,000 | ~2,000 | **90%+** |
| B | Dev (GitHub + Stitch) | 5 | ~10,600 | ~2,400 | **77%+** |
| C | Full Day (all 4) | 7 | ~49,600 | ~3,500 | **93%+** |

#### Session A: Morning Sport (Garmin + Intervals.icu)

| Prompt | Tool Invoked | Native MCP | LeanProxy |
|--------|-------------|------------|----------|
| 1 | `garmin_get_stats` | 10,000 | ~500 |
| 2 | `intervals_get_events` | 11,000 | ~500 |
| 3 | `intervals_get_activity_intervals` | cached | ~500 |
| 4 | `intervals_add_or_update_event` | cached | ~500 |
| **Total** | | **~21,000** | **~2,000** |

#### Session B: Dev Session (GitHub + Stitch)

| Prompt | Tool Invoked | Native MCP | LeanProxy |
|--------|-------------|------------|----------|
| 1 | `github_search_repositories` | 4,100 | ~600 |
| 2 | `github_get_file_contents` | cached | cached |
| 3 | `stitch_list_projects` | 5,300 | ~600 |
| 4 | `stitch_generate_screen_from_text` | cached | ~600 |
| 5 | `github_create_pull_request` | cached | ~600 |
| **Total** | | **~10,600** | **~2,400** |

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

#### Real Comparison: Native MCP vs LeanProxy

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

### How LeanProxy Achieves This

LeanProxy uses a **gateway pattern** with JIT (Just-In-Time) schema loading:

1. **Single router schema**: Only 2 tools (`invoke_tool`, `list_tools`) = **~110 tokens** vs 16,300+ for Native MCP
2. **On-demand tool registration**: Backend server schemas only load when actually needed (~500 tokens per invocation)
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