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

## Build from Source

### Prerequisites

- Go 1.21 or later
- Git

### Build

```bash
git clone https://github.com/mmornati/leanproxy-mcp.git
cd leanproxy-mcp
go build -o leanproxy-mcp ./main.go
```

### Run Tests

```bash
go test ./...
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
