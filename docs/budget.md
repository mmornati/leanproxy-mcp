# Budget Management

LeanProxy-MCP includes a budget governance system for managing token consumption across teams and projects, with soft/hard caps and webhook alerts.

## Configuration

Budgets are configured in `leanproxy.yaml`:

```yaml
budgets:
  webhook_url: "https://hooks.example.com/hook"
  teams:
    engineering:
      daily: 1000000
      monthly: 20000000
      hard_cap: true
      soft_cap_pct: 80.0
      projects:
        frontend:
          daily: 500000
          monthly: 10000000
        backend:
          monthly: 15000000
    data-science:
      daily: 2000000
      monthly: 50000000
      hard_cap: false
```

### Budget Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `daily` | int | 0 | Daily token budget (0 = unlimited) |
| `monthly` | int | 0 | Monthly token budget (0 = unlimited) |
| `hard_cap` | bool | false | Reject requests when exceeded |
| `soft_cap_pct` | float | 90.0 | Percentage that triggers downgrade (0-100) |
| `webhook_url` | string | — | Override global webhook URL |

## How It Works

### Budget Levels

1. **Team-level**: Daily budget uses a token-bucket algorithm (resets at midnight). Monthly budget is a calendar-month counter.
2. **Project-level**: Monthly only, nested under the parent team.

### Actions

| Action | Description |
|--------|-------------|
| `allow` | Request passes through normally |
| `downgrade` | Soft cap reached — throttle or degrade service |
| `reject` | Hard cap hit — request is blocked |

### Hard Cap vs Soft Cap

- **Hard cap** (`hard_cap: true`): Requests are **rejected** when daily OR monthly usage exceeds the budget.
- **Soft cap**: At `soft_cap_pct` (default 90%), usage triggers a downgrade action. Below that, requests are allowed.

## Webhook Alerts

Webhooks fire when a project threshold (80% by default) is exceeded during a `Deduct` operation.

### Webhook Payload

```json
{
  "team": "engineering",
  "project": "frontend",
  "metric": "daily",
  "usage": 450000,
  "limit": 500000,
  "percentage": 90.0,
  "timestamp": "2026-07-22T10:30:00Z"
}
```

### Configuration

```yaml
budgets:
  webhook_url: "https://hooks.example.com/alert"
  teams:
    engineering:
      webhook_url: "https://hooks.internal/eng-budget"  # per-team override
```

## CLI Commands

### Cost Attribution

```bash
# Full cost breakdown
leanproxy-mcp cost

# By tool only
leanproxy-mcp cost --by-tool

# By server only
leanproxy-mcp cost --by-server

# JSON output
leanproxy-mcp cost --json

# Reset counters
leanproxy-mcp cost --reset
```

### Cost Report

```bash
# Generate report
leanproxy-mcp report

# Export cost data
leanproxy-mcp report --export csv

# Filter by date
leanproxy-mcp report --export csv --since 2026-06-01
```

### Bypass Budgets

Individual requests can bypass budget enforcement:

```bash
leanproxy-mcp serve --ignore-budget
```

Or via the `X-Ignore-Budget` HTTP header.

## Best Practices

1. **Start with monthly caps**: Monthly limits are easier to predict than daily.
2. **Set soft caps at 80%**: Gives time to react before hard cap enforcement.
3. **Use per-project budgets**: Isolate costs for different teams and initiatives.
4. **Monitor webhooks**: Integrate with Slack/PagerDuty for real-time alerts.
5. **Regular reporting**: Schedule `leanproxy-mcp report --export csv` for external analytics.

## Next Steps

- [Web Dashboard](./dashboard.md) — Real-time monitoring
- [Configuration Reference](./configuration.md) — Full config options
- [Commands Reference](./commands.md) — CLI documentation
