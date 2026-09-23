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
| `server.max_concurrent_requests` | int | `64` | Maximum number of client requests `server run --stdio` handles in parallel (for `serve`: per connection). `0` or absent means the default; negative values are rejected. See below. |
| `server.max_line_bytes` | int | `67108864` (64 MiB) | Largest incoming JSON-RPC message (one line), for both `serve` and `server run --stdio`. A longer line gets a parse-error response and is skipped; `serve` also closes the connection. `0` or absent means the default |
| `server.max_connections` | int | `32` | `serve` only: client connections open at once; extra ones are closed right away. `0` or absent means the default |

### Concurrent Client Requests (`server.max_concurrent_requests`)

`leanproxy-mcp server run --stdio` handles every request from the IDE in its
own goroutine, so a slow tool call never blocks `ping`, `tools/list` or calls
to other servers. Responses are written one complete line at a time (a single
writer, flushed after every message), in whatever order the requests finish.

```yaml
server:
  max_concurrent_requests: 16   # default 64
```

- **Cap:** when `max_concurrent_requests` requests are already running, the
  proxy stops reading stdin until one finishes. Requests are delayed, never
  rejected.
- **Notifications** (messages without an `id`) never get a response.
- **Cancellation:** `notifications/cancelled` with the `requestId` of an
  in-flight request cancels it. The cancellation reaches the upstream server
  (stdio servers receive their own `notifications/cancelled`) and, as the MCP
  spec recommends, the canceled request gets no response.
- **Shutdown:** on EOF or a `shutdown` request the proxy stops reading, waits
  up to 5 seconds for in-flight requests to finish, cancels whatever is still
  running, then (for `shutdown`) answers the shutdown request and stops every
  upstream server.
- **Invalid JSON** is answered with a parse error whose `id` is `null`. The
  raw line is never logged, only its length.
- **Oversized lines:** a message over `server.max_line_bytes` (default 64
  MiB, shared with `serve`; see below) also gets a parse-error response with
  `id` `null`. It is discarded up to its next newline and the connection
  keeps serving.

### Serve Listener Limits

