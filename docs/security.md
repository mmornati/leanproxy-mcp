# Security

LeanProxy-MCP includes multiple security hardening features to protect your data and prevent common attack vectors.

## Features Overview

| Feature | Description |
|---------|-------------|
| **In-Memory Redaction** | Pre-configured patterns redact secrets before they reach LLM providers |
| **Token Authentication** | Optional Unix socket authentication for request-level access control |
| **Batch Size Limits** | Prevents DoS via large JSON-RPC batch requests |
| **ReDoS Protection** | Validates regex patterns to prevent catastrophic backtracking |
| **Path Validation** | Prevents path traversal attacks on configuration files |
| **Graceful Shutdown** | Ensures all goroutines are properly terminated |

## In-Memory Redaction

LeanProxy-MCP intercepts all data flowing through the proxy and redacts sensitive information before it reaches LLM providers. This operates entirely in-memory—no data is persisted or logged.

### Built-in Patterns

LeanProxy-MCP includes redaction patterns for common secrets:

- AWS Access Key IDs
- GitHub Personal Access Tokens (Classic and Fine-grained)
- Stripe API Keys
- Generic API Keys
- JWT Bearer Tokens
- Environment Variables

### Custom Patterns

Add custom redaction patterns in your configuration:

```yaml
bouncer:
  enabled: true
  patterns:
    - name: "my-secret"
      type: "regex"
      pattern: "MY_SECRET=[A-Za-z0-9]{32,}"
      replacement: "MY_SECRET=REDACTED"
```

## Token Authentication

Unix socket authentication provides request-level access control.

### Enabling Authentication

Configure an authentication token in your socket settings:

```yaml
socket:
  auth_token: "your-secret-token"
```

### Making Authenticated Requests

Include the `auth_token` in your JSON-RPC requests:

```json
{
  "jsonrpc": "2.0",
  "method": "tools/invoke",
  "params": {"name": "github_get_issue", "arguments": {}},
  "id": 1,
  "auth_token": "your-secret-token"
}
```

### Error Handling

| Error Code | Message | Description |
|------------|--------|-------------|
| -32604 | authentication required | Token missing or empty |
| -32605 | authentication failed | Token mismatch |

### Security Considerations

- Use TLS or Unix socket permissions for transport security
- Token comparison is exact (no hashing) - choose strong tokens
- Without a token configured, all requests are allowed

## Batch Size Limits

The `max_batch_size` setting prevents denial-of-service attacks via large batch requests.

### Configuration

```yaml
server:
  max_batch_size: 100  # Default: 100, 0 = unlimited
```

### Behavior

- Batch requests exceeding the limit are split into smaller chunks
- Each chunk is processed sequentially
- The limit applies to both request and response batches

## ReDoS Protection

LeanProxy-MCP validates all user-provided regex patterns before compilation to prevent Regular Expression Denial of Service (ReDoS) attacks.

### Blocked Patterns

| Pattern Type | Example | Risk |
|-------------|---------|------|
| Nested quantifiers | `(.+)+`, `(a+)*` | Exponential backtracking |
| Character class quantifiers | `([a-z]+)+` | Polynomial backtracking |
| Overlapping alternation | `(a\|b)*` | Catastrophic backtracking |

### Safe Patterns

| Pattern Type | Example | Description |
|------------|---------|-------------|
| Simple character class | `[A-Za-z0-9]+` | Linear matching |
| Anchored | `^api_key_[a-f0-9]{32}$` | Bounded matching |
| Quantified class | `[a-z]{8,64}` | Bounded quantifier |

### Validation

Check patterns before deployment:

```bash
leanproxy-mcp bouncer validate-patterns
```

Invalid patterns are logged and skipped with a warning.

## Path Traversal Protection

LeanProxy-MCP validates all file paths to prevent directory traversal attacks.

### Protected Operations

- Server configuration file loading
- Registry persistence files
- Compactor configuration

### Security Checks

1. **Traversal pattern detection**: Blocks `../` and URL-encoded variants
2. **Null byte prevention**: Rejects paths with `\x00`
3. **Directory boundary**: Resolved paths must stay within base directory

### Blocked Examples

```
../../../etc/passwd        -> BLOCKED
..%2F..%2F..%2Fetc/passwd  -> BLOCKED
config.yaml\x00           -> BLOCKED
```

## File Permissions

LeanProxy-MCP creates files with secure permissions:

| File Type | Permissions | Description |
|-----------|-------------|-------------|
| Socket directory | 0700 | Owner-only access |
| Config directory | 0700 | Owner-only access |
| Socket file | 0700 | Owner-only access |
| Config files | 0600 | Owner read/write only |

This prevents unauthorized users from reading sensitive configuration or authenticating to the socket.

## Graceful Shutdown

LeanProxy-MCP ensures all background goroutines are properly terminated on shutdown to prevent goroutine leaks.

### WaitGroup Tracking

All async operations are tracked using `sync.WaitGroup`:

- Connection handlers
- Background workers
- Health monitors
- Proxy routers

### Shutdown Procedure

1. Accept new connections: **STOPPED**
2. Wait for active requests: **TIMEOUT** (30s default)
3. Cancel pending operations
4. Drain connection pools
5. Close socket and exit

### Graceful Shutdown Example

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := server.Shutdown(ctx); err != nil {
    // Handle timeout or error
}
```

## Best Practices

### General Security

1. **Keep Go updated**: Use the latest Go version for security fixes
2. **Use authentication tokens**: Enable socket authentication in production
3. **Limit batch sizes**: Set `max_batch_size` to reasonable values
4. **Avoid logging secrets**: Ensure no sensitive data in logs

### Configuration

1. **Secure config files**: Ensure `0600` permissions on config files
2. **Use strong tokens**: Generate random tokens (32+ characters)
3. **Validate patterns**: Test regex patterns before deployment

### Deployment

1. **Restrict socket access**: Use filesystem permissions
2. **Monitor logs**: Watch for authentication failures
3. **Regular audits**: Review configuration patterns

## Common Security Considerations

### What LeanProxy-MCP Does NOT Do

- **TLS/SSL**: Use a reverse proxy (nginx, traefik) for TLS termination
- **Secret hashing**: Tokens are compared directly - use strong tokens
- **Rate limiting per-client**: Global rate limiting only
- **Audit logging**: Implement externally if needed

### Known Limitations

- Socket permissions depend on filesystem
- Config file access control is filesystem-based
- No built-in encryption for data at rest

## Security Configuration Reference

| Option | Type | Default | Security Impact |
|--------|------|---------|-----------------|
| `socket.auth_token` | string | `""` | Enables request authentication |
| `socket.perm` | int | `0700` | Socket file permissions |
| `server.max_batch_size` | int | `100` | Prevents DoS attacks |
| `socket.rate_limit` | int | `100` | Global rate limiting |

## Next Steps

- [Configuration](./configuration.md) - Full configuration options
- [Troubleshooting](./troubleshooting.md) - Security-related issues
- [Architecture](./architecture.md) - Security design details