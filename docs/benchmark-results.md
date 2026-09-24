# Benchmark Results

Every number on this page, and in the README benchmark tables, is copied
from one run of the end-to-end harness (`make harness`, `tests/harness`,
issue #301). Nothing here is hand-edited or estimated. The run's report is
written to `bench-results/harness.md`, and CI uploads the same file as the
`harness-results` artifact on every push and pull request.

> **TL;DR.** Measured through the real binary: a session costs **71–86%
> fewer tokens** than native MCP with `list_tools` discovery, and **65–94%**
> with `search_tools` discovery, once LeanProxy's own discovery calls are
> counted. Discovery adds **3–4 extra LLM turns** per session with
> `list_tools` and **4–10** with `search_tools` (one search per new tool).
> A `search_tools` lookup costs **152 tokens** on average, against **907**
> for `list_tools(server)`, and finds the right tool in its top 5 for
> **85.5%** of 83 labeled intents. The router the client loads is **318
> tokens**, against **10,049** for the five native `tools/list` payloads.
> The proxy adds **0.74 ms** at p95, and a 500-call burst finished with
> **0 errors**.

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
- **Router.** LeanProxy's `tools/list` response (`search_tools`,
  `list_servers`, `list_tools`, `invoke_tool`).
- **`list_servers` and `list_tools(server)`.** The real outputs of those
  router tools. `list_servers` is read once the background tool refresh has
  filled in every server's tool count.
- **`invoke_tool` overhead.** The token difference between an `invoke_tool`
  request and the same call sent natively as `tools/call`, and the same
  difference for the response.
- **`search_tools`.** Each of the 83 labeled intents of
  `pkg/toolsearch/testdata/intents.json` is sent as one `search_tools`
  call (k = 5, BM25, default config). The harness reports the average and
  maximum response size, and compares it with the `list_tools(server)`
  output of each intent's server, which is what the `list_tools` flow reads
  for the same lookup. It also reports recall@1 and recall@5 of those
  answers. 44 intents come from the audit prototype; 39 were written from
  the user's side without reusing the catalog descriptions.

### 3.3 Session model

A session is a list of prompts. Each prompt names the `(server, tool)` that
serves it and a plain-words query a model would search with. The harness
replays each session through the proxy: every `list_servers`, `list_tools`,
`search_tools` and `invoke_tool` call below really runs. Tokens are counted
per LLM turn.

**Native MCP.** All 5 configured servers' `tools/list` payloads are in
context on every turn. The first turn pays for them at full price. Later
turns pay the provider's cache-read rate of **0.25×**. There are no extra
turns.

**LeanProxy with `list_tools`.**

1. The router is in context from the first turn.
2. A turn that needs discovery adds that discovery output at full price:
   - `list_servers` on the first turn, because the router tells the model
     to call it first;
   - `list_tools(server)` the first time the session uses a server.
3. Each discovery output stays in context.
4. Every turn after the first re-reads the carried context (the router plus
   every earlier discovery output) at 0.25×.

**LeanProxy with `search_tools`.** The same rules, but the discovery is one
`search_tools(query)` the first time the session needs a tool (a tool found
earlier in the session is already in context). No `list_servers` call. When
the tool is not in the top 5, the model is assumed to fall back to
`list_tools(server)`: that output is added at full price too, with one more
extra turn. The queries are not tuned to the index; a miss is paid for, not
rewritten.

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

The prompts and their queries are in `tests/harness/harness_test.go`.

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

The mock embeds three fake credentials in every result, and in the
description of its first tool. The harness then checks five things:

- **Server → client.** No fake credential reaches the client.
- **Server → client via `search_tools`.** A search that returns the leaky
  tool shows no fake credential.
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
| Commit | `430f9a8` |
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
| p95 proxy overhead < 5 ms | 0.74 ms | PASS |

### 4.2 Discovery payloads

