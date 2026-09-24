# Frequently Asked Questions

Common questions about LeanProxy-MCP.

## General

### What is LeanProxy-MCP?

LeanProxy-MCP is a local proxy that sits between your MCP client (IDE or
agent) and your MCP servers. The client starts one MCP server, LeanProxy,
which starts and aggregates all the others. On the way through it redacts
secrets, can screen for prompt injection, enforces per-tool policy and tool
pinning, and can cut the tokens that tool schemas and tool results cost.

### Why do I need LeanProxy-MCP?

1. **Security**: secret redaction in tool arguments and results (on by
   default), tool pinning, per-tool allow/deny/confirm policy, an optional
   prompt-injection guard and an optional container sandbox for stdio
   servers.
2. **Token cost**: a 4-tool router instead of every tool schema for clients
   without their own tool search, and an optional response governor that
   trims large tool results.
3. **One configuration**: all MCP servers are defined once, in
   `~/.config/leanproxy_servers.yaml`, and every client uses the same set.

### Which clients work with LeanProxy-MCP?

Any MCP client that can start a stdio server, or connect to a Streamable
HTTP server. Setup snippets for Claude Code, Claude Desktop, Cursor, VS Code
and OpenCode are in the
[Quick Start](quickstart.md#connect-your-client).

### What languages/frameworks does it support?

LeanProxy-MCP is language-agnostic. It works with any MCP server regardless
of the implementation language.

---

## Installation

### Where can I download the binary?

From GitHub Releases: <https://github.com/mmornati/leanproxy-mcp/releases>.
See [Installation](installation.md) for a download script and Homebrew.

### Which platforms are supported?

| Platform | Architecture | Status |
|----------|--------------|--------|
| macOS | arm64 (Apple Silicon) | Released |
| macOS | amd64 (Intel) | Released |
| Linux | amd64 (x86_64) | Released |
| Linux | arm64 (aarch64) | Released |
| Windows | any | No release, not tested |

### Do I need Go to build from source?

Yes, Go 1.25.5 or later:

```bash
git clone https://github.com/mmornati/leanproxy-mcp.git
cd leanproxy-mcp
go build -o leanproxy-mcp .
```

### Can I use it on Windows?

There is no Windows release and Windows is not tested. On Windows, run the
Linux binary inside WSL, together with the MCP servers it starts.

---

## Configuration

### Where should I put the config file?

The default is `~/.config/leanproxy_servers.yaml`. To use another file:

- set `LEANPROXY_CONFIG=/path/to/file.yaml`. `server add`, `server list`,
  `server enable/disable/remove`, `add`, `migrate` and `server run` read it;
- or pass `--config /path/to/file.yaml` to `server run`, `server health` or
  `status`. There it takes precedence over `LEANPROXY_CONFIG`.

Not every command follows these rules: `server add/list/...` ignore
`--config`, and `serve` and `doctor` ignore `LEANPROXY_CONFIG`. The full
table is in
[Config File Locations](configuration.md#config-file-locations).

### How do I enable/disable redaction?

Redaction is on by default. There is no command to switch it; set it in the
config and restart the proxy:

```yaml
bouncer:
  enabled: false
```

`leanproxy-mcp doctor security` reports whether it is on.

### Can I add custom redaction patterns?

Yes. Each pattern has a `name` and a Go regular expression (`pattern`).
Matches are replaced by `[SECRET_REDACTED]`; you cannot set your own
replacement text.

```yaml
bouncer:
  patterns:
    - name: "my-secret"
      pattern: "MY_SECRET=[A-Za-z0-9]+"
```

Check the patterns before you restart the proxy. An invalid or dangerous
pattern stops the config from loading:

```bash
leanproxy-mcp bouncer validate-patterns --config ~/.config/leanproxy_servers.yaml
```

### What built-in patterns are available?

29 patterns: AWS keys, GCP and Google API credentials, GitHub, GitLab,
Slack, Stripe, OpenAI, Anthropic and npm tokens, JWTs and bearer tokens,
PEM/PGP private keys and certificates, HTTP Basic credentials, passwords in
connection strings, a generic API-key pattern, and environment-variable and
`.env` style secrets. Print the full list with severities:

```bash
leanproxy-mcp bouncer list-patterns
```

A high-entropy detector is also available but off by default
(`bouncer.entropy_detection: true`). There is no email or phone-number
detection. See [Security](security.md#in-memory-redaction).

### Is the prompt-injection guard on by default?

No. Turn it on with:

```yaml
injection:
  enabled: true
```

See [Prompt Injection Protection](security.md#prompt-injection-protection).

---

## Usage

### How do I add an MCP server?

Import the ones your IDE already has with `leanproxy-mcp migrate`, or add
one by hand. Put `--` before the server's command:

```bash
leanproxy-mcp server add myserver -- npx -y @modelcontextprotocol/server-filesystem "$HOME/projects"
```

For `http` and `sse` servers, edit `~/.config/leanproxy_servers.yaml` (see
[Quick Start](quickstart.md#1-configure-mcp-servers)).

### How do I start the proxy?

You normally don't: your MCP client starts
`leanproxy-mcp server run --stdio` for you
([Quick Start](quickstart.md#connect-your-client)). To run it by hand:

```bash
# One client, over stdio
leanproxy-mcp server run --stdio

# One shared gateway for several clients, over Streamable HTTP
leanproxy-mcp server run --http 127.0.0.1:8765
```

Do not use `leanproxy-mcp serve` for an IDE. It is a deprecated, non-MCP
TCP protocol.

### How do I see token savings?

```bash
leanproxy-mcp report
```

`report` is built from counters that running proxies record. The older
`savings` and `cost` commands are deprecated and show estimates only. See
[Savings Report](savings-report.md).

### How do I generate a report file?

`--output` writes the same format you would see on screen. Add `--export`
to choose the format:

```bash
leanproxy-mcp report --export md --output report.md
leanproxy-mcp report --export csv --output report.csv
leanproxy-mcp report --export json --output report.json
```

### Can I run in dry-run mode?

Not for the proxy. The global `-n/--dry-run` flag has no effect on
`server run` or `server add`. These commands have their own working
`--dry-run`:

```bash
leanproxy-mcp migrate --dry-run
leanproxy-mcp add <server-id> --dry-run
leanproxy-mcp marketplace update <name> --dry-run
```

---

## Troubleshooting

### "command not found" error

Make sure `leanproxy-mcp` is on your `PATH`:

```bash
which leanproxy-mcp
leanproxy-mcp version
```

Some IDEs, such as Claude Desktop, do not use your shell's `PATH`. Put the
absolute path to the binary in the IDE config. See
[Troubleshooting](troubleshooting.md#server-command-not-found-when-started-from-an-ide).

### Server won't start

Run the proxy by hand with debug logs:

```bash
leanproxy-mcp server run --stdio --log-level debug
```

Logs go to stderr. Check one server on its own with
`leanproxy-mcp server health <name>`. More in
[Troubleshooting](troubleshooting.md#server-wont-start).

### IDE cannot connect

1. Check that the IDE runs `leanproxy-mcp server run --stdio` (not `serve`).
2. Check that the config has at least one enabled server:
   `leanproxy-mcp server list`.
3. Check that a proxy is running while the IDE is connected:
   `leanproxy-mcp status --running`.

### Redaction not working

1. Check that it is on: `leanproxy-mcp doctor security`.
2. Validate your custom patterns:
   `leanproxy-mcp bouncer validate-patterns --config ~/.config/leanproxy_servers.yaml`.
3. Restart the proxy after any config change.

---

## Security

### Is my data sent anywhere?

LeanProxy runs on your machine. By default it sends data only to the MCP
servers you configure, and to nobody else. It contacts other services only
when you turn on one of these features:

| Feature | Where data goes |
|---|---|
| Hybrid tool search with `provider: openai` (`tool_search.hybrid`) | Tool names and descriptions, and search queries, to the OpenAI embeddings API |
| OpenTelemetry export (`telemetry`, or `OTEL_EXPORTER_OTLP_*`) | Traces and metrics to your OTLP endpoint |
| `marketplace sync` | A download of the server index from the MCP Registry (or your `registry.sources`). `add`, `marketplace search`, `outdated` and `update` read the local copy |
| Local LLM features (injection judge, response summarizer, Ollama embedder, `serve` sidecar) | The Ollama URL you configure, `http://localhost:11434` by default. If you point it at another host, tool data goes there. The summarizer refuses a non-loopback URL unless you set `response.summarize.allow_remote: true` |

The same features under the deprecated `serve` command (`--embed-provider
openai`) also send data to OpenAI.

### Where is data stored?

| Path | Content |
|---|---|
| `~/.config/leanproxy_servers.yaml` | Configuration |
| `~/.config/leanproxy/toolcache/` | Cached tool definitions |
| `~/.config/leanproxy/status/current.json` | Status of running instances |
| `~/.config/leanproxy/pins.json` | Tool pins |
| `~/.config/leanproxy/serve.token` | Token for `server run --http` and `serve` |
| `~/.leanproxy/usage/` | Usage counters for `report` (kept 90 days) |
| `~/.leanproxy/registry/index.json` | Marketplace index |
| `~/.leanproxy/quarantine/` | Content quarantined by the injection guard |
| `~/.leanproxy/results/` | Spilled tool results, only with `response.spill.disk: true` |
| `~/.leanproxy/cache/` | Semantic cache (`serve` only) |

Logs go to stderr, or to the file given with `--log-file`.

### Are my secrets safe?

Redaction runs locally, in memory, before a tool argument or result
reaches the client or the server. It catches known secret formats and your
own patterns; it cannot catch every secret. Secrets written in
`leanproxy_servers.yaml` (for example in `http.headers`) are stored in
plain text, so keep the file private. See [Security](security.md) for the
threat model.

---

## Contributing

### How do I contribute?

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Submit a pull request

### How do I report bugs?

Open an issue at <https://github.com/mmornati/leanproxy-mcp/issues>.

---

## Need More Help?

- GitHub Issues: <https://github.com/mmornati/leanproxy-mcp/issues>
- GitHub Discussions: <https://github.com/mmornati/leanproxy-mcp/discussions>
