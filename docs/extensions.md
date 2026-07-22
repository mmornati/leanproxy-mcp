# IDE Extensions

LeanProxy-MCP provides first-party IDE extensions for real-time cost monitoring directly in your development environment.

## VS Code Extension

The VS Code extension adds a status bar widget and a cost breakdown panel.

### Features

- **Status Bar Widget**: Shows estimated session cost, updates every second
- **Cost Breakdown Panel**: Side panel with per-tool and per-server token breakdown
- **Top 5 Expensive Tools**: Quick visibility into cost drivers

### Installation

Install from the VS Code Marketplace, or from the `.vsix` file in the repository:

```bash
code --install-extension extensions/vscode/leanproxy-*.vsix
```

### Configuration

| Setting | Default | Description |
|---------|---------|-------------|
| `leanproxy.metricsEndpoint` | `http://127.0.0.1:9091/metrics` | Metrics endpoint URL |
| `leanproxy.pollInterval` | `1000` | Polling interval in ms |
| `leanproxy.currencySymbol` | `$` | Currency symbol for display |
| `leanproxy.tokenCostPer1000` | `0.003` | Cost per 1K tokens |

### Commands

| Command | Description |
|---------|-------------|
| `LeanProxy: Open Cost Panel` | Open the cost breakdown panel |
| `LeanProxy: Refresh Status Bar` | Force refresh the status bar |

## JetBrains Plugin

The JetBrains plugin (IntelliJ IDEA, PyCharm, GoLand, WebStorm) provides equivalent functionality.

### Features

- **Status Bar Widget**: Estimated session cost with real-time updates
- **Tool Window**: Right-side panel with per-tool and per-server breakdown
- **Top 5 View**: Most expensive tools at a glance
- **IDE Settings UI**: Configurable polling interval and metrics endpoint

### Installation

Build from source or install from the JetBrains Marketplace:

```bash
cd extensions/jetbrains
./gradlew build
# Install the plugin from build/distributions/
```

### Configuration

Access via `Settings > Tools > LeanProxy`:

| Setting | Default | Description |
|---------|---------|-------------|
| Metrics Endpoint | `http://127.0.0.1:9091/metrics` | Metrics endpoint URL |
| Poll Interval | `1000` | Polling interval in ms |

## Prerequisites

Both extensions require the LeanProxy-MCP metrics endpoint to be enabled:

```bash
leanproxy-mcp serve --metrics-bind 127.0.0.1:9091
```

## Next Steps

- [Web Dashboard](./dashboard.md) — Browser-based monitoring
- [Budget Management](./budget.md) — Team/project spending limits
- [Commands Reference](./commands.md) — Full CLI documentation
