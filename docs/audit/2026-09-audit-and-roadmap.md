# LeanProxy-MCP — Full Audit & Next-Version Roadmap

*Audit date: 2026-09-22 · Baseline: `main` @ `baa2a11` (v0.9.2)*

This document covers four things: where the project stands against the market, a security audit, a performance and reliability audit, and a prioritised roadmap. Every finding was checked either by running the real binary end to end or by reading the code at the cited line. The experiments can be re-run from [`experiments/`](experiments/).

---

## 0. TL;DR

1. **The product people install is not the product the README describes.**
   - The README tells IDEs to run `server run --stdio`. That mode has:
     - no redaction;
     - no injection filter;
     - no cache, no model routing, no dashboard.
   - It tells the model to call a `list_servers` tool that does not exist.
   - It exposes 2 tools, not the advertised 3, and they cost 342 tokens, not 158.
   - All the "Token Firewall" features are wired only into `serve`. That mode is not MCP-compliant (`initialize` and `tools/list` return *Method not found*), so no IDE can use it directly.
   - **Fixing this gap is priority #1. It matters more than any new feature.**

2. **Reliability limits make the proxy fail under normal agent workloads.**
   - A hard-coded **10 req/s limit per server rejects** calls instead of queuing them. A burst of 500 calls: 463 rejected.
   - **One request at a time per stdio server.** 50 calls to a 100 ms tool: 5.05 s instead of 0.1 s.
   - **A single tool response over 1 MB permanently wedges that server** until the proxy restarts.
   - Upstream JSON-RPC errors are **dropped** and sent to the client as an invalid, empty response.

3. **The published benchmark overstates savings.**
   - `SessionReplay` charges LeanProxy only "router + 54 tokens" per prompt. The real flow also pays for the `list_tools(server)` dump, 281–1,637 tokens per server in our catalog.
   - Measured on a realistic 5-server, 118-tool catalog:
     - **~73% saving** today, against the claimed 94–96%;
     - **~92%** with a ranked cross-server `search_tools`, which we prototyped.

4. **Honest market verdict.**
   - The "schema tax" is now **solved natively** for the mainstream clients:
     - Anthropic tool search with `defer_loading`;
     - Claude Code tool search, on by default;
     - OpenAI `tool_search`;
     - Cursor dynamic context.
   - The 3-tool router pattern is a commodity. At least 8 projects ship it, two of them in Go (MCPProxy, ToolHive vMCP).
   - **For JIT schema loading alone, the market limit has been reached.** There is still real, under-served room in three areas:
     - response-side token reduction;
     - a local-first MCP security layer (redaction, tool pinning, rug-pull detection);
     - search that works with local and open models that lack native tool search.
   - The roadmap below re-centres the product on those three areas.

---

## 1. Method

| What | How | Result |
|---|---|---|
| Build | `go build .` (Go 1.25.5) | OK. Binary 24.5 MB, 16.9 MB stripped |
| Unit tests | `go test -race ./...` | 38 packages pass. 5 tests in `statusfile`/`toolstore` fail **only when run as root**, because they assume `/invalid/...` cannot be created |
| Lint / SAST | `gosec` with **no** CI exclusions | 109 raw issues: 82 × G104 unhandled errors, 15 × G304, 4 × G115, 4 × G101 (test fixtures). CI excludes G104/G204/G304/G115/G703 globally, and `.golangci.yml` disables errcheck/gosec/unused on ~30 core files |
| Vuln scan | `govulncheck` | Could not reach vuln.go.dev from the sandbox. **Must be added to CI** |
| End-to-end | Real binary against a 5-server, 118-tool mock catalog (GitHub, Jira/Confluence, Slack, Garmin, Postgres), driven over stdio JSON-RPC | Token cost, latency, burst, redaction and large-payload results below |
| Tool-search prototype | 44 natural-language intents with a gold tool each, comparing the current matcher, BM25, and BM25 with synonyms | See §4.3 |
| Code audits | Security and performance/architecture reviews, each finding with a PoC or a line reference | §3, §4 |
| Market | Web research on 20+ gateways and the MCP spec changelogs | §2 |

