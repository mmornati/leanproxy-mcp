# Commands Reference

Complete reference for all LeanProxy-MCP CLI commands.

## Main Command

```bash
leanproxy-mcp [command] [flags]
```

### Global Flags

| Flag | Type | Description |
|------|------|-------------|
| `--config` | string | Path to config file |
| `-n, --dry-run` | bool | Preview without making changes |
| `--log-level` | string | Log level (debug, info, warn, error) |
| `-v, --verbose` | bool | Enable verbose logging |
| `-h, --help` | bool | Show help |

### Available Commands

| Command | Description |
|---------|-------------|
| `serve` | Start the JSON-RPC streaming proxy |
| `server` | Manage MCP server configurations |
| `bouncer` | Manage redaction settings |
| `compactor` | Manage manifest caching |
| `cache` | Inspect persisted tool cache |
| `status` | Display real-time server status |
| `savings` | Display token savings statistics |
| `cost` | Display token cost attribution statistics |
| `report` | Generate token savings report |
| `migrate` | Import MCP configs from other tools |
| `completion` | Generate shell completions |
| `namespace` | Manage hierarchical namespaces |
| `version` | Print version information |

## `serve` - Start Proxy Server

Start the LeanProxy-MCP proxy server that listens for connections and forwards JSON-RPC requests.

### Usage

```bash
leanproxy-mcp serve [flags]
```

### Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--listen` | string | `127.0.0.1:8080` | Address to listen on |
| `--upstream` | string | `http://localhost:8081` | Upstream JSON-RPC server URL |

### Examples

```bash
# Start proxy server
leanproxy-mcp serve

# Listen on custom address
leanproxy-mcp serve --listen 0.0.0.0:9090

# Custom upstream server
leanproxy-mcp serve --upstream http://localhost:9000

# With verbose logging
leanproxy-mcp serve --verbose
```

## `server` - Manage MCP Servers

Add, remove, list, enable, or disable MCP servers.

### Usage

```bash
leanproxy-mcp server [command]
```

### Subcommands

| Command | Description |
|---------|-------------|
| `add` | Add a new MCP server |
| `remove` | Remove an MCP server |
| `list` | List all configured servers |
| `enable` | Enable a disabled server |
| `disable` | Disable an enabled server |
| `run` | Run leanproxy as an MCP stdio server |

---

### `server run` - Run Stdio Server

Run leanproxy-mcp as an MCP server in stdio mode. This command reads JSON-RPC requests from stdin and writes responses to stdout, proxying requests to configured MCP servers.

#### Usage

```bash
leanproxy-mcp server run --stdio [flags]
```

#### Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--stdio` | bool | false | Run in stdio mode (required) |
| `--config` | string | `~/.config/leanproxy_servers.yaml` | Path to config file |
| `--log-file` | string | "" | Path to log file |
| `--log-level` | string | `info` | Log level (debug, info, warn, error) |
| `-v, --verbose` | bool | false | Enable verbose logging |

#### Examples

```bash
# Run in stdio mode with default config
leanproxy-mcp server run --stdio

# Run with logging
leanproxy-mcp server run --stdio --log-file /tmp/leanproxy.log --log-level debug

# Run with custom config
leanproxy-mcp server run --stdio --config /path/to/config.yaml

# Dry-run mode
leanproxy-mcp server run --dry-run --stdio
```

#### OpenCode Configuration

To use with OpenCode, add to `~/.config/opencode/opencode.json`:

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

---

### `server add` - Add Server

Add a new MCP server configuration.

#### Usage

```bash
leanproxy-mcp server add <name> <command> [args...] [flags]
```

#### Flags

| Flag | Type | Description |
|------|------|-------------|
| `--cwd` | string | Working directory |
| `--env` | stringArray | Environment variables (KEY=value) |
| `--transport` | string | Transport type (stdio, http, sse) |

#### Examples

