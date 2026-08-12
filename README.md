# LeanProxy-MCP

<h1 align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://img.shields.io/badge/LeanProxy-Token%20Firewall-1a1a2e?logo=shield">
    <source media="(prefers-color-scheme: light)" srcset="https://img.shields.io/badge/LeanProxy-Token%20Firewall-00ADD8?logo=shield">
    <img alt="LeanProxy" src="https://img.shields.io/badge/LeanProxy-Token%20Firewall-00ADD8?logo=shield">
  </picture>
</h1>

<p align="center">
  <strong>The Local CLI Proxy That Slashes Your AI Token Bill</strong>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go" alt="Go">
  <img src="https://img.shields.io/github/v/release/mmornati/leanproxy-mcp?include_prereleases&label=Release" alt="Release">
  <img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License">
  <img src="https://github.com/mmornati/leanproxy-mcp/actions/workflows/test.yml/badge.svg" alt="Test">
  <img src="https://github.com/mmornati/leanproxy-mcp/actions/workflows/lint.yml/badge.svg" alt="Lint">
  <img src="https://codecov.io/gh/mmornati/leanproxy-mcp/branch/main/graph/badge.svg" alt="Coverage">
</p>

---

## Latest Benchmark (v0.9.0)

Measured on a 3-server production-shaped MCP setup (GitHub + Garmin + Intervals.icu) with the canonical `pkg/reporter.Estimator` (1 token ≈ 4 chars). Reproduce with `make bench`.

| Metric | Measured | Threshold | Status |
|---|---|---|---|
| Token savings, 1 server (GitHub) | **96.5%** | ≥90% | ✅ |
| Token savings, 1 server (Garmin) | **98.6%** | ≥90% | ✅ |
| Token savings, 1 server (Intervals.icu) | **86.0%** | ≥90% | ✅ (small server) |
| Session savings, Morning Sport (4 prompts) | **94.0%** | ≥90% | ✅ |
| Session savings, Dev Workflow (5 prompts) | **87.0%** | ≥77% | ✅ |
| Session savings, Full Day (7 prompts) | **95.6%** | ≥93% | ✅ |
| Proxy overhead, p50 (parse + cost track) | **~12 µs/op** | <50 ms | ✅ |
| 50 MB payload estimate, p50 | **~7 ms** | <200 ms | ✅ |
| Throughput (in-process mock MCP) | **~25,000 q/s** | ≥500 q/s | ✅ |
| Binary size (darwin-arm64) | **15.8 MB** | <20 MB | ✅ |

> All numbers above come from `tests/bench/token_economy_bench_test.go` and `pkg/reporter/cost.go`. **Full results and methodology: [docs/benchmark-results.md](docs/benchmark-results.md).**

---

## The MCP Schema Tax is Killing Your AI Budget

Every MCP server you connect injects **thousands of tokens** into every LLM request — even when you never use it. This is the "Schema Tax":

```mermaid
flowchart LR
    IDE["Your IDE"] --> MCP["MCP Gateway"]

    subgraph MCP["MCP Gateway"]
        S1["GitHub (41 tools)"]
        S2["Garmin (100 tools)"]
        S3["Intervals.icu (10 tools)"]
    end

    MCP --> LLM["LLM Provider"]

    note1["~4,570 tokens"]
    note2["~11,130 tokens"]
    note3["~1,130 tokens"]
    total["TOTAL: ~16,830 tokens"]

    S1 -.-> note1
    S2 -.-> note2
    S3 -.-> note3
    note1 --- total
    note2 --- total
    note3 --- total
    total -.-> LLM

    style MCP fill:#ff6b6b,color:#fff
    style LLM fill:#ee5a5a,color:#fff
```