---

## 2. Market comparison

### 2.1 Landscape (condensed)

| Product | Model | Lazy tools | Security | Notes |
|---|---|---|---|---|
| **MCPProxy** (Go, MIT) | Local binary + tray | BM25 search | Quarantine of new servers, tool-poisoning checks, redaction, Docker isolation, OAuth | **Closest direct competitor** |
| **ToolHive vMCP / Optimizer** (Stacklok) | Local CLI or K8s | `find_tool` (hybrid semantic + keyword) + `call_tool` | Container isolation, OWASP mapping | Almost the same design as LeanProxy |
| **Docker MCP Gateway** | Docker CLI plugin (Go) | `mcp-find` / `mcp-add`, code-mode | `--block-secrets`, `--verify-signatures`, sandboxing, interceptors | OpenTelemetry |
| **LiteLLM** | Self-hosted | MCP tool search (3 virtual tools) + semantic filter | OAuth on-behalf-of, guardrails | **Overlaps budgets and model routing** |
| **1MCP** | Node | Lazy mode (`tool_list` / `tool_schema` / `tool_invoke`) | – | Same 3-tool pattern |
| **IBM ContextForge** | Python, GA 1.0 | Virtual servers | 40+ plugins, Rust PII filter | Federation |
| **Cloudflare MCP Portals** | SaaS | Code Mode (`search` + `execute`) | Zero-Trust | – |
| **agentgateway** (Linux Foundation), **Envoy AI GW**, **Kong**, **Pomerium** | Infra gateways | Filtering | OAuth 2.1, CEL/per-tool authz, downscoped tokens | Enterprise and K8s |
| **Lasso**, **Snyk Agent Scan** | Security proxies | – | Presidio PII, injection detection, **tool hash-pinning (rug pull)**, reputation | – |
| **Native clients** | Anthropic API, Claude Code, OpenAI, Cursor | Built-in `defer_loading` / tool search | – | **Removes the headline problem for these clients** |

Full list: https://github.com/e2b-dev/awesome-mcp-gateways

### 2.2 Spec compliance gap

LeanProxy hard-codes `protocolVersion: "2024-11-05"` everywhere: `pkg/mcp/handlers.go:185,282`, `pkg/pool/reconnect.go:74`, `pkg/connpool/pool.go:379`, `cmd/server.go:804`. It also reports `serverInfo.version: "1.0.0"` regardless of the release.

Since 2024-11-05 the spec has added:
- **2025-06-18:** `outputSchema` / `structuredContent`, elicitation, resource links, OAuth resource-server with RFC 8707, and an explicit ban on token passthrough.
- **2025-11-25:** Tasks, URL-mode elicitation, icons, and CIMD.
- **2026-07-28**, per the research (verify before planning against it): a stateless core, `Mcp-Method` / `Mcp-Name` headers, `input_required` results (MRTR), list `ttlMs`, and deprecation of SSE, Roots, Sampling and Logging.

The `list_tools` text dump in the router drops these per-tool fields:
- `outputSchema`;
- `annotations` (`readOnlyHint`, `destructiveHint`, ...);
- icons;
- MCP Apps UI resources.

**Clients lose information by going through the proxy.**

### 2.3 Where LeanProxy still has a defensible angle

| Angle | Crowded? | Why it is open |
|---|---|---|
| Response-side token reduction: projection, truncation plus spill-to-resource, summarisation via a local LLM, dedup | **Open** | Native clients only cap or truncate. Gateways rarely touch results |
| Local-first, air-gapped security layer: redaction, tool pinning, rug-pull diffs, description scanning | Only MCPProxy is close | Enterprise gateways are K8s or SaaS |
| Search for open and local models (Ollama, llama.cpp, other agents) | Partly | These clients have no `defer_loading` |
| Auditable cost accounting across schema, response and cache | Open for local tools | LiteLLM covers the LLM side, not MCP |
| 3-tool router alone, model routing, team budgets, bundled GitHub/Postgres/Redis servers | **Commodity** | LiteLLM, vendors, and official servers already cover these |

