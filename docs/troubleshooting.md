# Troubleshooting

Solutions to common issues.

!!! tip "First step for any problem"
    Run the proxy by hand, with debug logs, using the same config your IDE
    uses:

    ```bash
    leanproxy-mcp server run --stdio --log-level debug --log-file /tmp/leanproxy.log
    ```

    Logs go to stderr unless you pass `--log-file`. Many IDEs also show the
    stderr of an MCP server in their MCP log view.

## Common Issues

### Server Won't Start

**Symptoms:** the IDE shows the LeanProxy MCP server as failed, or
`server run` exits right away.

**Solutions:**

1. Read the error. Frequent ones:
    - `no servers configured in <path>`: the config file is missing or
      empty. Add a server ([Quick Start](quickstart.md#1-configure-mcp-servers)),
      or point to the right file (see
      [Configuration Not Loading](#configuration-not-loading)).
    - `failed to load config: validate config: server x: url is required for
      sse transport`: fix the named server. `http` and `sse` servers both
      need `http.url`; there is no `sse:` block.
    - `exec: "npx": executable file not found in $PATH`: the server's
      command is not on the `PATH` LeanProxy sees (see
      [Server Command Not Found](#server-command-not-found-when-started-from-an-ide)).
2. Check one upstream server on its own. This starts a temporary copy of the
   server and runs the MCP handshake:
    ```bash
    leanproxy-mcp server health <name>
    ```
3. Run the server's command by hand, outside LeanProxy:
    ```bash
    npx -y @modelcontextprotocol/server-filesystem "$HOME/projects"
    ```
4. If the server needs an API key or another variable, check that it
   receives it. A stdio server gets only a minimal environment, plus what
   you list in `env` and `env_passthrough`:
    ```bash
    leanproxy-mcp doctor env
    ```
    See
    [Child Process Environment](configuration.md#child-process-environment-env-env_passthrough-inherit_env).

### Server Command Not Found When Started From an IDE

**Symptom:** everything works in a terminal, but the IDE cannot start
LeanProxy ("command not found", `ENOENT`), or LeanProxy logs
`executable file not found in $PATH` for an upstream server built on
`npx`/`uvx`.

**Why:** GUI apps such as Claude Desktop do not load your shell profile, so
their `PATH` lacks `/opt/homebrew/bin`, `~/.local/bin`, nvm directories, and
so on. LeanProxy and the servers it starts inherit that `PATH`.

**Solutions:**

1. Use the absolute path of `leanproxy-mcp` in the IDE config
   (`which leanproxy-mcp` prints it).
2. Give LeanProxy a full `PATH` in the IDE config. For Claude Desktop:
    ```json
    {
      "mcpServers": {
        "leanproxy": {
          "command": "/opt/homebrew/bin/leanproxy-mcp",
          "args": ["server", "run", "--stdio"],
          "env": { "PATH": "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin" }
        }
      }
    }
    ```
3. Or use absolute commands in `leanproxy_servers.yaml`
   (`command: /opt/homebrew/bin/npx`), and set `PATH` for that server with
   `stdio.env: ["PATH=/opt/homebrew/bin:/usr/bin:/bin"]` when the command
   starts other programs (`npx` starts `node`).

### Server Disconnects or Stops Responding

**Symptom:**
An MCP server (e.g. `garmin`) stops responding mid-session. Tool calls hang
or return "request timed out".

**Current behavior (auto-recovery):**

With auto-reconnect enabled (default), LeanProxy-MCP handles these cases
automatically:

- **Crash recovery**: if a `stdio` process exits unexpectedly it is respawned with exponential backoff, and the restart budget resets after a server stays up for `stable_window`.
- **Liveness probe**: idle/running servers are pinged every `health_check_interval`; `health_check_failures` consecutive failures trigger a restart. Pings are MCP pings and do **not** consume AI tokens.
- **Transport recovery**: `http`/`sse` servers reconnect transparently when the connection drops, and the next tool call re-establishes a dead session.
- **Request retry**: if a request lands on a server that is recovering, it waits briefly for the restart to finish instead of failing permanently. For `http`/`sse`, a request that fails on a genuine transport error is retried once after the reconnect.

Older versions had two bugs that caused this symptom: a fresh `stdio`
server was stopped by the idle timer about 30 seconds after start, and a
restarted server never answered. Both are fixed; upgrade if you see this
on an old version.

**If a server still appears stuck:**

1. Check the logs for restart activity:
```bash
leanproxy-mcp server run --stdio --log-level debug --log-file /tmp/leanproxy.log
# In another terminal:
tail -f /tmp/leanproxy.log | grep -i "reconnect\|restart\|crash\|error"
```

2. Confirm the server works standalone (use the command from your config):
```bash
leanproxy-mcp server health garmin
```

3. Tune the recovery knobs if the defaults are too aggressive or too slow (see [Auto-Reconnect configuration](./configuration.md#auto-reconnect)).

4. As a last resort, disable auto-reconnect if it is interfering with a specific server:
```yaml
reconnect:
  enabled: false
```

### Redaction Not Working

**Symptom:**
Sensitive data still appears in tool arguments or results.

**Solutions:**

1. Check that redaction is on. It is on unless `bouncer.enabled: false`:
```bash
leanproxy-mcp doctor security
```

2. Check that the secret matches a built-in pattern. There are 29, and
   there is no email or phone-number detection:
```bash
leanproxy-mcp bouncer list-patterns
```

3. If it does not, add a custom pattern. The block is `bouncer:`:
```yaml
bouncer:
  patterns:
    - name: "custom-secret"
      pattern: "MY_SECRET=[A-Z0-9]+"
```
   For random-looking tokens without a known prefix, you can also turn on
   the high-entropy detector with `bouncer.entropy_detection: true`.

4. Validate the patterns. Pass `--config`: this command defaults to
   `./leanproxy.yaml` in the current directory:
```bash
leanproxy-mcp bouncer validate-patterns --config ~/.config/leanproxy_servers.yaml
```

5. Restart the proxy. A running proxy does not reload its config.

### High Token Usage

**Symptom:**
Token usage is not decreasing.

**Solutions:**

1. Check which exposure mode your client gets. Claude Code, Claude
   Desktop, Cursor and VS Code get every tool directly (passthrough) and do
   their own tool search, so the router savings do not apply to them
   ([exposure modes](quickstart.md#which-exposure-mode-each-ide-gets)).

2. See what LeanProxy measured:
```bash
leanproxy-mcp report --since 1d
leanproxy-mcp report --export md --output report.md
```

3. Large tool results are not trimmed unless you turn on the response
   governor (`response.enabled: true`, see
   [Configuration](configuration.md)).

4. Watch what happens with debug logs:
```bash
leanproxy-mcp server run --stdio --log-level debug --log-file /tmp/leanproxy.log
```

There is no dry-run mode for the proxy: the global `--dry-run` flag has no
effect on `server run`.

### IDE Connection Issues

**Symptom:**
IDE cannot connect to LeanProxy-MCP.

**Solutions:**

1. Verify installation:
```bash
leanproxy-mcp version
```

2. Check the IDE configuration. The command must be
   `leanproxy-mcp server run --stdio`, never `serve`. For clients that use
   `mcpServers` (Cursor, Claude Desktop):
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
   VS Code uses a different file and key; see
   [Connect your client](quickstart.md#connect-your-client).

3. If the IDE cannot find the binary, see
   [Server Command Not Found](#server-command-not-found-when-started-from-an-ide).

4. Restart the IDE or reconnect the MCP server.

### Configuration Not Loading

**Symptom:**
Config changes have no effect.

**Solutions:**

1. Restart the proxy. `server run` reads the config once, at start.

2. Check which file is used. The default is
   `~/.config/leanproxy_servers.yaml`. `LEANPROXY_CONFIG` overrides it, and
   `server run --config` overrides both:
```bash
echo "${LEANPROXY_CONFIG:-$HOME/.config/leanproxy_servers.yaml}"
leanproxy-mcp server list
```
   `server list` shows the servers from that file. Remember that the IDE
   starts LeanProxy with its own environment, which may not include a
   `LEANPROXY_CONFIG` you set in your shell. See
   [Config File Locations](configuration.md#config-file-locations).

3. Check that the file loads. `server run` prints any validation error and
   exits:
```bash
leanproxy-mcp server run --stdio --config ~/.config/leanproxy_servers.yaml
```
   Unknown keys are ignored without a warning, so check the spelling of
   any setting that seems to have no effect.

4. For a security-focused view of the loaded settings (`doctor` ignores
   `LEANPROXY_CONFIG`, so pass `--config` if you use another file):
```bash
leanproxy-mcp doctor security --config ~/.config/leanproxy_servers.yaml
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

2. Verify the `list_tools` tool works:
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
# The next list_tools or search_tools call rebuilds it
```

### Cache Not Persisting

**Symptom:**
Cache files exist but are empty or disappear after restart.

**Solutions:**

1. Check that you own the directory. LeanProxy creates it with mode 0700
   (owner only); keep it that way:
```bash
ls -ld ~/.config/leanproxy/toolcache/
chmod 700 ~/.config/leanproxy/toolcache/
```

2. Check `LEANPROXY_TOOLCACHE_DIR`. When set, the cache lives there
   instead.

3. Verify disk space:
```bash
df -h ~/.config/leanproxy/
```

## Status File Issues

### status --running Shows "No running leanproxy instance found"

**Symptom:**
LeanProxy is running, but `leanproxy-mcp status --running` shows no
instances.

**Solutions:**

1. Check if the status file exists:
```bash
cat ~/.config/leanproxy/status/current.json
```

2. Check that the running instance wrote it recently:
```bash
ls -la ~/.config/leanproxy/status/
```

3. Make sure the IDE runs the binary you think it runs:
```bash
which leanproxy-mcp
leanproxy-mcp version
```

`status` without `--running` does not look at running instances: it starts
every enabled server itself to check it.

### Status File Not Updated

**Symptom:**
Status file exists but shows stale data.

**Solutions:**

1. Look for old processes:
```bash
ps aux | grep '[l]eanproxy-mcp'
kill <PID>
```

2. Restart the IDE (or its MCP server), then check again:
```bash
leanproxy-mcp status --running
```

## Debug Mode

There is no `--debug` flag. Use `--log-level debug` (or `-v`):

```bash
leanproxy-mcp server run --stdio --log-level debug --log-file /tmp/leanproxy.log
```

## Doctor Command

`leanproxy-mcp doctor` on its own only prints help. Use a subcommand:

| Command | What it checks |
|---|---|
| `leanproxy-mcp doctor security` | Local, read-only security report mapped to the OWASP MCP Top 10: redaction and custom patterns, injection guard, policy, tool pinning, sandbox, exposure, HTTP front end, token file permissions. Add `--json` or `--markdown` |
| `leanproxy-mcp doctor env` | Per stdio server, which environment variables are passed and which are dropped (names only) |
| `leanproxy-mcp doctor sandbox` | Per stdio server, its container sandbox status |

`doctor` does not test network connectivity or check that server commands
are installed. Use `leanproxy-mcp server health <name>` for that.

## Getting Help

- GitHub Issues: <https://github.com/mmornati/leanproxy-mcp/issues>
- Documentation: <https://mmornati.github.io/leanproxy-mcp/>
