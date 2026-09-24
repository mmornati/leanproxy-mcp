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

### 2b. Or run one shared gateway over HTTP

`server run --stdio` serves one client: every IDE spawns its own LeanProxy,
with its own child servers. `server run --http` serves the MCP **Streamable
HTTP** transport instead. It is one local gateway that any number of MCP
clients reach by URL, and they all share one set of child servers:

```bash
leanproxy-mcp server run --http 127.0.0.1:8765
# endpoint: http://127.0.0.1:8765/mcp
```

- **Token.** Clients authenticate with `Authorization: Bearer <token>`. The
  token is the same one `serve` uses: `--http-token`, else
  `$LEANPROXY_SERVE_TOKEN`, else `~/.config/leanproxy/serve.token`, which
  is generated (mode 0600) on first start. Print it with
  `cat ~/.config/leanproxy/serve.token`.
- **Loopback.** Bind a loopback address. Binding another interface requires
  the token, and `--no-auth` is only accepted on loopback.
- **Browsers.** Requests with an unknown `Host` header, or from a browser
  origin you did not allow (`--http-allowed-origins`), are refused with 403.

See [`server run`](commands.md#server-run-run-the-mcp-front-end) for every
flag and [Security](security.md#streamable-http-front-end-309) for the
details.

#### Client configuration: stdio or HTTP

In the HTTP snippets, replace `<token>` with the content of
`~/.config/leanproxy/serve.token`, or use your client's secret or
environment substitution.

**Claude Code**

```bash
# stdio: Claude Code starts its own LeanProxy
claude mcp add leanproxy -- leanproxy-mcp server run --stdio

# HTTP: connect to the shared gateway
claude mcp add --transport http leanproxy http://127.0.0.1:8765/mcp \
  --header "Authorization: Bearer $(cat ~/.config/leanproxy/serve.token)"
```

**Cursor** (`~/.cursor/mcp.json`)

```json
{
  "mcpServers": {
    "leanproxy-stdio": { "command": "leanproxy-mcp", "args": ["server", "run", "--stdio"] },
    "leanproxy-http": {
      "url": "http://127.0.0.1:8765/mcp",
      "headers": { "Authorization": "Bearer <token>" }
    }
  }
}
```

**VS Code** (`.vscode/mcp.json`)

```json
{
  "inputs": [
    { "type": "promptString", "id": "leanproxy-token", "description": "LeanProxy token", "password": true }
  ],
  "servers": {
    "leanproxy-stdio": { "type": "stdio", "command": "leanproxy-mcp", "args": ["server", "run", "--stdio"] },
    "leanproxy-http": {
      "type": "http",
      "url": "http://127.0.0.1:8765/mcp",
      "headers": { "Authorization": "Bearer ${input:leanproxy-token}" }
    }
  }
}
```

**OpenCode** (`~/.config/opencode/opencode.json`)

```json
{
  "mcp": {
    "leanproxy-stdio": { "type": "local", "command": ["leanproxy-mcp", "server", "run", "--stdio"], "enabled": true },
    "leanproxy-http": {
      "type": "remote",
      "url": "http://127.0.0.1:8765/mcp",
      "headers": { "Authorization": "Bearer {env:LEANPROXY_SERVE_TOKEN}" },
      "enabled": true
    }
  }
}
```

Configure only one of the two entries per client. With both, the client
would see every tool twice.

#### Which exposure mode each IDE gets

LeanProxy recognizes the client by the `clientInfo.name` it sends when it
connects and picks how to show it the upstream tools
([`exposure`](configuration.md#exposure-modes-exposure), #322):

| IDE / client | Mode | What the model sees | Why |
|---|---|---|---|
| Claude Code | `passthrough` | Every upstream tool, as `<server>__<tool>` (e.g. `github__create_issue`) | Claude Code's MCP tool search (on by default) keeps only the tool names in context and loads a definition when it needs it, so LeanProxy lets it do the discovery and keeps the security layer |
| Claude Desktop | `passthrough` | Same | Per-tool permissions and UI work on the real tools |
| Cursor | `passthrough` | Same | Cursor discovers MCP tools dynamically |
| VS Code (GitHub Copilot) | `passthrough` | Same | Its tool picker groups and selects tools per request |
| Anything else (OpenCode, Zed, custom agents, ...) | `router` | 4 discovery tools: `search_tools`, `list_servers`, `list_tools`, `invoke_tool` | A client that puts every tool definition in context would pay for the whole catalog on every turn; the router loads schemas only when needed |

In every mode the same security layers apply: redaction, the injection
guard, tool pinning (a pending tool is hidden), the per-tool policy (a denied
tool is hidden, a `confirm` tool is marked `[confirm]`) and the response
governor.

To force a mode for one IDE, add `--exposure` to that IDE's command, for
example to keep Claude Code on the router:

```bash
claude mcp add leanproxy -- leanproxy-mcp server run --stdio --exposure router
```

Or set it per client name in the config (`exposure.clients`), which also
covers the HTTP gateway, where every client shares one process:

```yaml
exposure:
  clients:
    - match: "claude-code"
      mode: hybrid          # passthrough + search_tools
```

Note that Claude Code keeps its tool search on for LeanProxy: the "custom
`ANTHROPIC_BASE_URL` disables tool search" rule is about the model API, not
MCP servers.

#### Migrating from `serve`

`leanproxy-mcp serve` speaks newline-delimited JSON-RPC over raw TCP, which
is not an MCP transport. It is **deprecated**: it logs a warning at start
and will be removed in v1.0. To move to the Streamable HTTP front end:

| `serve` | `server run --http` |
|---|---|
| `--listen 127.0.0.1:8080` | `--http 127.0.0.1:8765` (endpoint `/mcp`) |
| `--auth-token`, `$LEANPROXY_SERVE_TOKEN`, `serve.token` | `--http-token`, `$LEANPROXY_SERVE_TOKEN`, the same `serve.token` |
| First line `{"method":"auth","params":{"token":…}}` | `Authorization: Bearer <token>` header on every request |
| `--no-auth` (loopback only) | `--no-auth` (loopback only) |
| One TCP connection = one client session | One `Mcp-Session-Id` = one client session |
| `server.max_connections`, `server.max_line_bytes` | `server.http.max_sessions`, `server.http.max_body_bytes` |

Any MCP client that supports Streamable HTTP can connect directly, with no
custom client. The IDE extensions (`extensions/vscode`,
`extensions/jetbrains`) only read the metrics endpoint and never connected to
`serve`, so they need no change.

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

When LeanProxy-MCP aggregates tools from multiple servers, each tool is named
after its server:

- in `router` mode (discovery tools), `search_tools` and `list_tools` show
  `serverName_toolName` (e.g. `github_list_repos`), called with `invoke_tool`
  or directly as `github_list_repos` / `github.list_repos`;
- in `passthrough` and `hybrid` modes, `tools/list` names it
  `serverName__toolName` (two underscores, e.g. `github__list_repos`), cut
  to 64 characters with a hash suffix when needed.

### Tool Cache

LeanProxy-MCP automatically caches tool signatures from your MCP servers. This allows:
- **Tool search**: `search_tools` finds the right tool across all servers in one call
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

### Install IDE Extensions

Real-time cost monitoring in the editor status bar:

- **VS Code**: Install from Marketplace or `code --install-extension leanproxy.vsix`
- **JetBrains**: Install from Plugin Marketplace or build from source

Requires the metrics endpoint: `leanproxy-mcp serve --metrics-bind 127.0.0.1:9091`

## Next Steps

- [Commands Reference](./commands.md) - Full command documentation
- [Configuration](./configuration.md) - Customize LeanProxy-MCP
- [Web Dashboard](./dashboard.md) - Real-time monitoring
- [Troubleshooting](./troubleshooting.md) - Common issues and solutions