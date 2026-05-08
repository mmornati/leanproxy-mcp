---
stepsCompleted: [1, 2, 3, 4, 5, 6, 7]
inputDocuments:
  - /Users/mmornati/Projects/leanproxy-mcp/_bmad-output/planning-artifacts/prd.md
  - /Users/mmornati/Projects/leanproxy-mcp/_bmad-output/planning-artifacts/product-brief-LeanProxy-MCP.md
  - /Users/mmornati/Projects/leanproxy-mcp/_bmad-output/planning-artifacts/epics.md
  - /Users/mmornati/Projects/leanproxy-mcp/_bmad-output/planning-artifacts/research/market-mcp-proxy-server-features-token-savings-latency-2026-research-2026-05-07.md
workflowType: 'architecture'
project_name: 'LeanProxy-MCP'
user_name: 'mmornati'
date: '2026-05-07'
notes: |
  Updated 2026-05-07 to include new Epics 8-9 from market research:
  - Epic 8: Token Optimization & Performance
  - Epic 9: Enterprise Transport & Architecture
  Focus: Lazy-loading, connection pooling, cost attribution, Streamable HTTP
---

# Architecture Decision Document

_This document builds collaboratively through step-by-step discovery. Sections are appended as we work through each architectural decision together._

## Project Context Analysis

### Requirements Overview

**Functional Requirements:**
- **Proxy Architecture:** Intercept and route JSON-RPC traffic between IDE and local MCP servers (`stdio` / HTTP).
- **Security ("The Bouncer"):** Real-time, in-memory redaction of secrets, PII, and sensitive data using regex/heuristics.
- **Optimization:** Implement JIT Tool Discovery (signatures), manifest merging, and automated boilerplate pruning to reduce token consumption.
- **CLI/Developer Experience:** POSIX-compliant CLI with management commands (`compactor`, `server`, `context`) and universal installer.
- **Reporting:** `dry-run` simulation mode generating Markdown reports on token savings and intercepted security risks.

**Non-Functional Requirements:**
- **Performance:** Sub-50ms processing overhead per request.
- **Security:** Local-only, in-memory processing (no persistent sensitive data).
- **Reliability:** Bit-perfect pass-through for non-intercepted messages (protocol fidelity).
- **Distribution:** <20MB standalone binary for universal portability.

**Scale & Complexity:**
- **Primary domain:** Developer Tools (CLI/Proxy)
- **Complexity level:** Medium
- **Estimated architectural components:** Core Proxy/Orchestrator, Redaction Engine (Bouncer), Manifest Merging (Shadow Manifesting), JIT Discovery Engine.

### Technical Constraints & Dependencies

- Strict adherence to MCP JSON-RPC 2.0 protocol.
- Must manage lifecycle of multiple sub-processes (local MCP servers).
- Must operate in-memory (no persistence of sensitive data).
- Go ecosystem for performance and binary distribution.

### Cross-Cutting Concerns Identified

- **Data Governance:** Centralized redaction logic that must apply across all tool calls and server outputs.
- **Performance Budget:** Keeping proxy overhead low while performing complex parsing and redaction.
- **State Management:** Merging and maintaining global/local manifest states without introducing friction.

## Starter Template Evaluation

### Primary Technology Domain

CLI Tool / Proxy (Full-stack Go backend)

### Selected Starter: Minimal Go CLI Structure

**Rationale for Selection:**
We have selected a minimal, standard Go project structure leveraging the standard library and the industry-standard `cobra` library for CLI command handling. This ensures maximum portability, performance, and maintainability while allowing the user to learn idiomatic Go.

**Initialization Command:**

```bash
mkdir leanproxy-mcp && cd leanproxy-mcp
go mod init github.com/mmornati/leanproxy-mcp
go get github.com/spf13/cobra@latest
```

**Architectural Decisions Provided:**

- **Language & Runtime:** Go (latest stable)
- **Build Tooling:** Standard `go build` for static binary output; GitHub Actions for multi-platform cross-compilation.
- **Testing Framework:** Go's built-in `testing` package.
- **Code Organization:** Idiomatic Go structure (`cmd/` for CLI, `pkg/` for internal proxy/redaction logic).
- **Development Experience:** Standard Go toolchain (go fmt, go vet).

