# Configuration

Customize LeanProxy-MCP behavior through configuration files and environment variables.

## Config File Locations

LeanProxy-MCP searches for configuration in this order:

1. **Explicit path**: `--config <path>` flag
2. **Project**: `./leanproxy.yaml` or `./leanproxy.yml`
3. **Home**: `~/.config/leanproxy/config.yaml`
4. **Default**: `leanproxy.yaml` in current directory

## Config File Format

### YAML Configuration

```yaml
# Server configuration
server:
  host: "127.0.0.1"
  port: 8080

# Redaction (Bouncer) — enabled by default; the block is optional
bouncer:
  enabled: true
  patterns:
    - name: "custom-pattern"
      pattern: "API_KEY=[A-Za-z0-9]+"

# Logging
logging:
  level: "info"
  file: ""

# Watch mode default
watch:
  interval: "1s"
```

### JSON Configuration

```json
{
  "server": {
    "host": "127.0.0.1",
    "port": 8080
  },
  "bouncer": {
    "enabled": true,
    "patterns": []
  },
  "logging": {
    "level": "info"
  }
}
```

## Configuration Options

### Server Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `server.host` | string | `"127.0.0.1"` | Listen host |
| `server.port` | int | `8080` | Listen port |
| `servers[].timeout` | duration | `30s` | **Per-server** request timeout. Each server entry in `servers:` can set its own `timeout` (e.g. `timeout: 60s` for `garmin`). The proxy honors the per-server value end-to-end: the handler dispatches with it and the worker uses `min(per-server, caller)`. Use a larger value for servers that return slow / large payloads (FIT data, big search results). |
| `server.max_batch_size` | int | `100` | Maximum batch size for JSON-RPC batch requests (0 = unlimited) |

### Concurrent Requests per Stdio Server (`max_in_flight`)

A stdio server is one child process with one stdin/stdout pipe, but the
proxy does **not** serialize calls to it: concurrent requests are multiplexed
over the pipe and matched back to their callers by JSON-RPC ID, so a slow
tool call does not block the others. `max_in_flight` caps how many requests
may be outstanding on one server at a time:

```yaml
servers:
  - name: github
    transport: stdio
    stdio:
      command: npx
      args: ["-y", "@modelcontextprotocol/server-github"]
    max_in_flight: 8   # default 32
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `servers[].max_in_flight` | int | `32` | Maximum number of requests multiplexed concurrently over one stdio server's pipe. `0` or absent means the default. |

When a server is at its cap, further callers **wait** for a free slot
(bounded by their own timeout) instead of being rejected. When a caller
times out or gives up, the proxy removes its pending entry and sends the
server a `notifications/cancelled` message; a late response is discarded.
If the server process exits, every pending call fails immediately with
`server <name> exited` rather than waiting for its timeout.

Requests the server sends to the client (`roots/list`,
`sampling/createMessage`, `elicitation/create`, `ping`) are not relayed yet;
the proxy answers them with JSON-RPC error `-32601 Method not found` so the
server never hangs waiting.

### Per-Server Rate Limiting

There is **no rate limit by default** — local stdio servers don't need one. A
server only gets one when its entry in `servers:` sets a `rate_limit` block:

```yaml
servers:
  - name: github
    transport: stdio
    stdio:
      command: npx
      args: ["-y", "@modelcontextprotocol/server-github"]
    rate_limit:
      requests_per_second: 20   # 0 or absent = unlimited (default)
      burst: 40                 # defaults to requests_per_second, rounded up
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `servers[].rate_limit.requests_per_second` | float | `0` (unlimited) | Sustained request rate allowed for that server. `0` or an absent `rate_limit` block means unlimited. |
| `servers[].rate_limit.burst` | int | `requests_per_second` (rounded up, min 1) | Number of requests allowed to proceed immediately before the sustained rate applies. |

When a limit is configured, requests **wait** for a token instead of being
rejected outright: `PutRequest` blocks until either a token frees up or the
request's own timeout/deadline elapses, at which point it fails with
`rate limit wait exceeded deadline for <server>`. This applies to stdio,
HTTP and SSE servers alike (`transport: http` / `sse` servers can set the
same `rate_limit` block; it also defaults to off).

