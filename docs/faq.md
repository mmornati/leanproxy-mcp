# Frequently Asked Questions

Common questions about LeanProxy-MCP.

## General

### What is LeanProxy-MCP?

LeanProxy-MCP is a lightweight CLI proxy that sits between your IDE and MCP servers. It acts as a "Token Firewall" - reducing token consumption and redacting sensitive data (API keys, secrets, PII) before it reaches LLM providers.

### Why do I need LeanProxy-MCP?

1. **Security**: Automatically redacts secrets, API keys, and PII from prompts
2. **Cost Optimization**: Reduces token usage by removing boilerplate and sensitive data
3. **Centralized Management**: Manage all MCP servers from one configuration
4. **Shadow Manifesting**: Automatically merges global and project MCP configurations

### What IDEs support LeanProxy-MCP?

- Claude Desktop
- Cursor
- OpenCode
- Windsurf
- Any IDE with MCP support

### What languages/frameworks does it support?

LeanProxy-MCP is language-agnostic. It works with any MCP server regardless of the implementation language.

---

## Installation

### Where can I download the binary?

Download from GitHub Releases: https://github.com/mmornati/leanproxy-mcp/releases/tag/v0.2.0

### Which platforms are supported?

| Platform | Architecture | Status |
|----------|--------------|--------|
| macOS | arm64 (Apple Silicon) | Supported |
| macOS | x86_64 (Intel) | Supported |
| Linux | x86_64 | Supported |
| Linux | ARM64 | Supported |
| Windows | x86_64 | Supported |

### Do I need Go to build from source?

Yes, Go 1.21+ is required to build from source:

```bash
git clone https://github.com/mmornati/leanproxy-mcp.git
cd leanproxy-mcp
go build -o leanproxy .
```

### How do I install on Windows?

Download the `.exe` from releases and add to your PATH. For example:

```powershell
Invoke-WebRequest -Uri https://github.com/mmornati/leanproxy-mcp/releases/download/v0.2.0/leanproxy-mcp_windows_amd64.exe -OutFile leanproxy.exe
Move-Item leanproxy.exe $env:LOCALAPPDATA\Microsoft\Windows\Tools\
```

---

## Configuration

### Where should I put the config file?

LeanProxy-MCP searches for config in this order:

1. `--config <path>` flag
2. `./leanproxy.yaml` (project directory)
3. `~/.config/leanproxy/config.yaml` (home directory)
4. Current directory

### How do I enable/disable redaction?

```bash
# Disable
leanproxy bouncer disable

# Enable
leanproxy bouncer enable
```

Or via config:

```yaml
bouncer:
  enabled: false
```

### Can I add custom redaction patterns?

Yes! Add to your config:

```yaml
bouncer:
  patterns:
    - name: "my-secret"
      type: "regex"
      pattern: "MY_SECRET=[A-Za-z0-9]+"
      replacement: "MY_SECRET=REDACTED"
```

### What built-in patterns are available?

| Pattern | Description |
|---------|-------------|
| `aws-access-key` | AWS Access Key IDs |
| `github-classic-pat` | GitHub Classic PATs |
| `github-fine-grained-pat` | GitHub Fine-grained PATs |
| `stripe-secret-key` | Stripe Secret Keys |
| `stripe-publishable-key` | Stripe Publishable Keys |
| `generic-api-key` | Generic API keys |
| `bearer-token` | JWT Bearer tokens |
| `env-var-value` | Environment variables |

---

## Usage

### How do I add an MCP server?

```bash
leanproxy server add myserver "npx -y @modelcontextprotocol/server-filesystem" "./"
```

### How do I start the proxy?

```bash
# Start the proxy server
leanproxy serve

# Or use the server command
leanproxy server add ...
```

### How do I see token savings?

```bash
leanproxy savings
```

### How do I generate a report?

```bash
leanproxy report --output report.md
```

### Can I run in dry-run mode?

Yes! Preview without making changes:

```bash
leanproxy server add myserver "..." --dry-run
```

---

## Troubleshooting

### "command not found" error

Make sure LeanProxy is in your PATH:

```bash
# Verify installation
leanproxy version

# If not found, check installation
which leanproxy
# or
where leanproxy
```

### Server won't start

1. Check if port is available:
```bash
lsof -i :8080
```

2. Check logs:
```bash
leanproxy serve --verbose
```

### IDE cannot connect

1. Verify server is running:
```bash
leanproxy status
```

2. Check IDE configuration matches the server format

### Redaction not working

1. Verify bouncer is enabled:
```bash
leanproxy bouncer list-patterns
```

2. Check config file is being loaded

---

## Security

### Is my data sent anywhere?

No. LeanProxy-MCP runs entirely locally. Your data never leaves your machine except to the MCP server you're proxying to.

### Where is data stored?

- Config: `~/.config/leanproxy/`
- Cache: `~/.cache/leanproxy/`
- Logs: stdout or configured log file

### Are my secrets safe?

Yes. The redaction engine runs locally and secrets are never logged or sent anywhere except when explicitly allowed.

---

## Contributing

### How do I contribute?

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Submit a pull request

### How do I report bugs?

Open an issue at: https://github.com/mmornati/leanproxy-mcp/issues

---

## Need More Help?

- GitHub Issues: https://github.com/mmornati/leanproxy-mcp/issues
- GitHub Discussions: https://github.com/mmornati/leanproxy-mcp/discussions