## Core Architectural Decisions

### Decision Priority Analysis

**Critical Decisions (Block Implementation):**
- **JSON-RPC Handling:** Manual streaming implementation (No external library).
- **Redaction Strategy:** Streaming regex-based redaction engine ("The Bouncer").
- **Manifest Management:** Aggregated registry merge (Shadow Manifesting).

**Important Decisions (Shape Architecture):**
- **Project Structure:** Go idiomatic structure (`cmd/` + `pkg/`).
- **Binary Distribution:** Static compilation with GitHub Actions for cross-platform releases.

### API & Communication

- **Decision:** Manual, streaming JSON-RPC 2.0 implementation using `encoding/json` and `io` streams.
- **Rationale:** Prioritizing sub-50ms performance and maximum control over protocol fidelity/security interception.

### Data Architecture

- **Decision:** Streaming regex-based redaction ("The Bouncer").
- **Rationale:** Ensures <50ms processing overhead while protecting sensitive data in real-time.
- **Decision:** Shadow Manifesting (Deep merge of global/local configs, local priority).
- **Rationale:** Seamless developer experience for tool discovery.

### Infrastructure & Deployment

- **Decision:** GitHub Actions CI/CD for multi-platform binary generation.
- **Rationale:** Enables "zero-config" distribution and easy downloads for users.

## Epic 5: Reporting & Insights Architecture

### Decision: Token Savings Calculator (`pkg/reporter/`)

- **Algorithm:** Character-count heuristic (1 token ≈ 4 characters) for estimation without external dependencies.
- **Tracking:** Per-session cumulative savings stored in-memory; resets on proxy restart.
- **Breakdown:** Aggregate by MCP server to show which tools generate most savings.
- **Output:** Real-time logging to stderr via `slog.Info`; no stdout pollution.

### Decision: Markdown Report Generation

- **Format:** IDE-preview-optimized Markdown with tables and badges.
- **Sections:** Summary metrics → per-server breakdown → security events → session details.
- **Trigger:** On `leanproxy report` command or dry-run session end.
- **Output:** Writes to stdout for piping/redirection.

### Decision: Real-Time Health Monitor (`pkg/health/`)

- **Heartbeat:** 1-second polling interval for process health checks.
- **Metrics:** Status (running/error/stopped), uptime, last response time, memory usage, request count.
- **Watch Mode:** Streaming output with 1-second refresh; graceful exit on SIGINT.
- **Failure Detection:** Sub-1-second detection per NFR8; exponential backoff restart.

## Epic 6: Server Configuration & Migration Architecture

### Decision: Config Schema (`pkg/migrate/config.go`)

- **Primary Config:** `~/.config/leanproxy_servers.yaml` (YAML).
- **Discovery Locations:**
  - OpenCode: `~/.config/opencode/mcp.json`
  - Claude Code: `~/.claude.json`, `~/.config/claude/mcp_config.json`
  - VS Code: `settings.json` (MCP extensions section)
  - Cursor: `~/.cursor/mcp.json`
  - Generic: `~/.config/mcp.json`
- **Schema Validation:** Custom Go struct validation with descriptive error messages.
- **Conflict Resolution:** Local config wins for duplicates; imported servers get `_opencode`, `_claude` suffixes.

### Decision: Migration Engine (`pkg/migrate/`)

- **Phase 1 — Scan:** Detect all known config file locations; collect server definitions.
- **Phase 2 — Validate:** Check executables in PATH, validate transport types, confirm required fields.
- **Phase 3 — Import:** Merge into `leanproxy_servers.yaml` with duplicate handling.
- **Validation Output:** "Server 'name': command 'cmd' not found in PATH" style errors.

### Decision: IDE Socket API (`pkg/socket/`)

- **Path Convention:**
  - macOS: `~/.leanproxy/socket`
  - Linux: `~/.leanproxy/socket`
  - Windows: `\\.\pipe\leanproxy`
