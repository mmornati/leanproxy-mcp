# IDE Extensions

LeanProxy-MCP provides first-party IDE extensions that show the proxy's
token savings (today and week to date) directly in your development
environment. They read the `usage` section of the
[`/metrics` endpoint](dashboard.md#metrics-endpoint): the same measured
numbers as the dashboard and `leanproxy-mcp report`, across every proxy
process on the machine.

## Prerequisites

Both extensions need the metrics endpoint. It is off by default, so enable
it on one proxy process at the address the extensions expect by default,
`127.0.0.1:9091`:

```bash
# A shared Streamable HTTP gateway (recommended)
leanproxy-mcp server run --http 127.0.0.1:8765 --metrics-bind 127.0.0.1:9091

# Or the deprecated serve front end
leanproxy-mcp serve --metrics-bind 127.0.0.1:9091
```

Don't point the extensions at `9090`: that is the dashboard's port, and it
has no `/metrics` route, so you would get `404`.

Enable it on **one** process only, since two processes cannot bind the same
port. Because the numbers come from the shared usage store, that one
endpoint also covers the `server run --stdio` sessions your IDEs start.

Per-server and per-tool rows need the response governor
(`response.enabled: true`). Without it the extensions still show the
totals, which include schema savings.

If you set `--metrics-token`, give the extension the same token (see
below). A non-loopback `--metrics-bind` requires one.

## VS Code Extension

The VS Code extension adds a status bar item and a usage panel.

### Features

- **Status bar**: tokens saved today (`12.3K saved`), or an estimated cost
  saved when you set a price. The tooltip adds the percentage and the
  week-to-date figure.
- **Usage panel**: today and week-to-date totals, discovery cost, sessions,
  and today's per-server and top per-tool breakdown.
- **Token support**: the metrics token is kept in VS Code's SecretStorage,
  never in `settings.json`.

### Installation

Install from the VS Code Marketplace, or from the `.vsix` file in the repository:

```bash
code --install-extension extensions/vscode/leanproxy-*.vsix
```

### Configuration

| Setting | Default | Description |
|---------|---------|-------------|
| `leanproxy.metricsEndpoint` | `http://127.0.0.1:9091/metrics` | The `--metrics-bind` address plus `/metrics` |
| `leanproxy.pollInterval` | `5000` | Polling interval in ms (minimum `1000`), used by both the status bar and the panel. The proxy records a new snapshot every 5 seconds, so polling faster only repeats it |
| `leanproxy.currencySymbol` | `$` | Currency symbol for the estimated cost saved |
| `leanproxy.tokenCostPer1000` | `0` | Your price per 1000 tokens. `0` shows tokens only, because LeanProxy has no built-in price table |

Changes apply immediately, with no reload needed.

### Commands

| Command | Description |
|---------|-------------|
| `LeanProxy: Open Cost Panel` | Open the usage panel |
| `LeanProxy: Refresh Status Bar` | Force refresh the status bar |
| `LeanProxy: Set Metrics Token` | Store the `--metrics-token` value in SecretStorage (leave empty to clear it) |

## JetBrains Plugin

The JetBrains plugin (IntelliJ IDEA, PyCharm, GoLand, WebStorm) provides equivalent functionality.

### Features

- **Status bar widget**: tokens saved today, or an estimated cost saved at
  your price.
- **Tool window**: right-side panel with today and week-to-date totals and
  today's per-server and top per-tool breakdown.
- **IDE settings UI**: endpoint, token, polling interval, currency and price.

### Installation

Build from source or install from the JetBrains Marketplace:

```bash
cd extensions/jetbrains
./gradlew build
# Install the plugin from build/distributions/
```

### Configuration

Access via `Settings > Tools > LeanProxy Cost Monitor`:

| Setting | Default | Description |
|---------|---------|-------------|
| Metrics endpoint | `http://127.0.0.1:9091/metrics` | The `--metrics-bind` address plus `/metrics` |
| Metrics token | (none) | The `--metrics-token` value, stored in the IDE's password safe (not in `leanproxy-settings.xml`) |
| Poll interval | `5000` | Polling interval in ms (minimum `1000`); a change applies at the next poll |
| Currency symbol | `$` | Currency symbol for the estimated cost saved |
| Price per 1000 tokens | `0` | Your price; `0` shows tokens only |

## Troubleshooting

| Symptom | Cause |
|---------|-------|
| `HTTP 404` | The endpoint is not a metrics endpoint. Usually it's the dashboard's `9090`; use the `--metrics-bind` address |
| `HTTP 401` | The endpoint has a `--metrics-token`; set the same token in the extension |
| `HTTP 403` | The `Host` you connect with is not allowed; use `127.0.0.1`/`localhost` or add it with `--metrics-allowed-hosts` |
| "no usage data" | The proxy is running but could not read `~/.leanproxy/usage` (see its log) |
| Totals but no servers/tools | The response governor is off (`response.enabled: true`) |

## Next Steps

- [Web Dashboard](./dashboard.md): browser-based monitoring
- [Savings Report](./savings-report.md): how every number is measured
- [Commands Reference](./commands.md): full CLI documentation
