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

## Environment Variables

| Variable | Description |
|----------|-------------|
| `LEANPROXY_CONFIG` | Config file path |
| `LEANPROXY_LOG_LEVEL` | Log level |
| `LEANPROXY_HOST` | Server host |
| `LEANPROXY_PORT` | Server port |

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