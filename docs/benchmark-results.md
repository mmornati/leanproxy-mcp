# Benchmark Results

Every number on this page, and in the README benchmark tables, is copied
from one run of the end-to-end harness (`make harness`, `tests/harness`,
issue #301). Nothing here is hand-edited or estimated. The run's report is
written to `bench-results/harness.md`, and CI uploads the same file as the
`harness-results` artifact on every push and pull request.

> **TL;DR.** Measured through the real binary: a session costs **72–87%
> fewer tokens** than native MCP, once LeanProxy's own discovery calls are
> counted. Each session also takes **3–4 extra LLM turns**. The router the
> client loads is **237 tokens**, against **10,049** for the five native
> `tools/list` payloads. The proxy adds **0.72 ms** at p95, and a
> 500-call burst finished with **0 errors**.

The README used to claim 81.5–93.7% session savings, ~12 µs overhead and
~25,000 q/s. Those figures came from benchmarks that did not run the proxy
(see [§6](#6-what-changed-from-the-previous-numbers)).

## 1. How to reproduce

```bash
make harness                 # ~30 s cold, ~6 s warm; writes bench-results/harness.md
```

- The harness builds `leanproxy-mcp` and the catalog mock itself. It needs
  no network, no real MCP server and no credentials.
- Every process runs with a scratch `HOME` and `LEANPROXY_TOOLCACHE_DIR`, so
  the harness never touches your real config, tool cache or status file.
- The tests use the `harness` build tag, so `go test ./...` does not run
  them.

## 2. What the harness runs

- **Proxy.** The real `leanproxy-mcp server run --stdio` binary. It is built
  with the release flags (`-trimpath -ldflags="-s -w"`) and driven over
  stdin/stdout pipes, the way an IDE drives it.
- **Upstream servers.** `tests/harness/catalogmcp` is a Go stdio MCP server
  that serves one server of a realistic 118-tool catalog
  (`tests/harness/testdata/catalog.json`):

  | Server | Tools |
  |---|---:|
  | GitHub | 42 |
  | Jira/Confluence | 24 |
  | Slack | 14 |
  | Garmin | 28 |
  | Postgres | 10 |

  The catalog was ported from the audit prototype
  `docs/audit/experiments/catalog.py`. The tool names, descriptions and
  parameter names follow the real servers. Every parameter is a string with
  a one-line description, so real schemas are usually larger.
- **Mock flags.**
  - `--server`: which catalog server to serve.
  - `--delay-ms`: slow down each `tools/call`.
  - `--response-bytes`: pad each result.
  - `--secrets`: embed fake credentials in each result. The keys are built
    at runtime, so no literal key is committed.
  - `--concurrent`: answer out of order.
- **Proxy config.** Defaults, plus the prompt-injection guard switched on:
  - redaction on, with the built-in patterns;
  - injection guard: block at risk ≥ 80, redact at risk 50–79;
  - no rate limit;
  - no response cache.

## 3. Methodology

### 3.1 Token unit

`pkg/reporter.Estimator` counts 1 token per 4 characters. It is the same
estimator the runtime cost tracker uses. The harness applies it to the
**full JSON-RPC response line** as it crosses the pipe, so both sides pay
for the envelope. The chars/4 rule approximates BPE tokenizers. It is not
exact, but it is consistent, and consistency is what the ratios need.

### 3.2 Discovery payloads

- **Native.** Each catalog server's own `tools/list` response, read directly
  from the mock.
- **Router.** LeanProxy's `tools/list` response (`list_servers`,
  `list_tools`, `invoke_tool`).
- **`list_servers` and `list_tools(server)`.** The real outputs of those
  router tools. `list_servers` is read once the background tool refresh has
  filled in every server's tool count.
- **`invoke_tool` overhead.** The token difference between an `invoke_tool`
  request and the same call sent natively as `tools/call`, and the same
  difference for the response.
- **`search_tools`.** Not measured: it does not exist yet (Epic 20).

### 3.3 Session model

A session is a list of `(server, tool)` prompts. The harness replays each
session through the proxy: every `list_servers`, `list_tools` and
`invoke_tool` call below really runs. Tokens are counted per LLM turn.

**Native MCP.** All 5 configured servers' `tools/list` payloads are in
context on every turn. The first turn pays for them at full price. Later
turns pay the provider's cache-read rate of **0.25×**. There are no extra
turns.

**LeanProxy.**

1. The router is in context from the first turn.
2. A turn that needs discovery adds that discovery output at full price:
   - `list_servers` on the first turn, because the router tells the model
     to call it first;
   - `list_tools(server)` the first time the session uses a server.
3. Each discovery output stays in context.
4. Every turn after the first re-reads the carried context (the router plus
   every earlier discovery output) at 0.25×.

**Extra turns.** Each discovery call is one extra LLM round-trip.
The harness reports them in their own column. Their token cost (another
re-read of the context) is **not** added to the LeanProxy total, so the
savings column is an upper bound.

**Assumptions.**

- Tool results are identical on both paths, so both sides leave them out.
  The `invoke_tool` request wrapper adds 14 tokens per call; that is
  also left out.
- Cache reads cost 0.25× (Anthropic's prompt-cache pricing), with a 100%
  cache hit after the first turn on both sides.
- On a native client, every configured server's schemas are loaded on every
  turn, whether or not the session uses that server.

**Sessions.**

| Session | Prompts |
|---|---|
| Morning Sport | 3 Garmin, then 1 Slack |
| Dev Workflow | GitHub ×2, Jira ×2, GitHub |
| Full Day | GitHub ×2, Jira ×2, Slack ×2, GitHub |

### 3.4 Latency, throughput and memory

- **Paced sequential.**
  - 200 `invoke_tool` calls, one at a time, 5 ms apart, after 20 warm-up
    calls.
  - Every call has a unique argument, so no cache can serve it.
  - The same 200 calls are also sent directly to the same mock.
  - Overhead is the proxied percentile minus the direct percentile.
- **Burst.** 500 `invoke_tool` calls, written back to back without waiting,
  round-robin over the 5 servers. The harness counts errors, checks that
  every answer carries its own request's argument, and measures wall time.
- **Parallel.**
  - 50 calls sent at once to a single mock server that sleeps 100 ms per
    call and answers concurrently.
  - The pool's default `max_in_flight` of 32 per server splits them into
    two waves.
- **Large response.** One 5 MiB tool result, relayed through the proxy and
  also read directly from the mock.
- **RSS.** `VmRSS` of the proxy process, taken once all 5 servers are warm
  and again after the burst.

### 3.5 Safety

The mock embeds three fake credentials in every result. The harness then
checks four things:

- **Server → client.** No fake credential reaches the client.
- **Client → server.** The mock counts the fake credentials that arrive in
  the request it received. The count must be 0.
- **Injection.** A block-level payload is refused with -32600.
- **Server-initiated requests.** The mock sends `roots/list` in the middle
  of a call. The proxy must answer it, and the call must complete.

`TestHarness_RedactionAssertionTrips` runs the same checks with
`bouncer: {enabled: false}` and requires the redaction assertion to fail.
The harness therefore cannot pass vacuously.

## 4. Results

Run metadata:

| Field | Value |
|---|---|
| Commit | `994d8c8` |
| Date (UTC) | 2026-09-23 |
| Host | linux/amd64, 4 CPUs, go1.25.5 |

Latency depends on the host; token counts do not.

### 4.1 Assertions (a failure fails `make harness` and CI)

| Assertion | Measured | Result |
|---|---|---|
| Redaction active (both directions) | both directions clean | PASS |
| Injection payload blocked | JSON-RPC error -32600 returned | PASS |
| Server→client request does not hang | answered in 1 ms | PASS |
| 0 errors in a 500-call pipelined burst | 0 errors | PASS |
| 50 × 100 ms parallel calls < 1 s | 205 ms | PASS |
| 5 MB response relayed | 5,242,881 bytes | PASS |
| p95 proxy overhead < 5 ms | 0.72 ms | PASS |

### 4.2 Discovery payloads

| Payload | Tokens |
|---|---:|
| LeanProxy `tools/list` (router, 3 tools) | 237 |
| LeanProxy `list_servers` (5 servers) | 80 |
| LeanProxy `search_tools` | n/a (Epic 20) |
| `invoke_tool` request overhead vs a native `tools/call` | +14 |
| `invoke_tool` response overhead vs a native `tools/call` | +0 |

### 4.3 Per server

| Server | Tools | Native `tools/list` | LeanProxy `list_tools(server)` | LeanProxy router | Router vs native |
|---|---:|---:|---:|---:|---:|
| github | 42 | 4,443 | 1,638 | 237 | −94.7% |
| jira | 24 | 2,043 | 772 | 237 | −88.4% |
| slack | 14 | 1,128 | 441 | 237 | −79.0% |
| garmin | 28 | 1,795 | 760 | 237 | −86.8% |
| postgres | 10 | 640 | 282 | 237 | −63.0% |
| **all 5** | **118** | **10,049** | **3,893** | **237** | **−97.6%** |

"Router vs native" compares only what sits in context before any tool is
used. A real session also pays for discovery; that cost is in §4.4.

### 4.4 Session replay

| Session | Prompts | Servers used | Native tokens | LeanProxy tokens | Savings | Extra LLM turns |
|---|---:|---:|---:|---:|---:|---:|
| Morning Sport | 4 | 2 | 17,586 | 2,328 | −86.8% | +3 |
| Dev Workflow | 5 | 2 | 20,098 | 5,068 | −74.8% | +3 |
| Full Day | 7 | 3 | 25,123 | 7,093 | −71.8% | +4 |

The savings shrink as a session uses more servers and more tools. Each
newly used server brings its `list_tools` output into context. GitHub's
output alone is 1,638 tokens.

### 4.5 Latency, throughput and memory

| Measurement | Value |
|---|---:|
| Paced sequential `invoke_tool`, 200 unique calls: p50 / p95 / p99 via proxy | 0.80 / 1.06 / 1.65 ms |
| Same calls direct to the mock: p50 / p95 / p99 | 0.21 / 0.34 / 0.79 ms |
| Proxy overhead (proxy − direct): p50 / p95 / p99 | 0.59 / 0.72 / 0.85 ms |
| 500-call pipelined burst over 5 servers: wall / throughput / errors | 43 ms / 11,570 req/s / 0 |
| 50 parallel calls to a 100 ms tool on one server (default `max_in_flight` 32, so two waves): wall | 205 ms |
| 5 MB tool response: via proxy / direct | 479 ms / 55 ms |
| Proxy RSS: idle (5 servers warm) / after the burst | 20.1 MiB / 21.8 MiB |
| Binary size (linux/amd64, `-trimpath -ldflags="-s -w"`) | 16.3 MiB |

The 5 MB relay costs about 0.4 s more through the proxy than directly.
Most of that is response-side secret redaction, which scans the whole
result. A one-off check with `bouncer.enabled: false` relayed the same
response several times faster; the harness does not report that
configuration.

## 5. Micro-benchmarks that remain in `tests/bench`

`make bench` still runs these. Each one measures only what its name says.
None of them is a proxy-level number, and none is quoted in the README.

| Benchmark | What it measures |
|---|---|
| `BenchmarkSchemaTax_LeanProxyRouter` | Size of the real router `tools/list` payload (`pkg/mcp.GetAllToolDefinitions`) |
| `BenchmarkSchemaTax_StubSchema` | Size of one lazy-loading `registry.ToolStub` |
| `BenchmarkJSONParse_Literal` | `json.Unmarshal` of two literal strings plus one cost-tracker update, in process |
| `BenchmarkEstimateTokens_50MB` | The chars/4 estimator over a 50 MB buffer |
| `TestBinarySize_NFR3` | Size of the `dist/` binaries from `make build` (< 20 MB) |

## 6. What changed from the previous numbers

| Previous claim | Where it came from | Now (harness) |
|---|---|---|
| Session savings 81.5–93.7% | `SessionReplay_*` charged LeanProxy the router plus one ~26-token stub per prompt. It ignored the `list_tools` output and the extra turns, and its timing loop was empty. | **−71.8% to −86.8%**, with discovery outputs counted and 3–4 extra turns reported |
| Per-server savings 79.0–97.9% (GitHub 41 tools = 4,570 tokens, ...) | Synthetic `tools/list` payloads padded with dots to a byte count from a seeded snapshot | Real catalog payloads: router vs native **−63.0% to −94.7%** per server, **−97.6%** for all 5 |
| Proxy overhead ~12 µs/op (p50) | `BenchmarkProxyOverhead_NFR1`: two `json.Unmarshal` calls on literals, in process | **0.59 ms p50 / 0.72 ms p95** through the binary, measured against a direct baseline |
| Throughput ~25,000 q/s | `BenchmarkThroughput_MockMCP`: an in-process mock, with no proxy involved | **11,570 req/s** for a 500-call pipelined burst through the binary (one run; varies by host) |
| 50 MB payload estimate ~7 ms | Token estimator over a 50 MB buffer; no relay | **5 MB relayed in 479 ms** (estimator benchmark renamed `BenchmarkEstimateTokens_50MB`) |
| Binary 15.8 MB (darwin-arm64) | `dist/` build on the maintainer's machine | **16.3 MiB** (linux/amd64, harness build) |

## 7. Known limits and follow-ups

- **`search_tools` (Epic 20).** Once it lands, the harness should add its
  cost and a "LeanProxy with `search_tools`" session column. The audit
  prototype estimated it at about −92% for Full Day.
- **Extra-turn cost.** The extra turns are counted but not costed. Costing
  them needs a model of what the model re-sends on a discovery turn.
- **Transports.** Only the stdio front end and stdio upstreams are measured.
  `serve`, HTTP and SSE are not.
- **Catalog.** It is realistic but synthetic. Real servers often have
  larger schemas, with enums, nested objects and long descriptions. That
  raises both the native cost and the `list_tools` cost.
