# Troubleshooting

Solutions to common issues.

## Common Issues

### Server Won't Start

**Symptom:**
```
Error: connection refused
```

**Solutions:**

1. Check port availability:
```bash
lsof -i :8080
```

2. Verify MCP server command:
```bash
npx @modelcontextprotocol/server-filesystem ./  # Test standalone
```

3. Check logs:
```bash
leanproxy-mcp server --verbose --stdio "npx @modelcontextprotocol/server-filesystem ./"
```

### Redaction Not Working

**Symptom:**
Sensitive data still appears in LLM input.

**Solutions:**

1. Verify redaction is enabled:
```bash
leanproxy-mcp context show
```

2. Add custom pattern:
```yaml
redaction:
  enabled: true
  patterns:
    - name: "custom-secret"
      pattern: "MY_SECRET=[A-Z0-9]+"
```

3. Check pattern syntax:
```bash
leanproxy-mcp doctor
```

### High Token Usage

**Symptom:**
Token usage not decreasing.

**Solutions:**

1. Run in dry-run mode to see what's being redacted:
```bash
leanproxy-mcp server --dry-run --stdio "npx @modelcontextprotocol/server-filesystem ./"
```

2. Analyze manifest:
```bash
leanproxy-mcp compactor --manifest ./mcp.json --output report.md
```

3. Enable debug logging:
```bash
leanproxy-mcp server --debug --stdio "..."
```

### IDE Connection Issues

**Symptom:**
IDE cannot connect to LeanProxy-MCP.

**Solutions:**

1. Verify installation:
```bash
leanproxy-mcp version
```

2. Check IDE configuration:
```json
{
  "mcpServers": {
    "leanproxy": {
      "command": "leanproxy-mcp",
      "args": ["server", "--stdio", "npx", "@modelcontextprotocol/server-filesystem", "./"]
    }
  }
}
```

3. Restart IDE

### Configuration Not Loading

**Symptom:**
Config changes have no effect.

**Solutions:**

1. Check config file location:
```bash
echo $LEANPROXY_CONFIG
# or default:
~/.config/leanproxy/config.yaml
```

2. Validate config:
```bash
leanproxy-mcp context validate
```

3. Use explicit config:
```bash
leanproxy-mcp server --config /path/to/config.yaml --stdio "..."
```

## Debug Mode

Enable debug logging for troubleshooting:

```bash
leanproxy-mcp server run --stdio --log-level debug --log-file /tmp/leanproxy.log
```

## Doctor Command

Run diagnostics:

```bash
leanproxy-mcp doctor
```

Checks:
- Configuration syntax
- Network connectivity
- File permissions
- Dependencies

## Getting Help

- GitHub Issues: https://github.com/mmornati/leanproxy-mcp/issues
- Documentation: https://github.com/mmornati/leanproxy-mcp#readme