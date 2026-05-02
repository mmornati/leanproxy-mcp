# LeanProxy-MCP

**LeanProxy-MCP** is a lightweight, local CLI proxy designed to sit between your IDE and MCP (Model Context Protocol) servers. It acts as a "Token Firewall" — reducing token consumption and redacting sensitive data before it reaches LLM providers.

## Key Features

- **Token Firewall**: Pre-configured redaction engine ("The Bouncer") that intercepts secrets, API keys, and PII in real-time
- **Shadow Manifesting**: Automatically merges global (`~/.config/mcp.json`) and project-local MCP configurations
- **JIT Discovery**: On-demand tool registration via signatures to minimize initial context overhead
- **Dry-Run Mode**: Simulate proxy behavior and generate token savings reports without live execution
- **POSIX-Compliant CLI**: Manage MCP servers with simple commands (`server`, `compactor`, `context`)

## Architecture

For detailed architecture decisions, design patterns, and project structure, see the [Architecture Document](./_bmad-output/planning-artifacts/architecture.md).

## Quick Start

### Installation

```bash
# Download the latest binary for your platform from the releases page
curl -fsSL https://github.com/mmornati/leanproxy-mcp/releases/latest/download/leanproxy-mcp -o leanproxy-mcp
chmod +x leanproxy-mcp
sudo mv leanproxy-mcp /usr/local/bin/
```

### Usage

```bash
# Start the proxy with a local MCP server
leanproxy-mcp server --stdio "npx @modelcontextprotocol/server-filesystem ./my-project"

# Run in dry-run mode to see potential savings
leanproxy-mcp server --dry-run --stdio "npx @modelcontextprotocol/server-filesystem ./my-project"

# Generate a token savings report
leanproxy-mcp compactor --manifest ./mcp.json
```

## IDE Configuration

LeanProxy-MCP can be configured as an MCP server in your IDE to enable Claude Desktop, Cursor, OpenCode, or Windsurf to use LeanProxy-MCP's token firewall and redaction capabilities.

### Claude Desktop

1. Open `~/Library/Application Support/Claude/claude_desktop_config.json` in your editor
2. Add LeanProxy-MCP to the `mcpServers` section:

```json
{
  "mcpServers": {
    "leanproxy": {
      "command": "/path/to/leanproxy",
      "args": ["serve"]
    }
  }
}
```

3. Restart Claude Desktop

**Verification:** After restarting, run `leanproxy server list` to confirm the connection is working.

### Cursor

1. Open `~/.cursor/mcp.json` in your editor
2. Add LeanProxy-MCP to the `mcpServers` section:

```json
{
  "mcpServers": {
    "leanproxy": {
      "command": "/path/to/leanproxy",
      "args": ["serve"]
    }
  }
}
```

3. Reload Cursor window (Cmd+Shift+P → "Reload Window")

**Verification:** After reloading, run `leanproxy server list` to confirm the connection is working.

### OpenCode

1. Open `~/.config/opencode/mcp.json` in your editor
2. Add LeanProxy-MCP to the `mcpServers` section:

```json
{
  "mcpServers": {
    "leanproxy": {
      "command": "/path/to/leanproxy",
      "args": ["serve"]
    }
  }
}
```

3. Restart OpenCode

**Verification:** After restarting, run `leanproxy server list` to confirm the connection is working.

### Windsurf

1. Open `~/.windsurf/mcp.json` in your editor
2. Add LeanProxy-MCP to the `mcpServers` section:

```json
{
  "mcpServers": {
    "leanproxy": {
      "command": "/path/to/leanproxy",
      "args": ["serve"]
    }
  }
}
```

3. Restart Windsurf

**Verification:** After restarting, run `leanproxy server list` to confirm the connection is working.

### Migrating from Another MCP Tool

If you're migrating from another MCP tool (OpenCode, Claude Code, VS Code, or Cursor), use the built-in migration command:

```bash
leanproxy migrate
```

This will automatically detect your existing MCP configurations and import them into leanproxy's format. The resulting configuration is immediately usable by your IDE — no manual editing required.

### Verification

After configuring your IDE, verify the connection by checking that the leanproxy serve command is running without errors. Each IDE will show a connection indicator when the MCP server is active.

## Build from Source

### Prerequisites

- Go 1.21 or later
- Git
- (Optional) `golangci-lint` for linting

### Quick Build

```bash
git clone https://github.com/mmornati/leanproxy-mcp.git
cd leanproxy-mcp
make build
```

The binary will be placed in the `dist/` directory.

### Using Makefile

| Command | Description |
|---------|-------------|
| `make build` | Build all platform binaries to `dist/` |
| `make build-local` | Build for current platform only |
| `make test` | Run all tests |
| `make lint` | Run linter (requires golangci-lint) |
| `make clean` | Remove build artifacts |
| `make install` | Build and install to `$GOPATH/bin` |

### Build with Custom Version

```bash
make build VERSION=1.0.0
```

### Cross-Platform Builds

The Makefile builds for these platforms:
- macOS (amd64, arm64)
- Linux (amd64, arm64)
- Windows (amd64)

Binaries are output to `dist/` with naming scheme:
- `leanproxy-mcp-darwin-amd64`
- `leanproxy-mcp-darwin-arm64`
- `leanproxy-mcp-linux-amd64`
- `leanproxy-mcp-linux-arm64`
- `leanproxy-mcp-windows-amd64.exe`

## Development

### Running Tests

```bash
go test ./...
```

### Running the Application

```bash
go run main.go serve
```

## Project Structure

```
leanproxy-mcp/
├── cmd/                    # CLI entry points (cobra commands)
│   ├── root.go           # Root command configuration
│   ├── serve.go          # Server command
│   └── version.go        # Version command
├── pkg/
│   ├── bouncer/          # Redaction/security engine
│   ├── proxy/            # JSON-RPC 2.0 streaming handler
│   ├── registry/         # Manifest/tool management
│   └── utils/            # Shared helpers
├── main.go               # Application entry point
├── go.mod                # Go module definition
└── README.md
```

## License

[MIT License](./LICENCE.md)
