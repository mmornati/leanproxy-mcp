# Why LeanProxy: the economics of MCP

With pay-per-use API pricing, every token sent to an LLM costs money. This
page shows where MCP spends tokens and how much LeanProxy-MCP can save.

!!! note "Which clients get which savings"
    The schema savings on this page come from **router** mode, where
    LeanProxy replaces every tool definition with four small discovery tools.
    Clients with their own native tool search or deferred tool loading
    (Claude Code, Claude Desktop, Cursor and VS Code) get **passthrough**
    mode by default instead: they see every upstream tool and handle
    discovery themselves, so LeanProxy saves them no schema tokens. The
    router savings apply to other clients (OpenCode, Zed, custom agents, and
    so on), or to any client you switch to router mode.

    The response-side savings of the
    [response governor](configuration.md#response-token-governor-response)
    (truncation, field projection, dedup, summaries) apply to every client in
    every mode, once you enable it.

    See [Which exposure mode each IDE gets](quickstart.md#which-exposure-mode-each-ide-gets)
    and [Exposure Modes](configuration.md#exposure-modes-exposure).

## The MCP Schema Tax

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

## Real Examples: Replayed Working Sessions

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

## Prompt caching does not make schemas free

Prompt caching lowers the price of repeated input, but a cache read is still
billed. The exact rate depends on the provider and the model; check your
provider's current price list. The benchmark harness models cache reads at
**0.25×** the input price.

At that rate, the harness catalog's 10,049-token schema load still costs this
much on every cached turn:

```
10,049 tokens × 0.25 = ~2,512 "effective" tokens per turn
```

### What sits in context before the first tool call

| MCP Servers | Tools | Native `tools/list` | LeanProxy router | Savings |
|-------------|-------|---------------------|------------------|---------|
| 1 (GitHub) | 42 | 4,443 tokens | 318 | **−92.8%** |
| 1 (Garmin) | 28 | 1,795 tokens | 318 | **−82.3%** |
| 1 (Postgres) | 10 | 640 tokens | 318 | **−50.3%** |
| 5 (all) | 118 | 10,049 tokens | 318 | **−96.8%** |

*This table counts only the static schema load in router mode. LeanProxy
fetches tools on demand with `search_tools` or `list_tools`, and that cost is
included in the session table above. In passthrough mode the client receives
the full native list, so there is no schema saving.*

## Should You Use Caching with MCP?

| Scenario | Cache Hit | Recommendation |
|----------|----------|----------------|
| MCP tool schemas (100% same) | 100% | ❌ Still billed at the cache-read rate — use router mode |
| Conversation history (growing) | 90%+ | ✅ Caching saves money |
| Codebase/RAG context | 80%+ | ✅ Caching saves money |
| MCP schemas in short session | 100% | ❌ Cache read cost > savings |

**Key insight**: for MCP tool schemas that are **identical on every request**,
caching lowers the cost but does not remove it. In router mode, LeanProxy
replaces the schemas with a 318-token router, and pays for discovery calls
instead.

## How LeanProxy Achieves This

LeanProxy uses a **gateway pattern** with JIT (Just-In-Time) schema loading:

1. **Single router schema** (router mode): only 4 tools (`search_tools`, `list_servers`, `list_tools`, `invoke_tool`) = **318 tokens**, against 10,049 for the harness catalog's five native `tools/list` payloads. With the response governor on, a fifth tool, `read_result`, is added.
2. **On-demand tool discovery**: `search_tools` returns the best 5 tools across every server for a plain-words query (152 tokens per lookup on average in the harness); `list_tools` returns a whole server's list when the model wants to browse (282–1,638 tokens per server in the harness catalog)
3. **Smaller tool results** (every mode, opt-in): the response governor projects, deduplicates, truncates or summarizes large tool results, and keeps the full result available through `read_result`
4. **Persistent tool cache**: tool definitions are stored on disk, so the proxy answers `tools/list` immediately at start and refreshes servers in the background

To see what LeanProxy actually saved in your own sessions, run
[`leanproxy-mcp report`](./savings-report.md).

For full benchmark methodology and raw numbers, see [Benchmark Results](./benchmark-results.md).

## Decision Framework

G/N is the share of your prompts that use a given MCP server.

| Service Usage (G/N ratio) | Recommendation |
|--------------------------|----------------|
| > 40% (every prompt) | Native MCP justified |
| 5-40% (regular use) | **LeanProxy Gateway** |
| < 5% (rare use) | CLI or on-demand skill |

For example, if only 5–10% of your prompts touch GitHub (fetch an issue,
create a PR), loading its 42 tool definitions on every turn costs more than
discovering them when needed.
