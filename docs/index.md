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

Each MCP server you run adds its tool schemas to every LLM request. The
table below uses the benchmark harness (`make harness`). The harness puts
the real `leanproxy-mcp` binary in front of a realistic 118-tool catalog of
five servers:

| MCP Server | Tools | Native `tools/list` (tokens per request) |
|-------------|-------|-------------------|
| GitHub | 42 | 4,443 |
| Jira/Confluence | 24 | 2,043 |
| Garmin | 28 | 1,795 |
| Slack | 14 | 1,128 |
| Postgres | 10 | 640 |
| **All 5 combined** | **118** | **10,049** |

A native MCP client sends all 10,049 tokens on every turn, even when the
session uses only two of the five servers. LeanProxy replaces them with its
318-token router.

### Real Examples: Replayed Working Sessions

The harness replays each session through the binary. It counts LeanProxy's
own discovery calls, with two discovery styles: `list_servers` on the first
turn and `list_tools(server)` the first time each server is used, or one
`search_tools(query)` the first time each tool is needed (plus a
`list_tools` fallback when the search misses). It also reports the extra
LLM round-trips those calls cost.

| Session | Prompts | Servers used | Native MCP | LeanProxy (`list_tools`) | Savings | Extra LLM turns | LeanProxy (`search_tools`) | Savings | Extra LLM turns |
|---------|--------|--------------|------------|----------|---------|-----------------|----------|---------|-----------------|
| Morning Sport | 4 | 2 of 5 | 17,586 | 2,470 | **−86.0%** | +3 | 1,148 | **−93.5%** | +4 |
| Dev Workflow | 5 | 2 of 5 | 20,098 | 5,234 | **−74.0%** | +3 | 3,143 | **−84.4%** | +6 |
| Full Day | 7 | 3 of 5 | 25,123 | 7,302 | **−70.9%** | +4 | 8,793 | **−65.0%** | +10 |

Native MCP loads all five servers' schemas on every turn: the first turn at
full price, later turns at the 0.25× cache-read rate. LeanProxy carries the
router, plus every discovery output fetched so far, at the same rates.

The savings exclude the token cost of the extra turns, so they are an upper
bound. With `list_tools`, the savings shrink as a session uses more
servers, because each new server brings its `list_tools` output into
context (1,638 tokens for GitHub). A `search_tools` lookup costs 152 tokens
on average, but a miss (3 of Full Day's 7 queries) pays the `list_tools`
fallback on top.

### The Cache Read Cost Fallacy

**Providers advertise prompt caching as "free" or "90% savings", but cache
reads aren't free.**

On a prompt cache hit you still pay to read from the cache:

- **OpenAI**: cache reads at **0.25x** the input token price.
- **Anthropic**: cache reads at **0.25x** the input token price.
- **DeepSeek**: cache reads at **0.25x** the input token price.
- **Google Gemini**: cache reads at about **0.25x** the input token price.

So a **100% cache hit is not free**. The harness catalog's 10,049-token
schema load still costs this much on every cached turn:

```
10,049 tokens × 0.25 = ~2,512 "effective" tokens per turn
```

#### What sits in context before the first tool call

| MCP Servers | Tools | Native `tools/list` | LeanProxy router | Savings |
|-------------|-------|---------------------|------------------|---------|
| 1 (GitHub) | 42 | 4,443 tokens | 318 | **−92.8%** |
| 1 (Garmin) | 28 | 1,795 tokens | 318 | **−82.3%** |
| 1 (Postgres) | 10 | 640 tokens | 318 | **−50.3%** |
| 5 (all) | 118 | 10,049 tokens | 318 | **−96.8%** |

*This table counts only the static schema load. LeanProxy fetches tools on
demand with `search_tools` or `list_tools`, and that cost is included in
the session table above.*

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

1. **Single router schema**: Only 4 tools (`search_tools`, `list_servers`, `list_tools`, `invoke_tool`) = **318 tokens**, against 10,049 for the harness catalog's five native `tools/list` payloads
2. **On-demand tool discovery**: `search_tools` returns the best 5 tools across every server for a plain-words query (152 tokens per lookup on average in the harness); `list_tools` returns a whole server's list when the model wants to browse (282–1,638 tokens per server in the harness catalog)
3. **Session-aware caching**: Tool schemas persist across the session without per-request overhead

For full benchmark methodology and raw numbers, see [benchmark-results.md](./benchmark-results.md).

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
| **Prompt Injection Protection** | Classifies the decoded text of requests and of tool results, resources and prompts, with separate policies (block, quarantine, redact, annotate) |
| **Tool Pinning** | Pins a hash of every upstream tool definition; warns about (default) or blocks new/changed tools and poisoned descriptions until approved |
| **Per-Tool Policy** | Allow / deny / confirm (MCP elicitation) rules per tool glob and annotation; calls to tools a server does not advertise are refused |
| **Sidecar LLM Redaction** | Context-aware redaction via a local Ollama model |
| **Semantic Cache** | Vector-similarity caching reduces redundant LLM calls |
| **MCP Registry Marketplace** | Discover, search, and install community MCP servers |
| **Web Dashboard** | Real-time token usage monitoring with drill-down |
| **IDE Extensions** | VS Code and JetBrains plugins for cost monitoring |
| **JIT Discovery** | On-demand tool registration to minimize context overhead |
| **Dry-Run Mode** | Simulate proxy behavior without live execution |

## Getting Started

New to LeanProxy-MCP? Start here:

1. [Installation Guide](./installation.md) - Download and install
2. [Quick Start](./quickstart.md) - Basic usage
3. [Commands Reference](./commands.md) - Full command documentation

## New in v0.8.0

| Feature | Description |
|---------|-------------|
| [MCP Registry Marketplace](./commands.md) | `marketplace` CLI — sync, search, and install servers |
| [Prompt Injection Protection](./security.md) | Classifier engine with risk scoring and quarantine |
| [Semantic Cache](./configuration.md) | Vector similarity caching with Ollama/OpenAI embeddings |
| [Sidecar LLM Redaction](./configuration.md) | Context-aware redaction via local LLM |
| [Web Dashboard](./dashboard.md) | Real-time monitoring with server/tool drill-down |
| [IDE Extensions](./extensions.md) | VS Code and JetBrains plugins |
| [Cache Hit Rate Report](./commands.md) | `cache stats` for Anthropic prompt caching analytics |
| [CSV/JSON Cost Export](./commands.md) | `report --export csv/json` for external analysis |
| [Metrics Endpoint](./dashboard.md) | Prometheus-style JSON metrics for monitoring |

## Need Help?

- Check the [FAQ](./faq.md)
- Review the [Troubleshooting Guide](./troubleshooting.md)
- See [Commands Reference](./commands.md) for detailed command documentation