# Quick Start

Get up and running with LeanProxy-MCP in minutes.

## Basic Usage

### 1. Start the Proxy with an MCP Server

```bash
leanproxy-mcp server --stdio "npx @modelcontextprotocol/server-filesystem ./my-project"
```

### 2. Run in Dry-Run Mode

Simulate proxy behavior and see potential token savings:

```bash
leanproxy-mcp server --dry-run --stdio "npx @modelcontextprotocol/server-filesystem ./my-project"
```

### 3. Generate a Token Savings Report

```bash
leanproxy-mcp compactor --manifest ./mcp.json
```

## Common Workflows

### Reduce Token Usage

The token firewall automatically redacts:
- API keys and secrets
- Environment variables
- PII (emails, phone numbers, etc.)

```bash
# Start with default redaction rules
leanproxy-mcp server --stdio "npx @modelcontextprotocol/server-github"
```

### Merge MCP Configurations (Shadow Manifesting)

LeanProxy-MCP automatically merges:
- Global: `~/.config/mcp.json`
- Project-local: `./.mcp.json`

```bash
# Project-level config takes precedence
echo '{"mcpServers": {}}' > .mcp.json
```

### Custom Redaction Patterns

1. Create a config file:

```bash
mkdir -p ~/.config/leanproxy
cat > ~/.config/leanproxy/config.yaml << EOF
redaction:
  patterns:
    - name: "custom-secret"
      type: "regex"
      pattern: "MY_SECRET=[A-Z0-9]+"
EOF
```

2. Use custom config:

```bash
leanproxy-mcp server --config ~/.config/leanproxy/config.yaml --stdio "npx @modelcontextprotocol/server-filesystem ./"
```

## Next Steps

- [Commands Reference](./commands.md) - Full command documentation
- [Configuration](./configuration.md) - Customize LeanProxy-MCP