```bash
# Add filesystem server (stdio)
leanproxy-mcp server add filesystem "npx -y @modelcontextprotocol/server-filesystem" "./"

# Add GitHub server
leanproxy-mcp server add github "npx -y @modelcontextprotocol/server-github"

# Add with environment variables
leanproxy-mcp server add myserver "npx -y my-server" --env API_KEY=xxx --env SECRET=yyy

# Add HTTP transport server
leanproxy-mcp server add http-server "http://localhost:8081" --transport http
```

#### Output

```
Server 'filesystem' added successfully.
```

---

### `server remove` - Remove Server

Remove an MCP server configuration.

#### Usage

```bash
leanproxy-mcp server remove <name> [flags]
```

#### Examples

```bash
leanproxy-mcp server remove filesystem
```

#### Output

```
Server 'filesystem' removed.
```

---

### `server list` - List Servers

List all configured MCP servers.

#### Usage

```bash
leanproxy-mcp server list [flags]
```

#### Flags

| Flag | Type | Description |
|------|------|-------------|
| `--source` | string | Filter by source (opencode, claude, vscode, cursor, generic) |

#### Examples

```bash
# List all servers
leanproxy-mcp server list

# Filter by source
leanproxy-mcp server list --source opencode
```

#### Output

```
NAME        STATUS    TRANSPORT  SOURCE    COMMAND
filesystem enabled   stdio     generic   npx -y @modelcontextprotocol/server-filesystem ./
github     disabled  stdio     generic   npx -y @modelcontextprotocol/server-github
```

If no servers configured:
```
No servers configured.
```

---

### `server health` - Health Check

Check if an MCP server is healthy and responding. This command sends a `ping` request to the MCP server to verify it's working.

#### Usage

```bash
leanproxy-mcp server health <server_name> [flags]
```

#### Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--timeout` | duration | 10s | Health check timeout |
| `--config` | string | - | Path to leanproxy_servers.yaml config file |

#### Examples

```bash
# Check health of garmin server
leanproxy-mcp server health garmin

# Check health with custom timeout
leanproxy-mcp server health garmin --timeout 30s
```

#### Output (Server healthy)

```
✓ Server "garmin" is healthy (status: running, uptime: 5m30s)
  Note: Connected to running LeanProxy instance
```

#### Output (Server was stopped, restarted)

```
Note: Found running LeanProxy (PID: 1656) but server "garmin" may have stopped
      Attempting to restart server...
✓ Server "garmin" is healthy (latency: 2.1s)
  Note: Server was stopped in running LeanProxy, restarted successfully
```

#### Output (No running LeanProxy)

```
Note: No running LeanProxy instance found
time=2026-05-05T21:18:16.467+02:00 level=INFO msg="worker pool started" workers=4 queue_size=1000
time=2026-05-05T21:18:16.469+02:00 level=INFO msg="server spawned" name=garmin pid=91333
✓ Server "garmin" is healthy (latency: 1.7s)
  Note: Started new LeanProxy instance for health check
```

#### How It Works

1. **Check running LeanProxy**: First checks if there's a running LeanProxy instance
2. **Check server status**: If found, checks if the server is marked as "running" in the status file
3. **Connect or restart**:
   - If server is running → returns healthy immediately
   - If server is stopped but LeanProxy is running → restarts the MCP server
   - If no LeanProxy running → starts a new one just for health check
4. **Send ping**: Sends MCP protocol `ping` request to verify responsiveness

#### Use Cases

- **CI/CD verification**: Verify MCP servers are healthy before running tests
- **Monitoring**: Quick status check without using LLM tokens
- **Debugging**: Verify a specific server is responding

---

### `server enable` - Enable Server

Enable a disabled MCP server.

#### Usage

```bash
leanproxy-mcp server enable <name>
```

#### Examples

```bash
leanproxy-mcp server enable github
```

#### Output

```
Server 'github' enabled.
```

---

### `server disable` - Disable Server