---

## 3. Security findings

Severity uses a CVSS-style judgement for a local developer tool. The Confidence column is one of:
- **PoC**: reproduced by running code;
- **Read**: confirmed by reading the code.

| ID | Sev | Finding | Evidence | Conf. |
|---|---|---|---|---|
| **S1** | **Critical** (product integrity) | **The recommended `server run --stdio` mode redacts nothing** in either direction, and runs no injection classifier. AWS, GitHub and Stripe keys from an upstream response reach the client verbatim. The advertised Token Firewall only exists in `serve` | `cmd/server.go:636-692` → `pkg/mcp/handlers.go`, which never calls `bouncer`. Reproduced with `experiments/redact_check.py` | PoC ×2 |
| **S2** | High | **The `serve` TCP port has no authentication and can be driven from a browser** (cross-protocol). A `text/plain` POST from any web page reaches `127.0.0.1:8080`. HTTP header lines produce parse errors and are skipped, then the JSON body line is **executed** (for example `write_file` or `postgresql_execute`). Lines have no size cap, and each line gets its own goroutine | `cmd/serve.go:389, 525-560` | PoC |
| S3 | Medium | **The response cache is on by default in `serve` for every `tools/call`** (24 h TTL, no size bound). A repeated `create_issue` returns the cached result, so the second action silently never happens. The key is computed **after** redaction, so two different credentials share one cache entry, which leaks data across credentials | `serve.go:309, 1165-1206`, `pkg/cache/semantic_cache.go:19` | PoC |
| S4 | Medium | **The redactor misses common secret formats.** Examples: `ASIA…` / AWS secret keys, OpenAI `sk-proj-…`, Google `AIza…`, DSNs (`postgres://u:p@h`), JWTs without `Bearer`, PGP blocks, `sk_test_`, and secrets in JSON nested inside `content[].text`, which is how MCP returns data. Numeric values and object keys are skipped | `pkg/bouncer/allowlist.go:19-115`, `redactor.go:537-575` | PoC |
| S5 | Medium | **The injection classifier is easy to evade.** It regex-matches raw JSON, so ` ` escapes defeat it (score 90 → 0). It checks only requests, not tool **responses**, which is where indirect injection comes from. The "redact" action writes invalid JSON params | `serve.go:1075-1112`, `injection/actions.go:173` | PoC |
| S6 | Medium | **Marketplace install has no integrity check.** The trust score is whatever the feed claims, and an entry with no signals scores **100**. There is no checksum, signature or pin. The feed's `command` and `args` are written with `enabled: true` and never shown to the user | `pkg/registry/trust.go:36-43`, `pkg/migrate/installer.go:347-370`, `feed.go:21` (confirm who owns `registry.mcp.io`) | Read |
| S7 | Medium | **Every child server inherits the proxy's full environment** (`os.Environ()`), including `OPENAI_API_KEY`, cloud credentials and more, even for third-party marketplace servers | `pkg/pool/server.go:286` | Read |
| S8 | Medium | **Upstream tool descriptions reach the model unchecked.** There is no hash pinning or drift alert, so tool poisoning and rug pulls (OWASP MCP03) go undetected | `handlers.go` `list_tools` path | Read |
| S9 | Low-Med | **Dashboard access control.** With no token set it has no authentication, even on `0.0.0.0`. Loopback always bypasses the token. The `Host` header is not checked, so DNS rebinding can read server names, tool names and prompt hashes. `/metrics` has no authentication | `pkg/dashboard/auth.go:15-22` | PoC (Host) |
| S10 | Low-Med | **Sidecar-LLM redaction output replaces the request params wholesale.** A prompt planted in the arguments can steer the model to swap tool or arguments | `redactor.go:596-613`, `serve.go:1151` | Plausible |
| S11 | Low | **Secrets in logs.** Unparseable request lines are logged in full at Warn. Params and upstream stdout are logged at Debug without redaction | `cmd/server.go:666`, `handlers.go:318`, `pool/server.go:604` | Read |
| S12 | Low | **Postgres "read-only" query tool** can be bypassed with `EXPLAIN ANALYZE DELETE …` or `SELECT … INTO`. There is no real read-only mode (`SET TRANSACTION READ ONLY`) | `pkg/postgresql/tools.go:229` | Read |
| S13 | Low | **Redis client.** A failed redial leaks a pool slot, which can deadlock while `c.mu` is held. Server-sent RESP lengths are trusted, which allows huge allocations. There are no read deadlines | `pkg/redistools/tools.go:245-337` | Read |
| S14 | Low | **Unlisted tools can be called:** `srv.<anything>` is forwarded. There is no per-tool allow or deny list | `router.go:88-95` | Read |
| S15 | Low | **Dead but unsafe code.** `pkg/proxy/socket` compares the token with `==`, `pkg/proxy/http.go` binds `:8080` on all interfaces with no auth, and `pkg/federation` allows plain HTTP. None of these is wired in yet | – | Read |
| S16 | Info | **Mach-O binaries `github`, `postgres` and `redis` (35 MB) are committed at the repo root.** They were built from a `+dirty` tree containing `/Users/...` paths, cannot be reproduced, and bloat every clone | commits `8a4e002`, `b5514f5` | Read |
| S17 | Info | **The lint configuration hides problems.** errcheck, gosec and unused are disabled on ~30 core files, and gosec runs in CI with 5 rule exclusions. There is no govulncheck or dependency review in CI | `.golangci.yml`, `.github/workflows/lint.yml` | Read |

