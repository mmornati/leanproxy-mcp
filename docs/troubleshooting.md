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
leanproxy-mcp server run --verbose --stdio
```

### Server Disconnects or Stops Responding

**Symptom:**
An MCP server (e.g. `garmin`) stops responding mid-session. Tool calls hang or return "request timed out", and the only known fix used to be disconnecting/reconnecting the MCP in the client.

**Why this happened:**
Two bugs compounded each other:

1. A freshly started `stdio` server was stopped almost immediately because its "last request" timestamp was never initialized — the idle timer fired on the first tick (~30s after startup) and shut the process down.
2. When a `stdio` server was stopped and then restarted, the new process never got a working request loop, so it accepted connections but never answered — requests hung until timeout.

**Current behavior (auto-recovery):**

With auto-reconnect enabled (default), LeanProxy-MCP now handles these cases automatically:

- **Crash recovery**: if a `stdio` process exits unexpectedly it is respawned with exponential backoff, and the restart budget resets after a server stays up for `stable_window`.
- **Liveness probe**: idle/running servers are pinged every `health_check_interval`; `health_check_failures` consecutive failures trigger a restart. Pings are MCP pings and do **not** consume AI tokens.
- **Transport recovery**: `http`/`sse` servers reconnect transparently when the connection drops, and the next tool call re-establishes a dead session.
- **Request retry**: if a request lands on a server that is recovering, it is retried once instead of failing permanently.

**If a server still appears stuck:**

1. Check the logs for restart activity:
```bash
leanproxy-mcp server run --stdio --log-level debug --log-file /tmp/leanproxy.log
# Look for "auto-reconnect", "reconnect", "restart", "crash"
tail -f /tmp/leanproxy.log | grep -i "reconnect\|restart\|crash\|error"
```

2. Confirm the server binary works standalone:
```bash
garmin-mcp stdio
```

3. Tune the recovery knobs if the defaults are too aggressive or too slow (see [Auto-Reconnect configuration](./configuration.md#auto-reconnect)).

4. As a last resort, disable auto-reconnect if it is interfering with a specific server:
```yaml
reconnect:
  enabled: false
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
leanproxy-mcp server run --dry-run --stdio
```

2. Generate report:
```bash
leanproxy-mcp report --output report.md
```

3. Enable debug logging:
```bash
leanproxy-mcp server run --debug --stdio
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
      "args": ["server", "run", "--stdio"]
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
leanproxy-mcp server run --config /path/to/config.yaml --stdio
```

## Cache Issues

### Cache Empty After List Tools

**Symptom:**
`list_tools` returns no results or tools are not cached.

**Solutions:**

1. Check cache location:
```bash
leanproxy-mcp cache --location
ls -la ~/.config/leanproxy/toolcache/
```

2. Verify list_tools method works:
```bash
printf '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}\n{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_tools","arguments":{"server_name":"garmin"}}}\n' | leanproxy-mcp server run --stdio
```

3. Check logs for errors:
```bash
leanproxy-mcp server run --stdio --log-level debug --log-file /tmp/leanproxy.log
# Then list tools in another terminal
tail -f /tmp/leanproxy.log | grep -i "list_tools\|cache\|error"
```

4. Clear and rebuild cache:
```bash
leanproxy-mcp cache --clear --server garmin
leanproxy-mcp cache --clear --server Intervals_icu
# Then search again to rebuild cache
```

### Cache Not Persisting

**Symptom:**
Cache files exist but are empty or disappear after restart.

**Solutions:**

1. Check directory permissions:
```bash
ls -la ~/.config/leanproxy/
chmod 755 ~/.config/leanproxy/toolcache/
```

2. Verify disk space:
```bash
df -h ~/.config/leanproxy/
```

## Status File Issues

### status --running Shows "No running leanproxy instance found"

**Symptom:**
Running leanproxy but `leanproxy status --running` shows no instances.

**Solutions:**

1. Check if status file exists:
```bash
/bin/cat ~/.config/leanproxy/status/current.json
```

2. Verify you're running a recent version:
```bash
leanproxy-mcp version
# Should show version with status file support
```

3. Check if the running instance created the status file:
```bash
ls -la ~/.config/leanproxy/status/
# Should show current.json with recent timestamp
```

4. For stdio mode (OpenCode), ensure the binary is updated:
```bash
which leanproxy-mcp
# Verify it's the built binary, not an old version
go build -o /usr/local/bin/leanproxy-mcp .
```

### Status File Not Updated

**Symptom:**
Status file exists but shows stale data.

**Solutions:**

1. Kill old processes:
```bash
/bin/ps aux | grep leanproxy
kill <PID>  # Kill any running instances
```

2. Restart and verify:
```bash
# Start fresh instance
leanproxy-mcp server run --stdio &
# Wait a few seconds
leanproxy-mcp status --running
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