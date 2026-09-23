# Changelog

## Breaking in v0.11

- **Least-privilege child environment: stdio servers no longer inherit the proxy's full environment** ([#311](https://github.com/mmornati/leanproxy-mcp/issues/311)).
  - **What.** `spawnLocked` used to build a stdio child's environment as
    `os.Environ()` (the proxy's own, in full) plus the server's configured `env`. Every
    stdio MCP server — including third-party servers installed from the marketplace —
    therefore saw every secret the proxy's own environment held (`OPENAI_API_KEY`,
    `AWS_*`, `GITHUB_TOKEN`, database URLs, ...), whether or not it needed them.
  - **Now.** Each child gets a minimal, fixed allowlist by default (PATH, HOME, locale,
    TLS trust, outbound proxy, and npx/uvx/node runtime-manager variables — see
    [`docs/configuration.md`](docs/configuration.md#child-process-environment-env-env_passthrough-inherit_env)),
    plus `servers[].stdio.env_passthrough` (copy named parent variables as-is),
    plus `servers[].stdio.env` (explicit `KEY=VALUE`, with `${VAR}` expansion from the
    parent environment; an unresolved reference fails start with a named error).
  - **Migration.** Set `servers[].stdio.inherit_env: true` on a server to restore the old
    full-inheritance behavior (logs one warning), or add the variables it needs to
    `env_passthrough`/`env`. `leanproxy-mcp doctor env` lists, per server, which
    variable names are passed and which are dropped (never values). At start, LeanProxy
    also warns when a well-known server package (e.g. `@modelcontextprotocol/server-github`)
    likely needs a variable that is present in the proxy's environment but not being
    passed to it. Configs imported via `leanproxy-mcp migrate` are unaffected: they
    already write each server's env as explicit `stdio.env` entries.

## Added in v0.11

- **`search_tools`: ranked tool search across every server** ([#305](https://github.com/mmornati/leanproxy-mcp/issues/305)).
  - **What.** A fourth gateway tool in `server run --stdio`. One call ranks the cached tools of all servers
    against a plain-words query and returns the top matches (default 5, at most 20) in the `list_tools`
    line format. The recommended flow is now `search_tools` → `invoke_tool`; `list_servers` and `list_tools`
    stay for browsing.
  - **How.** New package `pkg/toolsearch`: Okapi BM25 over server name, tool name (×2), description and
    parameters, with light stemming, stopwords and a small synonym table. The index follows the background
    tool cache and re-indexes only a server whose tool list changed. Optional hybrid mode (off by default)
    fuses BM25 with embedding similarity by Reciprocal Rank Fusion.
  - **Config.** New `tool_search:` block: extra `synonyms`, `disable_default_synonyms`, and `hybrid`
    with an Ollama or OpenAI embedder.
  - **Measured.** 85.5% recall@5 and 66.3% recall@1 on 83 labeled intents, 152 tokens per lookup against
    907 for `list_tools(server)`, p99 under 0.2 ms for 1,000 tools. The harness adds a `search_tools`
    session model: −65.0% to −93.5% tokens against native, with one extra turn per new tool.
  - **Router size.** `tools/list` grows from 237 to 318 tokens (budget test now < 330).
  - **Removed.** The dead substring matchers `matchesQuery` and `gateway.SearchTools`.

## Changed in v0.10

- **Benchmark numbers now come from an end-to-end harness that runs the real binary** ([#301](https://github.com/mmornati/leanproxy-mcp/issues/301)).
  - **Why.** The README figures came from benchmarks that never ran the proxy:
    - a session model that ignored `list_tools` output and extra turns;
    - an empty timing loop;
    - two `json.Unmarshal` calls presented as proxy overhead;
    - an in-process mock presented as throughput.
  - **The harness.** `make harness` (`tests/harness`, `harness` build tag, a new CI job) builds
    `leanproxy-mcp` and drives `server run --stdio` over pipes. Behind it is a Go mock that serves a
    118-tool, 5-server catalog. It measures:
    - tokens, with discovery outputs counted and extra turns reported;
    - latency, against a direct baseline;
    - a 500-call burst, 50 parallel slow calls and a 5 MB relay;
    - RSS;
    - the Token Firewall.
  - **Assertions.** The harness fails when any of these breaks:
    - redaction in both directions;
    - 0 burst errors;
    - 50 × 100 ms calls in under 1 s;
    - the 5 MB relay;
    - p95 overhead under 5 ms.
  - **Measured.** README and `docs/benchmark-results.md` are regenerated from its output:
    - sessions save 72–87% of tokens, not 81.5–93.7%, and cost 3–4 extra LLM turns;
    - overhead is 0.72 ms at p95.
  - **Benchmarks.** Misleading `tests/bench` benchmarks were removed or renamed, and the unused
    `tests/bench/mockmcp` package was deleted.
  - **Fix: `invoke_tool`.** It no longer strips the server name from a tool that is really named with it,
    such as Slack's `slack_post_message` on a server called `slack`.
  - **Fix: status file.** It now honors `$HOME`.
- **Breaking: `serve` clients must authenticate; the listener rejects HTTP and caps size and concurrency** ([#298](https://github.com/mmornati/leanproxy-mcp/issues/298)).
  The `serve` TCP port had no authentication, so any local process, or any web page through a `text/plain`
  POST to `127.0.0.1:8080` (the HTTP header lines failed to parse and the JSON body line was executed), could
  call every upstream tool. The first line of each connection must now be
  `{"jsonrpc":"2.0","method":"auth","params":{"token":"…"}}`; otherwise the connection is closed without
  executing or answering anything. The token comes from `--auth-token`, `$LEANPROXY_SERVE_TOKEN`, or
  `~/.config/leanproxy/serve.token` (0600, generated on first start). A first line that looks like HTTP is
  rejected. `--no-auth` disables the handshake and is refused on a non-loopback `--listen` address. Lines are
  capped by `server.max_line_bytes` (64 MiB), requests per connection by `server.max_concurrent_requests`
  (64, the reader waits), connections by `server.max_connections` (32). A disconnect now cancels the
  connection's in-flight upstream calls, and accept errors back off (5 ms to 1 s) instead of spinning.
  **Migration:** make every `serve` client send the auth line first, reading the token from
  `~/.config/leanproxy/serve.token` (or set `LEANPROXY_SERVE_TOKEN` for both sides); for local development
  only, `serve --no-auth` keeps the old behavior on loopback. The IDE integration (`server run --stdio`) and
  the VS Code / JetBrains extensions (which read the metrics endpoint) are unaffected.
- **Server lifecycle: the pool owns the MCP handshake, tools refresh in the background** ([#297](https://github.com/mmornati/leanproxy-mcp/issues/297)).
  The handshake moved from the request handler into the pool: each stdio process generation sends
  `initialize` + `notifications/initialized` exactly once before any other request (a restarted server,
  including in `serve`, is re-initialized; tool-cache refreshes no longer cause a second `initialize`), and
  HTTP/SSE servers never receive `initialize` as a tool call. The server's `InitializeResult` is stored and
  `list_servers` shows its `serverInfo` and the first 120 characters of its `instructions`. At startup both
  front ends load the persistent tool cache and serve at once (`serve` used to wait up to 60 s before
  listening); each server's `tools/list` is refreshed in the background, in parallel, again after a restart or
  `notifications/tools/list_changed`, and retried while unknown. `list_tools` for an uncached server refreshes
  that server only, bounded by its timeout, and the handler no longer restarts servers inline with sleeps.
  In `serve` a server's routes are updated whenever its tool list changes, so a server that was down at
  startup becomes routable later. The stdio front end reads `notifications/cancelled` even when every
  concurrency slot is busy, and a `shutdown` request no longer closes the pools from inside the handler. The
  tool cache directory honors `$HOME` and the new `LEANPROXY_TOOLCACHE_DIR` override.
- **`server run --stdio` handles requests concurrently** ([#292](https://github.com/mmornati/leanproxy-mcp/issues/292)).
  The stdio front end used to read one request, wait for its response, then read the next, so one slow tool
  call blocked `ping`, `tools/list` and calls to every other server. Each request now runs in its own
  goroutine, capped by the new `server.max_concurrent_requests` setting (default 64; the reader waits at the
  cap instead of rejecting). `notifications/cancelled` cancels the matching in-flight request (and, through
  the pool, the upstream call); notifications never get a response; EOF and `shutdown` drain in-flight
  requests for up to 5 s before canceling the rest and stopping every upstream server. Invalid JSON is no
  longer logged verbatim (only its length). Concurrent first calls to a server now perform a single MCP
  initialize handshake.
- **Concurrent requests to one stdio server are multiplexed** ([#294](https://github.com/mmornati/leanproxy-mcp/issues/294)).
  The stdio pool used to serve one request at a time per server (50 parallel 100 ms calls took ~5 s); it now
  keeps a pending map keyed by the internal wire ID, so calls run concurrently up to the new per-server
  `max_in_flight` setting (default 32; callers beyond it wait instead of being rejected). A caller that times
  out sends `notifications/cancelled` to the server, a server that exits fails every pending call at once,
  and server-to-client requests are answered with `-32601` instead of being left unanswered. The stdout
  reader no longer drops responses when a buffer is full.
- **Removed the never-fed circuit breaker and `pkg/concurrent`.** The stdio pool created a circuit breaker
  per server but never recorded a success or failure, so it could not open. Crash recovery (restart budget
  plus health checks) already covers the failure modes it was meant for, so it was deleted rather than wired.
  With it went the rest of `pkg/concurrent` (an unused duplicate `StdioPool`, `RateLimiter`,
  `MultiServerRateLimiter`, `QueueManager`, `WorkerPool`) and `pkg/pool`'s unused request queues, none of
  which had non-test callers.

## Removed in v0.10

Epic 19 (["Make the README true"](https://github.com/mmornati/leanproxy-mcp/issues/288)) audited every
package and README/docs claim against what is actually wired into a command. The following features were
never wired into any command — they existed as code and/or config keys with no non-test callers — and were
removed rather than left to bit-rot. See issue [#303](https://github.com/mmornati/leanproxy-mcp/issues/303)
for the full audit.

- **Budget Management** (`pkg/budget`, `pkg/webhook`, `docs/budget.md`) — per-team/project token budgets
  with hard/soft caps and webhook alerts. LeanProxy does not pursue team budgets; LiteLLM, Portkey and the
  enterprise gateways own that.
- **Federation** (`pkg/federation`, the `federation:` config block) — multi-org peer routing. LeanProxy
  never saw LLM traffic across peers and the feature was never wired in. Existing configs that still set
  `federation:` keep loading; a single startup warning notes the key is ignored.
- **Model Routing** (`pkg/modelrouter`, `--model-router` / `--model-router-config`) — per-tool LLM model
  selection by complexity tier. LeanProxy does not route LLM traffic and never pursued this; LiteLLM,
  Portkey and the enterprise gateways own it. The flags are still accepted for this one release and now
  print a deprecation warning instead of doing nothing silently; they will be removed in a future release.
- **Lazy tool-schema loading** (`optimization.lazy_loading`, `Handler.EnableLazyLoading`, the
  `get_tool_schema` RPC method) — `EnableLazyLoading` was never called from any command, so the feature was
  always off. Existing configs that still set `optimization.lazy_loading:` keep loading; a single startup
  warning notes the key is ignored. Tool discovery is unaffected — it goes through the 3-tool gateway
  (`list_servers` / `list_tools` / `invoke_tool`), which was always the live path.
- **MLX sidecar** (`pkg/sidecar/mlx.go`, build tag `mlx`) — an Apple Silicon placeholder that never
  performed real inference (`Redact` returned a constant placeholder string). The Ollama sidecar is
  unaffected.
- **Duplicate/dead connection-pooling code**: `pkg/connpool` (an unused duplicate of `pkg/pool`),
  `pkg/proxy/socket` (an unused Unix-socket transport with a non-constant-time token comparison and a
  chmod-after-listen race), `pkg/proxy/jit.go`, `pkg/proxy/session.go`, `pkg/proxy/http.go`,
  `pkg/proxy/health_monitor.go`'s `HealthMonitor`/`ManagedServer` machinery and `pkg/proxy/process_health.go`
  (all definitions with no non-test callers), and `concurrent.Batcher` plus the unused `WorkerPool` wiring
  inside `pkg/pool`.
- **`cmd/serve.go`'s `handleSingleRequest` / `handleBatchRequest`** — dead synchronous copies of the live
  `handleSingleRequestAsync` / `handleBatchRequestAsync` path, kept alive only by their own tests. The tests
  now exercise the async functions directly.
- **Shadow Manifesting** README/docs claim — `utils.ManifestMerger` was never called from any command, so
  the claim that LeanProxy "merges global and project-local MCP configurations" was removed. LeanProxy does
  not auto-load project-local `.mcp.json` without an explicit trust prompt.

None of the above had any effect on a running LeanProxy instance before this release; removing them only
shrinks the binary and the surface future changes have to reason about.
