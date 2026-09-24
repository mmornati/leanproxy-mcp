# Web Dashboard and `/metrics`

LeanProxy-MCP has a small web dashboard and a JSON `/metrics` endpoint.

!!! warning "Only available under the deprecated `serve` command"
    The dashboard and `/metrics` are started only by `leanproxy-mcp serve`,
    the line-TCP front end that is deprecated and scheduled for removal in
    v1.0. No MCP client speaks the `serve` protocol, and the front ends that
    clients do use, `server run --stdio` and `server run --http`, start
    neither the dashboard nor `/metrics`.

    For savings and usage numbers from any front end, use
    [`leanproxy-mcp report`](./savings-report.md). For live metrics, use
    [OpenTelemetry](./observability.md).

!!! warning "Token and spend figures always read zero"
    Every token and spend figure on the dashboard (today's spend, WTD spend,
    top server, top tool, the server table, the per-tool drill-down and the
    prompt hashes) and the `by_tool`, `by_server`, `total_spend` and
    `top_5_expensive_tools` fields of `/metrics` come from an in-process cost
    tracker that nothing in the proxy currently feeds. They are always `0`,
    `-` or empty, however much traffic goes through the proxy.

    What does work: the **tool pinning** panel of the dashboard, and the
    `telemetry`, `response_cache` and `response_governor` blocks of
    `/metrics` (see [Metrics output](#metrics-output)).

## Enabling the Dashboard

The dashboard starts with `serve` by default, on `127.0.0.1:9090`:

```bash
# Default: dashboard on 127.0.0.1:9090
leanproxy-mcp serve

# Another address
leanproxy-mcp serve --dashboard-bind 127.0.0.1:9095

# Disable the dashboard
leanproxy-mcp serve --dashboard-bind off
```

!!! note "Port 9090 is taken by default"
    Because the dashboard binds `127.0.0.1:9090` by default, any other
    listener you give `serve` on that address (`--listen` or
    `--metrics-bind`) fails to start. Pick another port, or move the
    dashboard.

| Flag | Default | Description |
|------|---------|-------------|
| `--dashboard-bind` | `127.0.0.1:9090` | Bind address. `off` or empty disables the dashboard. |
| `--dashboard-token` | none | Bearer token. Required on a non-loopback bind. |
| `--dashboard-allowed-hosts` | none | Extra `Host` header values to accept. |

### Authentication

A non-loopback `--dashboard-bind` (anything but `127.0.0.1`, `localhost` or
`::1`) **requires** `--dashboard-token`. Without one, `serve` refuses to
start rather than expose the dashboard unauthenticated:

```bash
leanproxy-mcp serve --dashboard-bind 0.0.0.0:9090 --dashboard-token my-secret-token
```

A loopback bind (the default) works without a token. Once a token **is**
configured, every client must send it, loopback included. There is no bypass
for local processes.

Send the token on every request:

```
Authorization: Bearer my-secret-token
```

Or, from a browser, exchange it once for a cookie:

```
GET /login?token=my-secret-token
```

This sets an `HttpOnly`, `SameSite=Strict` cookie (also `Secure` when served
over TLS), so later page loads need no header. Without a configured token,
`/login` returns 404.

The dashboard does not use TLS itself. On a non-loopback bind, traffic,
including the token, is sent in clear text unless you put a TLS proxy in
front of it.

### Host and Origin validation

The dashboard only serves requests whose `Host` header is the bind host,
`localhost`, `127.0.0.1` or `[::1]` (with the listening port), or one of
`--dashboard-allowed-hosts`. Anything else gets `403 Forbidden`. This blocks
DNS rebinding: a page on an unrelated site cannot reach `127.0.0.1:9090`
through a victim's browser. A state-changing request whose `Origin` header
does not match the request's own host is rejected the same way. Every
response also carries a same-origin `Content-Security-Policy`,
`X-Frame-Options: DENY`, `Referrer-Policy: no-referrer` and
`X-Content-Type-Options: nosniff`.

## Dashboard UI

The dashboard is an HTMX page at `http://127.0.0.1:9090/` that refreshes
every 5 seconds.

| Section | Content | Status |
|---------|---------|--------|
| Summary cards | Today's spend, WTD spend, top server, top tool | Always `0` / `-`. WTD is the same number as today. |
| Server table | Per-server token counts | Always empty |
| Drill-down | Per-tool counts of a server, then prompt hashes of a tool | Always empty |
| Tool pinning | The latest tool pinning events of the `serve` process, newest first, refreshed every 10 seconds | **Works** |

The tool pinning panel lists servers pinned on first use, tools added,
changed or removed, server identity changes, scanner findings and cross-server
name collisions, with server, tool and severity. Review and approve them with
`leanproxy-mcp tools pins diff` and `approve` (see
[Commands](./commands.md#tools-pins-tool-pinning)).

## API Endpoints

| Endpoint | Method | Returns |
|----------|--------|---------|
| `/` | GET | Dashboard HTML page |
| `/api/dashboard` | GET | HTML fragment with the summary cards |
| `/api/dashboard/json` | GET | Summary as JSON (see below) |
| `/api/dashboard/servers` | GET | HTML rows of the server table |
| `/api/dashboard/servers/{server}` | GET | HTML drill-down for one server |
| `/api/dashboard/servers/{server}/tools/{tool}/prompts` | GET | HTML list of prompt hashes for one tool |
| `/api/dashboard/tool-pins` | GET | HTML table of the latest 50 tool pinning events |
| `/static/...` | GET | Static assets (`htmx.min.js`) |
| `/login?token=…` | GET | Exchanges a valid token for a session cookie |

`/api/dashboard/json` returns:

```json
{
  "today_spend": 0,
  "wtd_spend": 0,
  "top_server": "",
  "top_tool": "",
  "server_count": 0,
  "tool_count": 0,
  "per_server": [
    {"server": "github", "tokens": 0}
  ],
  "per_tool": [
    {"tool": "create_issue", "tokens": 0}
  ]
}
```

With the tracker unfed, `per_server` and `per_tool` are empty arrays.

## Metrics Endpoint

`/metrics` returns a **JSON** snapshot. It is not in the Prometheus text
format. It is off by default:

```bash
leanproxy-mcp serve --metrics-bind 127.0.0.1:9091
```

| Flag | Default | Description |
|------|---------|-------------|
| `--metrics-bind` | empty (disabled) | Bind address. `off` or empty disables it. Do not use `127.0.0.1:9090` while the dashboard is on its default address. |
| `--metrics-token` | none | Bearer token (`Authorization: Bearer <token>`). Required on a non-loopback bind. There is no cookie login. |
| `--metrics-allowed-hosts` | none | Extra `Host` header values to accept. |

The endpoint accepts only `GET`. Host validation is the same as for the
dashboard. The response is indented JSON, or compact JSON when the request
sends `Accept: application/json`.

### Metrics output

```json
{
  "by_tool": [],
  "by_server": [],
  "total_spend": 0,
  "top_5_expensive_tools": [],
  "response_cache": {
    "enabled": true,
    "hits": 12,
    "misses": 40,
    "evictions": 0,
    "bytes": 183422,
    "entries": 38
  },
  "response_governor": {
    "enabled": true,
    "max_tokens": 4000,
    "results": 52,
    "truncated": 7,
    "spilled": 7,
    "read_result_calls": 2,
    "original_tokens": 91230,
    "returned_tokens": 40112,
    "saved_tokens": 51118,
    "projected": 0,
    "projection_saved_tokens": 0,
    "projection_rules": 0,
    "default_projections": false,
    "dedup_enabled": false,
    "dedup_hits": 0,
    "dedup_saved_tokens": 0,
    "summarize_enabled": false,
    "summarized": 0,
    "summarize_saved_tokens": 0,
    "summarize_fallbacks": 0,
    "store": {"entries": 7, "bytes": 402113, "evictions": 0, "expirations": 0, "disk": false},
    "by_tool": [
      {"tool": "github.search_code", "results": 9, "truncated": 4, "original_tokens": 30211, "returned_tokens": 12000}
    ]
  },
  "telemetry": {
    "requests_total": 140,
    "errors_total": 3,
    "redactions_total": 2,
    "injection_detections_total": 0,
    "cache_hits_total": 12,
    "cache_misses_total": 40,
    "policy_decisions_total": 52,
    "rate_limit_waits_total": 0,
    "tool_pin_events_total": 1,
    "requests_in_flight": 0,
    "governor_results_total": 52,
    "governor_truncations_total": 7,
    "governor_tokens_saved_total": 51118,
    "governor_projections_total": 0,
    "governor_projection_tokens_saved_total": 0,
    "governor_dedup_hits_total": 0,
    "governor_dedup_tokens_saved_total": 0,
    "governor_summarizations_total": 0,
    "governor_summarization_tokens_saved_total": 0,
    "governor_summarization_fallbacks_total": 0,
    "schema_listings_total": 4,
    "schema_native_tokens_total": 48210,
    "schema_sent_tokens_total": 1630,
    "discovery_calls_total": 11,
    "discovery_tokens_total": 5120
  }
}
```

The numbers above are illustrative.

| Field | Source | Notes |
|-------|--------|-------|
| `by_tool` (`[{tool_name, token_count}]`), `by_server` (`[{server_name, token_count}]`), `total_spend`, `top_5_expensive_tools` (`[{tool_name, token_count}]`) | Cost tracker | Always empty or `0` (see the warning at the top). |
| `response_cache` | [Response cache](./configuration.md#response-cache) | Always present under `serve`, with `enabled: false` when the cache is off. |
| `response_governor` | [Response governor](./configuration.md#response-token-governor-response) | Omitted while the governor is off. Per-tool entries also carry `projected`, `dedup_hits` and similar fields when non-zero. |
| `telemetry` | Pipeline counters | Always present. Counted whether or not an OTLP exporter is configured. |

## Exporting data

The dashboard has no export. `leanproxy-mcp report` reads the usage records
that every front end writes, and can export them:

```bash
leanproxy-mcp report --export csv --output usage.csv
leanproxy-mcp report --export json --output usage.json
leanproxy-mcp report --export csv --since 2026-06-01
```

See [Savings Report](./savings-report.md).

## Next Steps

- [Savings Report](./savings-report.md) — measured savings for every front end
- [Observability](./observability.md) — OpenTelemetry traces and metrics
- [Commands Reference](./commands.md) — Full CLI documentation