**Strengths to keep:**
- `filesystemtools` uses `os.Root`, which blocks traversal and symlink escape.
- `html/template` everywhere; constant-time comparison for the dashboard token.
- Loopback binds by default.
- Files 0600 and directories 0700, with atomic writes.
- No `InsecureSkipVerify`.
- RE2 regexes with a ReDoS validator.
- Parameterised Postgres queries.
- Batch cap of 100.
- The streaming redactor zeroes its buffers.

---

## 4. Performance and reliability findings

### 4.1 Measured end to end

The latency and burst figures come from a real binary against the mock catalog. The large-response and cold-start figures come from the performance audit's own mock servers.

| Scenario | Direct | Via LeanProxy (`server run --stdio`) |
|---|---|---|
| Small call, paced, p50 / p95 / p99 | 0.05–0.5 ms | **0.91 / 1.21 / 1.60 ms**. The overhead is fine |
| 500 pipelined calls across 5 servers | 0 errors | **463 rejected**: "rate limit exceeded" |
| 50 parallel calls to a 100 ms tool | 101 ms | **5.05 s**: one request at a time per server |
| 1.5 MB tool response | OK | **Times out, and that server stays dead** for every later call (reproduced: 3/3 calls time out; other servers keep working) |
| 20 MB response via `serve` with an HTTP backend | – | 5.3 s, and peak RSS **246 MB that is never released** (cache) |
| Cold start: one server hung, one slow | – | First `list_tools` takes **10 s** and blocks everything |
| Idle RSS | – | ~20 MB |

### 4.2 Findings

