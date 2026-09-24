# Quick Start

Get LeanProxy-MCP running in front of your MCP servers in a few minutes.
This page assumes the binary is installed ([Installation](installation.md)).

## Basic Usage

### 1. Configure MCP Servers

LeanProxy reads its upstream servers from `~/.config/leanproxy_servers.yaml`.
There are three ways to fill it.

**Import what your IDE already has.** This is the fastest way:

```bash
leanproxy-mcp migrate --dry-run   # preview
leanproxy-mcp migrate             # import
```

`migrate` reads OpenCode, Claude Code (`~/.claude.json`), Cursor, VS Code
user settings and `~/.config/mcp.json`, and imports stdio servers only. It
misses some formats; see
[what `migrate` scans](installation.md#step-1-import-your-existing-mcp-servers).

**Add a server from the command line.** Put `--` before the server's
command, so flags such as `-y` belong to the server and not to LeanProxy:

```bash
leanproxy-mcp server add filesystem -- npx -y @modelcontextprotocol/server-filesystem "$HOME/projects"
```

The command (`npx` here) must be on your `PATH`. `server add` only writes
stdio servers correctly; add `http` and `sse` servers by editing the file.

**Or edit the file by hand**, with the format below.

!!! note "Which config file is used"
    By default every command uses `~/.config/leanproxy_servers.yaml`. To use
    another file, set `LEANPROXY_CONFIG=/path/to/file.yaml`, or pass
    `--config` to `server run`, `server health` and `status` (it takes
    precedence over `LEANPROXY_CONFIG` there). `server add`, `server list`,
    `server enable/disable/remove`, `add` and `migrate` ignore `--config` and
    only read `LEANPROXY_CONFIG`. Some commands (`serve`, `doctor`) ignore
    `LEANPROXY_CONFIG`. See
    [Config File Locations](configuration.md#config-file-locations) for the
    full table.

#### Server Configuration Format

A server uses one of three transports: `stdio`, `http` (Streamable HTTP) or
`sse`. Both `http` and `sse` read the URL from the `http:` block.

```yaml
version: "1.0"
servers:
  - name: <server-name>
    enabled: true
    transport: <stdio|http|sse>
    timeout: 30s              # per request
    # stdio only
    stdio:
      command: <command>
      args: [<args>]
      env: ["KEY=value"]      # ${VAR} is expanded from LeanProxy's environment
      env_passthrough: [<VAR>]
      cwd: <working-directory>
    # http and sse
    http:
      url: <url>
      headers:
        <header-key>: <header-value>
```

A stdio server does not get your whole environment. It gets a minimal set
(`PATH`, `HOME`, ...) plus what you list in `env` and `env_passthrough`.
Run `leanproxy-mcp doctor env` to see what each server receives. See
[Child Process Environment](configuration.md#child-process-environment-env-env_passthrough-inherit_env).

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
        - "@modelcontextprotocol/server-filesystem"
        - /Users/me/projects
    timeout: 30s
```

Quote any value that starts with `@`: YAML does not accept it unquoted.

##### HTTP Transport Example

```yaml
servers:
  - name: github
    enabled: true
    transport: http
    http:
      url: https://api.githubcopilot.com/mcp/
      headers:
        Authorization: Bearer ghp_yourPersonalAccessToken
    timeout: 30s
```

!!! warning
    Header values are stored as written: `${VAR}` is not expanded in `http:`.
    Keep the file private (`chmod 600 ~/.config/leanproxy_servers.yaml`).

##### HTTP Transport with an `auth` block

Instead of a raw header, the `http` transport accepts an `auth` block:

```yaml
servers:
  - name: enterprise-mcp
    enabled: true
    transport: http
    http:
      url: https://mcp.example.com/mcp
      auth:
        type: bearer
        client_secret: my-api-token
    timeout: 60s
```

#### Auth Types

| Type | What LeanProxy does |
|------|-------------|
| `bearer` | Sends `Authorization: Bearer <client_secret>` on every request |
| `oauth2` | Hands `client_id`, `client_secret` and `scopes` to the MCP Go client's OAuth support. LeanProxy runs no OAuth login and no token exchange, and ignores `token_url`, so the connection fails with `no valid token available, authorization required` |

!!! warning "OAuth servers"
    `oauth2` does not work yet. For a server that needs OAuth, get a token
    yourself and use `type: bearer` or an `Authorization` header.

`auth` applies to the `http` transport only. For `sse`, use `headers`.

##### SSE Transport Example

```yaml
servers:
  - name: my-sse-server
    enabled: true
    transport: sse
    http:
      url: https://your-server.example.com/sse
    timeout: 30s
```

There is no `sse:` block. A server with `transport: sse` and no `http.url`
fails validation.

!!! tip
    For GitHub's hosted MCP server, use the `http` transport with the
    `/mcp/` endpoint and your token in the `Authorization` header.

### 2. Run LeanProxy-MCP as an MCP Server

You normally do not start LeanProxy yourself: your IDE starts
`leanproxy-mcp server run --stdio` when it connects
([Connect your client](#connect-your-client)). Run it by hand to check that
your servers start:

```bash
leanproxy-mcp server run --stdio
```

It waits for MCP messages on stdin; press Ctrl+C to stop. If the config
has no servers, it exits with `no servers configured in <path>`.

With logging to a file (logs go to stderr by default):

```bash
leanproxy-mcp server run --stdio --log-file /tmp/leanproxy.log --log-level debug
```

With another config file:

```bash
leanproxy-mcp server run --stdio --config /path/to/leanproxy_servers.yaml
```

To check one server without an IDE:

```bash
leanproxy-mcp server health filesystem
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

## Connect your client

Every client runs the same command: `leanproxy-mcp server run --stdio`. The
HTTP variant connects to a gateway started with `server run --http`
([2b](#2b-or-run-one-shared-gateway-over-http)). In the HTTP snippets,
replace `<token>` with the content of `~/.config/leanproxy/serve.token`, or
use your client's secret or environment substitution.

Configure only one entry (stdio or HTTP) per client. With both, the client
sees every tool twice. Also remove the servers you imported into LeanProxy
from the client's own configuration, for the same reason.

=== "Claude Code"

    ```bash
    # stdio: Claude Code starts its own LeanProxy
    claude mcp add leanproxy -- leanproxy-mcp server run --stdio

    # the same, for every project (user scope)
    claude mcp add --scope user leanproxy -- leanproxy-mcp server run --stdio

    # HTTP: connect to the shared gateway
    claude mcp add --transport http leanproxy http://127.0.0.1:8765/mcp \
      --header "Authorization: Bearer $(cat ~/.config/leanproxy/serve.token)"
    ```

    Check it with `claude mcp list`.

=== "Claude Desktop"

    Edit `~/Library/Application Support/Claude/claude_desktop_config.json`
    (Claude Desktop > Settings > Developer > Edit Config), then restart
    Claude Desktop:

    ```json
    {
      "mcpServers": {
        "leanproxy": {
          "command": "/usr/local/bin/leanproxy-mcp",
          "args": ["server", "run", "--stdio"]
        }
      }
    }
    ```

    Claude Desktop does not use your shell's `PATH`. Use the absolute path
    that `which leanproxy-mcp` prints (for Homebrew on Apple Silicon, usually
    `/opt/homebrew/bin/leanproxy-mcp`). If your upstream servers use `npx` or
    `uvx`, see
    [Server command not found](troubleshooting.md#server-command-not-found-when-started-from-an-ide).

=== "Cursor"

    `~/.cursor/mcp.json` (all projects) or `.cursor/mcp.json` (one project):

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

    HTTP variant:

    ```json
    {
      "mcpServers": {
        "leanproxy": {
          "url": "http://127.0.0.1:8765/mcp",
          "headers": { "Authorization": "Bearer <token>" }
        }
      }
    }
    ```

=== "VS Code"

    `.vscode/mcp.json` in your workspace, or the user-level file opened by
    the **MCP: Open User Configuration** command. The top-level key is
    `servers`, not `mcpServers`:

    ```json
    {
      "servers": {
        "leanproxy": {
          "type": "stdio",
          "command": "leanproxy-mcp",
          "args": ["server", "run", "--stdio"]
        }
      }
    }
    ```

    HTTP variant, with the token asked once and stored by VS Code:

    ```json
    {
      "inputs": [
        { "type": "promptString", "id": "leanproxy-token", "description": "LeanProxy token", "password": true }
      ],
      "servers": {
        "leanproxy": {
          "type": "http",
          "url": "http://127.0.0.1:8765/mcp",
          "headers": { "Authorization": "Bearer ${input:leanproxy-token}" }
        }
      }
    }
    ```

=== "OpenCode"

    `~/.config/opencode/opencode.json`:

    ```json
    {
      "$schema": "https://opencode.ai/config.json",
      "mcp": {
        "leanproxy": {
          "type": "local",
          "command": ["leanproxy-mcp", "server", "run", "--stdio"],
          "enabled": true
        }
      }
    }
    ```

    HTTP variant:

    ```json
    {
      "$schema": "https://opencode.ai/config.json",
      "mcp": {
        "leanproxy": {
          "type": "remote",
          "url": "http://127.0.0.1:8765/mcp",
          "headers": { "Authorization": "Bearer {env:LEANPROXY_SERVE_TOKEN}" },
          "enabled": true
        }
      }
    }
    ```

!!! warning "Do not point an IDE at `serve`"
    `leanproxy-mcp serve` is not an MCP transport and no IDE can use it. See
    [Migrating from `serve`](#migrating-from-serve).

### Which exposure mode each IDE gets

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
guard (when enabled), tool pinning (a pending tool is hidden), the per-tool
policy (a denied tool is hidden, a `confirm` tool is marked `[confirm]`) and
the response governor (when enabled).

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

### Migrating from `serve`

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

!!! note
    A few features still exist only under `serve`: the web dashboard, the
    `/metrics` endpoint, the semantic cache and the sidecar LLM redactor.
    `server run` does not have them.

## Common Workflows

### What is protected by default

- **Secret redaction is on.** 29 built-in patterns (cloud keys, GitHub and
  other tokens, JWTs, passwords in connection strings, `.env` secrets, ...)
  are replaced in tool arguments and tool results, in both directions. List
  them with `leanproxy-mcp bouncer list-patterns`. The high-entropy detector
  is off until you set `bouncer.entropy_detection: true`.
- **The prompt-injection guard is off.** Turn it on with
  `injection: { enabled: true }`
  ([Security](security.md#prompt-injection-protection)).
- **Tool pinning is in `warn` mode**, and calls to tools a server never
  advertised are refused.

There is no email or phone-number (PII) detection. Run
`leanproxy-mcp doctor security` for a report of what your config turns on.

### See what LeanProxy saved

```bash
leanproxy-mcp report                                   # human-readable summary
leanproxy-mcp report --since 7d --by server
leanproxy-mcp report --export md --output report.md    # Markdown file
```

The report uses counters recorded by running proxies. See
[Savings Report](savings-report.md).

!!! note "There is no dry-run for the proxy"
    The global `-n/--dry-run` flag has no effect on `server run`. Only
    `migrate`, `add` and `marketplace update` have a working `--dry-run`.

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

LeanProxy-MCP caches the tool definitions of your servers on disk, in
`~/.config/leanproxy/toolcache/` (override with `LEANPROXY_TOOLCACHE_DIR`).
`search_tools` and `list_tools` use it, and you can inspect it without
starting any server:

```bash
# Servers with cached tools
leanproxy-mcp cache --list

# Cached tools of one server
leanproxy-mcp cache --server garmin

# Search every server's cached tools by name or description
leanproxy-mcp cache --search sleep
```

`--search` always searches all servers, even with `--server`.

### Running Status

Check whether a LeanProxy process is running:

```bash
leanproxy-mcp status --running
```

This reads the status file written by running instances
(`~/.config/leanproxy/status/current.json`). Without `--running`, `status`
starts every enabled server itself to check it.

### Enable/Disable Servers

```bash
leanproxy-mcp server list
leanproxy-mcp server disable github
leanproxy-mcp server enable github
```

A running proxy does not reload the file. Restart it (in most IDEs:
restart or reconnect the MCP server) after a change.

## Advanced Features

### Discover Servers from Marketplace

Browse and install servers from the official MCP Registry:

```bash
# Download the registry index to ~/.leanproxy/registry/index.json
leanproxy-mcp marketplace sync

# Search it
leanproxy-mcp marketplace search github

# Preview, then install (asks before enabling the server)
leanproxy-mcp add <server-id> --dry-run
leanproxy-mcp add <server-id>
```

Use the server id shown by `marketplace search`.

### Web Dashboard, Metrics and IDE Extensions

The web dashboard, the `/metrics` endpoint and the VS Code / JetBrains
extensions depend on the deprecated `serve` command, and their token and
cost figures are currently not populated. Read
[Web Dashboard](dashboard.md) and [IDE Extensions](extensions.md) before
you rely on them. For monitoring `server run`, use
[OpenTelemetry](observability.md).

## Next Steps

- [Commands Reference](commands.md) - Full command documentation
- [Configuration](configuration.md) - Customize LeanProxy-MCP
- [Security](security.md) - What each layer protects against
- [Troubleshooting](troubleshooting.md) - Common issues and solutions
