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
| `report` | Generate token savings report |
| `migrate` | Import MCP configs from other tools |
| `completion` | Generate shell completions |
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

## `compactor` - Manifest Caching

Manage distilled manifest caching and re-distillation.

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

Force re-distillation of all server manifests.

#### Usage

```bash
leanproxy-mcp compactor rebuild [flags]
```

#### Examples

```bash
# Rebuild all manifests
leanproxy-mcp compactor rebuild

# Dry run
leanproxy-mcp compactor rebuild --dry-run
```

#### Output

```
Distillates rebuilt for 3 servers.
```

---

## `cache` - Tool Cache Inspector

Inspect the persisted tool cache to see what tools have been indexed from MCP servers. The cache persists tool information from servers even when servers are stopped, allowing LLMs to search for tools without starting servers.

### Usage

```bash
leanproxy-mcp cache [flags]
```

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

# Search in cache
leanproxy-mcp cache --server garmin --search activity

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

  - garmin
  - Intervals.icu

Use --server <name> to see tools for a specific server
```

#### Output (--server garmin)

```
Cached tools for garmin (12 total):

  garmin_get_activities
    List activities for the authenticated user
    Parameters:
      - limit (number)
      - start_date (string)

  garmin_get_activity
    Downloads activity details with weather and gear
    Parameters:
      - activity_id (string)
      - max_chart_size (number)

  garmin_download_activity
    Downloads the original activity file
    Parameters:
      - activity_id (string)
      - download_format (string)
```

---

## `status` - Server Status

Display real-time status of all active proxied servers.

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

#### Examples

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
Running leanproxy instance (PID: 12345, started: 2026-05-03 19:30:00, listen: 127.0.0.1:8080)

SERVER      STATUS    HEALTH    UPTIME    REQUESTS
garmin      Up        healthy  5m32s    1,234
Intervals  Up        healthy  5m32s    567
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

## `version` - Version Info

Print version information.

### Usage

```bash
leanproxy-mcp version
```

#### Output

```
 leanproxy-mcp version v0.2.0
 build date: 2026-05-01
 platform: darwin/arm64
 go: go1.26.2
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