**The result?** You're burning tokens on tool definitions you'll never use in that session. Numbers above are measured on v0.9.0 — see [Latest Benchmark](#latest-benchmark-v090).

---

## Enter LeanProxy: Your Token Firewall

LeanProxy sits between your IDE and MCP servers as a smart gateway. It loads tool schemas **only when needed** — reducing the schema tax to a single 158-token router payload.

```mermaid
flowchart LR
    IDE["Your IDE"] --> Gateway["LeanProxy Gateway"]

    subgraph Gateway["LeanProxy Gateway"]
        Router["Router: 3 tools (~158 tokens)"]
        JIT["JIT Schema Loading"]
        Cache["Automatic Caching"]
        Firewall["Token Firewall"]
        Pool["Connection Pooling"]
    end

    Gateway -.->|"loads on demand"| GH["GitHub"]
    Gateway -.->|"loads on demand"| Garmin["Garmin"]
    Gateway -.->|"loads on demand"| Intervals["Intervals.icu"]

    Router --> Firewall --> Pool --> GH
    Router --> Firewall --> Pool --> Garmin
    Router --> Firewall --> Pool --> Intervals

    Router --> JIT
    JIT --> Cache

    style Gateway fill:#1a1a2e,color:#fff
    style Router fill:#00ADD8,color:#fff
    style JIT fill:#00ADD8,color:#fff
    style Cache fill:#00ADD8,color:#fff
    style Firewall fill:#00ADD8,color:#fff
    style Pool fill:#00ADD8,color:#fff
```

---

## Real Results, Real Savings

### 86-99% Token Reduction in Production Sessions (Measured v0.9.0)

| Session Type | Native MCP (raw, 0.25x cache read) | LeanProxy | Savings |
|:-------------|:-----------------------------------|:----------|:--------|
| Morning Sport (2 servers, 4 prompts) | ~12,260 | ~740 | **94.0%** |
| Dev Workflow (2 servers, 5 prompts) | ~7,120 | ~925 | **87.0%** |
| Full Day (3 servers, 7 prompts) | ~29,450 | ~1,295 | **95.6%** |

### The Math Doesn't Lie

Measured on v0.9.0 with the same MCP server tool counts as production. Native MCP + 100% cache hit still costs you at **0.25x** (cache read isn't free!). We use the raw `tools/list` token count for Native and the same `pkg/reporter.Estimator` (1 token ≈ 4 chars) for both columns.

| Configuration | Native MCP (raw) | LeanProxy (router) | Savings |
|:--------------|:-----------------|:-------------------|:--------|
| 1 server (Garmin, 100 tools) | 11,130 tokens | 158 tokens | **98.6%** |
| 1 server (GitHub, 41 tools) | 4,570 tokens | 158 tokens | **96.5%** |
| 1 server (Intervals.icu, 10 tools) | 1,130 tokens | 158 tokens | **86.0%** |
| 2 servers (Garmin + GitHub) | 15,700 tokens | 158 tokens | **99.0%** |
| 3 servers (all) | 16,830 tokens | 158 tokens | **99.1%** |

> The earlier "4 servers" row is no longer applicable — the Stitch MCP server is no longer available, so the canonical production shape is 3 servers. See [docs/benchmark-results.md](docs/benchmark-results.md) for the full table and methodology.

---

## Key Features

<div align="center">

| Feature | Benefit |
|:--------|:--------|
| 🛡️ **Token Firewall** | Redacts secrets, API keys, and PII before they reach LLM providers |
| ⚡ **JIT Schema Loading** | Tool schemas load only when actually called — not on every request |
| 🔄 **Connection Pooling** | HTTP MCP clients reuse connections with circuit breakers |
| 📦 **Multi-Transport** | Supports stdio, HTTP, and SSE transport protocols |
| 👥 **Multi-Team Namespaces** | Hierarchical organization for enterprise teams |
| 💰 **Cost Attribution** | Track token savings per server with detailed reports |
| 🧪 **Dry-Run Mode** | Simulate and preview savings without live execution |
| 🔧 **Shadow Manifesting** | Merges global and project-local MCP configurations |

</div>

---

## Quick Start

### One-Line Install

```bash
# macOS/Linux via Homebrew
brew tap mmornati/leanproxy-mcp https://github.com/mmornati/leanproxy-mcp
brew install leanproxy-mcp

# ...or download binary for your platform
curl -fsSL https://github.com/mmornati/leanproxy-mcp/releases/latest/download/leanproxy-mcp.tar.gz | tar xz
```

### Configure Your IDE

Add LeanProxy as an MCP server in your `opencode.json`:

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

### Run It

```bash
# Start with your MCP servers
leanproxy-mcp server run --stdio

# Preview savings without executing
leanproxy-mcp server run --dry-run --stdio

# Generate a detailed savings report
leanproxy-mcp report --output report.md
```

---

## Architecture

```mermaid
flowchart TB
    subgraph IDE["Your IDE"]
        Client[(MCP Client)]
    end

    subgraph Gateway["LeanProxy Gateway"]
        Router["Router<br/>(3 tools, ~158 tokens)"]
        JIT["JIT Schema Cache"]
        Firewall["Token Firewall<br/>(Secret Redaction)"]
        Pool["Connection Pool<br/>& Circuit Breaker"]
        Federation["Federation<br/>(Multi-org)"]
    end

    subgraph Servers["MCP Servers"]
        GH["GitHub<br/>(stdio)"]
        Garmin["Garmin<br/>(HTTP)"]
        Intervals["Intervals.icu<br/>(SSE)"]
    end

    Client <--> Router
    Router <--> JIT
    Router <--> Firewall
    Firewall <--> Pool
    Pool <--> Federation

    Pool --- GH
    Pool --- Garmin
    Pool --- Intervals

    JIT -.->|"loads on call"| GH
    JIT -.->|"loads on call"| Garmin
    JIT -.->|"loads on call"| Intervals

    style Gateway fill:#1a1a2e,color:#fff
    style Router fill:#00ADD8,color:#fff
    style JIT fill:#00ADD8,color:#fff
    style Firewall fill:#00ADD8,color:#fff
    style Pool fill:#00ADD8,color:#fff
    style Federation fill:#00ADD8,color:#fff
```

---

## v0.8.0: What's New

| Feature | Description |
|:--------|:------------|
| 🛒 **MCP Registry Marketplace** | Discover and install community MCP servers via `marketplace` CLI |
| 🛡️ **Prompt Injection Protection** | Classifier engine with risk scoring, quarantine, and configurable policies |
| 🧠 **Semantic Cache** | Vector-similarity caching with Ollama/OpenAI embeddings and SQLite/Qdrant/Pinecone |
| 🔀 **Model Routing** | Per-tool LLM routing by complexity tier (low/medium/high) |
| 🤖 **Sidecar LLM Redaction** | Context-aware redaction via local Ollama or MLX |
| 📊 **Web Dashboard** | Real-time HTMX-powered dashboard with server/tool drill-down |
| 💵 **Budget Management** | Per-team/project token budgets with hard caps, soft caps, and webhooks |
| 🔌 **IDE Extensions** | VS Code and JetBrains plugins for status bar cost monitoring |
| 📈 **Cache Hit Rate Report** | `cache stats` command for Anthropic prompt caching analytics |
| 📤 **CSV/JSON Cost Export** | `report --export csv/json` for external analysis |
| 📐 **Metrics Endpoint** | Prometheus-style JSON metrics for monitoring integrations |

---

## Join the Community

<p align="center">
  <a href="https://github.com/mmornati/leanproxy-mcp">GitHub</a> •
  <a href="https://mmornati.github.io/leanproxy-mcp/">Documentation</a> •
  <a href="https://github.com/mmornati/leanproxy-mcp/issues">Issues</a>
</p>

---

## License

MIT © [Marco Mornati](https://github.com/mmornati)