| Payload | Tokens |
|---|---:|
| LeanProxy `tools/list` (router, 4 tools) | 318 |
| LeanProxy `list_servers` (5 servers) | 80 |
| LeanProxy `search_tools` (k=5), per lookup: average / max over 83 labeled intents | 152 / 260 |
| LeanProxy `list_tools(server)` for the same intents' servers, per lookup: average | 907 |
| `invoke_tool` request overhead vs a native `tools/call` | +14 |
| `invoke_tool` response overhead vs a native `tools/call` | +0 |

### 4.3 Per server

| Server | Tools | Native `tools/list` | LeanProxy `list_tools(server)` | LeanProxy router | Router vs native |
|---|---:|---:|---:|---:|---:|
| github | 42 | 4,443 | 1,638 | 318 | −92.8% |
| jira | 24 | 2,043 | 772 | 318 | −84.4% |
| slack | 14 | 1,128 | 441 | 318 | −71.8% |
| garmin | 28 | 1,795 | 760 | 318 | −82.3% |
| postgres | 10 | 640 | 282 | 318 | −50.3% |
| **all 5** | **118** | **10,049** | **3,893** | **318** | **−96.8%** |

"Router vs native" compares only what sits in context before any tool is
used. A real session also pays for discovery; that cost is in §4.5.

