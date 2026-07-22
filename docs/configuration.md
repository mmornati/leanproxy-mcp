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

# Redaction (Bouncer)
bouncer:
  enabled: true
  patterns:
    - name: "custom-pattern"
      type: "regex"
      pattern: "API_KEY=[A-Za-z0-9]+"
      replacement: "API_KEY=REDACTED"

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
| `server.timeout` | duration | `30s` | Request timeout |
| `server.max_batch_size` | int | `100` | Maximum batch size for JSON-RPC batch requests (0 = unlimited) |

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

### Logging Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `logging.level` | string | `"info"` | Log level (debug, info, warn, error) |
| `logging.file` | string | `""` | Log file path (empty = stdout) |

### Watch Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `watch.interval` | string | `"1s"` | Status refresh interval |

### Optimization Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `optimization.lazy_loading.enabled` | bool | `true` | Enable lazy-loading tool schemas |
| `optimization.lazy_loading.stub_tokens` | int | `54` | Expected token count per stub |
| `optimization.lazy_loading.cache_ttl` | duration | `24h` | Cache validity duration |
| `optimization.lazy_loading.prewarm` | []string | `[]` | Tools to pre-load on startup |

#### Lazy Loading

Lazy-loading reduces initial context overhead by sending only compact tool stubs (~54 tokens each) at startup instead of full schemas. Full schemas are loaded on-demand when a tool is first invoked.

**Benefits:**
- 6-7x token reduction at startup
- Only loads full schemas for tools that are actually used
- In-memory caching with TTL for frequently accessed schemas

**Example:**

```yaml
optimization:
  lazy_loading:
    enabled: true
    stub_tokens: 54
    cache_ttl: 24h
    prewarm:
      - github_search_code
      - filesystem_read_file
```

### Federation Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `federation.enabled` | bool | `false` | Enable federation with other LeanProxy instances |
| `federation.peers` | array | `[]` | List of federated peer configurations |

#### Federation Configuration

Federation allows connecting multiple LeanProxy instances across organizations to share and route tool requests.

```yaml
federation:
  enabled: true
  peers:
    - name: "company-a"
      url: "https://proxy.company-a.internal:8080"
      auth_token: "optional-shared-secret"
    - name: "company-b"
      url: "https://proxy.company-b.internal:8080"
```

#### Peer Configuration Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Unique identifier for the peer |
| `url` | string | yes | HTTP endpoint of the peer |
| `auth_token` | string | no | Bearer token for authentication |

#### Federation Features

- **Peer Discovery**: Automatically discover available tools from federated peers
- **Cross-Instance Routing**: Route tool requests to the appropriate peer
- **Failover Handling**: Automatically switch to backup peers if primary fails

#### Federation API Endpoints

When federation is enabled, the following endpoints are available:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check for peer status |
| `/federation/list-tools` | POST | List available tools on the peer |
| `/federation/invoke` | POST | Invoke a tool on the peer |

**list-tools Request:**
```json
{}
```

**list-tools Response:**
```json
{
  "tools": ["github@create_issue", "github@list_repos", "jira@create_ticket"]
}
```

**invoke Request:**
```json
{
  "server": "github",
  "tool": "create_issue",
  "params": {"title": "Bug report", "body": "..."}
}
```

**invoke Response:**
```json
{
  "result": {"id": 123, "url": "https://..."}
}
```

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

## Socket Authentication

The socket server supports optional token-based authentication to prevent unauthorized access.

### Enabling Authentication

To enable authentication, set the `auth_token` in your socket configuration:

```yaml
socket:
  auth_token: "your-secret-token"
```

### Using Authenticated Requests

When authentication is enabled, all JSON-RPC requests must include the `auth_token` field:

```json
{
  "jsonrpc": "2.0",
  "method": "token.resolve",
  "params": {"uri": "api://example"},
  "id": 1,
  "auth_token": "your-secret-token"
}
```

### Error Responses

When authentication fails (missing or invalid token), the server returns:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32604,
    "message": "authentication required"
  }
}
```

### Security Notes

- Without an auth token configured, all requests are allowed
- The auth token is transmitted in plain text - use TLS or Unix socket permissions for security
- Token comparison is exact (no hashing)

## Custom Redaction Patterns

### Add Custom Pattern via Config

```yaml
bouncer:
  enabled: true
  patterns:
    - name: "my-api-key"
      type: "regex"
      pattern: "MY_API_KEY=[A-Za-z0-9]{32,}"
      replacement: "MY_API_KEY=REDACTED"
```

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

### Pattern Types

| Type | Description |
|------|-------------|
| `regex` | Regular expression match |
| `literal` | Exact string match |
| `glob` | Glob pattern match |

### Enable/Disable Bouncer

```bash
# Disable redaction
leanproxy-mcp bouncer disable

# Enable redaction
leanproxy-mcp bouncer enable
```

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

## Model Routing

Route tool calls to different LLM models based on complexity tier, configured per-server.

### Configuration

Separate YAML file referenced by `--model-router-config`:

```yaml
default_tier: medium
tiers:
  low:
    provider: "anthropic"
    model: "claude-3-haiku-20240307"
    api_key_env: "ANTHROPIC_API_KEY"
  medium:
    provider: "anthropic"
    model: "claude-3-sonnet-20240229"
    api_key_env: "ANTHROPIC_API_KEY"
  high:
    provider: "anthropic"
    model: "claude-3-opus-20240229"
    api_key_env: "ANTHROPIC_API_KEY"
```

### Per-Server Assignment

Declare the tier in each server entry within `leanproxy_servers.yaml`:

```yaml
servers:
  - name: github
    complexity_tier: "low"
  - name: code-review
    complexity_tier: "high"
```

### Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `default_tier` | string | `medium` | Fallback tier |
| `tiers.<tier>.provider` | string | — | LLM provider |
| `tiers.<tier>.model` | string | — | Model identifier |
| `tiers.<tier>.api_key` | string | — | API key inline |
| `tiers.<tier>.api_key_env` | string | — | API key from env var |

### CLI

```bash
leanproxy-mcp serve --model-router --model-router-config ./model-router.yaml
```

## Sidecar LLM Redaction

Offload sensitive content redaction to a local LLM (Ollama or MLX) for context-aware redaction beyond regex patterns.

### Configuration

```yaml
sidecar:
  provider: ollama    # "ollama" or "mlx"
  model: llama3.1:8b
  url: http://localhost:11434
```

### CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--sidecar-provider` | `""` | Sidecar provider (`ollama`, `mlx`); empty = disabled |
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

## Budget Management

Configure spending limits per team and project with soft/hard caps and webhook alerts.

### Configuration

```yaml
budgets:
  webhook_url: "https://hooks.example.com/alert"
  teams:
    engineering:
      daily: 1000000
      monthly: 20000000
      hard_cap: true
      soft_cap_pct: 80.0
      projects:
        frontend:
          daily: 500000
          monthly: 10000000
```

### Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `daily` | int | 0 | Daily token budget (0 = unlimited) |
| `monthly` | int | 0 | Monthly token budget (0 = unlimited) |
| `hard_cap` | bool | false | Reject when exceeded |
| `soft_cap_pct` | float | 90.0 | Downgrade threshold (0-100) |
| `webhook_url` | string | — | Override global webhook URL |

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