`leanproxy-mcp serve` (the TCP listener) requires an auth handshake on every
connection (see [`serve`](commands.md#client-protocol-and-authentication))
and applies these limits:

```yaml
server:
  max_line_bytes: 1048576        # default 64 MiB
  max_concurrent_requests: 16    # per connection, default 64
  max_connections: 8             # default 32
```

- **Message size:** input is read with a bounded reader, so a client sending
  a huge line without a newline cannot grow memory beyond about
  `max_line_bytes`. It gets an `Invalid Request` error and is disconnected.
- **Concurrency:** each connection runs at most `max_concurrent_requests`
  requests at once; at the cap the proxy stops reading that connection until
  one finishes.
- **Disconnects** cancel the connection's in-flight requests and their
  upstream calls.

### Child Process Environment (`env`, `env_passthrough`, `inherit_env`)

**Breaking change (#311):** stdio servers used to inherit the proxy's
**entire** process environment — every `OPENAI_API_KEY`, `AWS_*`,
`GITHUB_TOKEN`, database URL, etc. that happened to be set where the proxy
runs, whether or not that server needed it. As of this release, each child
gets a **least-privilege** environment by default:

1. A minimal, fixed allowlist copied from the proxy's own environment:
   `PATH`, `HOME`, `USER`, `LOGNAME`, `SHELL`, `TMPDIR`/`TEMP`/`TMP`,
   `LANG`, `LC_*`, `TZ`, `TERM`, `XDG_*`; the Windows equivalents
   (`SYSTEMROOT`, `COMSPEC`, `PATHEXT`, `APPDATA`, `LOCALAPPDATA`,
   `USERPROFILE`, `PROGRAMDATA`); `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`
   (both cases); TLS trust variables (`SSL_CERT_FILE`, `SSL_CERT_DIR`,
   `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`); and the runtime-manager
   variables `npx`/`uvx`/`node` commonly need (`NVM_*`, `VOLTA_HOME`,
   `PNPM_HOME`, `UV_*`, `PYENV_*`, `ASDF_*`, `GOPATH`, `GOROOT`).
2. `servers[].stdio.env_passthrough`: names copied from the parent
   environment as-is.
3. `servers[].stdio.env`: explicit `"KEY=VALUE"` entries. A value may
   reference `${VAR}`, expanded from the parent (proxy) environment at
   start. A reference to a variable that is not set there **fails the
   server's start** with an error naming the server and the variable.
4. `PYTHONUNBUFFERED=1` (unchanged from before).

```yaml
servers:
  - name: github
    transport: stdio
    stdio:
      command: npx
      args: ["-y", "@modelcontextprotocol/server-github"]
      env: ["GITHUB_PERSONAL_ACCESS_TOKEN=${GITHUB_TOKEN}"]  # explicit, ${VAR} expanded from the parent env
      env_passthrough: ["GITHUB_TOKEN"]                       # copy this name through as-is
      # inherit_env: true                                     # restore the old (pre-#311) full-inheritance behavior
```

**Migration steps** if your config relied on the old full-inheritance
behavior (for example a `GITHUB_TOKEN` exported in the shell that a server
picked up implicitly):

- Preferred: add the variable to that server's `env_passthrough` (copy
  as-is) or `env` (rename/derive it, e.g.
  `env: ["GITHUB_PERSONAL_ACCESS_TOKEN=${GITHUB_TOKEN}"]`).
- Quick migration: set `stdio.inherit_env: true` on that server to restore
  full inheritance. LeanProxy logs one warning per server that sets it.
- At start, for servers whose command matches a known package (e.g.
  `@modelcontextprotocol/server-github`), LeanProxy warns when a variable
  that package commonly needs is present in the proxy's environment but is
  not being passed to the child — a hint you likely need
  `env_passthrough`/`env`, not proof either way.
- Run `leanproxy-mcp doctor env` to see, per configured stdio server, which
  variable **names** are passed and which are dropped (never values).
- Configs imported via `leanproxy-mcp migrate` (from Claude/Cursor/VS Code)
  already write each imported server's `env` into explicit `stdio.env`
  entries, so they keep working unchanged.

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `servers[].stdio.env` | list of strings | none | Explicit `"KEY=VALUE"` entries added to the base environment. `${VAR}` is expanded from the parent (proxy) environment; an unresolved reference fails start. |
| `servers[].stdio.env_passthrough` | list of strings | none | Parent environment variable names copied to the child as-is, in addition to the base allowlist. |
| `servers[].stdio.inherit_env` | bool | `false` | `true` restores full inheritance of the proxy's environment (pre-#311 behavior). Logs one warning per server at start. |

See also [Security: Least-Privilege Child Environment](security.md#least-privilege-child-environment-311).

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

### Maximum Response Size per Stdio Server (`max_response_bytes`)

Each JSON-RPC message a stdio server writes on stdout (one line) may be at
most `max_response_bytes` long. The default, 64 MiB, comfortably covers large
tool results (file contents, search results, FIT data):

```yaml
servers:
  - name: files
    transport: stdio
    stdio:
      command: npx
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/data"]
    max_response_bytes: 1048576   # 1 MiB; default 64 MiB
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `servers[].max_response_bytes` | int | `67108864` (64 MiB) | Maximum size in bytes of one message read from a stdio server's stdout. `0` or absent means the default. |

A response over the limit is discarded up to its terminating newline, so the
stream stays in sync, and **only the call it answered** fails, with
`response from <server> exceeded max_response_bytes (<n>)` (logged at
Error). The server keeps running and the next call to it works normally.

Other stdio robustness guarantees:

- **stderr** is drained continuously for the life of the process. Each line
  is truncated to 8 KiB, redacted with the bouncer's built-in secret
  patterns, kept in a small buffer that is quoted in error messages, and
  logged at Debug only.
- If the server **closes stdout** (or reading it fails) while the process is
  still running, pending calls fail immediately and the process is killed
  and restarted with the usual `reconnect` settings.
- A server that **stops reading stdin** cannot block a caller past its
  timeout (Unix; pipe write deadlines are not available on Windows).
- On Unix each server runs in its own **process group**. Stopping a server
  sends SIGTERM to the whole group (so grandchildren started by `npx`,
  `uvx`, `docker run` or `sh -c` wrappers go too), then SIGKILL after a
  5-second grace period. On shutdown all servers are stopped in parallel.

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
| `bouncer.entropy_detection` | bool | `false` | Also redact high-entropy tokens (20+ characters, Shannon entropy >= 4.0) that sit within 20 characters of `key`, `secret`, `token` or `password`, or under a JSON key containing one of those words. See [Security: high-entropy detector](security.md#high-entropy-detector-optional). |

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
v0.10 (see the [changelog](https://github.com/mmornati/leanproxy-mcp/blob/main/CHANGELOG.md#removed-in-v010) and issue
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

## Response Cache

The response cache is an opt-in, exact-match cache for `tools/call`: off by
default, allowlisted tools only, keyed on the request *before* secret
redaction runs so two callers who differ only in a credential value never
share a cached response. It is bounded LRU by bytes, never caches an error
response, and never does embedding-similarity matching (that is the Semantic
Cache below, which — as of #299 — no longer answers `tools/call` at all).

It runs as a middleware shared by both `leanproxy-mcp server run --stdio` and
`leanproxy-mcp serve`.

### Configuration

```yaml
response_cache:
  enabled: false              # default: off
  ttl: 5m
  max_bytes: 67108864         # 64 MiB total, LRU by bytes
  max_entry_bytes: 1048576    # responses larger than this are never cached
  tools:                      # explicit allowlist: "server.tool" or a glob
    - github.get_file_contents
    - github.get_*
  honor_annotations: true     # accepted for forward compatibility; see below
```

### Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | `false` | Master opt-in switch |
| `ttl` | duration | `5m` | How long a cached response stays fresh |
| `max_bytes` | int | `67108864` (64 MiB) | Total cache budget; entries are evicted LRU when exceeded |
| `max_entry_bytes` | int | `1048576` (1 MiB) | A single response larger than this is never stored |
| `tools` | list of string | `[]` | Explicit allowlist: `"server.tool"` exact match, or a `path.Match` glob such as `"server.get_*"` |
| `honor_annotations` | bool | `false` | Accepted for forward compatibility. Tool annotations (`readOnlyHint`/`idempotentHint`) are not yet plumbed through the protocol (Epic 20); until then this flag has **no effect** — only the `tools` allowlist above is consulted |

### Why only the allowlist, keyed before redaction

- Only `tools/call` is ever cached, and only tools named in `tools` (or,
  once Epic 20 lands, tools annotated read-only *and* idempotent). A
  side-effecting tool like `create_issue` is never cached unless an operator
  explicitly opts it in.
- The cache key is `SHA-256(server + tool + canonical JSON of the
  *original*, unredacted arguments)`. Only that hash is stored — never the
  raw arguments — but deriving it before redaction means two calls that
  differ only in a secret value (two different API keys, for example) never
  collide into the same entry.
- The cached *value* is the redacted response, so a cache hit can never leak
  anything a cache miss wouldn't already have redacted.

## Tool Search (`search_tools`)

`search_tools` is the recommended discovery path of `leanproxy-mcp server run
--stdio`: one call ranks the cached tools of **every** server against a
natural-language query and returns the top matches (default 5, at most 20),
one line each in the `list_tools` format, ready for `invoke_tool`.

It uses Okapi BM25 (k1 = 1.2, b = 0.75) over the server name, the tool name
(weight ×2), the description, parameter names and parameter descriptions
(weight ×0.5). Words are lowercased, split on non-alphanumerics, `_` and
camelCase, lightly stemmed (`ing`, `ies`→`y`, `es`, `s`, `ed` on words longer
than 4 characters) and stopwords are dropped. The index follows the
background tool cache: when a server's tool list changes, only that server's
tools are re-indexed. Ties are broken by server, then tool name.

With no `tool_search` block, BM25 runs with the default synonyms and no
network access.

### Configuration

```yaml
tool_search:
  synonyms:                    # added to the defaults; same key replaces one
    k8s: kubernetes cluster
    mr: merge request
  disable_default_synonyms: false
  hybrid:                      # optional; off by default
    enabled: true
    embedder:
      provider: ollama         # or openai
      ollama:
        url: http://localhost:11434
        model: nomic-embed-text
      # openai:
      #   model: text-embedding-3-small   # key from OPENAI_API_KEY or api_key
```

### Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `synonyms` | map of string | `{}` | Query expansions: a single-word key (not a stopword) and the words it adds to the query. Added to the defaults below |
| `disable_default_synonyms` | bool | `false` | Drop the built-in synonyms: `pr`→`pull request`, `ticket`/`bug`→`issue`, `ci`→`workflow action`, `repo`→`repository` |
| `hybrid.enabled` | bool | `false` | Fuse BM25 with embedding similarity (Reciprocal Rank Fusion, k = 60) |
| `hybrid.embedder.provider` | string | – | `ollama` or `openai`, required when `hybrid.enabled` is true |
| `hybrid.embedder.ollama` / `.openai` | object | – | Same keys as the embedder elsewhere in the config (`url`, `model`; `model`, `api_key`) |

In hybrid mode, tool descriptions are embedded in the background and the
vectors are cached by tool-definition hash, so an unchanged tool is never
embedded twice. A tool not embedded yet ranks through BM25 only, and a query
whose embedding fails (embedder down, 3 s timeout) is answered with BM25
alone. Hybrid mode sends each tool description and each query to the
configured embedder.

An invalid `tool_search` block (a multi-word synonym key, hybrid enabled
without a valid embedder) fails config loading.

## Semantic Cache

Semantic caching stores and retrieves tool responses based on vector similarity, reducing redundant LLM calls for semantically similar requests.

> As of #299, the semantic cache no longer answers `tools/call` — use the
> Response Cache above for tool-call caching. The vector store is also no
> longer opened at startup unless `cache.vector_store` or `--embed-provider`
> is explicitly configured.

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
| `--dashboard-bind` | `127.0.0.1:9090` | Dashboard bind address. Set to `off` to disable. A non-loopback bind refuses to start without `--dashboard-token` |
| `--dashboard-token` | `""` | Bearer token for dashboard access. Once set, required from **every** client, loopback included — there is no loopback bypass. A browser exchanges it for an `HttpOnly`, `SameSite=Strict` cookie via `GET /login?token=…` (also `Secure` when served over TLS) |
| `--dashboard-allowed-hosts` | (none) | Extra `Host` header values accepted, beyond the bind host and `localhost`/`127.0.0.1`/`[::1]` |

See [Dashboard hardening](#dashboard-hardening-host-and-origin-validation) below for the Host/Origin and security-header details.

### Metrics Endpoint

```bash
leanproxy-mcp serve --metrics-bind 127.0.0.1:9091
```

| Flag | Default | Description |
|------|---------|-------------|
| `--metrics-bind` | `""` | Metrics endpoint bind address. Set to `off` or empty to disable. A non-loopback bind refuses to start without `--metrics-token` |
| `--metrics-token` | `""` | Bearer token for the metrics endpoint (`Authorization: Bearer <token>`); required on a non-loopback bind |
| `--metrics-allowed-hosts` | (none) | Extra `Host` header values accepted, beyond the bind host and `localhost`/`127.0.0.1`/`[::1]` |

### Dashboard hardening: Host and Origin validation

Both the dashboard and the metrics endpoint (issue #316) reject any request
whose `Host` header is not the bind host, `localhost`, `127.0.0.1` or
`[::1]` (each with the listening port) or one of `--dashboard-allowed-hosts`
/ `--metrics-allowed-hosts` — closing the DNS-rebinding path where a
malicious web page tricks a browser into sending requests to
`127.0.0.1:9090` under a hostname it controls. A state-changing request
(anything but GET/HEAD/OPTIONS) whose `Origin` header does not match the
request's own host is rejected the same way. Both get `403 Forbidden`.

The dashboard additionally sends these headers on every response:

- `Content-Security-Policy: default-src 'self'; script-src 'self'`
- `X-Frame-Options: DENY`
- `Referrer-Policy: no-referrer`
- `X-Content-Type-Options: nosniff`

### Export Cost Data

```bash
# CSV export
leanproxy-mcp report --export csv --output costs.csv

# JSON export
leanproxy-mcp report --export json --output costs.json

# Filtered by date range
leanproxy-mcp report --export csv --since 2026-06-01
```

## First-Party Servers: Postgres and Redis

`servers/postgres` and `servers/redis` are small, first-party stdio MCP servers, configured entirely
through environment variables passed to the child process (see
[Child Process Environment](#child-process-environment-env-env_passthrough-inherit_env) for how those
variables reach a `stdio` server declared in `leanproxy.yaml`). Bundling more first-party servers is a
non-goal — official vendor servers exist for most databases — so these two only cover the minimum a
proxy operator needs, and are kept intentionally small in surface area.

### Postgres (`servers/postgres`)

| Variable | Description | Default |
|----------|-------------|---------|
| `LEANPROXY_POSTGRES_CONNECTION` | PostgreSQL connection string, e.g. `postgres://user:pass@host:5432/db` | *(required)* |
| `LEANPROXY_POSTGRES_POOL_SIZE` | Connection pool size | `10` |
| `LEANPROXY_POSTGRES_STATEMENT_TIMEOUT` | Per-statement timeout (Go duration, e.g. `30s`) | `30s` |
| `LEANPROXY_POSTGRES_READ_ONLY` | `true`/`false` — see below | `true` |

**Read-only mode (`LEANPROXY_POSTGRES_READ_ONLY`, default `true`):**

- The `postgresql_execute` tool (INSERT/UPDATE/DELETE/DDL) is **not registered at all** — it does not
  appear in `tools/list` and calling it by name returns "unknown tool". Set
  `LEANPROXY_POSTGRES_READ_ONLY=false` to register it.
- The `postgresql_query` tool always runs the query inside a real `BEGIN ... READ ONLY` transaction
  (with `SET LOCAL statement_timeout`), rolled back afterwards, **regardless of `LEANPROXY_POSTGRES_READ_ONLY`**.
  This is the actual security boundary. A query text starting with `SELECT`, `EXPLAIN` or `WITH` is
  accepted by a prefix check first, but that check is a UX hint only — it rejects obvious misuse (a bare
  `INSERT`/`UPDATE`/`DELETE`/DDL statement) with a clearer message, not the thing stopping a write. Even a
  query that starts with `SELECT`/`EXPLAIN`/`WITH` and hides a side effect — `EXPLAIN ANALYZE DELETE ...`
  (which executes the statement), `SELECT ... INTO ...`, `SELECT pg_terminate_backend(...)`, or
  `WITH x AS (DELETE ... RETURNING *) SELECT * FROM x` — is stopped by Postgres itself refusing to write
  inside a read-only transaction.
- Queries are sent through pgx's extended query protocol (parse/bind/execute), which also rejects a
  query string containing more than one SQL statement, closing the classic `;`-separated multi-statement
  injection vector.

!!! warning "A read-only database role is real protection; this flag is defense in depth"
    `LEANPROXY_POSTGRES_READ_ONLY` and the read-only transaction it enforces protect against the tool
    running writes through this server, but the database user in `LEANPROXY_POSTGRES_CONNECTION` can
    still authenticate and, outside this server's control, do whatever that role is granted. For real
    protection, connect with a Postgres role that only has `SELECT` on the schemas it needs
    (`CREATE ROLE leanproxy_ro WITH LOGIN PASSWORD '...'; GRANT CONNECT ON DATABASE ... TO leanproxy_ro;
    GRANT USAGE ON SCHEMA public TO leanproxy_ro; GRANT SELECT ON ALL TABLES IN SCHEMA public TO
    leanproxy_ro;`), and grant a write-capable role only when `LEANPROXY_POSTGRES_READ_ONLY=false` is a
    deliberate, reviewed choice.

### Redis (`servers/redis`)

| Variable | Description | Default |
|----------|-------------|---------|
| `LEANPROXY_REDIS_ADDRESS` | `host:port` of the Redis server | `127.0.0.1:6379` |
| `LEANPROXY_REDIS_PASSWORD` | `AUTH` password | *(none)* |
| `LEANPROXY_REDIS_POOL_SIZE` | Connection pool size | `10` |
| `LEANPROXY_REDIS_TLS` | `true`/`1` to dial over TLS (min TLS 1.2) | `false` |
| `LEANPROXY_REDIS_DIAL_TIMEOUT` | Timeout for opening (or re-opening) a connection, including the `AUTH`/`SELECT` handshake (Go duration) | `5s` |
| `LEANPROXY_REDIS_COMMAND_TIMEOUT` | Read/write deadline applied to every command | `5s` |
| `LEANPROXY_REDIS_MAX_BULK_LEN` | Max bytes accepted for one RESP bulk-string reply before it is rejected | `16777216` (16 MiB) |
| `LEANPROXY_REDIS_MAX_ARRAY_LEN` | Max elements accepted for one RESP array reply before it is rejected | `1000000` |

Only `redis_get`, `redis_set`, `redis_delete`, `redis_keys` and `redis_exists` are exposed — there is no
`redis_execute`-style escape hatch to arbitrary commands, and that is intentional: dangerous commands
(`FLUSHALL`, `CONFIG`, `EVAL`, ...) stay unreachable through this server.

The connection pool never blocks indefinitely on a broken connection: every borrowed connection is
always returned to the pool, even when the operation failed and re-dialing to replace it also failed, so
`Close()` (and every other caller) can never deadlock behind a permanently drained pool. `MAX_BULK_LEN`
and `MAX_ARRAY_LEN` cap what the client will allocate for a single reply, so a malicious or compromised
Redis server (or a man-in-the-middle on a connection without `LEANPROXY_REDIS_TLS`) cannot force an
out-of-memory condition by advertising a huge `$`/`*` length; exceeding either limit is an error and the
connection is closed and re-dialed on the next use.

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