Disable an enabled MCP server.

#### Usage

```bash
leanproxy-mcp server disable <name>
```

#### Examples

```bash
leanproxy-mcp server disable github
```

#### Output

```
Server 'github' disabled.
```

---

## `bouncer` - Redaction Settings

Manage Bouncer redaction (token firewall) settings.

### Usage

```bash
leanproxy-mcp bouncer [command]
```

### Subcommands

| Command | Description |
|---------|-------------|
| `list-patterns` | List all active redaction patterns |
| `validate-patterns` | Validate custom patterns from config |

---

### `bouncer list-patterns` - List Patterns

List all active redaction patterns.

#### Usage

```bash
leanproxy-mcp bouncer list-patterns
```

#### Examples

```bash
leanproxy-mcp bouncer list-patterns
```

#### Output

```
# Built-in Patterns
  - aws-access-key: AWS Access Key ID (20 characters, starts with AKIA)
  - github-classic-pat: GitHub Classic Personal Access Token (starts with ghp_)
  - github-fine-grained-pat: GitHub Fine-grained PAT (starts with github_pat_)
  - stripe-secret-key: Stripe Live Secret Key (starts with sk_live_)
  - stripe-publishable-key: Stripe Live Publishable Key (starts with pk_live_)
  - generic-api-key: Generic API key pattern (case-insensitive)
  - bearer-token: JWT Bearer token (three base64url segments)
  - env-var-value: Environment variable assignment
```

---

### `bouncer validate-patterns` - Validate Patterns

Validate custom redaction patterns from config.

#### Usage

```bash
leanproxy-mcp bouncer validate-patterns
```

#### Examples

```bash
leanproxy-mcp bouncer validate-patterns --config custom.yaml
```

#### Output (success)

```
All patterns valid.
```

#### Output (error)

```
Error: invalid regex pattern '[' at line 3
```

---

## `compactor` - Token Optimization via Manifest Distillation

The compactor optimizes token usage by compressing MCP server tool descriptions using an LLM.

### What it does

When an MCP server starts, it provides a manifest listing all its tools with descriptions. These descriptions can be verbose, causing unnecessary token usage on every LLM request.

The compactor:
1. Takes each tool's description
2. Uses an LLM to compress it to ~50 characters while preserving technical accuracy
3. Caches the optimized version to avoid re-distillation

This is transparent to users - LeanProxy automatically uses distilled manifests when available.

### Example

**Before (raw manifest):**
```
Tool: "read_file" - "Reads the complete contents of a file from the filesystem, supporting both text and binary formats, with optional encoding selection"
```

**After (distilled):**
```
Tool: "read_file" - "Read file contents from filesystem"
```

### Configuration

The compactor requires LLM configuration in `leanproxy_servers.yaml`:

```yaml
compactor:
  enabled: true
  llm-endpoint: "https://api.openai.com/v1/chat/completions"
  llm-api-key: "${OPENAI_API_KEY}"
  llm-model: "gpt-4o-mini"
```

### Usage

```bash
leanproxy-mcp compactor [command]
```

### Subcommands

| Command | Description |
|---------|-------------|
| `rebuild` | Force re-distillation of server manifests |

---

### `compactor rebuild` - Rebuild Manifests

Force re-distillation of server manifests to refresh stale discovery signatures.

**When to use:**
- Tool descriptions have changed in the MCP server
- You want to re-optimize with a different LLM model
- The cache became stale

#### Usage

```bash
leanproxy-mcp compactor rebuild [server-name] [flags]
```

#### Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--all` | bool | false | Rebuild all servers |

#### Examples

```bash
# Rebuild a specific server
leanproxy-mcp compactor rebuild github

# Rebuild all servers
leanproxy-mcp compactor rebuild --all

# Dry run
leanproxy-mcp compactor rebuild github --dry-run
```

---

## `cache` - Tool Cache Inspector

