# Security

LeanProxy-MCP includes multiple security hardening features to protect your data and prevent common attack vectors.

## Features Overview

| Feature | Description |
|---------|-------------|
| **In-Memory Redaction** | Pre-configured patterns redact secrets before they reach LLM providers |
| **Prompt Injection Protection** | Classifies payloads against injection patterns with risk scoring and configurable actions |
| **Sidecar LLM Redaction** | Context-aware redaction via a local Ollama model for sensitive data beyond regex |
| **Batch Size Limits** | Prevents DoS via large JSON-RPC batch requests |
| **ReDoS Protection** | Validates regex patterns to prevent catastrophic backtracking |
| **Path Validation** | Prevents path traversal attacks on configuration files |
| **Graceful Shutdown** | Ensures all goroutines are properly terminated |

## Which modes are protected

Secret redaction and the prompt-injection guard (together, the **Token
Firewall**) run as one shared middleware pipeline (`pkg/mcp`) in **both**
front ends:

| Mode | Redaction | Injection guard |
|------|-----------|-----------------|
| `leanproxy-mcp server run --stdio` (what IDEs run) | Yes, on by default | Yes, when an `injection:` block enables it |
| `leanproxy-mcp serve` | Yes, on by default | Yes, when an `injection:` block enables it |

Pipeline order for every request:

```
client → redact request params → injection check → dispatch/upstream → redact response → client
```

- **Request redaction** covers every nested value of `params`, including the
  `arguments` of `tools/call` and the nested `arguments` of an `invoke_tool`
  call. If params cannot be redacted the request is rejected with
  `Secret redaction failed; request not forwarded` and never forwarded.
- **Response redaction** covers `result`, `error.message` and `error.data`
  of every response, including `list_tools` text (upstream tool descriptions)
  and error hints/schemas.
- Redaction is **on by default** with the built-in patterns when there is no
  `bouncer:` block. Only `bouncer.enabled: false` turns it off (in both
  modes). At startup the proxy logs one line such as
  `redaction enabled, 16 patterns; injection disabled`.

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

## Prompt Injection Protection

LeanProxy-MCP includes a classification engine that detects and responds to prompt injection attacks, jailbreak attempts, and system prompt extraction in tool call payloads.

### How It Works

The injection classifier runs against every tool call payload:

1. **Pattern matching**: 14 built-in regex patterns scan the payload (e.g., `ignore-previous-instructions`, `dan-jailbreak`, `system-prompt-extraction`)
2. **Risk scoring**: Each matched pattern contributes its weight to a total score, capped at 100
3. **Policy action**: The dispatcher applies the configured action based on the risk score range

### Risk Scoring

The classifier evaluates all enabled patterns against the payload. Each match contributes its weight to a cumulative score (0-100).

### Policy Configuration

Configured in `leanproxy.yaml`:

```yaml
injection:
  enabled: true
  threshold: 70
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

### Dispatcher Actions

| Action | Description |
|--------|-------------|
| `block` | Rejects the request with a JSON-RPC error; the upstream is not called |
| `quarantine` | Saves the payload to disk for analysis and returns a tool result with `isError: true` telling the model the call was quarantined; the upstream is not called |
| `redact` | Replaces every string value in the tool `arguments` with `[CONTENT_REDACTED]` (params stay valid JSON; `invoke_tool` routing fields `server`/`tool` are kept) and forwards the call |
| `log` | Forwards the request unchanged and logs it |

The injection guard runs only when the config has an `injection:` block with
`enabled: true`.

### Quarantine

Quarantined payloads are saved to `~/.leanproxy/quarantine/<uuid>.json`:

```json
{
  "id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "timestamp": "2026-07-22T10:30:00Z",
  "server": "filesystem",
  "tool": "read_file",
  "risk_score": 85,
  "matched_patterns": ["ignore-previous-instructions"],
  "payload": "{...}"
}
```

View quarantine status:

```bash
leanproxy-mcp doctor security
```

### Built-in Patterns (14)

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

### Custom Patterns

Add custom patterns to catch organization-specific injection attempts:

```yaml
injection:
  custom_patterns:
    - name: "my-pattern"
      pattern: "(?i)ignore previous instructions"
      weight: 90
      enabled: true
      description: "Detect instruction override attempts"
```

### Diagnostic CLI

```bash
# Show security policy and quarantine status
leanproxy-mcp doctor security
```

## Sidecar LLM Redaction

For context-aware redaction beyond regex patterns, deploy a sidecar LLM (Ollama). The sidecar analyzes already-redacted content and replaces any remaining sensitive data using an LLM.

### How It Works

1. Regex-based bouncer redaction runs first
2. Sidecar LLM receives the redacted content
3. LLM replaces remaining sensitive data (API keys, passwords, tokens, PII) with `[VALUE_REDACTED]`
4. Falls back to aggressive redact if LLM is unavailable

### Configuration

```yaml
sidecar:
  provider: ollama
  model: llama3.1:8b
  url: http://localhost:11434
```

### CLI

```bash
leanproxy-mcp serve --sidecar-provider ollama --sidecar-model llama3.1:8b
```

### Providers

| Provider | Status | Notes |
|----------|--------|-------|
| Ollama | Full support | Sends redaction prompt to `/api/generate`, 30s timeout |
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
| Config directory | 0700 | Owner-only access |
| Config files | 0600 | Owner read/write only |

This prevents unauthorized users from reading sensitive configuration.

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
2. **Limit batch sizes**: Set `max_batch_size` to reasonable values
3. **Avoid logging secrets**: Ensure no sensitive data in logs

### Configuration

1. **Secure config files**: Ensure `0600` permissions on config files
2. **Validate patterns**: Test regex patterns before deployment

### Deployment

1. **Monitor logs**: Watch for errors and warnings
2. **Regular audits**: Review configuration patterns

## Common Security Considerations

### What LeanProxy-MCP Does NOT Do

- **TLS/SSL**: Use a reverse proxy (nginx, traefik) for TLS termination
- **Rate limiting per-client**: Global rate limiting only
- **Audit logging**: Implement externally if needed

### Known Limitations

- Config file access control is filesystem-based
- No built-in encryption for data at rest

## Security Configuration Reference

| Option | Type | Default | Security Impact |
|--------|------|---------|-----------------|
| `server.max_batch_size` | int | `100` | Prevents DoS attacks |

## Next Steps

- [Configuration](./configuration.md) - Full configuration options including injection protection
- [Commands Reference](./commands.md) - `doctor security` and `bouncer` CLI commands
- [Troubleshooting](./troubleshooting.md) - Security-related issues
- [Architecture](./architecture.md) - Security design details