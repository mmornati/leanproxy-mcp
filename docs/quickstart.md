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
    timeout: 30s
    connect_timeout: 10s
```

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

## Next Steps

- [Commands Reference](./commands.md) - Full command documentation
- [Configuration](./configuration.md) - Customize LeanProxy-MCP
- [Troubleshooting](./troubleshooting.md) - Common issues and solutions