Inspect the persisted tool cache to see what tools have been indexed from MCP servers. The cache persists tool information from servers even when servers are stopped, allowing LLMs to search for tools without starting servers.

### Usage

```bash
leanproxy-mcp cache [flags]
```

### Cache Location

The tool cache is stored at:
```
~/.config/leanproxy/toolcache/
```

Each server's tools are cached in a separate JSON file:
- `garmin.json`
- `Intervals_icu.json`
- etc.

### Flags

| Flag | Type | Description |
|------|------|-------------|
| `--list` | bool | List all servers with cached tools |
| `--location` | bool | Show the cache directory location |
| `--server` | string | Show cached tools for a specific server |
| `--search` | string | Search cached tools by name or description |
| `--clear` | bool | Clear cache for specified server (use with --server) |
| `--json` | bool | Output in JSON format |

### Examples

```bash
# Show cache location
leanproxy-mcp cache --location

# List servers with cached tools
leanproxy-mcp cache --list

# Show cached tools for a server
leanproxy-mcp cache --server garmin

# Search in cache (across all servers)
leanproxy-mcp cache --search activity

# Search within a specific server
leanproxy-mcp cache --server garmin --search sleep

# Clear cache for a server
leanproxy-mcp cache --clear --server garmin
```

#### Output (--location)

```
Tool cache location: ~/.config/leanproxy/toolcache
```

#### Output (--list)

```
Servers with cached tools (2):

  - Intervals_icu
  - garmin

Use --server <name> to see tools for a specific server
```

#### Output (--search activity)

```
Intervals_icu (4 matches):
  get_activity_details
    Get detailed information for a specific activity from Intervals.icu
  get_activity_intervals
    Get interval data for a specific activity from Intervals.icu
  ...

garmin (18 matches):
  get_activities_by_date
    Get activities data between specified dates, optionally filtered by activity type
  ...

Total: 22 matches across 2 servers
```

#### Output (--server garmin)

```
Cached tools for garmin (100 total):

  garmin_get_activities_by_date
    Get activities data between specified dates, optionally filtered by activity type

        Args:
            start_date: Start date in YYYY-MM-DD format
            end_date: End date in YYYY-MM-DD format
            activity_type: Optional activity type filter (e.g., cycling, running, swimming)
         [start_date: string, end_date: string] {activity_type: string}

  garmin_get_activities_fordate
    Get activities for a specific date

        Args:
            date: Date in YYYY-MM-DD format
         [date: string]
```

---

## `list_tools` - MCP Method

LeanProxy-MCP supports a `list_tools` MCP method that allows LLMs to list all tools available on a specific MCP server. This is particularly useful when used with OpenCode - the LLM first calls `list_servers` to get available servers, then `list_tools` to see tools on a specific server.

### Request Format

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "list_tools",
    "arguments": {
      "server_name": "garmin",
      "max_description_chars": 200
    }
  }
}
```

### Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `server_name` | string | Yes | MCP server name (from `list_servers`). Identifies which server's tools to list. |
| `max_description_chars` | integer | No | Truncate descriptions to this length (default: 200, range: 50-500) |

### Response Format

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "content": [{
      "type": "text",
      "text": "github tools (12):\ngithub_create_issue: Create a new issue... [title: string, body: string] {labels: string}\ngithub_list_issues: List repository issues... [owner: string, repo: string] {state: string}\n..."
    }]
  }
}
```

### Tool Display Format

Each tool is displayed with:
- **Name**: `tool_name` (without server prefix in list_tools output)
- **Description**: Full or truncated description
- **Parameters**:
  - `[required: type]` - Required parameters in brackets
  - `{optional: type}` - Optional parameters in braces

Example:
```
garmin tools (5):
get_activities: Get activities data between specified dates [start_date: string, end_date: string] {activity_type: string}
get_sleep_data: Get sleep data [start_date: string, end_date: string] {}
```

### How Tool Caching Works

