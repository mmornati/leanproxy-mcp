# Architecture

Understanding the LeanProxy-MCP architecture.

## Overview

LeanProxy-MCP is designed as a proxy layer between your IDE and MCP servers.

```mermaid
graph LR
    subgraph Client
        IDE[IDE<br/>Claude/Cursor<br/>OpenCode]
    end
    
    subgraph LeanProxy["LeanProxy-MCP"]
        Gateway[Gateway]
        Router[Router]
        Bouncer[Bouncer<br/>Redaction Engine]
        Compactor[Compactor]
    end
    
    subgraph Server["MCP Server"]
        MCP[MCP Server]
    end
    
    IDE --> Gateway
    Gateway --> Router
    Router --> Bouncer
    Bouncer --> Compactor
    Compactor --> MCP
    MCP -->|Response| Compactor
    Compactor -->|Filtered| Bouncer
    Bouncer -->|Clean| Router
    Router -->|Clean| Gateway
    Gateway -->|Response| IDE
```

## Core Components

```mermaid
graph TD
    subgraph Components
        GW[Gateway<br/>Entry point]
        RG[Registry<br/>Tool registry]
        RT[Router<br/>Tool routing]
        CP[Compactor<br/>Token optimization]
        BC[Bouncer<br/>Redaction engine]
        PL[Pool<br/>Connection pooling]
    end
    
    GW --> RT
    RT --> RG
    RT --> BC
    RT --> CP
    BC --> PL
    CP --> PL
```

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

Token optimization engine that compresses prompts.

**Responsibilities:**
- Remove boilerplate
- Compact manifests
- Estimate token savings

### 5. Redaction Engine (Bouncer)

The "Bouncer" that intercepts sensitive data.

**Responsibilities:**
- Pattern matching for secrets
- PII detection
- Configurable redaction rules

## Request/Response Flow

```mermaid
sequenceDiagram
    participant IDE as IDE
    participant GW as Gateway
    participant RT as Router
    participant BC as Bouncer
    participant CP as Compactor
    participant MCP as MCP Server

    Note over IDE,MCP: Request Flow
    
    IDE->>GW: 1. Send request
    GW->>RT: 2. Route request
    RT->>BC: 3. Check redaction
    BC->>BC: 4. Apply patterns
    BC-->>RT: 5. Filtered request
    RT->>CP: 6. Optimize payload
    CP-->>RT: 7. Compacted request
    RT->>MCP: 8. Forward to server

    Note over MCP,IDE: Response Flow
    
    MCP-->>CP: 9. Receive response
    CP->>BC: 10. Filter secrets
    BC-->>GW: 11. Clean response
    GW-->>IDE: 12. Return to IDE
```

## Data Processing Pipeline

```mermaid
flowchart TB
    subgraph Input["Input Processing"]
        A[Raw Request] --> B[Parse JSON-RPC]
        B --> C[Extract Tools]
    end
    
    subgraph Security["Security Layer"]
        C --> D[Check Patterns]
        D --> E{Match Found?}
        E -->|Yes| F[Redact Data]
        E -->|No| G[Pass Through]
        F --> H[Log Event]
    end
    
    subgraph Optimization["Optimization"]
        G --> I[Remove Boilerplate]
        F --> I
        I --> J[Calculate Savings]
    end
    
    subgraph Output["Output"]
        J --> K[Forward to MCP]
        K --> L[Process Response]
        L --> M[Return to IDE]
    end
    
    style Security fill:#ffcccc
    style Optimization fill:#ccffcc
    style Input fill:#ccccff
    style Output fill:#ffffcc
```

## Key Concepts

### Shadow Manifesting

```mermaid
graph LR
    subgraph Config["Configuration Merge"]
        Global["~/.config/mcp.json<br/>Global Config"]
        Project["./.mcp.json<br/>Project Config"]
        Merge[Merge Priority]
    end
    
    Global --> Merge
    Project -->|Higher Priority| Merge
    Merge --> Final["Final Config"]
```

Automatically merges:
- Global config: `~/.config/mcp.json`
- Project config: `./.mcp.json`

Project config takes precedence over global.

### JIT Discovery

```mermaid
flowchart LR
    subgraph Traditional["Traditional"]
        A1[Startup] --> B1[Load ALL Tools]
        B1 --> C1[Register with IDE]
        C1 --> D1[Idle during use]
    end
    
    subgraph JIT["JIT Discovery"]
        A2[Startup] --> B2[Load Signatures Only]
        B2 --> C2[Wait for Request]
        C2 --> D2[On First Use<br/>Load Tool]
        D2 --> E2[Cache & Register]
    end
    
    style Traditional fill:#ffcccc
    style JIT fill:#ccffcc
```