- **Protocol:** JSON-RPC 2.0 over raw socket (same as stdio transport).
- **Auth:** Local-only socket with filesystem permissions (no auth token needed).
- **Ephemeral Updates:** In-memory config changes via socket; original file untouched.

## Story 4.3-4.5: Developer Experience Extensions

### Decision: Universal Installer

- **Primary:** `curl -fsSL https://get.leanproxy.dev | sh` pointing to GitHub Releases.
- **Verification:** SHA-256 checksum verification post-download.
- **Homebrew:** Official tap at `leanproxy/tap/leanproxy` for macOS/Linux.
- **Cross-platform:** GitHub Actions matrix for x64/ARM64 across darwin/linux/windows.

### Decision: Shell Completion

- **Implementation:** Cobra built-in completion generation for bash/zsh/fish/powershell.
- **Commands:** `leanproxy completion [bash|zsh|fish|powershell]`.
- **Auto-install:** Homebrew installation handles shell config automatically.

## Implementation Patterns & Consistency Rules

### Naming Patterns
- **Symbols:** `camelCase` for Go functions, variables, and methods.
- **CLI Flags:** `kebab-case` (e.g., `--dry-run`).

### Structure Patterns
- `cmd/`: CLI entry points.
- `pkg/proxy/`: JSON-RPC stream management.
- `pkg/bouncer/`: Redaction/Security logic.
- `pkg/registry/`: Manifest/Tool management.

### Communication Patterns
- **Error Handling:** Use `fmt.Errorf("context: %w", err)` for wrapping.
- **Logging:** Use `log/slog` for structured, leveled output to `stderr`.

## Project Structure & Boundaries

### Complete Project Directory Structure
```
leanproxy-mcp/
├── .github/
│   └── workflows/
│       └── release.yml     # CI/CD: Cross-compilation + Binary upload
├── cmd/
│   └── leanproxy/
│       └── main.go         # CLI Entry point (cobra setup)
├── pkg/
│   ├── bouncer/            # Redaction/Security logic (Regex engine)
│   ├── proxy/              # JSON-RPC 2.0 streaming handler
│   ├── registry/           # Shadow Manifesting (config merging)
│   ├── reporter/          # Token savings calculator, Markdown reports
│   ├── health/             # Real-time server health monitoring
│   ├── migrate/            # Auto-detection and config migration
│   ├── socket/             # IDE extension Unix/Windows socket API
│   └── utils/              # Shared helper functions
├── internal/               # Non-exported logic (e.g., dry-run simulator)
├── tests/
│   ├── integration/
│   └── unit/
├── go.mod                  # Module definition
└── README.md
```

### Architectural Boundaries
- **`cmd/`**: CLI orchestration and setup.
- **`pkg/proxy/`**: High-performance JSON-RPC protocol handling.
- **`pkg/bouncer/`**: Interceptor for data governance.
- **`pkg/registry/`**: Configuration management.
- **`pkg/reporter/`**: Token savings tracking and report generation.
- **`pkg/health/`**: Server process health monitoring.
- **`pkg/migrate/`**: IDE config detection and migration.
- **`pkg/socket/`**: IDE extension socket API.

## Architecture Validation Results

### Coherence Validation ✅
- **Decision Compatibility:** Verified. All technology choices (Go, cobra, manual stream parsing) are aligned.
- **Pattern Consistency:** Verified. Structure, naming, and communication patterns align with Go idioms.
- **Structure Alignment:** Verified. The proposed directory structure fully supports the architecture.

### Requirements Coverage Validation ✅
- **Epic/Feature Coverage:** All 6 epics and 28 FRs supported.
- **Epic 1-4:** Fully covered in original architecture.
- **Epic 5 (Reporting):** `pkg/reporter/` and `pkg/health/` decisions added.
- **Epic 6 (Migration):** `pkg/migrate/` and config schema decisions added.
- **Functional Requirements:** All 28 FRs supported (FR1-FR28).
- **Non-Functional Requirements:** Performance (<50ms), security (in-memory), and distribution (<20MB) are prioritized.

