# Web Dashboard and `/metrics`

LeanProxy-MCP has a small web dashboard and a JSON `/metrics` endpoint. They
show how many tokens the proxy handled and saved today and this week, per
upstream server and per tool. Both front ends have them: `server run`
(`--stdio` and `--http`) and the deprecated `serve`.

## Where the numbers come from

Both read the **usage store** (`~/.leanproxy/usage/`, see
[Savings Report](savings-report.md#where-the-numbers-come-from)). Every
front end (`server run --stdio`, `server run --http`, `serve`) appends a
snapshot of its real counters to it every 5 seconds, and
`leanproxy-mcp report` reads the same store. As a result:

- the dashboard covers **every proxy process on this machine**, not just
  the one serving it: a dashboard on a `server run --http` gateway also
  shows the `server run --stdio` sessions your IDEs started;
- the numbers survive restarts, and a new process starts with the day's
  and week's totals so far;
- every figure is measured, counted with the same `chars/4` estimator
  `report` uses. Nothing is modeled or priced.

Two windows are shown:

| Window | Starts at |
|--------|-----------|
| **Today** | 00:00 UTC of the current day |
| **Week to date** | 00:00 UTC of the current ISO week's Monday |

Each process's snapshots are cumulative, so a window counts, per process,
only what it recorded **inside** the window (its latest snapshot minus its
last snapshot before the window started). A proxy running since yesterday
contributes to today only what it did today, and the week-to-date figure
is genuinely larger than today's from the second day of the week on.

The totals (`original_tokens`, `saved_tokens`, `saved_percent`) are the
same as `report`'s `total_original_tokens` / `total_saved_tokens` /
`total_saved_percent`: schema savings plus the response governor's.
Discovery (`search_tools`, `list_tools`, `list_servers`) is reported
separately as a cost.

!!! note "Per-server and per-tool figures need the response governor"
    The per-server and per-tool rows (and so the top server and top tool)
    come from the response governor's per-tool accounting, the same data
    as `report --by tool|server`. The governor is off by default, so those
    rows stay empty until you set `response.enabled: true` (see
    [Configuration](configuration.md#response-token-governor-response)).
    The totals still include schema savings without it.

## Enabling the Dashboard

`serve` starts the dashboard on `127.0.0.1:9090` by default. `server run`
has the same flags, off by default, since an MCP client may start several
`server run --stdio` processes and they cannot all bind the same port.
Enable it on one process, typically a shared `server run --http` gateway:

```bash
# server run (the recommended front end)
leanproxy-mcp server run --http 127.0.0.1:8765 --dashboard-bind 127.0.0.1:9090

# serve (deprecated line-TCP front end): on by default
leanproxy-mcp serve

# Another address
leanproxy-mcp serve --dashboard-bind 127.0.0.1:9095

# Disable the dashboard
leanproxy-mcp serve --dashboard-bind off
```

!!! note "Port 9090 is taken by default under `serve`"
    Because `serve`'s dashboard binds `127.0.0.1:9090` by default, any other
    listener you give `serve` on that address (`--listen` or
    `--metrics-bind`) fails to start. Pick another port, or move the
    dashboard.

| Flag | Default | Description |
|------|---------|-------------|
| `--dashboard-bind` | `serve`: `127.0.0.1:9090`; `server run`: off | Bind address. `off` or empty disables the dashboard. |
| `--dashboard-token` | none | Bearer token. Required on a non-loopback bind. |
| `--dashboard-allowed-hosts` | none | Extra `Host` header values to accept. |

### Authentication

A non-loopback `--dashboard-bind` (anything but `127.0.0.1`, `localhost` or
`::1`) **requires** `--dashboard-token`. Without one, the proxy refuses to
start rather than expose the dashboard unauthenticated:

```bash
leanproxy-mcp server run --http 127.0.0.1:8765 --dashboard-bind 0.0.0.0:9090 --dashboard-token my-secret-token
```

A loopback bind works without a token. Once a token **is** configured,
every client must send it, loopback included. There is no bypass for local
processes.

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

| Section | Content |
|---------|---------|
| Summary cards | Tokens saved today and this week (with the original tokens and the percentage saved), and today's top server and top tool by response size. If the usage store cannot be read, the cards say so instead of showing zeros. |
| Server table | Today's servers, largest first: tools, calls, response tokens (before the governor), tokens returned to the client, and tokens saved. With the governor off, it explains how to enable it. |
| Drill-down | Click a server to see its tools today: calls, response, returned and saved tokens, and the average response size per call. The usage store never records payloads or prompt hashes, so there is no per-prompt drill-down. |
| Tool pinning | The latest tool pinning events of the process serving the dashboard, newest first, refreshed every 10 seconds. |

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
| `/api/dashboard/json` | GET | The usage summary as JSON (below); `503` when the usage store cannot be read |
| `/api/dashboard/servers` | GET | HTML table of today's servers |
| `/api/dashboard/servers/{server}` | GET | HTML drill-down: today's tools for one server |
| `/api/dashboard/tool-pins` | GET | HTML table of the latest 50 tool pinning events |
| `/static/...` | GET | Static assets (`htmx.min.js`) |
| `/login?token=…` | GET | Exchanges a valid token for a session cookie |

### JSON Response Format

`/api/dashboard/json` returns the usage summary. `/metrics` serves the
same object under `usage`:

```json
{
  "estimator": "chars/4",
  "today": {
    "since": "2026-09-23T00:00:00Z",
    "sessions": 2,
    "original_tokens": 13500,
    "saved_tokens": 10850,
    "saved_percent": 80.4,
    "discovery_calls": 3,
    "discovery_tokens": 420,
    "tool_calls": 4,
    "top_server": "fs",
    "top_tool": "fs.read_file",
    "by_server": [
      {"server": "fs", "tools": 1, "calls": 2, "original_tokens": 10000, "returned_tokens": 2000, "saved_tokens": 8000},
      {"server": "github", "tools": 1, "calls": 2, "original_tokens": 2000, "returned_tokens": 500, "saved_tokens": 1500}
    ],
    "by_tool": [
      {"server": "fs", "tool": "read_file", "calls": 2, "original_tokens": 10000, "returned_tokens": 2000, "saved_tokens": 8000},
      {"server": "github", "tool": "search", "calls": 2, "original_tokens": 2000, "returned_tokens": 500, "saved_tokens": 1500}
    ]
  },
  "week": { "since": "2026-09-21T00:00:00Z", "...": "same fields, week to date" }
}
```

The numbers above are illustrative. `by_server` and `by_tool` are empty
lists (never `null`) when the governor is off, and `top_server` /
`top_tool` are then omitted. A tool the governor could not attribute to a
server has `"server": ""`.

## Metrics Endpoint

`/metrics` returns a **JSON** snapshot. It is not in the Prometheus text
format. It is the endpoint the [IDE extensions](extensions.md) read,
available on both front ends and off by default:

```bash
leanproxy-mcp server run --http 127.0.0.1:8765 --metrics-bind 127.0.0.1:9091
leanproxy-mcp serve --metrics-bind 127.0.0.1:9091
```

`127.0.0.1:9091` is the extensions' default address. `9090` is the
dashboard's, and it has no `/metrics` route.

| Flag | Default | Description |
|------|---------|-------------|
| `--metrics-bind` | empty (disabled) | Bind address. `off` or empty disables it. Under `serve`, do not use `127.0.0.1:9090` while the dashboard is on its default address. |
| `--metrics-token` | none | Bearer token (`Authorization: Bearer <token>`). Required on a non-loopback bind. There is no cookie login. |
| `--metrics-allowed-hosts` | none | Extra `Host` header values to accept. |

The endpoint accepts only `GET`. Host validation is the same as for the
dashboard. The response is indented JSON, or compact JSON when the request
sends `Accept: application/json`.

### Metrics output

```json
{
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
  },
  "usage": {
    "estimator": "chars/4",
    "today": {"...": "see JSON Response Format above"},
    "week": {"...": "..."}
  }
}
```

The numbers above are illustrative.

| Field | Source | Notes |
|-------|--------|-------|
| `response_cache` | [Response cache](./configuration.md#response-cache) | This process. Present once the front end has set up the cache, with `enabled: false` when the cache is off. |
| `response_governor` | [Response governor](./configuration.md#response-token-governor-response) | This process. Omitted while the governor is off. Per-tool entries also carry `projected`, `dedup_hits` and similar fields when non-zero. |
| `telemetry` | Pipeline counters | This process, since it started. Always present. Counted whether or not an OTLP exporter is configured. |
| `usage` | [Usage store](#where-the-numbers-come-from) | Today and week to date, across every process on the machine (see [JSON Response Format](#json-response-format)). Omitted when the usage store cannot be read. |

The endpoint used to carry `total_spend`, `by_tool`, `by_server` and
`top_5_expensive_tools`, fed by a cost tracker nothing in the pipeline
called; they were always zero or empty and have been removed. Read
`usage.today` / `usage.week` instead.

## Exporting data

The dashboard has no export. For a full, auditable breakdown by mechanism
over any period, `leanproxy-mcp report` reads the same usage store and can
export it:

```bash
leanproxy-mcp report --export csv --output usage.csv
leanproxy-mcp report --export json --output usage.json
leanproxy-mcp report --export csv --since 2026-06-01
```

See [Savings Report](./savings-report.md).

## Next Steps

- [Savings Report](./savings-report.md) — how every number is measured
- [IDE Extensions](./extensions.md) — the same numbers in VS Code and JetBrains
- [Observability](./observability.md) — OpenTelemetry traces and metrics
- [Commands Reference](./commands.md) — Full CLI documentation
