# IDE Extensions

The repository contains two small IDE extensions, both named
**LeanProxy Cost Monitor**: one for VS Code (`extensions/vscode`) and one for
JetBrains IDEs (`extensions/jetbrains`). They show the proxy's token savings
(today and week to date) in the status bar, plus a panel with a per-server
and per-tool breakdown. They read the `usage` section of the
[`/metrics` endpoint](dashboard.md#metrics-endpoint): the same measured
numbers as the dashboard and `leanproxy-mcp report`, across every proxy
process on the machine.

## Setup

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

### Features

- **Status bar item**: tokens saved today (`12.3K saved`), or an estimated
  cost saved when you set a price. The tooltip adds the percentage and the
  week-to-date figure. Click it to open the panel.
- **Usage panel** (webview): today and week-to-date totals, discovery cost,
  sessions, and today's per-server and top per-tool breakdown.
- **Token support**: the metrics token is kept in VS Code's SecretStorage,
  never in `settings.json`.

### Installation

No packaged `.vsix` is committed to the repository. Build one from source
(Node.js 20 or later):

```bash
cd extensions/vscode
npm ci
npx @vscode/vsce package --skip-license   # runs `npm run compile`, writes leanproxy-cost-0.1.0.vsix
code --install-extension leanproxy-cost-0.1.0.vsix
```

### Settings

| Setting | Default | Description |
|---------|---------|-------------|
| `leanproxy.metricsEndpoint` | `http://127.0.0.1:9091/metrics` | The `--metrics-bind` address plus `/metrics` |
| `leanproxy.pollInterval` | `5000` | Polling interval in ms (minimum `1000`), used by both the status bar and the panel. The proxy records a new snapshot every 5 seconds, so polling faster only repeats it |
| `leanproxy.currencySymbol` | `$` | Currency symbol for the estimated cost saved |
| `leanproxy.tokenCostPer1000` | `0` | Your price per 1,000 tokens. `0` shows tokens only, because LeanProxy has no built-in price table |

Changes apply immediately, with no reload needed.

### Commands

| Command | Description |
|---------|-------------|
| `LeanProxy: Open Cost Panel` | Open the usage panel |
| `LeanProxy: Refresh Status Bar` | Poll the endpoint now |
| `LeanProxy: Set Metrics Token` | Store the `--metrics-token` value in SecretStorage (leave empty to clear it) |

## JetBrains Plugin

For IntelliJ-based IDEs, build 241 to 251.* (2024.1 to 2025.1).

### Features

- **Status bar widget**: tokens saved today, or an estimated cost saved at
  your price.
- **LeanProxy tool window** (right side): today and week-to-date totals and
  today's per-server and top per-tool breakdown.
- **Actions**: `LeanProxy: Open Cost Panel` and
  `LeanProxy: Refresh Status Bar`.

### Installation

The plugin is not shipped prebuilt. The directory has no Gradle wrapper, so
build it with a local Gradle 8 installation and JDK 17:

```bash
cd extensions/jetbrains
gradle buildPlugin
# The plugin ZIP is written to build/distributions/
```

Install the ZIP with **Settings › Plugins › ⚙ › Install Plugin from Disk…**.

### Settings

Open **Settings › Tools › LeanProxy Cost Monitor**:

| Setting | Default | Description |
|---------|---------|-------------|
| Metrics endpoint | `http://127.0.0.1:9091/metrics` | The `--metrics-bind` address plus `/metrics` |
| Metrics token | (none) | The `--metrics-token` value, stored in the IDE's password safe (not in `leanproxy-settings.xml`) |
| Poll interval (ms) | `5000` | Polling interval (minimum `1000`); a change applies at the next poll |
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

- [Web Dashboard](./dashboard.md) — the dashboard and the `/metrics` schema
- [Savings Report](./savings-report.md) — how every number is measured
