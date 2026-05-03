# Commands Reference

Complete reference for all LeanProxy-MCP CLI commands.

## Main Command

```bash
leanproxy [command] [flags]
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
leanproxy serve [flags]
```

### Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--listen` | string | `127.0.0.1:8080` | Address to listen on |
| `--upstream` | string | `http://localhost:8081` | Upstream JSON-RPC server URL |

### Examples

```bash
# Start proxy server
leanproxy serve

# Listen on custom address
leanproxy serve --listen 0.0.0.0:9090

# Custom upstream server
leanproxy serve --upstream http://localhost:9000

# With verbose logging
leanproxy serve --verbose
```

## `server` - Manage MCP Servers

Add, remove, list, enable, or disable MCP servers.

### Usage

```bash
leanproxy server [command]
```

### Subcommands

| Command | Description |
|---------|-------------|
| `add` | Add a new MCP server |
| `remove` | Remove an MCP server |
| `list` | List all configured servers |
| `enable` | Enable a disabled server |
| `disable` | Disable an enabled server |

---

### `server add` - Add Server

Add a new MCP server configuration.

#### Usage

```bash
leanproxy server add <name> <command> [args...] [flags]
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
leanproxy server add filesystem "npx -y @modelcontextprotocol/server-filesystem" "./"

# Add GitHub server
leanproxy server add github "npx -y @modelcontextprotocol/server-github"

# Add with environment variables
leanproxy server add myserver "npx -y my-server" --env API_KEY=xxx --env SECRET=yyy

# Add HTTP transport server
leanproxy server add http-server "http://localhost:8081" --transport http
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
leanproxy server remove <name> [flags]
```

#### Examples

```bash
leanproxy server remove filesystem
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
leanproxy server list [flags]
```

#### Flags

| Flag | Type | Description |
|------|------|-------------|
| `--source` | string | Filter by source (opencode, claude, vscode, cursor, generic) |

#### Examples

```bash
# List all servers
leanproxy server list

# Filter by source
leanproxy server list --source opencode
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
leanproxy server enable <name>
```

#### Examples

```bash
leanproxy server enable github
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
leanproxy server disable <name>
```

#### Examples

```bash
leanproxy server disable github
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
leanproxy bouncer [command]
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
leanproxy bouncer list-patterns
```

#### Examples

```bash
leanproxy bouncer list-patterns
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
leanproxy bouncer validate-patterns
```

#### Examples

```bash
leanproxy bouncer validate-patterns --config custom.yaml
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
leanproxy compactor [command]
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
leanproxy compactor rebuild [flags]
```

#### Examples

```bash
# Rebuild all manifests
leanproxy compactor rebuild

# Dry run
leanproxy compactor rebuild --dry-run
```

#### Output

```
Distillates rebuilt for 3 servers.
```

---

## `status` - Server Status

Display real-time status of all active proxied servers.

### Usage

```bash
leanproxy status [flags]
```

### Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--interval` | duration | `1s` | Watch mode refresh interval |
| `--json` | bool | false | Output in JSON format |
| `--server` | string | "" | Filter by server name |
| `--verbose` | bool | false | Show additional details |
| `--watch` | bool | false | Continuously update |

#### Examples

```bash
# Basic status
leanproxy status

# Watch mode
leanproxy status --watch

# JSON output
leanproxy status --json

# Verbose with more details
leanproxy status --verbose

# Filter by server
leanproxy status --server filesystem

# Custom refresh interval
leanproxy status --watch --interval 500ms
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
leanproxy savings [flags]
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
leanproxy savings

# JSON output
leanproxy savings --json

# Filter by server
leanproxy savings --server filesystem

# Reset counters
leanproxy savings --reset
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
leanproxy report [flags]
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
leanproxy report

# Output to file
leanproxy report --output savings.md

# JSON output
leanproxy report --json

# Exclude security
leanproxy report --no-security
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
leanproxy migrate [flags]
```

### Flags

| Flag | Type | Description |
|------|------|-------------|
| `--force` | bool | Overwrite existing servers |
| `--source` | string | Source tool (claude, cursor, vscode, opencode, generic) |

#### Examples

```bash
# Auto-detect all sources
leanproxy migrate

# From specific source
leanproxy migrate --source claude

# Force overwrite
leanproxy migrate --force
```

#### Output

```
Detected MCP servers:
  - claude-desktop: 2 servers
  - cursor: 1 server

Import? [y/N]
```

---

## `completion` - Shell Completions

Generate shell completion scripts.

### Usage

```bash
leanproxy completion [shell]
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
leanproxy completion bash > /etc/bash_completion.d/leanproxy

# Zsh
leanproxy completion zsh > ~/.zsh/completions/_leanproxy

# Fish
leanproxy completion fish > ~/.config/fish/completions/leanproxy.fish
```

---

## `version` - Version Info

Print version information.

### Usage

```bash
leanproxy version
```

#### Output

```
 leanproxy-mcp version 0.2.0
 build date: 2026-05-01
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