| ID | Sev | Finding | Location | Fix |
|---|---|---|---|---|
| **P1** | **High** | **Hard-coded 10 req/s limit per server** that rejects instead of waiting. Health pings consume the budget too | `pkg/pool/pool.go:209, 324` | Remove it, or make it configurable per server with wait-until-deadline semantics |
| **P2** | **High** | **A stdio pipe carries one request at a time.** A slow call blocks every other call to that server | `pkg/pool/server.go:770` | Multiplex: a writer goroutine plus a map from wire ID to response channel. The wire IDs already exist |
| **P3** | **High** | **The `server run --stdio` front end is serial.** One slow tool blocks the IDE's pings and cancellations | `cmd/server.go:677` | Dispatch each request on its own goroutine, with a mutex-protected writer and a concurrency cap |
| **P4** | **High** | **A response over 1 MB kills the reader permanently.** The limit is not configurable. The error branch tests the wrong sentinel (`ErrBufferFull` instead of `ErrTooLong`) inside the `Scan()==true` branch, so it never runs | `pkg/pool/server.go:167, 585-600` | Use `ReadBytes` or a configurable limit (e.g. 64 MB). When the reader dies, fail pending calls and respawn the server |
| **P5** | **High** | **Stderr is read with a 64 KB `Scanner`.** One long log line stops draining, and the child process blocks on write | `pkg/pool/server.go:636` | Drain continuously and truncate long lines |
| **P6** | **High** | **Upstream errors are dropped.** Stdio mode returns `{"jsonrpc","id"}` with neither result nor error, which is invalid JSON-RPC. Serve mode falls through to the next pool on error, so every error reads "sse_pool: server not found" | `handlers.go:385, 852`, `http_pool.go:633` | Pass `resp.Error` through, and route by the configured transport |
| P7 | Medium | **Redaction round-trips JSON through `interface{}`.** Throughput is ~15 MB/s with 16× allocation amplification. It **corrupts data**: large integers lose precision, key order changes, and HTML is escaped. `RedactStream`, which runs at ~300 MB/s, is already written but unused | `pkg/bouncer/redactor.go:507` | Use `RedactStream`, or a token-level pass with `UseNumber` |
| P8 | Medium | **Debug log arguments are built even when debug is off.** Removing them took an 840 KB relay from 132 ms to 50 ms. Responses are also unmarshalled twice | `pool/server.go:604, 892`, `handlers.go:318` | Guard with `logger.Enabled`; decode once |
| P9 | Medium | **Tool-cache refresh is synchronous across all servers** and happens on the request path, with `time.Sleep` backoff | `handlers.go:472, 604`, `serve.go:383` | Refresh per server with singleflight, serve from the persisted toolstore, refresh in the background |
| P10 | Medium | **No timeouts on HTTP/SSE calls**, so a hung remote freezes the stdio front end | `http_pool.go:315`, `sse_pool.go:306` | Wrap calls in `context.WithTimeout` |
| P11 | Medium | **Dead pool controls.** The circuit breaker is never fed, `WorkerPool` is never used, `currentLoad` is always 0, maps are read without their lock, and requests that already timed out are still executed later | `pool.go:148, 210, 320` | Wire them in or delete them |
| P12 | Medium | **Process-group handling is missing.** `Setpgid` is never set, so `npx`/`uvx` grandchildren are orphaned. `Close` stops servers one at a time while holding the lock | `pool/server.go:296`, `pool.go:350` | Set `Setpgid`, kill the whole group, close servers in parallel |
| P13 | Medium | **Serve never re-initialises a restarted server.** Stdio sends a second `initialize` | `serve.go` `forwardableRequest`; `PopulateToolCache` | Move the handshake into the pool, once per process generation |
| P14 | Low | **`parseToolName` splits on the first `_`**, so a server named `my_srv` cannot be reached | `handlers.go:972` | Use the server registry to split |
| P15 | Low | **`invoke_tool` re-marshals arguments through `map[string]interface{}`**, losing float precision | `handlers.go:733-745` | Pass the arguments through as `json.RawMessage` |

### 4.3 Tool discovery and the honesty of the token numbers

- **No ranked search exists.**
  - The flow is `list_tools(server)`, which dumps every tool on that server, then `invoke_tool`.
  - `searchToolCache` is dead code. `gateway.SearchTools` matches substrings of the tool name only.
- **`list_servers` is referenced in both tool descriptions and in error hints, but is not exposed** in stdio mode. Calling it fails.
- **Prototype result** (`experiments/search_eval.py`, 44 intents, 118 tools):

