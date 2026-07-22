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

Loopback addresses (`127.0.0.1`, `localhost`, `::1`) bypass authentication automatically. For remote access, set a token:

```bash
leanproxy-mcp serve --dashboard-bind 0.0.0.0:9090 --dashboard-token my-secret-token
```

Include the token on every request:

```
Authorization: Bearer my-secret-token
```

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

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/` | GET | Main dashboard HTML with 5s auto-refresh |
| `/api/dashboard` | GET | HTML partial for summary cards |
| `/api/dashboard/json` | GET | Full JSON payload |
| `/api/dashboard/servers` | GET | HTML table of all servers |
| `/api/dashboard/servers/{server}` | GET | Drill-down for a specific server |
| `/api/dashboard/servers/{server}/tools/{tool}/prompts` | GET | Prompt hashes for a tool |
| `/static/...` | GET | Static assets (htmx.min.js) |

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

Disable with `--metrics-bind off` or omit the flag.

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

- [Budget Management](./budget.md) — Set team/project spending limits
- [Configuration Reference](./configuration.md) — Dashboard config options
- [Commands Reference](./commands.md) — Full CLI documentation
