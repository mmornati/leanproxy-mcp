# Changelog

## Breaking in v0.11

- **`serve`'s line-TCP protocol is deprecated** ([#309](https://github.com/mmornati/leanproxy-mcp/issues/309)).
  - **What.** `serve` speaks newline-delimited JSON-RPC over raw TCP, which is not an MCP
    transport.
  - **Now.** It still works unchanged, but logs a deprecation warning at start. **It will be
    removed in v1.0.**
  - **Migration.** Point MCP clients at `leanproxy-mcp server run --http 127.0.0.1:8765` (the
    `/mcp` endpoint, same token file, `Authorization: Bearer` header). See
    [Migrating from `serve`](docs/quickstart.md#migrating-from-serve). The IDE extensions only
    read the metrics endpoint and are not affected.

- **Calls to tools a server does not advertise are refused** ([#314](https://github.com/mmornati/leanproxy-mcp/issues/314), audit S14).
  - **What.** `invoke_tool`, a namespaced `tools/call` and `serve`'s `server.tool` methods forwarded
    any tool name to the upstream, including tools the server never listed in its `tools/list`
    (hidden debug or admin tools, typos a model turned into a call).
  - **Now.** The new per-tool policy's `unknown_tools: deny` default refuses such a call with a
    JSON-RPC `-32600` error that suggests `search_tools`; the upstream is not called. The server's
    tool list is fetched (or refreshed once, at most every 10 s per server) before a name is
    declared unknown, so a tool added without `notifications/tools/list_changed` is still found.
    Everything the servers advertise keeps working exactly as before (`policy.default: allow`).
  - **Migration.** If you rely on calling tools a server does not list, set
    `policy: { unknown_tools: allow }` (see
    [`docs/configuration.md`](docs/configuration.md#per-tool-policy-policy)).

- **Marketplace supply-chain integrity: official registry, version pinning, honest trust score, confirm-before-enable** ([#313](https://github.com/mmornati/leanproxy-mcp/issues/313)).
  - **What.** `pkg/registry/trust.go`'s `CalculateTrustScore` used to return a feed-provided
    `trust_score` as-is when present, and scored an entry with **no** trust-relevant data **100**
    (maximum trust) — the feed author decided how trustworthy their own entry looked. Nothing
    installed from the registry was checksummed, signed or pinned: `add` wrote the feed's
    `command`/`args`/`env` straight into `leanproxy_servers.yaml` with `enabled: true` and no
    version pin, so `npx -y pkg` ran whatever was latest on every start, without showing the
    command first. The default feed URL, `https://registry.mcp.io/index.ndjson`, is a domain this
    project does not own.
  - **Now.**
    - `marketplace sync` defaults to the **official MCP Registry**
      (`registry.modelcontextprotocol.io`, API `v0`), which verifies package identifiers, versions
      and namespaces. The old unowned default domain is no longer used at all; a custom NDJSON feed
      is now opt-in only, via `registry.sources` in `leanproxy_servers.yaml` (see
      [`docs/configuration.md`](docs/configuration.md#marketplace-registry-sources-issue-313)).
    - `CalculateTrustScore` **never** uses a feed-provided `trust_score`. It scores only from
      signals LeanProxy can verify (registry namespace verification, license presence, release
      recency, open issues, downloads). An entry with no signal at all scores **0**, labeled
      `unverified` — never a free pass to 100. `marketplace search` and `add`'s install preview
      both show the individual signals, not just the number.
    - Installed stdio servers are pinned to an exact version (`npx -y pkg@1.2.3`,
      `uvx pkg==1.2.3`, `docker run ... image@sha256:…` when available), recorded as
      `installed_from: {registry, name, version, installed_at}` on the server entry.
      `marketplace outdated` / `marketplace update <name>` show the version diff and ask for
      confirmation.
    - `add` prints the exact command line, env var *names* only (values are never printed or
      logged), transport/URL and trust signals, then asks `Enable this server? [y/N]` before
      writing anything. `--yes` skips the prompt for scripts; `--dry-run` only previews. A newly
      installed server that is not confirmed is still written, but with `enabled: false`.
  - **Migration.** Configs written by earlier versions of `add`/`marketplace update` keep working;
    the new `installed_from` block is only added on the next install/update. If you relied on the
    default `registry.mcp.io` feed, add it explicitly under `registry.sources` (or point at your
    own feed) — see [`docs/security.md`](docs/security.md#marketplace-trust-model-issue-313) for
    the full trust model.

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

- **Dashboard & metrics hardening: no unauthenticated non-loopback bind, no loopback token bypass, Host/Origin validation** ([#316](https://github.com/mmornati/leanproxy-mcp/issues/316)).
  - **What.** `pkg/dashboard`'s `requireBearerToken` let *every* request through when no token was
    configured, even on a `0.0.0.0` bind (only a warning was logged), and let any loopback client
    skip a configured token entirely — a reverse proxy or any other local process on the same host
    could read server names, tool names, token counts and prompt hashes. Neither the dashboard nor
    the metrics endpoint (`--metrics-bind`) validated the `Host` header, so a malicious web page
    could reach `127.0.0.1:9090`/`9091` via DNS rebinding from a victim's browser. `--metrics-bind`
    had no authentication at all.
  - **Now.** `serve` **refuses to start** if `--dashboard-bind` or the new `--metrics-bind` is bound
    to a non-loopback address without a token (`--dashboard-token` / new `--metrics-token`), instead
    of warning and serving the data unauthenticated. A configured token is required from **every**
    client, loopback included — the loopback bypass is removed. For browser use, the dashboard
    supports exchanging the token for an `HttpOnly`, `SameSite=Strict` cookie (`Secure` over TLS) via
    `GET /login?token=…`. Both endpoints validate the `Host` header against the bind host,
    `localhost`, `127.0.0.1`, `[::1]` and the new `--dashboard-allowed-hosts` /
    `--metrics-allowed-hosts`, and reject a state-changing request whose `Origin` does not match
    (`403 Forbidden`). The dashboard now sends `Content-Security-Policy`, `X-Frame-Options: DENY`,
    `Referrer-Policy: no-referrer` and `X-Content-Type-Options: nosniff` on every response. New
    shared package `pkg/httpsec` holds the Host/Origin validation and security-header middleware for
    both `pkg/dashboard` and `pkg/metrics`, which otherwise stay independent of each other.
  - **Migration.** A `serve` invocation that binds `--dashboard-bind`/`--metrics-bind` to a
    non-loopback address (e.g. `0.0.0.0:9090`) must now also pass a token, or `serve` exits with an
    error naming the missing flag. A deployment that relied on the loopback bypass while a token was
    set must now send the token (header or `/login` cookie) from loopback too. See
    [`docs/dashboard.md`](docs/dashboard.md#authentication) and
    [`docs/security.md`](docs/security.md#dashboard--metrics-hardening-316).

- **First-party servers hardening: real Postgres read-only mode, Redis pool deadlock and RESP allocation fixes** ([#318](https://github.com/mmornati/leanproxy-mcp/issues/318)).
  - **What.** `servers/postgres`'s `postgresql_query` tool relied on a `SELECT`/`EXPLAIN` text prefix
    check that a query can slip past while still writing (`EXPLAIN ANALYZE DELETE ...` executes the
    statement, `SELECT ... INTO ...` creates a table, side-effecting functions like
    `pg_terminate_backend(...)`, or `WITH x AS (DELETE ... RETURNING *) SELECT * FROM x`), and there was
    no way to disable `postgresql_execute` (arbitrary INSERT/UPDATE/DELETE/DDL) at all.
    `servers/redis`'s connection pool (`pkg/redistools`) could deadlock: `withConn` received from the
    pool channel while holding the client's mutex, and on a failed re-dial it never returned the
    borrowed slot, so sustained failures permanently drained the pool and every later call — including
    `Close()` — blocked forever. Its RESP parser also trusted server-sent `$n`/`*n` lengths outright, so
    a malicious or compromised server (or a MITM on a connection without `LEANPROXY_REDIS_TLS`) could
    force an unbounded allocation, and connections had no read/write deadlines.
  - **Now.** Postgres: `LEANPROXY_POSTGRES_READ_ONLY` (default **true**) removes `postgresql_execute`
    from `tools/list` entirely; `postgresql_query` always runs inside a real `BEGIN ... READ ONLY`
    transaction (with `SET LOCAL statement_timeout`), rolled back afterwards, regardless of that flag —
    the prefix check is now a UX hint only, not the security boundary. Queries go through pgx's extended
    protocol, which also rejects `;`-separated multi-statement injection. Redis: every borrowed
    connection is returned to the pool on every path (including a failed re-dial), so the pool can never
    drain and `Close()` always returns promptly; `LEANPROXY_REDIS_MAX_BULK_LEN` (default 16 MiB) and
    `LEANPROXY_REDIS_MAX_ARRAY_LEN` (default 1,000,000) cap RESP reply allocations; `LEANPROXY_REDIS_DIAL_TIMEOUT`
    and `LEANPROXY_REDIS_COMMAND_TIMEOUT` (both default `5s`) bound connect/re-connect and every command.
  - **Migration.** A deployment that calls `postgresql_execute` through `servers/postgres` must now set
    `LEANPROXY_POSTGRES_READ_ONLY=false` explicitly — it is no longer registered by default. See
    [`docs/configuration.md`](docs/configuration.md#first-party-servers-postgres-and-redis) and
    [`docs/security.md`](docs/security.md#first-party-servers-hardening-postgres-redis-318).

## Added in v0.11

- **Response token governor (part 3): in-session dedup of repeated results and optional local-LLM summarization** ([#321](https://github.com/mmornati/leanproxy-mcp/issues/321), audit §2.3).
  - **What.** Agents often re-read the same thing within a session (the same file after a failed
    edit, the same issue, the same listing), each time added to the context again. Some results
    (long logs, long documents) stay large even after projection (#320) and truncation (#319).
  - **Now**, both opt-in and both off by default:
    - **`response.dedup: on`**: for every result over ~500 estimated tokens, the governor
      remembers a hash of its (already redacted and projected) content, keyed to the current
      **client session only**. A later result identical to one already returned this session is
      replaced with a short stub (`identical to the result of <server>.<tool> returned earlier
      (result_id=r_…)`); `read_result` still serves the full content by that id. Two sessions
      that happen to see the same content never learn anything about each other, and a session's
      dedup memory is dropped when the session ends, along with its spilled results. Error
      results are never deduped.
    - **`response.summarize`**: for a result from an allowlisted tool (`summarize.tools`, a glob
      allowlist, **required** — nothing is summarized unless listed) still over
      `threshold_tokens` (default 8,000) after projection and dedup, the redacted text is sent to
      a **local** model (the existing `pkg/sidecar` Ollama plumbing, #315) with a fixed prompt
      ("keep identifiers, numbers, paths, errors verbatim; list what was omitted"), capped to
      `max_summary_tokens` (default 800) and a strict `timeout` (default 10s). The summary
      replaces the result, with a `read_result` pointer to the full copy. Any failure — timeout,
      error, empty output — falls back to ordinary structural truncation, never a hang or a lost
      result. `summarize.url` must be a loopback address unless `allow_remote: true` (only local
      providers are allowed by default). The summary is treated as untrusted content and run back
      through the injection guard's response scan (#315) before it is ever returned; a summary is
      never cached or reused across sessions. Error results are never summarized.
    - Pipeline order: redaction and the injection check, then projection (#320), then dedup, then
      summarize-or-truncate.
    - Accounting (dedup hits and estimated tokens saved; summaries made, estimated tokens saved
      and fallbacks) on `/metrics` (`response_governor.dedup_*` / `summariz*`, also per tool) and
      as OTel counters (`leanproxy.governor.dedup`, `leanproxy.governor.dedup.tokens`,
      `leanproxy.governor.summarizations`, `leanproxy.governor.summarization.tokens`,
      `leanproxy.governor.summarization.fallbacks`).
  - **Measured.** `make harness`, a repeated-reads session (the same file read once, then
    re-read three more times): **13,780 → 3,640 tokens (−73.6%)** over truncation alone, a
    repeat read **98.1%** smaller than the first. Summarization is covered by unit and e2e tests
    with a fake Ollama server (`httptest`), not the harness's fixed-catalog measurements, since it
    needs a real local model to measure honestly. See
    [`docs/configuration.md`](docs/configuration.md#in-session-dedup-responsededup) and
    [`docs/benchmark-results.md`](docs/benchmark-results.md#9-in-session-dedup-repeated-reads).

- **Response token governor (part 2): schema-aware field projection per tool** ([#320](https://github.com/mmornati/leanproxy-mcp/issues/320), audit §2.3).
  - **What.** API-backed tools return verbose JSON (GitHub issues with full user objects, URLs,
    reactions and node ids; Jira issues with dozens of fields) while the model needs a handful of
    fields. Truncation (#319) keeps the first items whole, noise included.
  - **Now.** With the governor on (`response.enabled: true`), fields are dropped before the
    budget is applied:
    - `response.projections`: per server/tool rules (`path.Match` globs, first match wins), each
      a `keep` allowlist or a `drop` denylist of paths in a small syntax: dot paths, `[]` for
      array elements, `**` for any depth, `*` for any run of characters in a key
      (`"[].labels[].name"`, `"**.node_id"`, `"**.*_url"`); a rule with neither exempts the tool;
    - `response.default_projections: true` (off by default): a conservative built-in drop pack
      (`*_url`, `node_id`, `avatar_url`, `gravatar_id`, `_links`, `self`, `etag`), never a keep;
    - `invoke_tool` takes an optional `fields` argument (a list of paths): a one-off `keep`
      applied by the proxy and never forwarded upstream. While the governor is on, `tools/list`
      declares it on `invoke_tool`; the default `tools/list` is unchanged (318 tokens).
    It applies to text items whose text is a JSON object or array and to `structuredContent`
    (only when the tool declares no `outputSchema`, so a strict schema is never broken). Kept
    values are byte for byte (numbers keep their precision), the output is compact and not
    HTML-escaped. It runs after redaction and the injection check and before truncation; the full
    redacted result stays readable with `read_result` / `resources/read`, and a note (plus a
    `resource_link` on MCP 2025-06-18+) tells the model what was left out and how to get it.
    Error results, non-JSON text and bad paths are left untouched. Tokens before and after
    projection are counted per tool (`/metrics`, OTel `leanproxy.governor.projections` and
    `leanproxy.governor.projection.tokens`).
  - **Measured.** A realistic 30-issue GitHub `list_issues` fixture: **29,536 → 8,225 tokens
    (−72.2%)** with the issue's `github.*` drop pack. `make harness`, large-results listings:
    −21.5% from projection alone, and within the same 4,000-token budget 26 instead of 19 issues
    (64 with `fields`). See
    [`docs/configuration.md`](docs/configuration.md#field-projection-responseprojections) and
    [`docs/benchmark-results.md`](docs/benchmark-results.md#8-field-projection-large-results).

- **Response token governor (part 1): smart truncation and spill-to-resource with paged retrieval** ([#319](https://github.com/mmornati/leanproxy-mcp/issues/319), audit §2.3).
  - **What.** Once tool schemas are handled, tool results (file contents, listings, search
    results, rows) are the biggest token cost of an agent session, and each one is re-sent on
    every later turn. No front end touched them.
  - **Now.** An opt-in `response:` block (off by default) caps each tool result at a token budget
    (`max_tokens`, default 4,000, per-server/per-tool overrides by glob, `passthrough`):
    - text keeps its head (~70%) and tail (~20%), cut on line boundaries, with a marker naming
      the result id and the offset to page from;
    - JSON (a text item holding JSON, and `structuredContent`) is shortened structurally and
      stays valid: arrays keep their first elements plus an `__leanproxy_omitted` object;
    - the full redacted result is kept per client session (memory by default, or `0600` files
      with `spill.disk`), with a TTL (30 min) and an LRU byte cap (128 MiB);
    - a new `read_result` gateway tool pages through it (`offset`, `limit_tokens`), searches it
      (`grep` with line numbers) or queries it (`jsonpath`: `$.items[10:20]`, `$..name`);
      clients on MCP 2025-06-18+ also get a `resource_link` to `leanproxy://results/<id>`,
      served by `resources/read`;
    - error results, images and audio are never shortened;
    - accounting (tokens before/after per tool) on `/metrics` and as OTel counters.
    It runs in all three front ends, after redaction and the injection check, and outside the
    response cache (a hit is governed like the miss, for the calling session only).
  - **Measured.** `make harness`, large-results session (a 200 KB file and four list/search
    endpoints): **234,700 → 18,515 tokens (−92.1%)**; normal-size results byte-identical; the
    file pages back byte for byte. See
    [`docs/benchmark-results.md`](docs/benchmark-results.md#7-response-governor-large-results)
    and [`docs/configuration.md`](docs/configuration.md#response-token-governor-response).

- **Streamable HTTP MCP front end: one shared local gateway, with auth and Origin checks** ([#309](https://github.com/mmornati/leanproxy-mcp/issues/309), audit §2.2).
  - **What.** `serve`, the only front end that took more than one client, speaks a custom
    newline-JSON protocol over TCP that no MCP client supports. So every IDE had to spawn
    its own `server run --stdio`, each with its own child servers.
  - **Now.** `leanproxy-mcp server run --http 127.0.0.1:8765` serves the MCP Streamable HTTP
    transport (2025-03-26, 2025-06-18, 2025-11-25) at `/mcp`:
    - POST gets a JSON or SSE response, GET opens the session stream, DELETE ends the
      session.
    - `Mcp-Session-Id` carries a per-session negotiated version, and `MCP-Protocol-Version`
      is validated.
    - Any number of clients (Claude Code, Cursor, VS Code, OpenCode, other agents) connect
      by URL and share one set of child servers.
    - Every message runs through the same pipeline as stdio: redaction, injection guard,
      response cache, tool pinning, per-tool policy (confirm through elicitation) and
      telemetry.
    - The server-to-client relays of #308 (elicitation, sampling, roots, progress,
      cancellation, resource updates) travel on the session's SSE streams. A message caused
      by a request goes on that request's own stream.
  - **Security.**
    - Loopback bind by default. A non-loopback bind without a token is refused, and
      `--no-auth` is accepted on loopback only.
    - `Authorization: Bearer` with the `serve` token file (`--http-token`,
      `$LEANPROXY_SERVE_TOKEN`), compared in constant time.
    - `Host` and `Origin` validation through `pkg/httpsec` (DNS rebinding, cross-site calls),
      with `server.http.allowed_hosts` and `allowed_origins` allowlists.
    - 256-bit session ids bound to the credential, capped (`max_sessions`) and expired when
      idle (`session_idle_timeout`).
    - Body (`max_body_bytes`, 64 MiB), header, read and write limits, and the shared
      `server.max_concurrent_requests` cap.
    - `doctor security` reports the running endpoint's exposure.
  - **Not included.** Resumability (`Last-Event-ID`) is not supported: events have no id.
    Full OAuth 2.1 resource-server support is a follow-up. See
    [`docs/quickstart.md`](docs/quickstart.md#2b-or-run-one-shared-gateway-over-http) for
    client snippets.

- **Optional sandbox runner for stdio servers: Docker/Podman network & filesystem isolation** ([#312](https://github.com/mmornati/leanproxy-mcp/issues/312), audit S7 follow-up, market parity with Docker MCP Gateway / ToolHive / MCPProxy).
  - **What.** Every stdio MCP server, including marketplace-installed third-party packages
    (`npx some-package`), used to run directly on the host with the proxy's own filesystem and
    network access. New `stdio.sandbox` runs the server's command inside a container instead,
    off by default per server.
  - **Runner.** `docker run --rm -i --init --name leanproxy-<server>-<gen> --network <net>
    --read-only --tmpfs /tmp --cap-drop ALL --security-opt no-new-privileges --pids-limit 256
    [-m <mem>] [--cpus <cpus>] [-v host:container[:ro]]... [-e NAME]... <image> <command>
    <args>`, built as argv (never a shell) so no config or environment value can inject an
    extra flag. `network: none` (default), a read-only root filesystem with a `/tmp` tmpfs,
    dropped capabilities and `no-new-privileges` are always applied; `mounts` (explicit,
    default none), `memory`, `cpus` and a `cache_volume` for the npm/uv package cache are
    opt-in.
  - **Env never touches argv.** The least-privilege child environment (#311) is set on the
    container runtime CLI process itself; the container gets each variable via a bare `-e
    NAME` (name only), so a secret value never appears in the runtime's argv, in the "server
    spawned" log line, or in `ps` output.
  - **Detection & cleanup.** A configured runtime binary missing from `PATH` fails that
    server's start with a clear error — never a silent fallback to unsandboxed — without
    affecting any other server. Every process generation gets a unique container name; since
    the pool's own process-group kill only ever reaches the runtime CLI (the daemon keeps the
    container running independently), the pool also runs `<runtime> rm -f <name>` on every
    stop, restart and crash, so `docker ps -a --filter name=leanproxy-` is empty once a
    sandboxed server (or the proxy) has stopped.
  - **Marketplace integration.** `leanproxy-mcp add <server-id> --sandbox docker` (or
    `podman`) records `stdio.sandbox` on install (#313); the low-trust install tip now points
    at this flag. The actual default stays off for compatibility.
  - **`leanproxy-mcp doctor sandbox`** (and `doctor security`) reports, per stdio server,
    whether it is sandboxed, its runtime/image/network, and whether the runtime binary is
    currently available — without starting anything.
  - See [`docs/security.md`](docs/security.md#sandboxing-servers-312) (isolation, limits,
    Docker Desktop specifics) and
    [`docs/configuration.md`](docs/configuration.md#sandbox-stdiosandbox-312).

- **Per-tool policy: allow / deny lists, confirmation of destructive tools, no calls to unlisted tools** ([#314](https://github.com/mmornati/leanproxy-mcp/issues/314), audit S14, OWASP MCP02 / MCP07).
  - New `policy:` block: `default` (allow | deny), `unknown_tools` (deny | allow) and ordered
    `rules` — a glob on `server.tool` (`*`, `?`), optional `annotations`
    (`destructiveHint: true`, ...) and an `action` (allow | deny | confirm). The first matching
    rule wins; `unknown_tools` is checked before the rules, `default` applies when nothing
    matches. Validated when the config is loaded.
  - One middleware of the unified pipeline, in both front ends (`server run --stdio` and
    `serve`), for every call form (`invoke_tool`, namespaced `tools/call`, `serve`'s
    `invoke_tool` and `server.tool` methods), placed after tool pinning and before the response
    cache: a refused call never reaches the cache or the upstream.
  - `deny` → JSON-RPC `-32600` naming the rule (as tool pinning does); `confirm` → an
    `elicitation/create` form (Approve / Approve for this session / Deny) showing the redacted,
    500-character-max argument summary; a client without form elicitation is refused, never
    silently allowed.
  - Discovery reflects the policy: denied tools are hidden from `list_tools` / `search_tools`,
    tools that need confirmation are marked `[confirm]` (and `"policy": "confirm"` in
    `structuredContent`).
  - Audit log line per deny / confirm decision (server, tool, rule, outcome, SHA-256 of the
    redacted arguments — never the arguments); `leanproxy.policy.decisions` metric (`outcome`,
    `mcp.server.name`) and `leanproxy.policy.decision` / `leanproxy.policy.rule` span attributes.
  - Per-tool injection policy (carried over from #315): a rule's `injection.request_policies` /
    `injection.response_policies` replace the guard's risk bands for the tools it matches.
  - `leanproxy-mcp policy check <server.tool>` explains which rule decides a call;
    `doctor security` reports the active policy.
  - `response_cache.honor_annotations` now works (carried over from #307): a tool annotated
    `readOnlyHint: true` and `idempotentHint: true` (and not `destructiveHint: true`) is cached
    without being listed in `response_cache.tools`.

- **Tool pinning and rug-pull detection** ([#310](https://github.com/mmornati/leanproxy-mcp/issues/310), OWASP MCP03).
  - **What.** Upstream tool definitions used to reach the model unchecked: a server could hide
    instructions in a description (tool poisoning), change a tool after it was approved (rug pull), or
    shadow another server's tool. Every tool refresh (startup, `tools/list_changed`, restart) is now
    compared with a pin file (`~/.config/leanproxy/pins.json`, mode 0600, written atomically) holding,
    per server, its `serverInfo` and, per tool, a SHA-256 of the canonical JSON of
    `{name, title, description, inputSchema, outputSchema, annotations}` (key order and whitespace never
    change it; see `docs/security.md`), first-seen time and approval status. A server seen for the
    first time is trusted on first use (one info line per server).
  - **Policy** (`security.tool_pinning.mode`): `warn` (**default**) logs `tool_added` / `tool_changed`
    (with a unified diff) / `tool_removed` / `server_identity_changed` / `tool_flagged` /
    `tool_shadowed`, reports them in `doctor security`, the dashboard and the new
    `leanproxy.tool_pin.events` metric, and adds a one-line warning to `list_tools` / `search_tools`;
    `block` hides new or changed tools from discovery and refuses calls to them (in both front ends,
    whatever the call form) with an error naming the approval command; `off` disables it.
  - **Description scanner.** New and changed tools (including on first use) are scanned with the
    injection classifier's engine and a dedicated pattern set plus the injection guard's defaults:
    hidden instructions (`<IMPORTANT>`, "before using this tool", "do not tell the user"), secrets
    files (`~/.ssh/id_rsa`, `.env`), invisible or bidi unicode, external URLs, base64 blobs, overlong
    descriptions. A high-severity finding keeps the tool pending even on first use.
  - **Always, in every mode,** invisible and bidi characters are stripped from tool metadata before it
    reaches the client.
  - **CLI.** `leanproxy-mcp tools pins list | diff [server] | approve <server> [tool…|--all] | reset <server>`;
    running proxies pick an approval up within a second.

- **MCP protocol upgrade, part 2: server-to-client requests, progress, cancellation and resource updates** ([#308](https://github.com/mmornati/leanproxy-mcp/issues/308)).
  - **What.** An upstream's `elicitation/create`, `roots/list` and `sampling/createMessage` requests used to
    be answered `-32601` (the feature was lost); they are now relayed to the client — on both front ends
    (`server run --stdio` and every `serve` connection), for stdio and Streamable HTTP upstreams — and the
    client's answer is sent back. The client sees the request under a proxy id (`lp-<n>`), so ids of
    different upstreams and clients never collide; the upstream keeps its own. Only a client that declared
    the capability in `initialize` is asked; otherwise the upstream gets `-32601` at once, nothing hangs.
  - **Policy.** Elicitation is relayed by default with `[<server>] ` prepended to the message (spoofing
    protection). Sampling is **off by default** and relayed only with the new `servers[].allow_sampling: true`
    (each use logged). `roots/list` is relayed, or answered from the new static `servers[].roots` list.
    What the proxy declares to each upstream follows the same policy.
  - **Progress.** A client `_meta.progressToken` on `tools/call`/`invoke_tool` (and `resources/read`,
    `prompts/get`) is forwarded upstream as a proxy token unique per call; the upstream's
    `notifications/progress` reach that client only, with its own token.
  - **Cancellation, both ways.** A client cancel reaches stdio *and* HTTP/SSE upstreams as
    `notifications/cancelled` with the upstream's request id (HTTP/SSE used to send none), and the
    canceled request gets no response (`serve` now honors client cancels too). An upstream cancel of a
    relayed request reaches the client. Pending entries are freed at once in every case.
  - **Resource updates.** `resources.subscribe` is now advertised when an upstream supports it; upstream
    `notifications/resources/updated` reach the subscribed clients, namespaced. Clients share one upstream
    subscription (unsubscribe is forwarded when the last subscriber leaves or disconnects).
  - **Firewall.** Relayed requests are secret-redacted before the client and the client's answers before
    the upstream; sampling and elicitation text goes through the prompt-injection guard's response policy.
    See [`docs/security.md`](docs/security.md#server-to-client-traffic-308).
  - **Telemetry.** Each relayed request gets a SERVER span `<method> <server>`.
  - **Not yet.** Legacy SSE upstreams cannot send server-to-client requests (mcp-go's SSE transport drops
    them), so nothing is declared to them; their progress and resource updates are relayed. Upstream
    `notifications/message` (logging) is not relayed. stdio upstreams still get no `traceparent` in
    `params._meta`.

- **OpenTelemetry traces & metrics for the MCP pipeline** ([#317](https://github.com/mmornati/leanproxy-mcp/issues/317)).
  - **What.** Optional OTLP export, off by default, enabled by the standard `OTEL_EXPORTER_OTLP_ENDPOINT`
    / `OTEL_EXPORTER_OTLP_PROTOCOL` env vars or a `telemetry:` config block. Both front ends
    (`server run --stdio` and `serve`) get identical instrumentation from the unified middleware pipeline
    (`pkg/mcp`): one SERVER span per request (`<method> <tool>`), a child span per firewall/cache stage
    (`mcp.middleware.cache` / `.redact_request` / `.injection` / `.redact_response`), and a CLIENT span
    around the upstream call, following the GenAI/MCP semantic conventions (semconv v1.41.0). Histograms
    (request duration, response size), counters (requests, errors by `error.type`, redactions, injection
    detections by action, cache hits/misses, policy decisions, rate-limit waits) and an in-flight gauge are
    recorded; the existing `/metrics` JSON endpoint keeps working, now with a `telemetry` section fed by
    the same counters. Every HTTP/SSE upstream call carries a W3C `traceparent` header.
  - **Never payloads.** Spans and metrics carry only names, sizes, counts and status codes — never tool
    argument or result content.
  - **Performance.** Telemetry off (the default) costs a couple of atomic increments per request; the
    span/attribute machinery only runs once telemetry is actually enabled
    (`BenchmarkPipeline_TelemetryDisabled` in `pkg/mcp`, well under the 5% budget).
  - **Exporter.** A small hand-written OTLP/HTTP JSON exporter (`pkg/telemetry`) instead of
    `go.opentelemetry.io/otel/exporters/otlp/*`: those packages transitively pull in `google.golang.org/grpc`
    and `google.golang.org/protobuf` through a shared internal config package, adding roughly 6 MB to the
    binary — well over the +3 MB budget. The custom exporter measured about +2 MB.
  - **Docs.** New [`docs/observability.md`](docs/observability.md) with a Jaeger/otel-collector
    docker-compose example, and a new "Telemetry" section in `docs/configuration.md`.
  - **Not yet.** stdio upstreams do not carry `traceparent` in `params._meta` (only HTTP/SSE upstreams get
    the header); a follow-up can add it once the `_meta` convention for trace context is settled.

- **Prompt-injection defense v2: decoded text, tool outputs, valid actions, optional local judge** ([#315](https://github.com/mmornati/leanproxy-mcp/issues/315)).
  - **Decoded text.** The classifier ran its regexes over the raw JSON of `params`, so a JSON escape
    (`ignore\u0020previous instructions`, `ignore\tprevious…`) scored 0. The guard now walks the message
    with the redactor's lossless JSON scanner, classifies the *decoded* strings (keys included; JSON inside a
    string is opened) after normalization — compatibility folding, accents, zero-width/bidi/tag characters,
    Cyrillic/Greek look-alikes, case, whitespace — and samples head and tail beyond `max_scan_bytes`
    (256 KiB).
  - **Responses.** Tool results (`content[].text`, `structuredContent`), `resources/read` and `prompts/get`
    are classified too — indirect injection arrives there. New `injection.response_policies` (default:
    `annotate` from `threshold`, 70): `annotate` prepends a warning item, `redact` replaces the matching
    spans, `block` returns `isError: true` (a JSON-RPC error for resources/prompts), `log`. Requests use
    `injection.request_policies` (the old `policies` still works). `scan_responses: false` turns response
    classification off. Both front ends behave identically.
  - **Patterns.** Eight new patterns for tool-use hijacking and exfiltration (`tool-call-hijack`,
    `ai-directive`, `exfiltrate-secrets`, `send-to-url`, `exfiltrate-verb`, `markdown-image-beacon`,
    `hidden-instruction-tag`, `chat-template-token`); the original 14 keep names and weights but match on
    word boundaries and no longer fire on ordinary documents. Patterns declare `triggers`: one pass finds
    them and the regex runs only on small windows around them — a benign request check went from ~110 µs to
    ~3 µs; a 2 KiB tool round trip (request + response) costs ~26 µs, a 64 KiB result ~0.6 ms.
  - **Measured** (`TestClassify_ResponseCorpus`, 32 indirect injections vs 96 benign READMEs, docs, issues,
    code, e-mails, pages): precision 100%, recall 90.6% at the default threshold; 100% recall at any
    (logged) score. No false positive at the threshold on 4,474 Markdown files of the Go module cache nor on
    the Go standard library sources.
  - **Local judge.** Optional `injection.judge` (Ollama, off by default): regex scores in 30-80 get a
    strict-JSON yes/no verdict with a confidence, applied above `threshold`; 2 s timeout, falls back to the
    regex score.
  - **Behavior change.** With an `injection:` block enabled, flagged tool outputs are now annotated. Set
    `scan_responses: false` to keep the old request-only behavior.

- **MCP protocol upgrade, part 1: version negotiation, tool metadata and result passthrough, resources & prompts aggregation** ([#307](https://github.com/mmornati/leanproxy-mcp/issues/307)).
  - **Versions.** Both front ends negotiate MCP `2024-11-05`, `2025-03-26`, `2025-06-18` and `2025-11-25`
    (one list, `pkg/mcp/protocol.go`): a supported requested revision is echoed, anything else gets the
    latest. The revision is kept per client session (per connection in `serve`) and gates the fields
    LeanProxy adds, so a `2024-11-05` client sees no new field. The pool's handshake with every upstream
    now asks for the latest revision (it was hard-coded to `2024-11-05`) and stores the answer with the
    server's capabilities.
  - **Tool metadata.** Upstream tools keep `title`, `outputSchema`, `annotations`, `icons` and `_meta`
    (also in the persistent tool cache). `list_tools` and `search_tools` tag hinted tools `[read-only]` /
    `[destructive]`, and return the full tool objects as `structuredContent` to clients on `2025-06-18`+.
    The gateway tools advertise `readOnlyHint` to `2025-03-26`+ clients. Upstream `tools/list` pagination
    (`nextCursor`) is followed.
  - **Results.** `invoke_tool` returns the upstream `CallToolResult` unchanged apart from redaction
    (`structuredContent`, `isError`, `resource_link` and every other content type). HTTP/SSE upstreams now
    relay every MCP method as raw JSON instead of through mcp-go's typed results, which dropped fields and
    re-encoded numbers through float64; their JSON-RPC errors now reach the client with their code,
    message and data instead of a generic "tool call failed".
  - **Resources & prompts.** `resources/list`, `resources/templates/list` and `prompts/list` (which
    returned empty lists) now merge every upstream's lists in parallel, namespaced as
    `leanproxy://<server>/<uri>` and `<server>.<prompt>`; `resources/read`, `resources/subscribe` /
    `unsubscribe` and `prompts/get` (not proxied before) are routed to the owning server. `initialize`
    advertises `resources` / `prompts` only when an upstream serves them, with `listChanged: true`, and
    clients get `notifications/resources/list_changed` / `notifications/prompts/list_changed` when an
    upstream's lists change or it restarts. Everything is redacted. `serve` answers the same methods.
  - **Tests.** New conformance smoke test in the harness: an mcp-go client drives the real binary at every
    supported revision.
  - **Not yet** (story 20.4, [#308](https://github.com/mmornati/leanproxy-mcp/issues/308)): server-to-client
    requests (elicitation, sampling, roots), progress, cancellation relay and `notifications/resources/updated`
    (so `resources.subscribe` is not advertised). The `2026-07-28` revision is out of scope.

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

## Fixed in v0.11

- **A canceled HTTP/SSE call no longer tears down the upstream connection** ([#308](https://github.com/mmornati/leanproxy-mcp/issues/308)).
  The raw relay treated the error of a request whose caller gave up (a client cancel, a timeout) as a
  transport failure: it marked the server disconnected and reconnected — under every other call in flight
  on that connection — and retried the request on the caller's dead context. It now only reconnects on a
  real transport failure, and tells the upstream the request was canceled.
- **`SendServerNotification` reaches HTTP and SSE upstreams** ([#308](https://github.com/mmornati/leanproxy-mcp/issues/308)).
  It was a silent no-op for both (so, for example, a client's `notifications/roots/list_changed` could
  not be forwarded); an unknown server is now an error, as for stdio. A stdio server whose session is not
  initialized yet no longer gets a notification before its `initialize`.

- **Injection actions return valid, honest results** ([#315](https://github.com/mmornati/leanproxy-mcp/issues/315)).
  The dispatcher's `redact` action produced `[CONTENT_REDACTED]` as the new params (not valid JSON), and the
  middleware replaced every string argument; now only the matching spans are replaced and the params stay
  valid JSON (routing fields kept). A quarantined request returns `isError: true` (or a JSON-RPC error for a
  non-tool method) carrying the quarantine ID, never a success.
- **Sidecar output can no longer rewrite a call** ([#315](https://github.com/mmornati/leanproxy-mcp/issues/315), audit S10).
  `RedactJSONWithSidecar` accepted any valid JSON the local LLM returned as the new params, and `serve`
  re-read the tool name from it, so text planted in the arguments could steer the model into calling another
  tool (`read_file` → `write_file`). The output must now have the input's structure (same keys, types,
  numbers, routing fields; strings may only shrink or be masked); otherwise it is discarded with a warning
  and the regex-redacted params are forwarded. The tool name always comes from the original request.

- **`serve` never answers `resources/read` / `prompts/get` from the semantic cache** ([#307](https://github.com/mmornati/leanproxy-mcp/issues/307)).
  The semantic (embedding-similarity) cache in `serve` was consulted for every routed method other than
  `tools/call`, so a resource read or a prompt could be replayed stale — or for a merely *similar* request.
  It now follows the stdio front end's cache policy: only a tool addressed by its namespaced method that the
  `response_cache.tools` allowlist declares cacheable, never an MCP protocol method, never an `isError` result.
- **`serve` simple gateway mode relays `invoke_tool` arguments byte for byte** ([#307](https://github.com/mmornati/leanproxy-mcp/issues/307)).
  `gateway.InvokeToolParams.Arguments` was a `map[string]any`, so every number went through float64 and an
  integer above 2^53 was corrupted (`9007199254740993` became `…992`). It is now a `json.RawMessage`,
  validated as a JSON object and relayed unchanged.

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