| Strategy | Recall@1 | Recall@5 | Tokens per lookup |
|---|---|---|---|
| Existing AND-substring matcher (`matchesQuery`) | 0% | 0% | – |
| **BM25** over name, description and parameter names | **75%** | **91%** | **~100** |
| BM25 with a small synonym table | 77% | 91% | ~105 |
| Current flow: `list_tools(server)` dump | n/a (the model must first guess the server) | – | **~885** |

  *Caveat:* the intents were written by the auditor, so there is some bias. The size of the effect is still clear.

- **Session model** (7 prompts over 3 of the 5 servers, Anthropic cache reads at 0.25×):

| | Native (all schemas) | LeanProxy today | LeanProxy with `search_tools` |
|---|---|---|---|
| Tokens | 26,682 | 7,263 (**−73%**) | 2,111 (**−92%**) |

  - The README says −94% to −96%.
  - The bench also does not count the **extra LLM round-trips** that `list_tools` forces. Each extra turn re-sends the conversation.
  - The `tests/bench` NFR benchmarks measure `json.Unmarshal` of literals and an in-process mock, not the proxy.
  - The `SessionReplay` timing loop is empty (0.72 ns/op).

### 4.4 Architecture and code health

- **The two front ends have diverged. This is the root cause of S1 and P3.**
  - `serve` has the middleware but is not MCP. `server run` is MCP but has no middleware.
  - `cmd/serve.go` is 1,705 lines with package-level globals, and contains four copies of the request pipeline. The tests exercise the unused copies.
- **Advertised but unwired features:**
  - `pkg/budget` (and `pkg/webhook` behind it), `pkg/federation`, `pkg/connpool`, `pkg/proxy/socket`, `gateway_server.go` and `concurrent.Batcher` have **no importers**.
  - Lazy loading is parsed but never enabled.
  - The model router only emits a Debug log line.
  - The MLX sidecar is a placeholder.
  - The README advertises Budget Management and Federation.
- **Duplication:**
  - `pool` vs `connpool`;
  - `concurrent.RateLimiter` vs `ratelimit.TokenBucket`;
  - `serve` requires `enabled: true` while `server run` defaults missing values to enabled.
- **Weak coverage:** `cmd` 36%, `federation` 18%, `redistools` 11%, `postgresql` 25%, `mcp` 63%. Tests assume a non-root user.
- **Binary weight:** modernc SQLite is always linked, and a vector DB is opened at startup even when no embedder is configured.

---

## 5. Roadmap

Each item is scored on four axes. Higher Value and Effort mean more; higher Risk means doing nothing is more dangerous.

- **Criticality:** P0 = release blocker, P1 = next minor, P2 = planned, P3 = opportunistic.
- **Value** (to users) and **Effort**: 1–5.
- **Risk if skipped:** Low / Med / High.

### v0.10 — "Make the README true" (release blocker, about 2–3 weeks)

The goal: the mode users actually run is correct, safe and robust. **No new features.**

| # | Item | Fixes | Crit. | Value | Effort | Risk if skipped |
|---|---|---|---|---|---|---|
| 1 | **Unify the front ends.** One MCP handler with a middleware chain (redact-in, injection check, route, redact-out), used by stdio and by an optional Streamable-HTTP listener. Retire the non-MCP line-TCP `serve` protocol, or put it behind auth | S1, S2, P3, §4.4 | P0 | 5 | 4 | High |
| 2 | **Pool correctness:** remove or configure the rate limiter; multiplex stdio; configurable response limit with fail-and-respawn; continuous stderr drain; timeouts on HTTP/SSE; `Setpgid` | P1–P5, P10, P12 | P0 | 5 | 3 | High |
| 3 | **Error fidelity:** pass upstream `error` through; route by transport | P6 | P0 | 4 | 1 | High |
| 4 | **Make the response cache opt-in**, allowlisted only for tools marked `readOnlyHint`/`idempotentHint`, keyed on the pre-redaction hash, bounded by an LRU | S3 | P0 | 4 | 2 | High |
| 5 | **Fix the router contract:** expose `list_servers` or remove it from every description and hint; report the real `serverInfo.version` | P-§4.3 | P0 | 4 | 1 | High |
| 6 | **Honest benchmarks:** an end-to-end harness through the real binary (these `experiments/` scripts); count `list_tools` output and extra turns; update the README numbers | §4.3 | P0 | 4 | 2 | Med (credibility) |
| 7 | **Repo hygiene:** delete the committed Mach-O binaries (and purge them from history if possible); add `govulncheck`, dependency review and SBOM/provenance to releases; re-enable errcheck/gosec per file as code is fixed; make tests pass when run as root | S16, S17 | P0 | 3 | 1 | Med |
| 8 | **Cut or flag dead features:** remove Budget, Federation, `connpool`, `proxy/socket` and the MLX placeholder from the README, or mark them "experimental, not wired" | §4.4 | P0 | 3 | 1 | Med (trust) |

