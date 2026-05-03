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
      type: "regex"
      pattern: "MY_API_KEY=[A-Za-z0-9]{32,}"
      replacement: "MY_API_KEY=REDACTED"
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