The router grew from 237 to 318 tokens when `search_tools` was added
(#305).

### 4.4 `search_tools` ranking through the binary

| Measurement | Value |
|---|---:|
| Recall@1 (right tool first) | 55/83 (66.3%) |
| Recall@5 (right tool in the answer) | 71/83 (85.5%) |
| Tokens per lookup vs `list_tools(server)` | −83.2% |

On the 44 audit intents alone recall@5 is 88.6%; on the 39 independent
intents it is 82.1% (`go test ./pkg/toolsearch -run TestRecall -v`). The
independent intents miss mostly on vocabulary the catalog does not use
("time booked" for a worklog, "watches" for devices); the optional hybrid
mode (`tool_search.hybrid`, off by default and not measured here) targets
those.

### 4.5 Session replay

| Session | Prompts | Servers used | Native tokens | LeanProxy `list_tools` tokens | Savings | Extra LLM turns | LeanProxy `search_tools` tokens | Savings | Extra LLM turns | Search misses |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Morning Sport | 4 | 2 | 17,586 | 2,470 | −86.0% | +3 | 1,148 | −93.5% | +4 | 0 |
| Dev Workflow | 5 | 2 | 20,098 | 5,234 | −74.0% | +3 | 3,143 | −84.4% | +6 | 1 |
| Full Day | 7 | 3 | 25,123 | 7,302 | −70.9% | +4 | 8,793 | −65.0% | +10 | 3 |

With `list_tools`, the savings shrink as a session uses more servers. Each
newly used server brings its `list_tools` output into context; GitHub's
output alone is 1,638 tokens.

With `search_tools`, a session that finds its tools pays about 150 tokens
per new tool instead of a whole server's list, and saves more (Morning
Sport, Dev Workflow). A miss is expensive: the modeled fallback reads the
whole `list_tools` output on top of the search. Full Day misses 3 of its 7
distinct tools (a README read, a Jira search phrased "find my tickets in
progress", a Slack post phrased "status update"), so it ends up above the
`list_tools` flow. `search_tools` also adds more extra turns than
`list_tools`: one per distinct tool rather than one per server.

### 4.6 Latency, throughput and memory

| Measurement | Value |
|---|---:|
| Paced sequential `invoke_tool`, 200 unique calls: p50 / p95 / p99 via proxy | 0.73 / 1.04 / 1.42 ms |
| Same calls direct to the mock: p50 / p95 / p99 | 0.17 / 0.30 / 0.38 ms |
| Proxy overhead (proxy − direct): p50 / p95 / p99 | 0.56 / 0.74 / 1.04 ms |
| 500-call pipelined burst over 5 servers: wall / throughput / errors | 51 ms / 9,723 req/s / 0 |
| 50 parallel calls to a 100 ms tool on one server (default `max_in_flight` 32, so two waves): wall | 205 ms |
| 5 MB tool response: via proxy / direct | 581 ms / 51 ms |
| Proxy RSS: idle (5 servers warm) / after the burst | 20.4 MiB / 22.5 MiB |
| Binary size (linux/amd64, `-trimpath -ldflags="-s -w"`) | 16.4 MiB |

The 5 MB relay costs about 0.5 s more through the proxy than directly.
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
| Session savings 81.5–93.7% | `SessionReplay_*` charged LeanProxy the router plus one ~26-token stub per prompt. It ignored the `list_tools` output and the extra turns, and its timing loop was empty. | **−70.9% to −86.0%** with `list_tools` (3–4 extra turns), **−65.0% to −93.5%** with `search_tools` (4–10 extra turns), discovery outputs counted |
| Per-server savings 79.0–97.9% (GitHub 41 tools = 4,570 tokens, ...) | Synthetic `tools/list` payloads padded with dots to a byte count from a seeded snapshot | Real catalog payloads: router vs native **−50.3% to −92.8%** per server, **−96.8%** for all 5 |
| Proxy overhead ~12 µs/op (p50) | `BenchmarkProxyOverhead_NFR1`: two `json.Unmarshal` calls on literals, in process | **0.56 ms p50 / 0.74 ms p95** through the binary, measured against a direct baseline |
| Throughput ~25,000 q/s | `BenchmarkThroughput_MockMCP`: an in-process mock, with no proxy involved | **9,723 req/s** for a 500-call pipelined burst through the binary (one run; varies by host) |
| 50 MB payload estimate ~7 ms | Token estimator over a 50 MB buffer; no relay | **5 MB relayed in 581 ms** (estimator benchmark renamed `BenchmarkEstimateTokens_50MB`) |
| Binary 15.8 MB (darwin-arm64) | `dist/` build on the maintainer's machine | **16.4 MiB** (linux/amd64, harness build) |

## 7. Response governor (large results)

The response governor (#319, `response:` block, off by default) caps each
tool result at a token budget and keeps the full result for `read_result`.
The harness measures it with `catalogmcp --large-results`, where five tools
answer with realistic large results:

| Call | Result |
|---|---|
| `github.get_file_contents` | a ~200 KB Go source file |
| `github.list_issues` | 300 issues with user objects, URLs, labels and bodies |
| `github.search_code` | 400 code-search hits with text matches |
| `jira.jira_search` | 250 issues with custom fields |
| `postgres.pg_query` | 1,500 rows |

The same calls go through two proxies: the default config (governor off)
and `response: {enabled: true, max_tokens: 4000}`. Tokens are those of the
whole JSON-RPC response line. The session row applies the session model of
§3: each result enters the context once and is re-read at 0.25× on every
later turn.

| Call | Governor off | Governor on | Savings |
|---|---:|---:|---:|
| `github.get_file_contents` | 54,850 | 3,445 | −93.7% |
| `github.list_issues` | 57,949 | 3,748 | −93.5% |
| `github.search_code` | 41,635 | 3,784 | −90.9% |
| `jira.jira_search` | 35,069 | 3,739 | −89.3% |
| `postgres.pg_query` | 45,197 | 3,799 | −91.6% |
| **Total (each result once)** | **234,700** | **18,515** | **−92.1%** |
| **Session (5 turns)** | **362,597** | **27,598** | **−92.4%** |

The harness also asserts that:

- every governed response line stays within `max_tokens` (largest: 3,799);
- `read_result` pages the file back (13 pages of 4,000 tokens) and the
  concatenation is byte-identical to the ungoverned result;
- the 16 normal-size calls of the replayed sessions are byte-identical with
  the governor on (a result under budget is not even parsed), and the paced
  `invoke_tool` latency is unchanged within noise (p50 0.54 ms off, 0.65 ms
  on, same run; the stage costs about 3 µs per call in
  `BenchmarkGovernor_SmallResult`).

The savings are an upper bound for what the model keeps in context: a
model that needs the omitted part pays for the `read_result` pages it
reads (the file above is 13 pages).

## 8. Field projection (large results)

Field projection (#320, `response.projections` and
`response.default_projections`, off by default) drops JSON fields the model
does not need before the budget is applied. The harness measures it on the
same large-results calls, with the `github.*` drop pack of the issue
(`**.node_id`, `**.*_url`, `**.url`, `**.reactions`, `**.avatar_url`,
`**.gravatar_id`) and the built-in default pack for the other servers,
through three proxies: projection only (`max_tokens: 0`), truncation only
(the §7 row) and projection then truncation (`max_tokens: 4000`). "Items"
is the number of list elements the model sees within the budget.

| Call | Off | Projection only | Truncation only (items) | Projection + truncation (items) |
|---|---:|---:|---:|---:|
| `github.get_file_contents` | 54,850 | 54,850 (not JSON) | 3,445 | 3,445 |
| `github.list_issues` | 57,949 | 41,020 (−29.2%) | 3,748 (19) | 3,719 (26) |
| `github.search_code` | 41,635 | 33,303 (−20.0%) | 3,784 (36) | 3,715 (43) |
| `jira.jira_search` | 35,069 | 31,350 (−10.6%) | 3,739 (27) | 3,670 (29) |
| `postgres.pg_query` | 45,197 | 45,197 (nothing to drop) | 3,799 (126) | 3,799 (126) |
| **Total** | **234,700** | **205,720 (−12.3%)** | **18,515** | **18,348** |

- On the three noisy listings, projection alone saves **21.5%**
  (134,653 → 105,673 tokens); the file and the SQL rows have nothing to
  drop and come back unchanged.
- On top of truncation, the budget is the same, so the token totals barely
  move (the projection note and its link take part of the budget); what
  changes is how much of the list fits: 26 issues instead of 19, 43 code
  hits instead of 36.
- With the model's `fields` argument (`[].number`, `[].title`, `[].state`,
  `[].labels[].name`, `[].user.login`, `[].updated_at`), `list_issues`
  shows **64 issues** in 3,738 tokens, against 19 with truncation only.
- The catalog fixtures carry little noise. On a realistic GitHub
  `list_issues` response (`pkg/mcp/governor/testdata/github_list_issues.json`,
  30 issues with the REST API's full user objects, URL templates and
  reactions), the same drop pack saves **72.2%** (29,536 → 8,225 tokens),
  and the default pack alone 59.6%
  (`TestProjection_GitHubDropPackOnListIssues`).
- `read_result` on the projection's result id returns the full, redacted
  result with the dropped fields (harness assertion).

Cost: projection works on the bytes, without decoding values. Projecting
the 118 KB fixture takes about 1.6 ms (`BenchmarkProjection_DropPack`), and
the governor stage with projection only about 1 ms on a 30-issue result
(`BenchmarkGovernor_ProjectionOnly`). Per-call latency in the harness is
within noise of truncation only (for example `github.list_issues`: 15.9 ms
truncation only, 19.1 ms projection then truncation, 22.0 ms projection
only, which returns a 164 KB line).

## 9. Known limits and follow-ups

- **`search_tools`.** The audit prototype estimated about −92% for Full
  Day; the harness measures −65.0%, because 3 of Full Day's 7 queries miss
  the top 5 and pay the `list_tools` fallback. Hybrid mode is not measured
  (the harness has no network and no embedder), and the fallback model is
  a conservative guess: a model might retry the search instead.
- **Extra-turn cost.** The extra turns are counted but not costed. Costing
  them needs a model of what the model re-sends on a discovery turn.
- **Transports.** Only the stdio front end and stdio upstreams are measured.
  `serve`, HTTP and SSE are not.
- **Catalog.** It is realistic but synthetic. Real servers often have
  larger schemas, with enums, nested objects and long descriptions. That
  raises both the native cost and the `list_tools` cost.