1. **On First Call**: When `list_tools` is called for a specific server for the first time (or after cache invalidation), LeanProxy-MCP:
   - Starts the specified MCP server (if not running)
   - Sends `initialize` request to the server
   - Sends `tools/list` request to the server
   - Caches the tool signatures locally in `~/.config/leanproxy/toolcache/`

2. **On Subsequent Calls**: Tool signatures are loaded from the persistent cache, avoiding server startup.

3. **Cache Invalidation**: Cache is invalidated when:
   - `leanproxy cache --clear --server <name>` is called
   - Server configuration changes
   - Tool list changes are detected (if `listChanged` capability is supported)

### Status File

When `server run --stdio` or `serve` is running, a status file is written to:
```
~/.config/leanproxy/status/current.json
```

This allows `leanproxy status --running` to detect running instances.

**Status File Contents:**
```json
{
  "pid": 12345,
  "started_at": "2026-05-03T19:30:00+02:00",
  "listen_addr": "stdio",
  "servers": [
    {
      "name": "garmin",
      "status": "running",
      "request_count": 10,
      "error_count": 0,
      "restart_count": 1
    }
  ]
}
```

The status file is:
- Written immediately when the server starts
- Updated every 5 seconds while running
- Removed when the server shuts down gracefully

---

## `status` - Server Status

Display real-time status of all active proxied servers. Can show status either from running instances (via status file) or from configuration.

### Usage

```bash
leanproxy-mcp status [flags]
```

### Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--interval` | duration | `1s` | Watch mode refresh interval |
| `--json` | bool | false | Output in JSON format |
| `--running` | bool | false | Only show running instances from status file |
| `--server` | string | "" | Filter by server name |
| `--verbose` | bool | false | Show additional details |
| `--watch` | bool | false | Continuously update |

### Status File

When using `--running`, status is read from the status file written by running instances:

```
~/.config/leanproxy/status/current.json
```

This file is created by:
- `leanproxy-mcp serve` (HTTP proxy mode)
- `leanproxy-mcp server run --stdio` (stdio mode, used by OpenCode)

### Examples

```bash
# Basic status (from config)
leanproxy-mcp status

# Status from running instance only
leanproxy-mcp status --running

# Watch mode
leanproxy-mcp status --watch

# JSON output
leanproxy-mcp status --json

# Verbose with more details
leanproxy-mcp status --verbose

# Filter by server
leanproxy-mcp status --server filesystem

# Custom refresh interval
leanproxy-mcp status --watch --interval 500ms
```

#### Output (--running)

```
Running leanproxy instance (PID: 12345, started: 2026-05-03 19:30:00, listen: stdio)

SERVER       STATUS      UPTIME     LAST RESPONSE   RESTARTS
──────────────────────────────────────────────────────────────
garmin       running    0s         -          1
Intervals.icu running    0s         -          1
```

**Note**: The `--running` flag reads from the status file. If no instances are running, you'll see:
```
No running leanproxy instance found
No servers configured
```

#### Output (basic)

```
SERVER      STATUS    HEALTH    UPTIME    REQUESTS
filesystem Up        healthy  5m32s    1,234
github     Down     -         -         -
```

#### Output (JSON)

```json
{
  "servers": [
    {
      "name": "filesystem",
      "status": "Up",
      "health": "healthy",
      "uptime": "5m32s",
      "requests": 1234
    }
  ]
}
```

#### Output (verbose)

```
SERVER      STATUS    HEALTH    UPTIME    REQUESTS  ERRORS  MEMORY
filesystem Up        healthy  5m32s    1,234    0       45MB
github     Down     -         -         -        -       -
```

---

## `savings` - Token Savings

Display cumulative token savings statistics.

### Usage

```bash
leanproxy-mcp savings [flags]
```

### Flags

| Flag | Type | Description |
|------|------|-------------|
| `--json` | bool | Output in JSON format |
| `--reset` | bool | Reset cumulative counters |
| `--server` | string | Filter by server name |