### Implementation Readiness Validation ✅
- **Decision Completeness:** All critical decisions documented.
- **Structure Completeness:** Complete project tree defined.
- **Pattern Completeness:** All naming/structure/communication patterns defined.

### Architecture Readiness Assessment
- **Overall Status:** READY FOR IMPLEMENTATION
- **Confidence Level:** High

---

# ARCHITECTURE UPDATE: May 2026 (Epics 8-9)

Added based on market research findings for token optimization and enterprise features.

## Epic 8: Token Optimization & Performance Architecture

### Decision: Lazy-Loading Tool Schemas (`pkg/registry/lazy.go`)

Architecture pattern adapted from `mcp-lazy-proxy` npm package (6-7x token reduction):

- **Cache Structure:** In-memory map `toolName → cachedFullSchema`
- **Stub Schema:** ~54 tokens per tool (name + 1-line description)
- **Loading Trigger:** On first `get_tool_schema` request per tool
- **Cache Invalidation:** Configurable TTL (default: 24h) or manual ` leanproxy schema refresh`

**Trade-off Decision:** 
> Memory vs Tokens: Cache grows with tools invoked. For 100+ servers, ~50MB RAM for full schemas is acceptable trade-off for 6-7x token savings.

**Implementation Path:**
1. Intercept `tools/list` → return stubs only
2. Intercept `get_tool_schema(tool_name)` → load + cache + return
3. Background: Pre-warm cache for frequently-used tools

### Decision: Connection Pooling (`pkg/proxy/pool.go`)

Based on `maxim-ai/bifrost` architecture patterns (11µs overhead):

- **Pool Strategy:** Reuse `*Client` across requests, not create per-call
- **Pool Size:** Configurable (default: 5 connections per server)
- **Keepalive:** HTTP keepalive + periodic ping to detect dead connections
- **Queueing:** Requests queue when pool exhausted (FIFO)

**Trade-off Decision:**
> Complexity vs Latency: Adding pooling adds code complexity but fixes 187x latency issue. Worth it for production use.

**Implementation Path:**
1. Create `ConnectionPool` struct with `sync.Pool`
2. Wrap existing MCP client with pooling layer
3. Add metrics: pool hit rate, wait time

### Decision: Session State Reuse (`pkg/proxy/session.go`)

Prevents repeated MCP initialize handshakes:

- **Session Serialization:** JSON-serializable session state
- **Restore:** Re-establish without full handshake
- **Multiple Clients:** Share sessions where safe

**Implementation Path:**
1. Capture init response state
2. Serialize (exclude volatile state)
3. On reconnect: restore state + verify

### Decision: Cost Attribution (`pkg/reporter/cost.go`)

Per-tool, per-server token tracking:

- **Tracking:** Increment counters per tool call
- **Aggregation:** Sum by tool, by server, total
- **Output:** `leanproxy cost` command + socket API

**Implementation Path:**
1. Add counters to proxy context
2. Increment on each tool call completion
3. Aggregate in reporter

---

## Epic 9: Enterprise Transport & Architecture

### Decision: Streamable HTTP Transport (`pkg/proxy/http.go`)

Based on MCP 2026 spec recommendation to replace SSE:

- **Endpoint:** Single `/mcp` HTTP endpoint
- **Protocol:** Streamable HTTP (not SSE)
- **Headers:** `Content-Type: application/json`, `Transfer-Encoding: chunked`
- **Fallthrough:** For specs that still require SSE, add `/sse` endpoint

**Trade-off Decision:**
> Compatibility vs Simplicity: Support both transports. SSE is deprecated but still in use. Don't break existing clients.

**Implementation Path:**
1. Add HTTP listener on configurable port (default: 8080)
2. Implement Streamable HTTP handler
3. Add SSE handler for backward compat
4. Config: `transports: [stdio, http, sse]`

### Decision: Hierarchical Namespaces (`pkg/registry/namespace.go`)

Multi-team access control:

- **Config Schema:** Nested YAML structure with `namespaces:`
- **Hierarchy:** Parent includes child tools
- **Access Control:** Per-namespace client allowances