Internal housekeeping traffic — health pings, the internal `initialize`
handshake, and periodic `tools/list` cache refreshes — always bypasses the
limiter, so a busy limiter can never make a server look unhealthy.

### Socket Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `socket.path` | string | `"~/.leanproxy/leanproxy.sock"` | Unix socket path |
| `socket.perm` | int | `0700` | Socket file permissions |
| `socket.max_msg_size` | int | `1048576` (1MB) | Maximum message size |
| `socket.rate_limit` | int | `100` | Rate limit (requests/second) |
| `socket.auth_token` | string | `""` | Authentication token (empty = no auth) |

**Security:** Socket directories and config directories are created with `0700` permissions (owner read/write/execute only) to prevent unauthorized access to sensitive data.

### Bouncer (Redaction) Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `bouncer.enabled` | bool | `true` | Enable/disable redaction |
| `bouncer.patterns` | array | (see below) | Custom patterns |
| `bouncer.sidecar_always_call` | bool | `false` | When `false`, the sidecar LLM is consulted only when the regex layer matched zero secrets. When `true`, the sidecar runs on every request regardless of regex outcome. |

#### `bouncer.sidecar_always_call`

When `false` (the default), the sidecar LLM is consulted only when the
regex layer matched **zero** secrets in the request. This is the
recommended default: the regex-cleaned payload is forwarded without an
extra LLM round-trip on already-cleaned requests.

When `true`, the sidecar runs on every request regardless of regex
outcome. Use this if you need LLM coverage of secrets the regex may have
missed (e.g. novel token formats, multi-segment bearer tokens), accepting
the per-request latency / cost of one extra LLM call.

```yaml
bouncer:
  enabled: true
  sidecar_always_call: false   # default — skip sidecar when regex already redacted
  patterns:
    - name: github_pat
      pattern: 'ghp_[A-Za-z0-9]{36}'
```

### Logging Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `logging.level` | string | `"info"` | Log level (debug, info, warn, error) |
| `logging.file` | string | `""` | Log file path (empty = stdout) |

### Watch Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `watch.interval` | string | `"1s"` | Status refresh interval |

### Removed in v0.10