#### Examples

```bash
# Show all savings
leanproxy-mcp savings

# JSON output
leanproxy-mcp savings --json

# Filter by server
leanproxy-mcp savings --server filesystem

# Reset counters
leanproxy-mcp savings --reset
```

#### Output

```
Total token savings: 45,678 (12.3%)
By server:
  filesystem: 32,456 (11.2%)
  github: 13,222 (14.1%)
```

#### Output (JSON)

```json
{
  "total": 45678,
  "percentage": 12.3,
  "by_server": {
    "filesystem": {"savings": 32456, "percentage": 11.2},
    "github": {"savings": 13222, "percentage": 14.1}
  }
}
```

---

## `cost` - Token Cost Attribution

Display token usage broken down by tool and server for the current session. This allows you to see which tools consume the most tokens.

### Usage

```bash
leanproxy-mcp cost [flags]
```

### Flags

| Flag | Type | Description |
|------|------|-------------|
| `--by-tool` | bool | Show cost breakdown by tool only |
| `--by-server` | bool | Show cost breakdown by server only |
| `--json` | bool | Output in JSON format |
| `--reset` | bool | Reset cost counters |

#### Examples

```bash
# Show full cost breakdown
leanproxy-mcp cost

# Show cost by tool only
leanproxy-mcp cost --by-tool

# Show cost by server only
leanproxy-mcp cost --by-server

# JSON output
leanproxy-mcp cost --json

# Reset counters
leanproxy-mcp cost --reset
```

#### Output (full breakdown)

```
=== Token Cost Summary ===
Total Session Tokens: 1234
Session Duration:     5m30s

=== Token Cost by Tool ===
github.create_issue: 450 tokens
github.list_issues: 350 tokens
filesystem.read_file: 280 tokens
filesystem.list_directory: 154 tokens

=== Token Cost by Server ===
github: 800 tokens
filesystem: 434 tokens
```

#### Output (--by-tool)

```
=== Token Cost Summary ===
Total Session Tokens: 1234
Session Duration:     5m30s

=== Token Cost by Tool ===
github.create_issue: 450 tokens
github.list_issues: 350 tokens
filesystem.read_file: 280 tokens
filesystem.list_directory: 154 tokens
```

#### Output (--by-server)

```
=== Token Cost Summary ===
Total Session Tokens: 1234
Session Duration:     5m30s

=== Token Cost by Server ===
github: 800 tokens
filesystem: 434 tokens
```

#### Output (JSON)

```json
{
  "by_tool": [
    {"tool_name": "github.create_issue", "token_count": 450},
    {"tool_name": "github.list_issues", "token_count": 350},
    {"tool_name": "filesystem.read_file", "token_count": 280},
    {"tool_name": "filesystem.list_directory", "token_count": 154}
  ],
  "by_server": [
    {"server_name": "github", "token_count": 800},
    {"server_name": "filesystem", "token_count": 434}
  ],
  "total": 1234,
  "duration": "5m30s"
}
```

### How It Works

The cost tracking system monitors token usage during tool invocations:

1. **Token Estimation**: When a tool is called, the system estimates token count from request/response size (using ~4 characters per token)
2. **Per-Tool Tracking**: Tokens are attributed to the specific tool that was invoked
3. **Per-Server Tracking**: Tokens are also aggregated by the MCP server that handled the request
4. **Session Duration**: The time since the session started is tracked

### Status File Integration

Cost tracking data is also available via the status file at:
```
~/.config/leanproxy/status/current.json
```

The status file includes a `cost_tracking` section when enabled:
```json
{
  "pid": 12345,
  "started_at": "2026-05-08T10:00:00+02:00",
  "listen_addr": "stdio",
  "servers": [...],
  "cost_tracking": {
    "by_tool": {"github.create_issue": 450, "github.list_issues": 350},
    "by_server": {"github": 800},
    "total": 1234,
    "enabled": true
  }
}
```

