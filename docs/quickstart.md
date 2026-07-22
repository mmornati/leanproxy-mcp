# Quick Start

Get up and running with LeanProxy-MCP in minutes.

## Basic Usage

### 1. Configure MCP Servers

First, add your MCP servers to the configuration file at `~/.config/leanproxy_servers.yaml`:

```bash
# Add a server
leanproxy-mcp server add filesystem "npx -y @modelcontextprotocol/server-filesystem" "./"

# Or manually edit ~/.config/leanproxy_servers.yaml
```

#### Server Configuration Format

The server configuration file supports three transport types: `stdio`, `http`, and `sse`.

```yaml
version: "1.0"
servers:
  - name: <server-name>
    enabled: true
    transport: <stdio|http|sse>
    timeout: 30s
    connect_timeout: 10s
    # Transport-specific configuration
    stdio:
      command: <command>
      args: [<args>]
      env: [<env-vars>]
      cwd: <working-directory>
    http:
      url: <http-url>
      headers:
        <header-key>: <header-value>
    sse:
      url: <sse-url>
```

##### Stdio Transport Example

```yaml
servers:
  - name: filesystem
    enabled: true
    transport: stdio
    stdio:
      command: npx
      args:
        - -y
        - @modelcontextprotocol/server-filesystem
        - ./"
      cwd: .
    timeout: 30s
```

##### HTTP Transport Example (with Authentication)

```yaml
servers:
  - name: github
    enabled: true
    transport: http
    http:
      url: https://api.githubcopilot.com/mcp
      headers:
        Authorization: Bearer ghp_yourPersonalAccessToken
        Content-Type: application/json
      auth:
        type: bearer
        client_secret: ghp_yourPersonalAccessToken
    timeout: 30s
    connect_timeout: 10s
```

##### HTTP Transport with OAuth2 Authentication

```yaml
servers:
  - name: enterprise-mcp
    enabled: true
    transport: http
    http:
      url: https://api.enterprise.com/mcp
      auth:
        type: oauth2
        client_id: my-client-id
        client_secret: my-client-secret
        scopes:
          - mcp:read
          - mcp:write
    timeout: 60s
```

#### Auth Types

| Type | Description |
|------|-------------|
| `bearer` | Simple API key in Authorization header |
| `oauth2` | Full OAuth 2.0 flow with automatic token refresh |

##### SSE Transport Example

```yaml
servers:
  - name: my-sse-server
    enabled: true
    transport: sse
    sse:
      url: https://your-server.com/mcp/sse
    timeout: 30s
```

> **Tip**: For GitHub Copilot, use the `/mcp` endpoint (not `/mcp/sse`) with HTTP transport and your PAT in the Authorization header.

### 2. Run LeanProxy-MCP as an MCP Server

Start leanproxy-mcp in stdio mode to proxy all configured MCP servers:

```bash
leanproxy-mcp server run --stdio
```

With logging:
```bash
leanproxy-mcp server run --stdio --log-file /tmp/leanproxy.log --log-level debug
```

With custom config:
```bash
leanproxy-mcp server run --stdio --config /path/to/config.yaml
```

### 3. Run in Dry-Run Mode

Simulate proxy behavior and see potential token savings:

```bash
leanproxy-mcp server run --dry-run --stdio
```

## Common Workflows

### OpenCode Configuration

To use LeanProxy-MCP as an MCP proxy in OpenCode, add this to your OpenCode config at `~/.config/opencode/opencode.json`:

```json
{
  "mcp": {
    "leanproxy": {
      "type": "local",
      "command": ["leanproxy-mcp", "server", "run", "--stdio"],
      "enabled": true
    }
  }
}
```

### Reduce Token Usage

The token firewall automatically redacts:
- API keys and secrets
- Environment variables
- PII (emails, phone numbers, etc.)

### Tool Naming

When LeanProxy-MCP aggregates tools from multiple servers, each tool name is prefixed with the server name:

```
serverName_toolName
```

For example, if you have a `github` server with a tool called `list_repos`, the full tool name would be `github_list_repos`.

### Tool Cache

LeanProxy-MCP automatically caches tool signatures from your MCP servers. This allows:
- **Fast tool listing**: Use `list_tools` to list tools on a specific server
- **Offline access**: Tool information is persisted to disk at `~/.config/leanproxy/toolcache/`
- **No server startup**: Search cached tools without starting backend servers

```bash
# View cached tools
leanproxy-mcp cache --list

# Search tools by name or description
leanproxy-mcp cache --search activity

# Search within a specific server
leanproxy-mcp cache --server garmin --search sleep
```

### Running Status

Check if LeanProxy-MCP is currently running:

```bash
leanproxy-mcp status --running
```

This reads from the status file written by running instances (`~/.config/leanproxy/status/current.json`).

### Enable/Disable Servers

```bash
# Enable a server
leanproxy-mcp server enable github

# Disable a server
leanproxy-mcp server disable github
```

## Advanced Features

### Discover Servers from Marketplace

Browse and install community MCP servers from the MCP Registry:

```bash
# Sync the registry cache
leanproxy-mcp marketplace sync

# Search for servers
leanproxy-mcp marketplace search github

# Install from registry
leanproxy-mcp add github
```

### Enable Web Dashboard

Start the proxy with the dashboard for real-time monitoring:

```bash
leanproxy-mcp serve --dashboard-bind 127.0.0.1:9090 --metrics-bind 127.0.0.1:9091
```

Open `http://127.0.0.1:9090` in your browser.

### Enable Semantic Caching

Reduce redundant LLM calls with vector-similarity caching:

```bash
# Using Ollama embeddings
leanproxy-mcp serve --embed-provider ollama

# View cache stats
leanproxy-mcp cache --semantic
```

### Set Budget Limits

Control spending per team and project:

```yaml
# leanproxy.yaml
budgets:
  teams:
    engineering:
      daily: 1000000
      monthly: 20000000
      hard_cap: true
```

### Install IDE Extensions

Real-time cost monitoring in the editor status bar:

- **VS Code**: Install from Marketplace or `code --install-extension leanproxy.vsix`
- **JetBrains**: Install from Plugin Marketplace or build from source

Requires the metrics endpoint: `leanproxy-mcp serve --metrics-bind 127.0.0.1:9091`

## Next Steps

- [Commands Reference](./commands.md) - Full command documentation
- [Configuration](./configuration.md) - Customize LeanProxy-MCP
- [Web Dashboard](./dashboard.md) - Real-time monitoring
- [Budget Management](./budget.md) - Spending limits
- [Troubleshooting](./troubleshooting.md) - Common issues and solutions