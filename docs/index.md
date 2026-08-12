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
| Garmin | 100 | ~11,130 tokens |
| GitHub | 41 | ~4,570 tokens |
| Intervals.icu | 10 | ~1,130 tokens |
| **All 3 combined** | **151** | **~16,830 tokens** |

> These tool counts come from the canonical live snapshot at `tests/bench/fixtures/live-snapshot.json` (refreshable with `go run ./tests/bench/live_snapshot`). The Stitch server is no longer available, so the canonical production shape is 3 servers. Each tool adds ~100 tokens of schema + arguments.

For a 7-prompt mixed session where all 3 MCP servers are configured but only 2-3 actually invoked, Native MCP wastes **~16,830 tokens** on schemas never used.

### Real Examples: Working Sessions (Measured v0.9.0)

Reproduced by `tests/bench/token_economy_bench_test.go` using the same Estimator as the runtime cost tracker:

| Session | Description | Prompts | Native MCP | LeanProxy | Savings |
|---------|-------------|--------|------------|----------|---------|
| A | Sport (Garmin + Intervals.icu) | 4 | ~12,260 | ~740 | **94.0%** |
| B | Dev (GitHub + Intervals.icu) | 5 | ~7,120 | ~925 | **87.0%** |
| C | Full Day (all 3) | 7 | ~29,450 | ~1,295 | **95.6%** |

#### Session A: Morning Sport (Garmin + Intervals.icu)

| Prompt | Tool Invoked | Native MCP (raw) | LeanProxy |
|--------|-------------|-----------------|----------|
| 1 | `garmin_get_stats` | ~11,130 | ~184 |
| 2 | `intervals_get_events` | ~2,780 | ~184 |
| 3 | `intervals_get_activity_intervals` | ~2,780 | ~184 |
| 4 | `intervals_add_or_update_event` | ~2,780 | ~184 |
| **Total** | | **~12,260** | **~740** |

#### Session B: Dev Session (GitHub + Intervals.icu)

| Prompt | Tool Invoked | Native MCP (raw) | LeanProxy |
|--------|-------------|-----------------|----------|
| 1 | `github_search_repositories` | ~4,570 | ~184 |
| 2 | `github_get_file_contents` | ~1,140 | ~184 |
| 3 | `intervals_get_events` | ~1,420 | ~184 |
| 4 | `intervals_add_or_update_event` | ~1,420 | ~184 |
| 5 | `github_create_pull_request` | ~1,420 | ~184 |
| **Total** | | **~7,120** | **~925** |

### The Cache Read Cost Fallacy

**Providers advertise prompt caching as "free" or "90% savings" — but cache reads aren't free.**

When a prompt cache hit occurs, you still pay for reading from cache:
- **OpenAI**: Cache reads at **0.25x** input token price
- **Anthropic**: Cache reads at **0.25x** input token price
- **DeepSeek**: Cache reads at **0.25x** input token price
- **Google Gemini**: Cache reads at ~**0.25x** input token price

This means **100% cache hit doesn't mean 100% free**. A 16,830-token MCP schema at 100% cache hit still costs:
```
16,830 tokens × 0.25x = 4,208 "effective" tokens worth of money
```

#### Real Comparison: Native MCP vs LeanProxy (Measured v0.9.0)

| MCP Servers | Tools | Native MCP (100% cache hit, 0.25x) | LeanProxy | Savings |
|-------------|-------|-----------------------------------|----------|---------|
| 1 (GitHub) | 41 | 1,143 tokens | 158 | **86.2%** |
| 1 (Garmin) | 100 | 2,783 tokens | 158 | **94.3%** |
| 2 (Garmin + GitHub) | 141 | 3,925 tokens | 158 | **96.0%** |
| 3 (all) | 151 | 4,208 tokens | 158 | **96.2%** |

*Native MCP sends tool schemas every prompt at 0.25x cache read. LeanProxy sends only the 158-token router payload (3 tools: list_servers, invoke_tool, list_tools) regardless of backend servers.*