### v0.11 — "Current and secure" (about 4–6 weeks)

| # | Item | Fixes / why | Crit. | Value | Effort | Risk if skipped |
|---|---|---|---|---|---|---|
| 9 | **Ranked `search_tools(query, k)` across all servers.** BM25 over name, description and parameters, with optional hybrid embeddings through the existing Ollama embedder. Returns full schemas for the top-k and replaces the `list_tools` dump as the default path | §4.3: −73% → −92% tokens, fewer turns | P1 | 5 | 2 | High |
| 10 | **Redactor v2:** streaming, lossless path (`RedactStream`, `UseNumber`); broader pattern pack (ASIA / AWS secret, `sk-proj`, `AIza`, DSN, bare JWT, PGP, Slack `xox*`, entropy-gated generic); decode nested JSON in `content[].text`; ship a public test corpus | S4, P7 | P1 | 5 | 3 | High |
| 11 | **Protocol upgrade** to 2025-06-18 and 2025-11-25 (then dual-era for 2026-07-28): pass through `outputSchema`, `structuredContent`, annotations, icons, resource links, elicitation and progress/cancel; include annotations in search results | §2.2 | P1 | 4 | 3 | High |
| 12 | **Tool pinning and rug-pull detection:** hash each tool definition on first sight, alert and quarantine on drift, and scan descriptions for hidden instructions (see the S5 notes) | S8, OWASP MCP03 | P1 | 5 | 2 | Med |
| 13 | **Least-privilege child processes:** minimal environment plus an explicit `env` allowlist, and an optional container/sandbox runner (Docker or Podman) for marketplace servers | S7 | P1 | 4 | 2–4 | Med |
| 14 | **Marketplace integrity:** pin versions and digests, verify signatures and provenance where the registry offers them, show the command and ask for confirmation before enabling, no default trust score of 100 | S6 | P1 | 4 | 2 | Med |
| 15 | **Policy layer:** per-tool allow/deny lists, and confirmation or deny for `destructiveHint` tools; later, CEL expressions | S14 | P1 | 4 | 2 | Med |
| 16 | **Injection v2:** classify the decoded text of both **responses** and requests; add an optional local-LLM judge; emit valid JSON for every action | S5 | P2 | 3 | 3 | Med |
| 17 | **Dashboard hardening:** require a token on non-loopback binds, validate the `Host` header, put `/metrics` behind auth | S9 | P2 | 2 | 1 | Low |
| 18 | **OpenTelemetry** traces and metrics following the GenAI/MCP semantic conventions, replacing the custom JSON metrics | market parity | P2 | 3 | 2 | Low |

### v1.0 — "Differentiate where the market is not" (about one quarter)

These are the only new-feature bets worth making. Everything else is already done by someone else.

