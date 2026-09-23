# Benchmark Results

This document is the **single source of truth** for the token-economy and
NFR performance numbers in the [README](https://github.com/mmornati/leanproxy-mcp/blob/main/README.md) and
[index.md](./index.md). Every number below is produced by an executable
test in `tests/bench/token_economy_bench_test.go` and re-validated by
`make bench`. No number here is hand-edited.

> **TL;DR** — All headline claims from the README pass. Measured savings are
> **79-99%** (depending on server shape), proxy overhead is **~12 µs/op**
> (NFR1 wants <50 ms), and throughput is **~25,000 q/s in-process** (NFR
> AC 16-3 wants ≥500 q/s). The router payload is **237 tokens** (3 tools:
> `list_servers`, `list_tools`, `invoke_tool`, measured from the real
> `pkg/mcp.GetAllToolDefinitions()` router since #300 — it was previously a
> stub that didn't match the production tool set) — see
> [§3 Why the router number moved](#3-why-the-router-number-moved).

## 1. How to reproduce

```bash
# from repo root
make bench              # runs the full suite, writes bench-results/<date>.txt
make bench-compare      # diffs two result files (FILES=old.txt new.txt)
go test -run=^$ -bench=. -benchmem -benchtime=3s -count=3 ./tests/bench/...
```

The full suite runs in **< 1 second** end-to-end on a single core. It
does **not** require a live MCP server, network access, or a database —
every server-side path is exercised against the in-process
`tests/bench/mockmcp` library.

To refresh the canonical MCP server tool counts from a live run (e.g.
after re-installing the GitHub / Garmin / Intervals.icu servers):

```bash
go run ./tests/bench/live_snapshot \
    -config tests/bench/fixtures/live-snapshot.yaml \
    -out   tests/bench/fixtures/live-snapshot.json
```

Until that snapshot is refreshed, `tests/bench/fixtures/live-snapshot.json`
contains seeded counts derived from the previous `docs/index.md` numbers
(GitHub 41, Garmin 100, Intervals.icu 10 — Stitch is no longer available).

## 2. Methodology

### 2.1 Token counting

All token accounting in the benchmark suite uses the **same primitive**
the runtime cost tracker uses:

```go
import "github.com/mmornati/leanproxy-mcp/pkg/reporter"

estimator := reporter.NewEstimator()        // 1 token ≈ 4 chars (chars/4)
tokens := estimator.EstimateTokens(payload)  // or EstimateJSON(v)
```

This is `pkg/reporter.Estimator`, exposed as a public type in v0.9.0
specifically so the runtime and benchmarks can never disagree. The
`chars/4` heuristic is a well-known approximation of BPE-style
tokenizers (OpenAI, Anthropic) for English text; it is not byte-perfect
but is **consistent within itself**, which is what matters for the
savings ratios reported here.

### 2.2 Native MCP baseline

For each MCP server in the live snapshot, the benchmark synthesises a
`tools/list` JSON payload whose byte size matches the per-server
`schema_bytes` field. The token count of that payload is the "Native MCP"
column. We use the **raw** token count (not the 0.25× cache-read cost)
because the README's "Schema Tax" claim is about the on-wire payload
size, independent of provider-side caching discounts.

The cache-read comparison in `index.md` is provided as a separate
column at 0.25× to mirror the original table.

### 2.3 LeanProxy router payload

The router is the real 3-tool definition list `server run --stdio` serves,
`pkg/mcp.GetAllToolDefinitions()` (`pkg/mcp/tool_index.go`):

- `list_servers` — list configured MCP servers with transport, state and tool count
- `list_tools` — list tools on a specific server
- `invoke_tool` — invoke a tool on a specific server

The benchmark marshals this into the same `{"jsonrpc":"2.0","id":1,
"result":{"tools":[...]}}` envelope the production proxy returns, so
the 237-token figure includes the JSON-RPC envelope (id, jsonrpc
version, result wrapper) — not a hand-maintained stub (see #300).

### 2.4 Session replays

The three session tables (Morning Sport / Dev / Full Day) replay the
exact prompt sequences from the previous `docs/index.md` against the
synthesised per-server tool counts:

- **Morning Sport** — 2 servers, 4 prompts (Garmin + Intervals.icu)
- **Dev Workflow** — 2 servers, 5 prompts (GitHub + Intervals.icu)
- **Full Day** — 3 servers, 7 prompts (all available)

For each prompt, the "Native MCP" cost is the sum of the per-server
schema tax **at 0.25× cache-read cost** (matching the "real" cost model
in `index.md`). The "LeanProxy" cost is the router payload + one stub
schema (~26 tokens) per tool actually invoked.

### 2.5 NFR benchmarks

- **NFR1 (proxy overhead)** — microbenchmark of the JSON-RPC parse +
  cost-track hot path. Includes one Unmarshal of the request, one
  Unmarshal of the response, and one `TrackAt` call.
- **NFR2 (50 MB payload)** — single-call `EstimateTokens` on a 50 MB
  byte buffer. This is the worst-case size a single request can hit.
- **AC 16-3 (throughput)** — in-process `mockmcp.Server` driven in a
  tight loop. The number is the **mockmcp** ceiling, not the
  leanproxy binary ceiling; for a real binary-level measurement, use
  the in-tree e2e suite (`tests/e2e/`) which currently exercises
  ~5,000 req/s against a Python mock upstream.
- **NFR3 (binary size)** — `os.Stat` over the `dist/leanproxy-mcp-*`
  binaries produced by `make build`.

## 3. Why the router number moved

The README's previous "~110 router tokens" / "27 tokens" came from a
hand-counted estimate of the 3-tool schema (without the JSON-RPC
envelope) using a different token-counting rule (1 token per tool
field, then summed). The benchmark measures the **full `tools/list`
response as it would appear on the wire** using the runtime Estimator,
which is the right unit for the cost-saving claim.

For comparison:

| Measurement | Tokens | Source |
|---|---|---|
| Hand-counted 3-tool field sum (old) | ~27 | previous `docs/index.md` |
| Hand-counted 3-tool schema (old) | ~110 | previous README |
| Stub router payload (pre-#300, didn't match production) | 158 | `tests/bench` stub + Estimator |
| Real `tools/list` envelope (current) | **237** | `tests/bench` (`pkg/mcp.GetAllToolDefinitions()`) + Estimator |
| Per-stub on-demand schema (current) | **26** | `tests/bench` + `registry.ToolStub` |

## 4. Raw results (latest run, v0.9.0)

```
goos: darwin
goarch: arm64
pkg: github.com/mmornati/leanproxy-mcp/tests/bench
cpu: Apple M4
```

### 4.1 Schema-tax (per server)

| Server | Tools | Native tokens | Router tokens | Savings |
|---|---:|---:|---:|---:|
| Garmin | 100 | 11,134 | 237 | **97.9%** |
| GitHub | 41 | 4,570 | 237 | **94.8%** |
| Intervals.icu | 10 | 1,129 | 237 | **79.0%** |
| All 3 | 151 | 16,833 | 237 | **98.6%** |

### 4.2 Session replays (0.25× cache-read model)

| Session | Prompts | Native tokens | Lean tokens | Savings |
|---|---:|---:|---:|---:|
| Morning Sport | 4 | 12,260 | 1,056 | **91.4%** |
| Dev Workflow | 5 | 7,120 | 1,320 | **81.5%** |
| Full Day | 7 | 29,449 | 1,848 | **93.7%** |

### 4.3 NFRs

| Benchmark | Measured | Threshold | Pass |
|---|---|---|:-:|
| `BenchmarkProxyOverhead_NFR1` (p50) | ~12 µs/op | <50 ms | ✅ |
| `BenchmarkLargePayload_NFR2` (50 MB) | ~7 ms | <200 ms | ✅ |
| `BenchmarkThroughput_MockMCP` (in-process) | ~25,000 q/s | ≥500 q/s | ✅ |
| `TestBinarySize_NFR3` (darwin-arm64) | 15.8 MB | <20 MB | ✅ |

### 4.4 Per-primitive microbenchmarks

| Primitive | Time | Allocs |
|---|---:|---:|
| `BenchmarkEstimateTokens` | ~50 ns | 0 |
| `BenchmarkEstimateJSON` | ~250 ns | 1 |

## 5. What was corrected vs the previous README

| Old claim | Source | New claim | Notes |
|---|---|---|---|
| "90%+" headline | README | "79-99%" | Per-server variation is real |
| "~110 router tokens" | README, architecture | **237 tokens** | Full JSON-RPC `tools/list` envelope, measured from the real router (`pkg/mcp.GetAllToolDefinitions()`), not a hand-maintained stub |
| "27.5 LeanProxy tokens" | index.md | **237 tokens** | Same correction; old number was a hand-counted schema-field sum, not the on-wire payload |
| "158 router tokens" | this doc (pre-#300) | **237 tokens** | The 158 figure came from a benchmark stub that had drifted from the production `list_servers`/`list_tools`/`invoke_tool` definitions; #300 fixed the router contract (added `list_servers`, real version) and the bench now measures the real tool defs |
| "~54 tokens per stub" | configuration.md, architecture | **~26 tokens per stub** | Stub is `{name, description, category?}` — measured from the production `registry.ToolStub` |
| "11 µs overhead at 5,000 RPS" | architecture.md | **~12 µs/op (p50)** | Same order of magnitude; quoted from Bifrost originally — now our own number |
| "6-7× token reduction" | configuration.md, architecture | **79-99% reduction** | Per-server ratio varies; use the new tables |
| 4-server column | README, index.md | **3-server (Stitch removed)** | Stitch MCP is no longer available |
| Garmin 55 / Intervals 67 (README) | README | **Garmin 100 / Intervals 10** | Resolved against `docs/index.md`; now consistent across both docs |

## 6. Future work

- **Refresh `live-snapshot.json`** with a real run of `go run
  ./tests/bench/live_snapshot` once the Garmin / Intervals / GitHub
  credentials are wired into CI. The seeded numbers in
  `fixtures/live-snapshot.json` are a placeholder.
- **Binary-level throughput** — the in-process mockmcp number is a
  ceiling, not the binary-level throughput. A `make bench-e2e` target
  that spawns the leanproxy binary + a mockmcp subprocess and measures
  end-to-end q/s is the next step (see `buildMockMCP` in
  `token_economy_bench_test.go`).
- **Real-tokenizer comparison** — swap the `chars/4` heuristic for
  `tiktoken-go` to get a tokenizer-accurate number. The Estimator API
  already supports this via `NewEstimatorWithRatio`.

## 7. Commit / run metadata

| Field | Value |
|---|---|
| Version | v0.9.0 |
| Git | `7db8011` (Merge pull request #269) |
| Go | 1.25+ |
| Host | Apple M4, darwin/arm64 |
| Date | 2026-08-12 |