**The key insight**: With Native MCP + caching, you pay for every tool schema on every request (at 0.25x). LeanProxy sends only the router schema — the backend tool schemas only load when actually invoked.

### Provider Caching on "Same Input Context"

For MCP tool schemas that are **identical every request**, caching only reduces cost by 75% — you're still paying for the read. The "same input context" scenario:

| Scenario | Input Tokens | Cache Rate | Cache Cost (0.25x) | LeanProxy | Savings |
|----------|--------------|-----------|-------------------|----------|---------|
| 1 server (Garmin) | 11,130 | 100% hit | 2,783 | **158** | 94% |
| 2 servers (Garmin + GitHub) | 15,700 | 100% hit | 3,925 | 158 | 96% |
| **3 servers (all)** | **16,830** | 100% hit | **4,208** | **158** | **96.2%** |

> **Critical insight**: With "same input context" caching, 100% cache hit STILL costs at 0.25x. LeanProxy sends only 158 tokens, making the cache-read cost negligible. This is the real advantage.

### Monthly Total Token Savings (100 sessions/month)

Measured on v0.9.0 with 3 servers. Native MCP sends tool schemas every request (at 0.25x cache read). LeanProxy only sends the 158-token router schema.

| Servers | Tools | GPT-4o-mini ($0.0375/M) | Anthropic Sonnet ($0.40/M) |
|---------|-------|--------------------------|----------------------------|
| 1 (GitHub) | 41 | $1.14 → **$1.14 saved** | $12.19 → **$12.17 saved** |
| 1 (Garmin) | 100 | $2.78 → **$2.78 saved** | $29.68 → **$29.64 saved** |
| 3 (all) | 151 | $4.21 → **$4.21 saved** | $44.88 → **$44.84 saved** |

*Formula: native_tokens × 0.25x × 100 sessions / 1M × price. LeanProxy cost: 158 × 100 / 1M × price (negligible).*

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

1. **Single router schema**: Only 3 tools (`list_servers`, `invoke_tool`, `list_tools`) = **158 tokens** (measured) vs 16,830 for Native MCP
2. **On-demand tool registration**: Backend server schemas only load when actually needed (~26 tokens per stub)
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
| **Prompt Injection Protection** | Classifies payloads against injection patterns with risk scoring and quarantine |
| **Sidecar LLM Redaction** | Context-aware redaction via local Ollama/MLX |
| **Semantic Cache** | Vector-similarity caching reduces redundant LLM calls |
| **Model Routing** | Route per-tool to different LLM models by complexity tier |
| **MCP Registry Marketplace** | Discover, search, and install community MCP servers |
| **Web Dashboard** | Real-time token usage monitoring with drill-down |
| **Budget Management** | Per-team/project spending limits with webhook alerts |
| **IDE Extensions** | VS Code and JetBrains plugins for cost monitoring |
| **Shadow Manifesting** | Merges global and project-local MCP configurations |
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
| [Model Routing](./configuration.md) | Per-tool LLM routing by complexity tier |
| [Sidecar LLM Redaction](./configuration.md) | Context-aware redaction via local LLM |
| [Web Dashboard](./dashboard.md) | Real-time monitoring with server/tool drill-down |
| [Budget Management](./budget.md) | Per-team/project budgets with webhooks |
| [IDE Extensions](./extensions.md) | VS Code and JetBrains plugins |
| [Cache Hit Rate Report](./commands.md) | `cache stats` for Anthropic prompt caching analytics |
| [CSV/JSON Cost Export](./commands.md) | `report --export csv/json` for external analysis |
| [Metrics Endpoint](./dashboard.md) | Prometheus-style JSON metrics for monitoring |

## Need Help?

- Check the [FAQ](./faq.md)
- Review the [Troubleshooting Guide](./troubleshooting.md)
- See [Commands Reference](./commands.md) for detailed command documentation