| # | Item | Why it is open territory | Crit. | Value | Effort |
|---|---|---|---|---|---|
| 19 | **Response-side token governor.** Per-tool, schema-aware field projection (drop unused JSON fields); smart truncation that spills the remainder into an MCP resource link the model can page through; dedup of repeated results within a session; optional local-LLM summary for oversized results. Measure it the same way as the schema savings | Native clients only cap outputs. **This is where the tokens are once schemas are solved** | P1 | 5 | 4 |
| 20 | **"Works with native search" adapters.** Return Anthropic `tool_reference` blocks and OpenAI namespaces, and emit `defer_loading` hints, so LeanProxy augments client-side search instead of hiding tools from it. Keep the 3-tool router as the fallback for clients without native search | Removes the conflict with Claude Code and OpenAI; keeps the Ollama and open-model audience | P1 | 4 | 3 |
| 21 | **Local-first security bundle as the headline:** redaction, pinning, sandbox and policy in one ~20 MB binary with no cloud dependency; `leanproxy doctor security` produces an OWASP-MCP-mapped report | Only MCPProxy competes, and it is Go/MIT, so ship quality and measured results | P1 | 5 | (rolled up from 10, 12–15) |
| 22 | **Auditable savings report:** per session and per team, split into schema, response, cache and turns saved, backed by real counters rather than an estimate | Credible ROI is a selling point once the benchmark is honest | P2 | 3 | 2 |
| 23 | *(Optional)* **Code-mode tool** (`execute` in a WASM/JS sandbox over typed tool stubs) | High ceiling, but Cloudflare, Docker and Anthropic already do this, and it needs a sandbox. **Only if 19–21 land first** | P3 | 3 | 5 |

### What *not* to build, and what to drop

- **Team budgets and multi-provider model routing.** LiteLLM and Portkey (now Palo Alto) own this, and LeanProxy never sees LLM traffic anyway, only MCP. Remove it or hand it off to LiteLLM.
- **Federation and multi-org hierarchy.** Enterprise K8s gateways (agentgateway, Envoy, MS, ContextForge) own this. It is unwired today: delete it.
- **More bundled first-party servers** (GitHub, Postgres, Redis). Official vendor servers exist. Keep only `filesystem` as a reference server, or move them to a separate repo.
- **Semantic caching of `tools/call` by embedding similarity.** It is dangerous for anything with side effects. Keep only the exact-match cache, restricted to read-only tools.
- **Pinecone and Qdrant back ends** for a local tool. SQLite, or pure in-memory BM25, is enough.

---

## 6. Honest answer: have we hit the market limit?

**For the original pitch, yes.** "Stop paying the schema tax with a 3-tool router" was new in 2025. By 2026:
- Anthropic, OpenAI, Claude Code and Cursor do it natively.
- At least 8 gateways (including two Go ones) ship the same router.

Adding more features on that axis (team budgets, federation, model routing, more first-party servers) will not create value. The unwired packages suggest the project has already been over-expanding.

**For a focused product, no.** Three things are still under-served and fit a local Go binary well:
1. **Token reduction on tool responses.** This is where the remaining cost is.
2. **A local-first MCP security layer** that actually runs in the IDE path: redaction, pinning, sandbox, policy.
3. **Discovery that works with any model**, including open and local ones without native tool search.

The prerequisite is v0.10. Today the security features users rely on are not active in the mode they run, and the reliability limits (10 req/s, a 1 MB wedge, serial pipes) would surface quickly in real agent workloads. Fix those first, publish honest numbers, then compete on items 19–21.

---

### Appendix: reproducing the experiments

```bash
go build -o /tmp/lp .
export AUDIT_DIR=/tmp/leanproxy-audit LEANPROXY_BIN=/tmp/lp
python3 docs/audit/experiments/gen_config.py      # writes $AUDIT_DIR/cat.yaml (5 servers, 118 tools)
python3 docs/audit/experiments/lp_drive.py        # router/list_tools token cost, latency, RSS
python3 docs/audit/experiments/lat.py             # paced latency + 500-call burst (rate-limit rejections)
python3 docs/audit/experiments/redact_check.py server run --stdio --config $AUDIT_DIR/cat.yaml   # redaction check
python3 docs/audit/experiments/search_eval.py     # substring vs BM25 tool-search recall and token cost
```
