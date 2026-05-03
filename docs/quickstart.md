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