The `optimization.lazy_loading` and `federation` config blocks were never
wired into any command — they parsed but had no effect — and were removed in
v0.10 (see the [changelog](../CHANGELOG.md#removed-in-v010) and issue
[#303](https://github.com/mmornati/leanproxy-mcp/issues/303)). Tool discovery
is instead handled by [JIT Discovery](architecture.md#jit-discovery), which
is wired into every transport. Existing configs that still contain either
key keep loading; LeanProxy logs one startup warning per key and ignores it.

## Built-in Redaction Patterns

LeanProxy-MCP includes these built-in patterns:

| Pattern Name | Type | Description |
|--------------|------|-------------|
| `aws-access-key` | regex | AWS Access Key ID (20 chars, starts with AKIA) |
| `github-classic-pat` | regex | GitHub Classic PAT (starts with ghp_) |
| `github-fine-grained-pat` | regex | GitHub Fine-grained PAT (starts with github_pat_) |
| `stripe-secret-key` | regex | Stripe Live Secret Key (starts with sk_live_) |
| `stripe-publishable-key` | regex | Stripe Live Publishable Key (starts with pk_live_) |
| `generic-api-key` | regex | Generic API key pattern |
| `bearer-token` | regex | JWT Bearer token |
| `env-var-value` | regex | Environment variable assignment |

### List Active Patterns

```bash
leanproxy-mcp bouncer list-patterns
```

Output:
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

## Custom Redaction Patterns

### Add Custom Pattern via Config

```yaml
bouncer:
  enabled: true
  patterns:
    - name: "my-api-key"
      pattern: "MY_API_KEY=[A-Za-z0-9]{32,}"
```

Custom patterns are Go (RE2) regular expressions and are applied alongside the built-in set. Matches are replaced with `[SECRET_REDACTED]`. `custom_patterns` is accepted as an alias for `patterns`.

### Pattern Safety (ReDoS Protection)

LeanProxy-MCP validates all user-provided regex patterns to prevent Regular Expression Denial of Service (ReDoS) attacks. Dangerous patterns that can cause catastrophic backtracking are rejected.

**Blocked dangerous patterns include:**
- Nested quantifiers: `(.+)+`, `(.*)*`, `(a+)*`
- Character class with nested quantifiers: `([a-z]+)+`
- Overlapping alternation: `(a|b)*`

**Safe patterns:**
- Simple character classes: `[A-Za-z0-9]+`
- Anchored patterns: `^api_key_[a-f0-9]{32}$`
- Quantified character classes: `[a-z]{8,64}`

If an invalid pattern is detected, it is logged and skipped with a warning message.

### Validate Patterns

Check if your patterns are safe before deploying:

```bash
leanproxy-mcp bouncer validate-patterns
```

### Enable/Disable Bouncer

Redaction is on by default, even with no `bouncer:` block. To turn it off explicitly:

```yaml
bouncer:
  enabled: false
```

The proxy logs a warning at startup when redaction is disabled.

## Path Traversal Protection

LeanProxy-MCP validates all file paths to prevent path traversal attacks. This protection applies to:
- Server configuration files
- Registry persistence files
- Compactor configuration

### Protected Operations

| Operation | Protection |
|-----------|------------|
| Config file loading | Path must be within parent directory |
| Registry save/load | Path must be within parent directory |
| Compactor config | Path must be within parent directory |

### Security Checks

1. **Traversal pattern detection**: Blocks `../` and URL-encoded variants (`%2E%2E%2F`)
2. **Null byte prevention**: Rejects paths containing `\x00`
3. **Directory boundary enforcement**: Resolved paths must stay within base directory

### Example Attacks Blocked

```
../../../etc/passwd        -> BLOCKED
..%2F..%2F..%2Fetc/passwd  -> BLOCKED
config.yaml\x00           -> BLOCKED
```

## Hierarchical Namespaces

Namespaces allow organizing MCP servers into hierarchical groups for multi-team organizations.

### Configuration

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
    allowed_clients:
      - "ops-team"
      - "*"
```

### Namespace Fields

| Field | Type | Description |
|-------|------|-------------|
| `description` | string | Human-readable description |
| `servers` | []string | Server IDs in this namespace |
| `children` | map | Nested namespace definitions |
| `allowed_clients` | []string | Allowed clients (supports `*` wildcard) |

### Access Control

Namespaces support client-level access control:

```yaml
namespaces:
  restricted:
    allowed_clients:
      - "team-alpha"
      - "team-beta"
      - "*"  # Allow any authenticated client
    servers:
      - secure-server
```

### CLI Commands

```bash
# List all namespaces
leanproxy-mcp namespace list

# List tools in a namespace
leanproxy-mcp namespace list engineering --tools

# Add a new namespace (generates config example)
leanproxy-mcp namespace add frontend --servers=storybook,figma

# Assign server to namespace (generates config example)
leanproxy-mcp namespace assign engineering github
```

## Environment Variables

| Variable | Description |
|----------|-------------|
| `LEANPROXY_CONFIG` | Config file path |
| `LEANPROXY_LOG_LEVEL` | Log level |
| `LEANPROXY_HOST` | Server host |
| `LEANPROXY_PORT` | Server port |

## Prompt Injection Protection

Injection protection analyzes tool call payloads against known prompt injection patterns and applies configurable actions (block, quarantine, log) based on risk scoring.

### Configuration

```yaml
injection:
  enabled: true
  threshold: 70
  action: block
  custom_patterns:
    - name: "my-pattern"
      pattern: "(?i)ignore previous instructions"
      weight: 90
      enabled: true
      description: "Detect instruction override attempts"
  policies:
    - min_risk: 80
      max_risk: 100
      action: block
    - min_risk: 50
      max_risk: 79
      action: quarantine
    - min_risk: 1
      max_risk: 49
      action: log
```

### Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | `true` | Enable injection protection |
| `threshold` | int | `70` | Minimum risk score to trigger action (1-100) |
| `action` | string | `block` | Fallback action (`block`, `quarantine`, `log`, `redact`) |
| `custom_patterns` | array | `[]` | User-defined injection patterns |
| `policies` | array | (see default) | Ordered risk-range rules (overrides `action`) |

### Default Policy Rules

| Risk Range | Default Action |
|------------|----------------|
| 80-100 | `block` |
| 50-79 | `quarantine` |
| 1-49 | `log` |

### Dispatcher Actions

| Action | Description |
|--------|-------------|
| `block` | Rejects the request outright |
| `quarantine` | Writes payload to quarantine directory, returns quarantine ID |
| `redact` | Replaces payload content with `[CONTENT_REDACTED]` |
| `log` | Forwards request with debug log only |

### Default Built-in Patterns (14)

| Pattern | Weight | Description |
|---------|--------|-------------|
| `ignore-previous-instructions` | 90 | Override system instructions |
| `new-instruction-override` | 85 | Redefine assistant role |
| `system-prompt-extraction` | 80 | Extract system prompt |
| `dan-jailbreak` | 75 | DAN-style jailbreaks |
| `role-impersonation` | 70 | Boundary removal |
| `repeat-everything` | 70 | Conversation dump attempts |
| `token-smuggling` | 65 | Encoded payloads |
| `forget-everything` | 75 | Context reset |
| `inject-command` | 80 | Explicit injection markers |
| `separator-injection` | 85 | Delimiter-based injection |
| `important-override` | 30 | Urgency-based |
| `roleplay-context-switch` | 40 | Roleplay |
| `hypothetical-override` | 25 | Hypothetical scenarios |
| `ignore-above` | 50 | Selective ignoring |

## Semantic Cache

Semantic caching stores and retrieves tool responses based on vector similarity, reducing redundant LLM calls for semantically similar requests.

### Configuration

```yaml
cache:
  vector_store:
    backend: sqlite-vec  # sqlite-vec, qdrant, or pinecone
    dimension: 1536
    sqlite:
      path: "~/.leanproxy/cache/vectors.db"
    qdrant:
      url: "http://localhost:6333"
      api_key_env: "QDRANT_API_KEY"
      collection: "leanproxy_cache"
    pinecone:
      index: "my-index"
      api_key_env: "PINECONE_API_KEY"
```

### Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `backend` | string | `sqlite-vec` | Vector store backend (`sqlite-vec`, `qdrant`, `pinecone`) |
| `dimension` | int | `1536` | Embedding vector dimension |

### Embedder Providers

Supported via `--embed-provider` flag:

| Provider | Default Model | Default URL |
|----------|---------------|-------------|
| `ollama` | `nomic-embed-text` | `http://localhost:11434` |
| `openai` | `text-embedding-3-small` | `https://api.openai.com/v1/embeddings` |

### Similarity

- Threshold: 0.92 (cosine similarity)
- Candidates retrieved: 5
- TTL: 24 hours
- Eviction interval: 1 hour

### CLI

```bash
# Enable with Ollama
leanproxy-mcp serve --embed-provider ollama

# Enable with OpenAI
leanproxy-mcp serve --embed-provider openai

# Show cache stats
leanproxy-mcp cache --semantic
leanproxy-mcp cache --semantic --json
```

## Auto-Reconnect

LeanProxy-MCP monitors proxied MCP servers and reconnects them automatically when a server crashes, hangs, or loses its transport connection. This applies to all transports (`stdio`, `http`, `sse`).

Auto-reconnect is enabled by default. Configure it with a top-level `reconnect` block in `leanproxy_servers.yaml`:

```yaml
servers:
  - name: garmin
    enabled: true
    transport: stdio
    stdio:
      command: garmin-mcp
      args: [stdio]
    timeout: 30s
    connect_timeout: 10s
    # idle_timeout: 30m   # empty/absent defaults to 30m; "0" disables

# Optional global auto-reconnect settings
reconnect:
  enabled: true
  health_check_interval: 30s
  health_check_failures: 3
  max_restart_attempts: 5
  restart_backoff: 1s
  stable_window: 2m
```

### Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | `true` | Master switch for auto-reconnect |
| `health_check_interval` | duration | `30s` | How often idle/running servers are pinged. Set to `0` to disable the proactive health check |
| `health_check_failures` | int | `3` | Consecutive failed pings before a server is restarted automatically |
| `max_restart_attempts` | int | `5` | Max crash-restarts before a server is left in an error state |
| `restart_backoff` | duration | `1s` | Initial delay before restarting a crashed server (grows exponentially, capped at 1 minute; clamped to [10ms, 1m]) |
| `stable_window` | duration | `2m` | If a server survives this long, its restart budget resets |

### How It Works

1. **Crash detection**: when a `stdio` process exits unexpectedly it is respawned automatically with exponential backoff. A process that stays up past `stable_window` resets the restart budget, so long-running servers keep their full budget. Once the budget is exhausted the server stays in the error state until its next request — any deliberate restart (on next use or via the API) grants a fresh budget.
2. **Liveness probe**: every `health_check_interval`, idle/running servers are sent an MCP `ping` (which does **not** consume AI/LLM tokens). `health_check_failures` consecutive failures trigger a restart. This catches processes that are alive but unresponsive. Deliberately stopped servers (idle timeout) and servers already in an error state are left alone — crash recovery owns the error state, and stopped servers revive lazily on their next request.
3. **Transport recovery**: `http`/`sse` servers reconnect automatically when the connection drops, and the next tool call transparently re-establishes a dead session. Only genuine transport failures (connection reset/refused, EOF, dial errors) trigger a reconnect — server-side JSON-RPC errors are never retried blindly.
4. **Stop-then-restart**: when a server is idle and times out, or is explicitly restarted, the process is fully torn down and a fresh one with a working request loop is spawned. New requests issued during recovery wait briefly for the restart to finish; a request already in flight when a restart begins fails fast instead of hanging until its timeout. A child process that ignores SIGTERM is escalated to SIGKILL after a 5s grace period so a wedged server can never block recovery.

> **Note**: `idle_timeout` still defaults to `30m` when left empty. Set it to `0` (or `"0"`) to keep a server running indefinitely. With auto-reconnect enabled, an idle-stop is now fully recoverable — the server simply restarts on its next use.
>
> **Note**: `reconnect.enabled: false` disables *automatic* recovery only (crash auto-restart and the health probe). Servers can still be restarted on demand — including an idle-stopped server reviving on its next request.

## Sidecar LLM Redaction

Offload sensitive content redaction to a local Ollama model for context-aware redaction beyond regex patterns.

### Configuration

```yaml
sidecar:
  provider: ollama
  model: llama3.1:8b
  url: http://localhost:11434
```

### CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--sidecar-provider` | `""` | Sidecar provider (`ollama`); empty = disabled |
| `--sidecar-model` | `llama3.1:8b` | Model name |
| `--sidecar-url` | `http://localhost:11434` | Server URL |

### How It Works

1. Regex-based bouncer redaction runs first
2. Sidecar LLM receives the already-redacted content
3. LLM replaces any remaining sensitive data (API keys, PII, tokens) with `[VALUE_REDACTED]`
4. Falls back to aggressive redact if LLM is unavailable

### Example

```bash
leanproxy-mcp serve --sidecar-provider ollama --sidecar-model llama3.1:8b
```

## Dashboard

The web dashboard provides real-time monitoring of token usage.

### Configuration

Configured via CLI flags on `serve`:

| Flag | Default | Description |
|------|---------|-------------|
| `--dashboard-bind` | `127.0.0.1:9090` | Dashboard bind address. Set to `off` to disable |
| `--dashboard-token` | `""` | Bearer token for non-loopback access |

### Metrics Endpoint

```bash
leanproxy-mcp serve --metrics-bind 127.0.0.1:9091
```

### Export Cost Data

```bash
# CSV export
leanproxy-mcp report --export csv --output costs.csv

# JSON export
leanproxy-mcp report --export json --output costs.json

# Filtered by date range
leanproxy-mcp report --export csv --since 2026-06-01
```

## Validate Configuration

```bash
leanproxy-mcp bouncer validate-patterns
```

## Show Current Config

```bash
leanproxy-mcp config show
```

## Next Steps

- [Commands Reference](./commands.md) - Full command documentation
- [Architecture](./architecture.md) - Understand how LeanProxy-MCP works
- [Troubleshooting](./troubleshooting.md) - Common configuration issues