Tools are registered on-demand, not at startup. This minimizes initial context overhead.

### Token Firewall

Pre-configured redaction for:
- API keys and secrets
- Environment variables
- PII (emails, phone numbers)
- AWS credentials

```mermaid
flowchart TB
    subgraph Input
        R[Request with<br/>secrets]
    end
    
    subgraph Patterns["Built-in Patterns"]
        P1[aws-access-key]
        P2[github-pat]
        P3[stripe-key]
        P4[bearer-token]
        P5[env-var-value]
    end
    
    subgraph Actions
        R --> P1
        R --> P2
        R --> P3
        R --> P4
        R --> P5
        P1 --> M1[REDACTED]
        P2 --> M2[REDACTED]
        P3 --> M3[REDACTED]
        P4 --> M4[REDACTED]
        P5 --> M5[REDACTED]
    end
    
    subgraph Output
        M1 & M2 & M3 & M4 & M5 --> C[Clean Request]
    end
    
    style Patterns fill:#ffcccc
    style Actions fill:#ffffcc
    style Output fill:#ccffcc
```

## Directory Structure

```mermaid
graph TD
    root["leanproxy-mcp/"] --> cmd["cmd/"]
    root --> pkg["pkg/"]
    root --> docs["docs/"]
    root --> install["install/"]

    cmd --> cmd_root["root.go"]
    cmd --> cmd_serve["serve.go"]
    cmd --> cmd_server["server.go"]
    cmd --> cmd_status["status.go"]
    cmd --> cmd_cache["cache.go"]

    pkg --> gw["gateway/"]
    pkg --> rt["router/"]
    pkg --> rg["registry/"]
    pkg --> pl["pool/"]
    pkg --> cn["concurrent/"]
    pkg --> cp["compactor/"]
    pkg --> ut["utils/"]
    pkg --> bc["bouncer/"]
    pkg --> mc["mcp/"]
    pkg --> ts["toolstore/"]
    pkg --> sf["statusfile/"]

    mc --> mc_handlers["handlers.go"]
    mc --> mc_gateway["gateway_server.go"]
    mc --> mc_types["types.go"]

    ts --> ts_filecache["filecache.go"]

    sf --> sf_file["file.go"]

    docs --> docs_index["index.md"]
    docs --> docs_install["installation.md"]
    docs --> docs_commands["commands.md"]
    docs --> docs_config["configuration.md"]

    style root fill:#ccccff
    style pkg fill:#ccffcc
    style cmd fill:#ffcc99
    style docs fill:#ffcccc
```

```
leanproxy-mcp/
├── cmd/              # CLI entry points
│   ├── root.go      # Main command
│   ├── serve.go     # serve command (HTTP proxy)
│   ├── server.go    # server command (stdio mode)
│   ├── status.go    # status command
│   └── cache.go     # cache command
├── pkg/
│   ├── gateway/    # HTTP/stdio gateway
│   ├── router/     # Tool routing
│   ├── registry/   # Tool registry
│   ├── pool/       # Connection pooling
│   ├── concurrent/ # Concurrency utilities
│   ├── compactor/  # Token optimization
│   ├── bouncer/    # Redaction engine
│   ├── mcp/        # MCP protocol implementation
│   │   ├── handlers.go    # MCP request handlers
│   │   ├── gateway_server.go  # Gateway tool implementation
│   │   └── types.go     # MCP types
│   ├── toolstore/  # Persistent tool cache
│   │   └── filecache.go  # File-based cache
│   └── statusfile/ # Shared status file
│       └── file.go   # Status file implementation
├── docs/            # Documentation
└── install/        # Installation scripts
```

## Key Packages

### MCP Package (`pkg/mcp/`)

Implements the MCP protocol handling including:
- Request routing and handling
- Tool discovery and caching
- Gateway tools (search_tools method)
- Protocol type definitions

### Tool Store (`pkg/toolstore/`)

Persistent tool cache that stores tool signatures to disk:
- `FileCache`: Persists tools to `~/.config/leanproxy/toolcache/`
- Per-server cache files (e.g., `garmin.json`, `Intervals_icu.json`)
- Avoids starting servers for tool discovery

### Status File (`pkg/statusfile/`)

Shared status file for detecting running instances:
- `FileStatusStore`: Writes status to `~/.config/leanproxy/status/current.json`
- Updated every 5 seconds by running instances
- Used by `leanproxy status --running` to show active instances

## Next Steps

- [Commands Reference](./commands.md) - Full command documentation
- [Configuration](./configuration.md) - Customize behavior