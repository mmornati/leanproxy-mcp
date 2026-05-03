# Architecture

Understanding the LeanProxy-MCP architecture.

## Overview

LeanProxy-MCP is designed as a proxy layer between your IDE and MCP servers:

```
┌─────────────┐      ┌─────────────────┐      ┌──────────────┐
│   IDE      │──────│ LeanProxy-MCP  │──────│ MCP Server  │
│ (Client)  │      │ (Token Firewall) │      │            │
└─────────────┘      └─────────────────┘      └──────────────┘
                           │
                    ┌──────┴──────┐
                    │ Redaction   │
                    │ Engine     │
                    └────────────┘
```

## Core Components

### 1. Gateway

The entry point that handles IDE connections and routes requests.

**Responsibilities:**
- Accept stdio and HTTP connections
- Route requests to appropriate handlers
- Manage connection lifecycle

### 2. Registry

Tool registry that maintains MCP server signatures.

**Responsibilities:**
- JIT (Just-In-Time) tool discovery
- Cache tool signatures
- Manage tool lifecycle

### 3. Router

Routes tool calls to registered MCP servers.

**Responsibilities:**
- Match tools to servers
- Load balance across servers
- Handle connection pooling

### 4. Compactor

Token optimizationEngine that compresses prompts.

**Responsibilities:**
- Remove boilerplate
- Compact manifests
- Estimate token savings

### 5. Redaction Engine

The "Bouncer" that intercepts sensitive data.

**Responsibilities:**
- Pattern matching for secrets
- PII detection
- Configurable redaction rules

## Data Flow

```
1. IDE sends request
       │
       ▼
2. Gateway receives request
       │
       ▼
3. Router matches tool to server
       │
       ▼
4. Redaction Engine filters secrets
       │
       ▼
5. Compactor optimizes payload
       │
       ▼
6. Request forwarded to MCP server
       │
       ▼
7. Response filtered (secrets removed)
       │
       ▼
8. Response returned to IDE
```

## Key Concepts

### Shadow Manifesting

Automatically merges:
- Global config: `~/.config/mcp.json`
- Project config: `./.mcp.json`

Project config takes precedence over global.

### JIT Discovery

Tools are registered on-demand, not at startup. This minimizes initial context overhead.

### Token Firewall

Pre-configured redaction for:
- API keys and secrets
- Environment variables
- PII (emails, phone numbers)
- AWS credentials

## Directory Structure

```
leanproxy-mcp/
├── cmd/              # CLI entry points
├── pkg/
│   ├── gateway/     # HTTP/stdio gateway
│   ├── router/     # Tool routing
│   ├── registry/   # Tool registry
│   ├── pool/       # Connection pooling
│   ├── concurrent/ # Concurrency utilities
│   ├── compactor/  # Token optimization
│   └── utils/      # Utilities
├── docs/            # Documentation
└── install/        # Installation scripts
```

## Next Steps

- [Commands Reference](./commands.md) - Full command documentation
- [Configuration](./configuration.md) - Customize behavior