### Use Cases

- **Identify expensive tools**: Find which tools consume the most tokens
- **Cost allocation**: Understand which MCP servers are driving costs
- **Optimization insights**: Identify opportunities to optimize tool usage

---

## `report` - Generate Report

Generate a Markdown-formatted report on token savings and security risks.

### Usage

```bash
leanproxy-mcp report [flags]
```

### Flags

| Flag | Type | Description |
|------|------|-------------|
| `--json` | bool | Output JSON instead of Markdown |
| `--no-security` | bool | Exclude security events |
| `--output` | string | Output file path |
| `--session-id` | string | Generate for specific session |

#### Examples

```bash
# Generate report
leanproxy-mcp report

# Output to file
leanproxy-mcp report --output savings.md

# JSON output
leanproxy-mcp report --json

# Exclude security
leanproxy-mcp report --no-security
```

#### Output (Markdown)

```markdown
# LeanProxy Session Report

## Summary
- Session ID: abc123
- Duration: 1h 23m
- Total Requests: 1,456

## Token Savings
| Server | Original | Redacted | Savings |
|--------|----------|---------|---------|
| filesystem | 45,678 | 32,222 | 29.4% |
| github | 12,345 | 9,876 | 20.0% |

## Security Events
| Type | Count |
|------|-------|
| api-key | 15 |
| bearer-token | 3 |
```

---

## `migrate` - Import Configurations

Auto-detect and import MCP server configurations from other tools.

### Usage

```bash
leanproxy-mcp migrate [flags]
```

### Flags

| Flag | Type | Description |
|------|------|-------------|
| `--dry-run` | bool | Preview scan results without importing |
| `--target` | string | Target config file path |
| `--validate-only` | bool | Only validate servers without importing |
| `--yes` | bool | Skip confirmation prompt |

#### Examples

```bash
# Auto-detect all sources
leanproxy-mcp migrate

# Dry run (preview what would be imported)
leanproxy-mcp migrate --dry-run

# Skip confirmation
leanproxy-mcp migrate --yes

# Validate without importing
leanproxy-mcp migrate --validate-only
```

#### Output

```
Found 4 MCP server(s) from 1 source(s):

  OpenCode: 4 server(s)

  [1] nexus-dev (opencode) - /usr/bin/env
  [2] nexus-dev-test (opencode) - /usr/bin/env
  [3] garmin (opencode) - uvx
  [4] Intervals.icu (opencode) - /usr/bin/env

Import to ~/.config/leanproxy_servers.yaml? [y/N]:
```

---

## `completion` - Shell Completions

Generate shell completion scripts.

### Usage

```bash
leanproxy-mcp completion [shell]
```

### Arguments

| Shell | Description |
|-------|-------------|
| `bash` | Bash completion |
| `zsh` | Zsh completion |
| `fish` | Fish completion |
| `powershell` | PowerShell completion |

#### Examples

```bash
# Bash
leanproxy-mcp completion bash > /etc/bash_completion.d/leanproxy-mcp

# Zsh
leanproxy-mcp completion zsh > ~/.zsh/completions/_leanproxy-mcp

# Fish
leanproxy-mcp completion fish > ~/.config/fish/completions/leanproxy-mcp.fish
```

---

## `namespace` - Hierarchical Namespace Management

Manage hierarchical namespaces for organizing MCP servers. Namespaces allow multi-team organizations to manage access to MCP servers by grouping them under logical organizational units.

### Usage

```bash
leanproxy-mcp namespace [command]
```

### Subcommands

| Command | Description |
|---------|-------------|
| `list` | List all namespaces or tools in a namespace |
| `add` | Add a new namespace |
| `assign` | Assign a server to a namespace |

---

### `namespace list` - List Namespaces

List all configured namespaces or show details about a specific namespace.

