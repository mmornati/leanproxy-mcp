# Security

LeanProxy-MCP includes multiple security hardening features to protect your data and prevent common attack vectors.

## Features Overview

| Feature | Description |
|---------|-------------|
| **Least-Privilege Child Environment** | Stdio MCP servers get a minimal environment by default instead of the proxy's full environment (#311) |
| **Dashboard & Metrics Hardening** | Host/Origin validation (DNS-rebinding defense), no unauthenticated non-loopback bind, no loopback token bypass, CSP and other security headers (#316) |
| **First-Party Servers Hardening** | Postgres: real read-only transaction, not just a text prefix check. Redis: pool that can't deadlock, bounded RESP allocations, per-command deadlines (#318) |
| **In-Memory Redaction** | Pre-configured patterns redact secrets before they reach LLM providers |
| **Prompt Injection Protection** | Classifies the decoded text of requests and tool outputs (indirect injection) with risk scoring and per-direction actions |
| **Sidecar LLM Redaction** | Context-aware redaction via a local Ollama model for sensitive data beyond regex |
| **Batch Size Limits** | Prevents DoS via large JSON-RPC batch requests |
| **ReDoS Protection** | Validates regex patterns to prevent catastrophic backtracking |
| **Path Validation** | Prevents path traversal attacks on configuration files |
| **Graceful Shutdown** | Ensures all goroutines are properly terminated |

## Least-Privilege Child Environment (#311)

**Breaking change.** Every stdio MCP server used to inherit the proxy's
**full** process environment, including any `OPENAI_API_KEY`,
`ANTHROPIC_API_KEY`, `AWS_*`, `GITHUB_TOKEN`, database URLs, etc. set where
the proxy runs — visible to a compromised or malicious server regardless of
whether it needed them. As of this release each child gets a
**least-privilege environment by default**: a small fixed allowlist (PATH,
HOME, locale, TLS trust, outbound proxy, and npx/uvx/node runtime-manager
variables), plus whatever a server's own config explicitly asks for via
`env_passthrough` / `env`. See [Configuration: Child Process
Environment](configuration.md#child-process-environment-env-env_passthrough-inherit_env)
for the full allowlist, the `${VAR}`-expansion syntax and migration steps
(including `leanproxy-mcp doctor env`, which lists per-server passed/dropped
variable **names**, never values).

`inherit_env: true` restores the old full-inheritance behavior per server,
for migration; LeanProxy logs one warning per server that sets it, and at
start also warns when a well-known server package (e.g.
`@modelcontextprotocol/server-github`) likely needs a variable that is
present in the proxy's environment but not being passed to it.

## Dashboard & metrics hardening (#316)

`leanproxy-mcp serve`'s dashboard (`--dashboard-bind`) and metrics
(`--metrics-bind`) endpoints:

- **Refuse to start** on a non-loopback bind with no token configured
  (`--dashboard-token` / `--metrics-token`), instead of only logging a
  warning and serving the data unauthenticated.
- **No loopback bypass.** Once a token is configured, it is required from
  *every* client, including `127.0.0.1` — a reverse proxy or any other local
  process on the same host can no longer skip it. The dashboard supports an
  `HttpOnly`, `SameSite=Strict` cookie (`Secure` over TLS) obtained via
  `GET /login?token=…`, so a browser session doesn't need to attach the
  header to every request.
- **Validate the `Host` header** against the bind host, `localhost`,
  `127.0.0.1`, `[::1]` and any `--dashboard-allowed-hosts` /
  `--metrics-allowed-hosts` entries, closing the DNS-rebinding path where a
  malicious web page makes a victim's browser send requests to
  `127.0.0.1:9090` under an attacker-controlled hostname. A state-changing
  request whose `Origin` does not match the request's own host is rejected
  the same way (`403 Forbidden`).
- **Security headers** on every dashboard response: `Content-Security-Policy:
  default-src 'self'; script-src 'self'`, `X-Frame-Options: DENY`,
  `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`.

See [Dashboard](dashboard.md#authentication) for the full flag reference.

## First-party servers hardening: Postgres, Redis (#318)

`servers/postgres` and `servers/redis` are small first-party stdio servers (bundling more is a
non-goal — official vendor servers exist for most databases). This release fixes the security and
robustness bugs found in them.

**Postgres — a real read-only mode, not just a text check.** The `postgresql_query` tool used to gate
queries on a prefix check (`SELECT`/`EXPLAIN`) that a query text can slip past while still writing:
`EXPLAIN ANALYZE DELETE ...` executes the statement, `SELECT ... INTO ...` creates a table, `SELECT
pg_terminate_backend(...)`/`set_config(...)`/`lo_import(...)`/`dblink_exec(...)` all have side effects,
and `WITH x AS (DELETE ... RETURNING *) SELECT * FROM x` hides a DELETE in a CTE. There was also no way
to disable `postgresql_execute` (arbitrary INSERT/UPDATE/DELETE/DDL) at all. Now:

- `LEANPROXY_POSTGRES_READ_ONLY` (default `true`) makes the server not register `postgresql_execute` at
  all — it is absent from `tools/list`.
- `postgresql_query` always runs inside a real `BEGIN ... READ ONLY` transaction (with `SET LOCAL
  statement_timeout`), rolled back afterwards, regardless of that flag. This is the actual boundary: the
  prefix check is a UX hint only, and Postgres itself refuses any write attempted inside the read-only
  transaction, however the query text disguised it.
- Queries go through pgx's extended protocol, which also rejects a `;`-separated multi-statement string.
- A read-only Postgres **role** is still the real defense in depth for `LEANPROXY_POSTGRES_CONNECTION`;
  see [Configuration: First-Party Servers](configuration.md#first-party-servers-postgres-and-redis) for
  the recommended grants.

**Redis — pool deadlock and unbounded RESP allocations.** `withConn` used to receive from the connection
pool channel while holding the client's mutex, and on a failed re-dial it returned without ever putting
the borrowed slot back — under sustained failures the pool drained permanently and every subsequent call,
including `Close()`, blocked forever. The RESP parser also trusted server-sent `$n`/`*n` lengths outright,
so a malicious or compromised Redis server (or a man-in-the-middle on a connection without
`LEANPROXY_REDIS_TLS`) could force an unbounded allocation. Now:

- Every borrowed connection is returned to the pool on every path, including a failed re-dial, so the
  pool can never drain and `Close()` always returns promptly.
- `LEANPROXY_REDIS_MAX_BULK_LEN` (default 16 MiB) and `LEANPROXY_REDIS_MAX_ARRAY_LEN` (default 1,000,000)
  cap what a single reply can make the client allocate; exceeding either is an error and the connection
  is closed.
- `LEANPROXY_REDIS_DIAL_TIMEOUT` and `LEANPROXY_REDIS_COMMAND_TIMEOUT` (both default `5s`) bound
  connecting/re-connecting and every command, so an unresponsive server can no longer hang a caller.
- The exposed command set is unchanged and intentionally small: `redis_get`, `redis_set`,
  `redis_delete`, `redis_keys`, `redis_exists` — no escape hatch to arbitrary Redis commands.

See [Configuration: First-Party Servers](configuration.md#first-party-servers-postgres-and-redis) for the
full environment-variable reference.

## `serve` listener authentication

`leanproxy-mcp serve` accepts JSON-RPC over TCP only from clients whose first
line is `{"jsonrpc":"2.0","method":"auth","params":{"token":"…"}}` with the
token from `~/.config/leanproxy/serve.token` (created with mode 0600 on first
start), `--auth-token` or `$LEANPROXY_SERVE_TOKEN`. Connections that fail the
handshake, or whose first line looks like an HTTP request (the browser
cross-protocol attack), are closed without executing anything. `--no-auth` is
only accepted on loopback addresses. See
[`serve`](commands.md#client-protocol-and-authentication) for the protocol and
limits.

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
client → redact request params → injection check → dispatch/upstream → injection check (response) → redact response → client
```

- **Request redaction** covers every nested value of `params`, including the
  `arguments` of `tools/call` and the nested `arguments` of an `invoke_tool`
  call. If params cannot be redacted the request is rejected with
  `Secret redaction failed; request not forwarded` and never forwarded.
- **Response redaction** covers `result`, `error.message` and `error.data`
  of every response, including `list_tools` and `search_tools` text
  (upstream tool descriptions) and `structuredContent`, the aggregated
  `resources/list`, `resources/templates/list` and `prompts/list`, resource
  contents (`resources/read`) and prompt messages (`prompts/get`),
  and error hints/schemas.
- Redaction is **on by default** with the built-in patterns when there is no
  `bouncer:` block. Only `bouncer.enabled: false` turns it off (in both
  modes). At startup the proxy logs one line such as
  `redaction enabled, 29 patterns; injection disabled` (or
  `injection enabled (requests and responses)`).

## In-Memory Redaction

LeanProxy-MCP intercepts all data flowing through the proxy and redacts sensitive information before it reaches LLM providers. This operates entirely in-memory—no data is persisted or logged.

### Built-in Patterns

Every match is replaced by `[SECRET_REDACTED]`, whatever its severity (the
severity is informational; `leanproxy-mcp bouncer list-patterns` prints it).
Patterns marked "only ... is replaced" keep the surrounding context readable,
for example `postgres://app:[SECRET_REDACTED]@db:5432/app`.

| Pattern | Severity | Matches |
|---------|----------|---------|
| `aws-access-key` | critical | AWS Access Key ID (20 characters, starts with `AKIA`) |
| `aws-temporary-access-key` | critical | AWS temporary (STS) Access Key ID (20 characters, starts with `ASIA`) |
| `aws-secret-access-key` | critical | AWS Secret Access Key (40 base64 characters after `aws_secret_access_key`; only the key is replaced) |
| `github-classic-pat` | critical | GitHub classic personal access token (`ghp_` + 36 or more characters) |
| `github-app-token` | critical | GitHub OAuth, user-to-server, server-to-server and refresh tokens (`gho_`, `ghu_`, `ghs_`, `ghr_`) |
| `github-fine-grained-pat` | critical | GitHub fine-grained personal access token (`github_pat_`) |
| `gitlab-pat` | critical | GitLab personal access token (`glpat-`) |
| `stripe-secret-key` | critical | Stripe secret key, live or test mode (`sk_live_`, `sk_test_`) |
| `stripe-restricted-key` | critical | Stripe restricted key, live or test mode (`rk_live_`, `rk_test_`) |
| `stripe-publishable-key` | low | Stripe live publishable key (`pk_live_`) |
| `pem-private-key` | critical | PEM private keys (RSA, EC, DSA, OpenSSH, PKCS#8, encrypted) |
| `pgp-private-key` | critical | ASCII-armored PGP/GPG private key blocks |
| `pem-certificate` | low | PEM X.509 certificates |
| `gcp-service-account` | low | `"type": "service_account"` marker in free text (in JSON, the `private_key` field is redacted by key name) |
| `gcp-oauth-token` | high | Google OAuth2 access token (`ya29.`) |
| `google-api-key` | high | Google API key (Maps, Firebase, Gemini, ...: `AIza` + 35 characters) |
| `slack-token` | high | Slack bot, user, app and refresh tokens (`xoxb-`, `xoxp-`, `xoxa-`, `xoxr-`, `xoxs-`) |
| `slack-webhook` | high | Slack incoming-webhook URL |
| `openai-api-key` | critical | OpenAI API key, legacy format (`sk-` + 40 or more alphanumerics) |
| `openai-project-key` | critical | OpenAI project, service-account and admin keys (`sk-proj-`, `sk-svcacct-`, `sk-admin-`) |
| `anthropic-api-key` | critical | Anthropic API key (`sk-ant-`) |
| `npm-token` | high | npm access token (`npm_`) |
| `generic-api-key` | medium | `api_key=...` / `apikey...` followed by 16 or more alphanumerics (case-insensitive) |
| `bearer-token` | high | `Bearer` followed by a JWT |
| `jwt` | high | JSON Web Token without a `Bearer` prefix (`eyJ...` `.` `eyJ...` `.` signature) |
| `basic-auth-header` | high | `Authorization: Basic <base64>` (only the credentials are replaced) |
| `dsn-credentials` | critical | Password in a connection string or URL: `postgres://`, `mysql://`, `mongodb+srv://`, `redis://`, `amqp://`, `https://user:pass@...` (only the password is replaced) |
| `env-var-value` | medium | `$NAME=value` shell assignment |
| `env-file-secret` | high | `.env` / shell line whose UPPER_CASE name ends in `PASSWORD`, `SECRET`, `TOKEN`, `API_KEY`, `PRIVATE_KEY` or `ACCESS_KEY` (only the value is replaced) |

### Sensitive JSON keys

In JSON payloads, the value of a sensitive key is redacted whatever it is: a
string, a number (`"password": 12345678`), an array or an object. Keys are
compared case-insensitively after removing `-`, `_` and spaces, so `API-Key`,
`api_key` and `apiKey` are the same key.

- Exact names: `password`, `passwd`, `pwd`, `passphrase`, `secret`, `token`,
  `apikey`, `apisecret`, `appsecret`, `apitoken`, `accesstoken`,
  `refreshtoken`, `idtoken`, `authtoken`, `bearertoken`, `sessiontoken`,
  `csrftoken`, `xsrftoken`, `clientsecret`, `privatekey`, `secretkey`,
  `secretaccesskey`, `authorization`, `proxyauthorization`, `cookie`,
  `setcookie`, `sessionid`, `awssecretaccesskey`, `awssessiontoken`,
  `xapikey`, `xauthtoken`.
- Suffixes (so `db_password`, `stripeApiKey` and `github_access_token` are
  covered too): `password`, `passwd`, `passphrase`, `secret`, `apikey`,
  `secretkey`, `privatekey`, `accesstoken`, `refreshtoken`, `authtoken`,
  `sessiontoken`, `apitoken`, `clientsecret`. `token` on its own is not a
  suffix: `next_page_token` and `max_tokens` are not credentials.
- `true`, `false`, `null` and empty strings are kept (they hide nothing).
- An **object** under a sensitive key inside a JSON Schema `properties`,
  `patternProperties`, `definitions` or `$defs` map is a parameter
  definition, not a value, and is kept, so a `tools/list`
  entry with a `password` or `token` parameter stays usable. Strings inside
  it are still scanned with the patterns.
- Object **keys** are scanned with the patterns too: a secret used as a key
  is replaced.

### How JSON payloads are redacted

Request params and response results are redacted **losslessly**: the
redactor copies every byte through unchanged except the string literals that
contain a secret and the values of sensitive keys. Numbers (including
integers beyond 2^53), key order, whitespace, escaping and `<`, `>`, `&`
reach the other side byte for byte. A payload without secrets is forwarded
as is.

- Patterns see the **decoded** string, so a secret written with JSON escapes
  (`\u0041KIA...`) is still found; only the matched part of the literal is
  rewritten.
- A string whose value is itself a JSON object or array (MCP returns tool
  data this way, in `result.content[].text`) is redacted recursively, with
  the key rules above, up to 3 levels deep and 8 MiB per string. Only the
  redacted parts of the inner document change.
- Input that is not valid JSON is scanned byte by byte with the patterns
  instead of being passed through.
- Error messages, upstream stderr lines and logged payloads use the same
  engine.

### High-entropy detector (optional)

For tokens no pattern knows, you can turn on a generic detector:

```yaml
bouncer:
  entropy_detection: true   # default: false
```

It redacts runs of 20 or more characters from `[A-Za-z0-9+/=_-]` whose
Shannon entropy is at least 4.0 bits per character, **only** when a
key-like word (`key`, `secret`, `token`, `password`) is within 20
characters of the run, or when the run is the value of a JSON key containing
such a word (`"signing_key": "..."`). Hex digests, UUIDs and ordinary
identifiers stay below the threshold; base64 blobs are left alone unless a
key-like word sits right next to them. It is off by default because it can
still redact random-looking values that are not secrets.

### Measured coverage

`pkg/bouncer/testdata/corpus/` holds realistic MCP tool results (a GitHub
`.env` file and service-account key, Postgres rows, a Slack export, an HTTP
request log) with labeled fake secrets, plus a negative set (UUIDs, SHAs, a
base64 image, lorem ipsum, a `tools/list` response, source code).
`TestRedactionCorpus` reports recall and the false-positive rate and fails
below 95% recall or at one false positive per 100 KB or more.

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

LeanProxy-MCP classifies what goes **to** tools and what comes **back** from
them. Indirect prompt injection — instructions planted in a web page, an
issue, an e-mail or a file that a tool returns — is the main real-world MCP
threat, so since v0.11 ([#315](https://github.com/mmornati/leanproxy-mcp/issues/315))
tool results, resource reads and prompts are classified too, with their own
policy. Configuration: [`injection:`](./configuration.md#prompt-injection-protection).

### How it works

1. **Decoded text, not raw JSON.** The guard walks the message with the same
   lossless JSON scanner as the redactor and collects the *decoded* string
   values in scope (keys included for requests and `structuredContent`).
   Strings that are JSON documents themselves (a tool's text is often one)
   are opened, up to three levels. `ignore\u0020previous instructions` and
   `ignore\tprevious…` therefore score like the plain phrase, and a phrase
   split across two fields is still seen (strings are joined by line
   breaks).
2. **Normalization.** Compatibility folding (full-width and mathematical
   letters, ligatures) with accents removed; zero-width, bidi-control and
   other invisible characters dropped; Unicode "tag" characters (ASCII
   smuggling) mapped back to ASCII; common Cyrillic/Greek look-alikes mapped
   to Latin; lower-casing; whitespace collapsed.
3. **Cap.** At most `max_scan_bytes` (256 KiB) of text per message is
   classified; beyond that the head and the tail are sampled. An injection
   in the middle of a larger output is not seen.
4. **Pattern matching.** 22 weighted patterns (below); matched weights add up
   to a risk score capped at 100. Each pattern declares *triggers* (literal
   words one of which every match contains): one pass over the text finds
   them all, a pattern whose triggers are absent is skipped, and the regex
   only runs on small windows around the trigger occurrences. Custom
   patterns may declare `triggers` too; without them they run on the whole
   text.
5. **Optional judge.** Scores in the grey band (30-80) can be sent to a
   local model for a strict-JSON verdict ([`injection.judge`](./configuration.md#local-judge-injectionjudge)).
6. **Policy.** The risk selects an action from `request_policies` or
   `response_policies`.

| Direction | Default policy | Actions |
|-----------|----------------|---------|
| Requests (`params`) | block ≥ 80, quarantine 50-79, log 1-49 | `block`, `quarantine`, `redact`, `log` |
| Tool results, `resources/read`, `prompts/get` | annotate ≥ `threshold` (70), log below | `annotate`, `redact`, `block`, `log` |

Every action keeps the message valid JSON: `redact` replaces only the
matching spans inside string values (routing fields such as the tool name
are never touched), `annotate` inserts one warning item and leaves every
other byte alone, `quarantine` and `block` never look like a success
(`isError: true` or a JSON-RPC error). A message the policy lets through is
relayed byte for byte.

### Measured

`TestClassify_ResponseCorpus` (`pkg/bouncer/injection`) reports detection on
the response side of [`tests/security/injection_corpus.json`](https://github.com/mmornati/leanproxy-mcp/blob/main/tests/security/injection_corpus.json)
(32 indirect injections in pages, issues, e-mails, READMEs and code; 96
benign READMEs, docs, issues, chat, code, e-mails and web pages, many of
them chosen to look like attacks):

| Threshold | Precision | Recall | False-positive rate |
|-----------|-----------|--------|---------------------|
| risk ≥ 70 (default annotate) | 100% | 90.6% | 0% |
| any risk > 0 (logged) | 72.7% | 100% | 12.5% |

The 200 request-side samples keep 100% recall and 0 false positives. On
text the patterns were not tuned on — 4,474 Markdown files from the Go
module cache and the Go standard library sources (7,340 files) — no file
reaches the default threshold (0.65% and 0.23% get a log-level score).
Classifying a benign 2 KiB tool round trip costs about 26 µs, a 64 KiB
result about 0.6 ms.

### Quarantine

Quarantined request payloads (already secret-redacted) are saved to
`~/.leanproxy/quarantine/<uuid>.json` with the matched patterns. The client
receives the quarantine ID. View the quarantine status:

```bash
leanproxy-mcp doctor security
```

### Built-in Patterns (22)

| Pattern | Weight | Description |
|---------|--------|-------------|
| `ignore-previous-instructions` | 90 | Override system instructions |
| `new-instruction-override` | 85 | Redefine assistant role |
| `separator-injection` | 85 | Delimiter-based injection |
| `system-prompt-extraction` | 80 | Extract system prompt |
| `inject-command` | 80 | Explicit injection markers |
| `dan-jailbreak` | 75 | DAN-style jailbreaks |
| `forget-everything` | 75 | Context reset |
| `role-impersonation` | 70 | Boundary removal |
| `repeat-everything` | 70 | Conversation dump attempts |
| `exfiltrate-secrets` | 70 | Secrets or files sent to an external destination |
| `markdown-image-beacon` | 70 | Markdown image/link leaking data through its URL (`![](https://x/?data=…)`) |
| `chat-template-token` | 70 | Fake conversation turns (`<\|im_start\|>`, `[INST]`, `<<SYS>>`) |
| `token-smuggling` | 65 | Encoded payloads |
| `ignore-above` | 50 | Selective ignoring |
| `ai-directive` | 50 | Instructions addressed to the AI reading the text |
| `hidden-instruction-tag` | 50 | `<IMPORTANT>`-style pseudo-tags |
| `tool-call-hijack` | 45 | Tells the model to call a tool |
| `roleplay-context-switch` | 40 | Roleplay |
| `exfiltrate-verb` | 40 | Explicit exfiltration wording |
| `important-override` | 30 | Urgency-based |
| `send-to-url` | 30 | Data sent to a URL |
| `hypothetical-override` | 25 | Hypothetical scenarios |

The original 14 patterns keep their names and weights; v0.11 anchored them on
word boundaries and narrowed the phrasings that fired on ordinary documents
("you are now logged in", "no limits on file size", "show all commands",
"dependency injected", the name "Dan"). The full definitions are in
[`patterns_default.yaml`](https://github.com/mmornati/leanproxy-mcp/blob/main/pkg/bouncer/injection/patterns_default.yaml).

### Custom Patterns

Add custom patterns to catch organization-specific injection attempts.
They are matched against normalized (lower-case) text:

```yaml
injection:
  custom_patterns:
    - name: "acme-roster"
      pattern: "send\\s+the\\s+acme\\s+roster"
      weight: 80
      enabled: true
      description: "Exfiltration of the ACME roster"
      triggers: ["acme"]   # optional: skip the regex when "acme" is absent
```

### Diagnostic CLI

```bash
# Show security policy and quarantine status
leanproxy-mcp doctor security
```

## Server-to-client traffic (#308)

Upstream servers can send requests to the client (`elicitation/create`,
`sampling/createMessage`, `roots/list`) and notifications (progress,
resource updates). LeanProxy relays them (see
[configuration](./configuration.md#server-to-client-requests-allow_sampling-roots)),
and the Token Firewall covers that traffic as follows:

| Traffic | Secret redaction | Prompt-injection guard | Why |
|---------|------------------|------------------------|-----|
| Server → client request params (elicitation, sampling, roots) | yes | sampling and elicitation, with `response_policies` | The text is written by the upstream and put in front of the client's LLM (a sampling prompt) or its user (an elicitation message): the same untrusted direction as a tool result. `annotate` prepends the warning to the elicitation `message` or the sampling `systemPrompt`; `block` (and `quarantine`) refuses the request and the server gets a JSON-RPC error; `redact` replaces only the matching spans. Runs only when response scanning is enabled. |
| Client → server answers (elicitation input, sampling output, roots) | yes | no | The answer is the user's own input or the client LLM's output, i.e. the side the guard protects; it can carry the user's data (a pasted token), so it is redacted before it reaches the server. |
| Server → client notifications (progress `message`, resource updates, elicitation completion) | yes | no | Short status text; redacted like any other upstream output. |

Other safeguards:

- **Spoofing.** An elicitation message is always prefixed with
  `[<server>] `, so a server cannot pose as the IDE or as another server.
- **Sampling is opt-in** (`servers[].allow_sampling`, default `false`): a
  server cannot spend the user's LLM tokens unless the operator allows it,
  and every relayed sampling request is logged.
- **Only declared capabilities.** A request is relayed only to a client
  that declared the capability, and the proxy declares to each upstream
  only what it can relay (nothing to legacy SSE upstreams; `sampling` only
  with `allow_sampling`).
- **No cross-client leakage.** With several `serve` connections, a request
  goes to the client whose call to that server is in flight; when that is
  ambiguous it is refused rather than shown to another client. Progress
  notifications are routed by a proxy token unique per call, never
  broadcast; an unknown token is dropped.
- **Bounded.** At most 32 concurrent server-to-client requests per upstream
  connection and 64 pending requests per client; a request the client never
  answers is failed after 10 minutes; a disconnect or an upstream
  cancellation frees everything at once.

## Sidecar LLM Redaction

For context-aware redaction beyond regex patterns, deploy a sidecar LLM (Ollama). The sidecar analyzes already-redacted content and replaces any remaining sensitive data using an LLM.

### How It Works

1. Regex-based bouncer redaction runs first
2. Sidecar LLM receives the redacted content
3. LLM replaces remaining sensitive data (API keys, passwords, tokens, PII) with `[VALUE_REDACTED]`
4. Falls back to aggressive redact if LLM is unavailable
5. The LLM output is **verified** before use ([#315](https://github.com/mmornati/leanproxy-mcp/issues/315)):
   it must have the same structure as its input — same keys, array lengths and
   value types, unchanged numbers, booleans and routing fields (`name`,
   `server`, `tool`, `uri`, ...) — and every other string may only lose parts or
   have them replaced by a redaction marker. Text planted in the arguments can
   otherwise steer the model into rewriting the call (e.g. `read_file` into
   `write_file`). Output that fails the check is discarded with a warning and
   the regex-redacted params are forwarded. The tool name is always taken from
   the request as it was before the sidecar ran.

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