**Implementation Path:**
1. Define `Namespace` struct
2. Parse nested config
3. Filter tools by namespace in registry

### Decision: Simple Federation (`pkg/federation/`)

Cross-instance tool discovery:

- **Discovery:** mDNS or configured peer URLs
- **Routing:** Route to peer with matching tool
- **Fallback:** On peer failure, try next peer

**Implementation Path:**
1. Federation config section
2. Peer connection manager
3. Cross-instance tool lookup
4. Error handling with fallback

---

## Updated Project Structure

```
leanproxy-mcp/
├── .github/
│   └── workflows/
│       └── release.yml
├── cmd/
│   └── leanproxy/
│       └── main.go
├── pkg/
│   ├── bouncer/            # Redaction/Security
│   ├── proxy/              # JSON-RPC core
│   │   ├── pool.go        # NEW: Connection pooling
│   │   └── session.go    # NEW: Session reuse
│   ├── registry/           # Config/Tools
│   │   ├── lazy.go       # NEW: Lazy-loading
│   │   ├── namespace.go # NEW: Hierarchical namespaces
│   ├── reporter/          # Token/Security reports
│   │   └── cost.go      # NEW: Cost attribution
│   ├── health/             # Server health
│   ├── migrate/            # IDE config migration
│   ├── socket/             # IDE extension socket
│   ├── http.go           # NEW: Streamable HTTP
│   ├── federation/       # NEW: Multi-instance
│   └── utils/
├── internal/
├── tests/
├── go.mod
└── README.md
```

---

## Updated Requirements Coverage

| Epic | Stories | Architecture Package | Status |
|------|---------|---------------------|--------|
| Epic 8.1 | Lazy-loading | `pkg/registry/lazy.go` | NEW |
| Epic 8.2 | Connection pooling | `pkg/proxy/pool.go` | NEW |
| Epic 8.3 | Session reuse | `pkg/proxy/session.go` | NEW |
| Epic 8.4 | Cost attribution | `pkg/reporter/cost.go` | NEW |
| Epic 9.1 | Streamable HTTP | `pkg/proxy/http.go` | NEW |
| Epic 9.2 | Namespaces | `pkg/registry/namespace.go` | NEW |
| Epic 9.3 | Federation | `pkg/federation/` | NEW |

---

## Updated Architecture Validation

### Coherence ✅
- All existing patterns preserved
- NEW: Lazy-loading integrates with registry
- NEW: Pooling wraps proxy client
- NEW: HTTP adds to transport layer

### Requirements Coverage ✅
- **Epic 8:** 4 stories → 4 packages
- **Epic 9:** 3 stories → 3 packages  
- **Total:** 7 new stories → 7 new/updated packages

### Implementation Readiness ✅
- **Epic 8:** CRITICAL priority - start here
- **Epic 9:** HIGH priority - after Epic 8

---

## Winston's Assessment

| Aspect | Rating | Notes |
|--------|--------|-------|
| Token Optimization | ✅ Ready | Lazy-loading proven (6-7x reduction) |
| Latency Fix | ✅ Ready | Connection pooling fixes 187x |
| Enterprise | 🟡 Planned | Streamable HTTP needed for corp |
| Multi-org | 🟡 Planned | Federation is future work |

### Trade-offs Made

1. **Memory vs Tokens:** Accept 50MB RAM cache for 6-7x token savings
2. **Complexity vs Latency:** Pooling adds code but fixes critical latency
3. **Compatibility vs Simplicity:** Support both SSE + Streamable HTTP for now

### Recommended Implementation Order

1. **Sprint 1:** Epic 8.1 (Lazy-loading) + 8.2 (Pooling)
2. **Sprint 2:** Epic 8.3 (Session reuse) + 8.4 (Cost)
3. **Sprint 3:** Epic 9.1 (Streamable HTTP)
4. **Future:** Epic 9.2 + 9.3 (Enterprise features)

---

**Overall Status:** Architecture UPDATED - Ready to implement Epics 8-9

**Confidence Level:** High - Based on proven patterns from market research