#### Usage

```bash
leanproxy-mcp namespace list [namespace] [flags]
```

#### Flags

| Flag | Type | Description |
|------|------|-------------|
| `--tools` | bool | List tools in the namespace |

#### Examples

```bash
# List all namespaces
leanproxy-mcp namespace list

# Show details of a specific namespace
leanproxy-mcp namespace list engineering

# List tools in a namespace
leanproxy-mcp namespace list engineering --tools
```

#### Output (all namespaces)

```
Configured namespaces:
  - engineering: Engineering team tools [2 servers]
  - ops: Operations infrastructure [2 servers]
  - engineering.frontend: Frontend team [1 servers]
```

#### Output (specific namespace)

```
Namespace: engineering
Description: Engineering team tools
Servers: [github jira]
Children: [frontend]
```

#### Output (tools in namespace)

```
Tools in namespace 'engineering':
  - engineering.github (server: github)
  - engineering.jira (server: jira)
```

---

### `namespace add` - Add Namespace

Generate example configuration for a new namespace.

#### Usage

```bash
leanproxy-mcp namespace add <namespace> [flags]
```

#### Flags

| Flag | Type | Description |
|------|------|-------------|
| `--servers` | string | Comma-separated list of servers |
| `--description` | string | Namespace description |

#### Examples

```bash
# Add a new namespace
leanproxy-mcp namespace add engineering --servers=github,jira --description="Engineering team"

# Add with description
leanproxy-mcp namespace add frontend --description="Frontend team tools"
```

#### Output

```
Adding namespace 'engineering'
  Servers: github,jira
  Description: Engineering team

Note: Namespace configuration should be added to leanproxy.yaml
Example configuration:
  namespaces:
    engineering:
      servers:
        - github
        - jira
      description: "Engineering team"
```

---

### `namespace assign` - Assign Server

Generate example configuration for assigning a server to a namespace.

#### Usage

```bash
leanproxy-mcp namespace assign <namespace> <server>
```

#### Examples

```bash
# Assign a server to a namespace
leanproxy-mcp namespace assign engineering github
```

#### Output

```
Assigning server 'github' to namespace 'engineering'

Note: This operation requires updating leanproxy.yaml
Add 'github' to the 'engineering' namespace servers list.
```

---

### Configuration

Namespaces are configured in `leanproxy.yaml` under the `namespaces` key:

```yaml
namespaces:
  engineering:
    description: "Engineering team tools"
    servers:
      - github
      - jira
    children:
      frontend:
        servers:
          - storybook
  ops:
    servers:
      - aws
      - kubernetes
```

#### Namespace Options

| Field | Type | Description |
|-------|------|-------------|
| `description` | string | Human-readable description of the namespace |
| `servers` | []string | List of server IDs in this namespace |
| `children` | map | Nested namespaces (parent includes children) |
| `allowed_clients` | []string | Clients allowed to access this namespace (supports `*` for wildcard) |

#### Access Control Example

```yaml
namespaces:
  restricted:
    description: "Restricted access namespace"
    allowed_clients:
      - "client1"
      - "client2"
      - "*"  # Wildcard allows any client
    servers:
      - secure-server
  public:
    description: "Public namespace (no access restrictions)"
    servers:
      - public-server
```

---

## `version` - Version Info

Print version information.

### Usage

```bash
leanproxy-mcp version
```

#### Output

```
 leanproxy-mcp version 0.5.2
 build date: 2026-05-04
 platform: darwin/arm64
 go: go1.25.5
```

---

## Exit Codes

| Code | Meaning |
|------|--------|
| 0 | Success |
| 1 | General error |
| 2 | Configuration error |
| 3 | Network error |
| 4 | Permission error |

---

## Next Steps

- [Quick Start](./quickstart.md) - Get started quickly
- [Configuration](./configuration.md) - Customize behavior
- [Troubleshooting](./troubleshooting.md) - Common issues