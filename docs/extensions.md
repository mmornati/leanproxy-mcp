# IDE Extensions

The repository contains two small IDE extensions, both named
**LeanProxy Cost Monitor**: one for VS Code (`extensions/vscode`) and one for
JetBrains IDEs (`extensions/jetbrains`). They poll the JSON `/metrics`
endpoint and show an estimated cost in the status bar, plus a panel with a
per-tool and per-server breakdown.

!!! warning "Read this before installing"
    - **They only work with the deprecated `serve` command.** The `/metrics`
      endpoint they read exists only under `serve --metrics-bind`. The
      `server run --stdio` and `server run --http` front ends that MCP clients
      actually use have no `/metrics` endpoint. See
      [Dashboard](./dashboard.md#metrics-endpoint).
    - **The figures they show are always zero.** They read `total_spend`,
      `by_tool`, `by_server` and `top_5_expensive_tools`. Nothing in the
      proxy currently feeds the tracker behind those fields, so the status bar
      shows a cost of `0.0000` and the breakdown is empty.
    - **The default endpoint points at the dashboard port.** Both extensions
      default to `http://127.0.0.1:9090/metrics`. Port 9090 is the default
      *dashboard* port of `serve`, and the dashboard has no `/metrics` route.
      With the defaults you get a 404, shown as "disconnected". Start the
      metrics endpoint on another port and change the setting (see
      [Setup](#setup)).
    - **No token support.** The extensions send no `Authorization` header, so
      they cannot reach an endpoint started with `--metrics-token`. Keep the
      metrics endpoint on a loopback address without a token.

    To see real savings, use [`leanproxy-mcp report`](./savings-report.md),
    which works with every front end.

## Setup

Start `serve` with the metrics endpoint on a free loopback port:

```bash
leanproxy-mcp serve --metrics-bind 127.0.0.1:9091
```

`serve` also starts the dashboard on `127.0.0.1:9090` by default. Then set the
extension's metrics endpoint to `http://127.0.0.1:9091/metrics`.

## VS Code Extension

### Features

- **Status bar item**: estimated cost, computed as
  `total_spend / 1000 × tokenCostPer1000`. Click it to open the panel.
- **Cost breakdown panel** (webview): total, per-tool and per-server token
  counts, and the top 5 tools. The panel polls every second.

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
| `leanproxy.metricsEndpoint` | `http://127.0.0.1:9090/metrics` | Metrics endpoint URL. Change it (see the warning above). |
| `leanproxy.pollInterval` | `1000` | Status bar polling interval in ms (minimum 500). Read at start; reload the window after changing it. |
| `leanproxy.currencySymbol` | `$` | Currency symbol shown in the status bar |
| `leanproxy.tokenCostPer1000` | `0.002` | Price per 1,000 tokens used for the estimate |

### Commands

| Command | Description |
|---------|-------------|
| `LeanProxy: Open Cost Panel` | Open the cost breakdown panel |
| `LeanProxy: Refresh Status Bar` | Poll the endpoint now |

## JetBrains Plugin

For IntelliJ-based IDEs, build 241 to 251.* (2024.1 to 2025.1).

### Features

- **Status bar widget**: the same cost estimate as the VS Code extension.
- **LeanProxy tool window** (right side): per-tool and per-server breakdown
  and the top 5 tools.
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

Open **Settings › Tools › LeanProxy Cost Monitor**. The page has two fields:

| Setting | Default | Description |
|---------|---------|-------------|
| Metrics endpoint | `http://127.0.0.1:9090/metrics` | Metrics endpoint URL. Change it (see the warning above). |
| Poll interval (ms) | `1000` | Polling interval |

The currency symbol (`$`) and price per 1,000 tokens (`0.002`) are also
stored, in `leanproxy-settings.xml` in the IDE's options directory, but have
no field on the settings page.

## Next Steps

- [Web Dashboard](./dashboard.md) — the `serve` dashboard and `/metrics` schema
- [Savings Report](./savings-report.md) — measured savings for every front end
