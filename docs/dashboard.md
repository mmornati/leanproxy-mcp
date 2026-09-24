# Web Dashboard

LeanProxy-MCP includes a web dashboard and a `/metrics` JSON endpoint that
show how many tokens the proxy handled and saved today and this week, per
upstream server and per tool.

## Where the numbers come from

Both read the **usage store** (`~/.leanproxy/usage/`, see
[Savings Report](savings-report.md#where-the-numbers-come-from)): every
front end (`server run --stdio`, `server run --http`, `serve`) appends a
snapshot of its real counters to it every 5 seconds. `leanproxy-mcp report`
reads the same store. As a result:

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
    [Configuration](configuration.md)). The totals still include schema
    savings without it.

## Enabling the Dashboard

`serve` starts the dashboard on `127.0.0.1:9090` by default. `server run`
has the same flags, off by default, since an MCP client may start several
`server run --stdio` processes and they cannot all bind the same port.
Enable it on one process, typically a shared `server run --http` gateway:

```bash
# server run (the recommended front end)
leanproxy-mcp server run --http 127.0.0.1:8765 --dashboard-bind 127.0.0.1:9090

# serve (deprecated line-TCP front end): on by default
leanproxy-mcp serve --dashboard-bind 127.0.0.1:9090

# Disable the dashboard
leanproxy-mcp serve --dashboard-bind off
```

### Authentication

A non-loopback `--dashboard-bind` (anything but `127.0.0.1`, `localhost` or
`::1`) **requires** `--dashboard-token`; without one, the proxy refuses to
start rather than exposing the dashboard unauthenticated:

```bash
leanproxy-mcp server run --http 127.0.0.1:8765 --dashboard-bind 0.0.0.0:9090 --dashboard-token my-secret-token
```

A loopback bind works without a token. Once a token **is** configured, it
is required from every client, loopback included. There is no bypass for
local processes.

Include the token on every request:

```
Authorization: Bearer my-secret-token
```

Or, from a browser, exchange it once for a cookie:

```
GET /login?token=my-secret-token
```

which sets an `HttpOnly`, `SameSite=Strict` cookie (also `Secure` when
served over TLS) so subsequent page loads don't need the header.

### Host and Origin validation

The dashboard only serves requests whose `Host` header is the bind host,
`localhost`, `127.0.0.1` or `[::1]` (with the listening port), or one of
`--dashboard-allowed-hosts`. Anything else gets `403 Forbidden`, including
a page from an unrelated site trying to reach `127.0.0.1:9090` from a
victim's browser (DNS rebinding). A state-changing request whose
`Origin` header does not match the request's own host is rejected the same
way. Every response also carries a same-origin `Content-Security-Policy`,
`X-Frame-Options: DENY`, `Referrer-Policy: no-referrer` and
`X-Content-Type-Options: nosniff`.

## Dashboard UI

The dashboard is an HTMX-powered HTML page at `http://127.0.0.1:9090/` with auto-refresh every 5 seconds.

### Summary Cards

| Card | Description |
|------|-------------|
| **Saved today** | Tokens saved today, with the original tokens and the percentage saved |
| **Saved this week** | The same, week to date |
| **Top server today** | Upstream server with the largest tool responses today |
| **Top tool today** | Tool (`server.tool`) with the largest responses today |

If the usage store cannot be read, the cards say so instead of showing zeros.

### Server Table

Today's servers, largest first: tools, calls, response tokens (before the
governor), tokens returned to the client, and tokens saved. With the
governor off, the table explains how to enable it.

### Drill-Down

Click a server to see its tools today: calls, response tokens, returned
tokens, saved tokens and the average response size per call. The usage
store never records payloads or prompt hashes, so there is no per-prompt
drill-down.

### Tool Pinning

The latest tool pinning events of the process serving the dashboard (#310),
newest first, refreshed every 10 seconds. They cover servers pinned on
first use, tools added, changed or removed, server identity changes,
scanner findings and cross-server name collisions, each with its server,
tool and severity. Review and approve them with
`leanproxy-mcp tools pins diff` / `approve` (see [Commands](./commands.md#tools-pins-tool-pinning)).

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/` | GET | Main dashboard HTML with 5s auto-refresh |
| `/api/dashboard` | GET | HTML partial for the summary cards |
| `/api/dashboard/json` | GET | The usage summary as JSON (below); `503` when the usage store cannot be read |
| `/api/dashboard/servers` | GET | HTML table of today's servers |
| `/api/dashboard/servers/{server}` | GET | Today's tools for one server |
| `/api/dashboard/tool-pins` | GET | HTML table of the latest tool pinning events (#310) |
| `/static/...` | GET | Static assets (htmx.min.js) |
| `/login?token=…` | GET | Exchanges a valid dashboard token for an `HttpOnly` session cookie |

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

`by_server` and `by_tool` are empty lists (never `null`) when the governor
is off. A tool the governor could not attribute to a server has
`"server": ""`.

## Metrics Endpoint

A separate JSON metrics endpoint, the one the [IDE extensions](extensions.md)
read, is available on both front ends. It is off by default:

```bash
leanproxy-mcp server run --http 127.0.0.1:8765 --metrics-bind 127.0.0.1:9091
leanproxy-mcp serve --metrics-bind 127.0.0.1:9091
```

`127.0.0.1:9091` is the extensions' default address. `9090` is the
dashboard's, and it has no `/metrics` route. Disable the endpoint with
`--metrics-bind off` or omit the flag. As with the dashboard, a non-loopback
bind requires `--metrics-token` (sent as `Authorization: Bearer <token>`),
otherwise the proxy refuses to start, and the same Host validation applies.

### Metrics Output

`GET /metrics` returns this process's live counters plus the usage store's
windows:

```json
{
  "response_cache": {"enabled": true, "hits": 12, "misses": 30, "evictions": 0, "bytes": 48213, "entries": 30},
  "response_governor": {"enabled": true, "results": 4, "original_tokens": 12000, "returned_tokens": 2500, "by_tool": ["..."]},
  "telemetry": {"requests_total": 42, "errors_total": 1, "schema_native_tokens_total": 5000, "...": "..."},
  "usage": {"estimator": "chars/4", "today": {"...": "..."}, "week": {"...": "..."}}
}
```

- `response_cache` and `response_governor` are omitted while those
  features are off; `telemetry` is always present (see
  [Observability](observability.md#existing-metrics-json-endpoint)).
  These are **this process's** counters since it started.
- `usage` is the [usage summary](#json-response-format) across every
  process on the machine. It is omitted when the usage store cannot be read.

The endpoint used to carry `total_spend`, `by_tool`, `by_server` and
`top_5_expensive_tools`, fed by a cost tracker nothing in the pipeline
called; they were always zero or empty and have been removed. Read
`usage.today` / `usage.week` instead.

## CSV/JSON Export

For a full, auditable breakdown by mechanism over any period, use
[`report`](savings-report.md):

```bash
# Export as CSV
leanproxy-mcp report --export csv --output savings.csv

# Export as JSON
leanproxy-mcp report --export json --output savings.json

# Filter by date
leanproxy-mcp report --export csv --since 2026-06-01
```

## Next Steps

- [Savings Report](savings-report.md): how every number is measured
- [IDE Extensions](extensions.md): the same numbers in VS Code and JetBrains
- [Commands Reference](./commands.md): full CLI documentation
