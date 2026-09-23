# Web Dashboard

LeanProxy-MCP includes a real-time web dashboard for monitoring token usage, server activity, and cost attribution across your MCP infrastructure.

## Enabling the Dashboard

Start the dashboard with `serve`:

```bash
# Default dashboard on 127.0.0.1:9090
leanproxy-mcp serve --dashboard-bind 127.0.0.1:9090

# Disable the dashboard
leanproxy-mcp serve --dashboard-bind off
```

### Authentication

A non-loopback `--dashboard-bind` (anything but `127.0.0.1`, `localhost` or
`::1`) **requires** `--dashboard-token`; without one, `serve` refuses to
start rather than exposing the dashboard unauthenticated:

```bash
leanproxy-mcp serve --dashboard-bind 0.0.0.0:9090 --dashboard-token my-secret-token
```

A loopback bind (the default, `127.0.0.1:9090`) works without a token. Once
a token **is** configured, it is required from every client, loopback
included — there is no bypass for local processes.

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
`--dashboard-allowed-hosts`; anything else — including a page from an
unrelated site trying to reach `127.0.0.1:9090` from a victim's browser
(DNS rebinding) — gets `403 Forbidden`. A state-changing request whose
`Origin` header does not match the request's own host is rejected the same
way. Every response also carries a same-origin `Content-Security-Policy`,
`X-Frame-Options: DENY`, `Referrer-Policy: no-referrer` and
`X-Content-Type-Options: nosniff`.

## Dashboard UI

The dashboard is an HTMX-powered HTML page at `http://127.0.0.1:9090/` with auto-refresh every 5 seconds.

### Summary Cards

| Metric | Description |
|--------|-------------|
| **Today's Spend** | Total tokens consumed today |
| **WTD Spend** | Week-to-date token consumption |
| **Top Server** | Server with highest token usage |
| **Top Tool** | Tool with highest token usage |

### Server Table

Real-time table of all servers with per-server token counts, automatically updated.

### Drill-Down

Click any server to drill into per-tool breakdown, then click a tool to see individual prompt hashes.

### Tool Pinning

The latest tool pinning events of the `serve` process (#310), newest first,
refreshed every 10 seconds: servers pinned on first use, tools added, changed
or removed, server identity changes, scanner findings and cross-server name
collisions, with server, tool and severity. Review and approve them with
`leanproxy-mcp tools pins diff` / `approve` (see [Commands](./commands.md#tools-pins-tool-pinning)).

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/` | GET | Main dashboard HTML with 5s auto-refresh |
| `/api/dashboard` | GET | HTML partial for summary cards |
| `/api/dashboard/json` | GET | Full JSON payload |
| `/api/dashboard/servers` | GET | HTML table of all servers |
| `/api/dashboard/servers/{server}` | GET | Drill-down for a specific server |
| `/api/dashboard/servers/{server}/tools/{tool}/prompts` | GET | Prompt hashes for a tool |
| `/api/dashboard/tool-pins` | GET | HTML table of the latest tool pinning events (#310) |
| `/static/...` | GET | Static assets (htmx.min.js) |
| `/login?token=…` | GET | Exchanges a valid dashboard token for an `HttpOnly` session cookie |

### JSON Response Format

```json
{
  "today_spend": 35000,
  "wtd_spend": 35000,
  "top_server": "github",
  "top_tool": "create_issue",
  "server_count": 5,
  "tool_count": 23,
  "per_server": [
    {"server": "github", "tokens": 15000}
  ],
  "per_tool": [
    {"tool": "create_issue", "tokens": 8000}
  ]
}
```

## Metrics Endpoint

A separate Prometheus-style metrics endpoint is available:

```bash
leanproxy-mcp serve --metrics-bind 127.0.0.1:9091
```

Disable with `--metrics-bind off` or omit the flag. Like the dashboard, a
non-loopback bind requires `--metrics-token` (sent as
`Authorization: Bearer <token>`) or `serve` refuses to start, and the same
Host validation applies.

### Metrics Output

```json
{
  "total_spend": 35000,
  "top_5_expensive_tools": ["create_issue", "search_code"],
  "servers": [
    {"name": "github", "tokens": 15000, "requests": 42}
  ],
  "tools": [
    {"name": "create_issue", "tokens": 8000, "requests": 12}
  ]
}
```

## CSV/JSON Cost Export

Export raw cost data for external analysis:

```bash
# Export as CSV
leanproxy-mcp report --export csv --output cost-report.csv

# Export as JSON
leanproxy-mcp report --export json --output cost-report.json

# Filter by date range
leanproxy-mcp report --export csv --since 2026-06-01
```

## Next Steps

- [Configuration Reference](./configuration.md) — Dashboard config options
- [Commands Reference](./commands.md) — Full CLI documentation
