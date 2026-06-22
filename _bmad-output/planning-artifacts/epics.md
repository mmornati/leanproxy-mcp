---
stepsCompleted: [step-01-validate-prerequisites, step-02-design-epics, step-03-create-stories, step-04-final-validation]
inputDocuments:
  - _bmad-output/planning-artifacts/prd.md
  - _bmad-output/planning-artifacts/architecture.md
  - code-analysis-2026-05-02.md
  - _bmad-output/planning-artifacts/research/market-mcp-proxy-server-features-token-savings-latency-2026-research-2026-05-07.md
  - _bmad-output/brainstorming/brainstorming-session-2026-05-01.md
notes: |
  2026-05-02: Added Epic 7: Multi-Server Gateway Mode based on code analysis finding
  that FR5 (multi-server routing) was not implemented. Current proxy only supports
  single upstream server. New epic enables LeanProxy-MCP to act as gateway for 100+ MCP servers.
  
  2026-05-07: Added EPICs 8 and 9 from market research findings:
  - Epic 8: Token Optimization & Performance (lazy-loading, connection pooling, cost attribution)
  - Epic 9: Enterprise Transport & Architecture (Streamable HTTP, namespaces, federation)
  
  2026-06-22: Added Epics 10-18 from brainstorming session (Top 10 of 102 market-trend ideas):
  - Epic 10: Anthropic Prompt Caching Bridge
  - Epic 11: MCP Registry Mirror & Discovery
  - Epic 12: Semantic Prompt Cache
  - Epic 13: AI Safety — Prompt-Injection Firewall v2
  - Epic 14: IDE Plugin (VS Code / JetBrains) + Live Cost Sidebar
  - Epic 15: Per-Tool Model Router & Local LLM Sidecar
  - Epic 16: First-Party MCP Servers (GitHub / FS / DB)
  - Epic 17: Token Budget Governor
  - Epic 18: Cost Attribution Web Dashboard
  
  18 new functional requirements (FR40-FR49) and 3 new non-functional requirements (NFR11-NFR13) added.
  Total document: 9 original epics + 9 new epics = 18 epics, ~50 stories.
  
  Stories created (implementation-artifacts):
  - 8-1-lazy-loading-tool-schemas.md (CRITICAL)
  - 8-2-connection-pooling.md (CRITICAL)
  - 8-3-minimal-session-reinit.md (HIGH)
  - 8-4-cost-attribution.md (HIGH)
  - 9-1-streamable-http-transport.md (HIGH)
  - 9-2-hierarchical-namespaces.md (MEDIUM)
  - 9-3-simple-federation.md (MEDIUM)
  
  Pending implementation-artifact files (for new epics 10-18):
  - 10-1-detect-anthropic-calls.md
  - 10-2-inject-cache-breakpoints.md
  - 10-3-cache-hit-rate-report.md
  - 11-1-registry-sync.md
  - 11-2-one-click-install.md
  - 11-3-trust-scoring.md
  - 12-1-embed-payloads.md
  - 12-2-vector-store-pluggable.md
  - 12-3-cache-ttl-dashboard.md
  - 13-1-injection-classifier.md
  - 13-2-configurable-actions.md
  - 13-3-red-team-corpus.md
  - 14-1-metrics-endpoint.md
  - 14-2-vscode-extension.md
  - 14-3-jetbrains-plugin.md
  - 15-1-per-tool-model-routing.md
  - 15-2-ollama-sidecar.md
  - 15-3-mlx-apple-silicon.md
  - 16-1-first-party-github.md
  - 16-2-first-party-filesystem.md
  - 16-3-first-party-db-servers.md
  - 17-1-budget-config.md
  - 17-2-auto-throttle-downgrade.md
  - 18-1-web-dashboard.md
  - 18-2-drill-down.md
  - 18-3-csv-json-export.md
---
  
  Market Research Key Findings:
  - 72% context consumed by tool schemas (token bloat)
  - 187x latency in naive proxy implementations (15s vs 80ms)
  - Open design space: no tool fully satisfies hierarchy + federation + lightweight
  - 2026 emerging trends: MCP Registry, prompt caching, semantic caching, AI safety guardrails
---

# LeanProxy-MCP - Epic Breakdown

## Overview

This document provides the complete epic and story breakdown for LeanProxy-MCP, decomposing the requirements from the PRD, UX Design if it exists, and Architecture requirements into implementable stories.

## Requirements Inventory

### Functional Requirements

FR1: The system can intercept and route JSON-RPC traffic between an IDE and multiple local `stdio` MCP servers.
FR2: The system can manage the lifecycle (start/stop/restart) of configured MCP sub-processes.
FR3: The system can merge global and project-specific MCP manifests into a single runtime registry.
FR4: Users can dynamically add or remove MCP servers from the active proxy registry via CLI commands.
FR5: The system can route specific tool calls to the correct underlying MCP server based on the merged registry.
FR6: The system can register tools with the Model Provider using "Discovery Signatures" (minimal name/description).
FR7: The system can intercept `get_tool_schema` requests and inject full JSON schemas only for requested tools (JIT Discovery).
FR8: The system can "compact" raw third-party MCP manifests into token-dense signatures using a distillation workflow.
FR9: Users can force a re-distillation of any MCP server manifest to refresh stale discovery signatures.
FR10: The system can prune redundant imports and copyright boilerplate from file-read results (Boilerplate Blindness).
FR11: The system can scan outgoing JSON-RPC messages for sensitive data patterns (API keys, secrets, PII).
FR12: The system can redact identified sensitive data with a standardized placeholder (`[SECRET_REDACTED]`).
FR13: Users can define custom redaction patterns using regex in their local project configuration.
FR14: The system can operate entirely in-memory to prevent local persistence of sensitive intercepted data.
FR15: The system can alert the user via an out-of-band channel (stderr) when a redaction event occurs.
FR16: Users can run the proxy in a non-destructive `dry-run` mode to simulate savings and security alerts.
FR17: Users can interact with the proxy via a standard POSIX-compliant CLI (Go binary).
FR18: The system provides a local Unix/Windows socket for high-fidelity communication with IDE extensions.
FR19: Users can install the system via a universal shell script or platform-specific package managers (e.g., Homebrew).
FR20: The system provides automated shell completion for all management subcommands.
FR21: The system can calculate and report real-time token savings per session.
FR22: The system can generate Markdown-formatted reports summarizing "Total Tokens Saved" and "Security Risks Intercepted."
FR23: The system can provide real-time status of all active proxied servers and their health.
FR24: Users can define MCP server entries in `leanproxy_servers.yaml` with transport type (stdio/http/sse), command/args, environment variables, and timeouts.
FR25: Users can add, remove, list, enable, and disable MCP servers via CLI commands (`leanproxy server add`, `leanproxy server remove`, `leanproxy server list`, `leanproxy server enable`, `leanproxy server disable`).
FR26: The system can auto-detect existing MCP configurations from OpenCode, Claude Code, VS Code, Cursor, and generic `mcp.json` locations.
FR27: Users can run `leanproxy migrate` to auto-detect and import all found MCP configurations into `leanproxy_servers.yaml`, presenting a summary of imported servers.
FR28: The system validates imported server configurations and reports any errors (missing commands, invalid transport types) during migration.
FR29: The system can expose gateway tools (`list_servers`, `invoke_tool`, `search_tools`) to enable AI-driven server discovery.
FR30: The system can route tool calls to the correct MCP server based on method name lookup in the merged registry.
FR31: The system can handle concurrent requests across 100+ MCP servers without request mixing.
FR32: The system can pool and reuse stdio connections to multiple MCP servers efficiently.

### NonFunctional Requirements

NFR1: The system shall add an average processing overhead of **<50ms** per JSON-RPC request. (Performance/Latency)
NFR2: The system shall handle JSON payloads up to **50MB** (common in large file reads) without crashing or exceeding 200ms of latency. (Performance/Throughput)
NFR3: The standalone binary shall remain **<20MB** in size to ensure fast distribution and minimal memory usage. (Performance/Resource Footprint)
NFR4: The system shall execute all redaction and optimization logic **locally in-memory**. No unredacted user data shall ever be persisted to disk or sent to LeanProxy-MCP's own servers. (Security/Local-Only Processing)
NFR5: The "Bouncer" shall use an **allow-list approach** for its core redaction patterns to minimize false negatives and ensure 100% interception of standard secret formats. (Security/Redaction Integrity)
NFR6: The proxy shall run each MCP server in its own sub-process to prevent cross-server data leakage or state interference. (Security/Process Isolation)
NFR7: The system shall ensure **bit-perfect pass-through** for all non-intercepted JSON-RPC messages, ensuring zero breakage of the standard MCP protocol. (Reliability/Protocol Fidelity)
NFR8: The proxy shall detect and report the failure of any underlying MCP process within **1 second** and provide a graceful recovery path for the IDE session. (Reliability/Process Health)
NFR9: The system shall output real-time health and savings metrics (tokens saved, secrets redacted) to **stderr** to avoid polluting the primary protocol stream. (Observability/Operational Transparency)
NFR10: Users can enable a local, rotated JSON log file to audit redaction events for enterprise compliance. (Observability/Audit Logging)

### Additional Requirements

- **Starter Template**: Minimal Go CLI structure with `cobra` for CLI command handling. Initialize with `go mod init github.com/mmornati/leanproxy-mcp` and `go get github.com/spf13/cobra@latest`.
- **Project Structure**: Go idiomatic structure with `cmd/` for CLI entry points, `pkg/` for internal proxy/redaction logic (bouncer, proxy, registry, utils).
- **JSON-RPC Handling**: Manual streaming implementation using `encoding/json` and `io` streams (no external library) for sub-50ms performance.
- **Redaction Strategy**: Streaming regex-based redaction engine for "The Bouncer".
- **Manifest Management**: Aggregated registry merge for "Shadow Manifesting" with local priority.
- **Naming Patterns**: `camelCase` for Go functions/variables/methods, `kebab-case` for CLI flags (e.g., `--dry-run`).
- **Error Handling**: Use `fmt.Errorf("context: %w", err)` for wrapping.
- **Logging**: Use `log/slog` for structured, leveled output to `stderr`.
- **Binary Distribution**: Static compilation with GitHub Actions for cross-platform releases.

### UX Design Requirements

N/A - This is a CLI-only project with no user interface.

### FR Coverage Map

FR1: Epic 1 - Intercept and route JSON-RPC traffic between IDE and MCP servers
FR2: Epic 1 - Manage lifecycle (start/stop/restart) of MCP sub-processes
FR3: Epic 1 - Merge global and project-specific MCP manifests
FR4: Epic 1 - Dynamically add/remove MCP servers via CLI
FR5: Epic 7 - Route tool calls to correct MCP server based on merged registry (explicitly scoped)
FR6: Epic 3 - Register tools using Discovery Signatures (minimal name/description)
FR7: Epic 3 - JIT Discovery: inject full schemas only for requested tools
FR8: Epic 3 - Compact raw manifests into token-dense signatures
FR9: Epic 3 - Force re-distillation of MCP server manifests
FR10: Epic 3 - Prune redundant imports and boilerplate from file reads
FR11: Epic 2 - Scan for sensitive data patterns (API keys, secrets, PII)
FR12: Epic 2 - Redact sensitive data with [SECRET_REDACTED] placeholder
FR13: Epic 2 - Define custom redaction patterns via regex in local config
FR14: Epic 2 - Operate entirely in-memory (no persistence of sensitive data)
FR15: Epic 2 - Alert user via stderr when redaction occurs
FR16: Epic 4 - Run proxy in dry-run mode to simulate savings/alerts
FR17: Epic 4 - POSIX-compliant CLI for proxy interaction
FR18: Epic 4 - Local Unix/Windows socket for IDE extension communication
FR19: Epic 4 - Install via universal shell script or Homebrew
FR20: Epic 4 - Automated shell completion for all subcommands
FR21: Epic 5 - Calculate and report real-time token savings per session
FR22: Epic 5 - Generate Markdown reports on tokens saved and risks intercepted
FR23: Epic 5 - Provide real-time status of all active proxied servers
FR24: Epic 6 - Define server entries with transport type (stdio/http/sse), command/args, env, timeouts
FR25: Epic 6 - Add, remove, list, enable, and disable servers via CLI commands
FR26: Epic 6 - Auto-detect MCP configs from OpenCode, Claude Code, VS Code, Cursor, generic mcp.json
FR27: Epic 6 - Migrate all found MCP configs with summary
FR28: Epic 6 - Validate imported server configs and report errors
FR29: Epic 7 - Expose gateway tools (list_servers, invoke_tool, search_tools)
FR30: Epic 7 - Route tool calls to correct MCP server based on registry
FR31: Epic 7 - Handle concurrent requests across 100+ servers
FR32: Epic 7 - Pool and reuse stdio connections

## Epic List

### Epic 1: Core Proxy Infrastructure
Users can intercept, route, and manage JSON-RPC traffic between IDE and MCP servers.
**FRs covered:** FR1, FR2, FR3, FR4, FR5

### Epic 2: Security & Data Governance (The Bouncer)
Users are protected from secret leaks and PII exposure with real-time, in-memory redaction.
**FRs covered:** FR11, FR12, FR13, FR14, FR15

### Epic 3: Context Optimization (JIT Discovery & Compactor)
Users experience 50-80% token reduction through intelligent discovery and boilerplate pruning.
**FRs covered:** FR6, FR7, FR8, FR9, FR10

### Epic 4: Developer Experience & CLI Interface
Users can install, configure, and interact with the proxy via a polished POSIX CLI.
**FRs covered:** FR16, FR17, FR18, FR19, FR20

### Epic 5: Reporting & Insights
Users can see real-time metrics on token savings and security events.
**FRs covered:** FR21, FR22, FR23

### Epic 6: Server Configuration & Migration
Users can define server entries with rich configuration and migrate from existing MCP tools.
**FRs covered:** FR24, FR25, FR26, FR27, FR28

### Epic 7: Multi-Server Gateway Mode
LeanProxy-MCP acts as a unified gateway that proxies requests to 100+ MCP servers, routing tool calls to the correct server based on tool name registry.
**FRs covered:** FR5 (explicitly scoped)

## Epic 1: Core Proxy Infrastructure

Core Proxy Infrastructure goal: Users can intercept, route, and manage JSON-RPC traffic between IDE and MCP servers.

### Story 1.1: Initialize Go Project with CLI Structure

**As a** developer,
**I want to** initialize the project with a proper Go CLI structure using cobra,
**So that** I can build a POSIX-compliant CLI tool with proper command organization.

**Acceptance Criteria:**

**Given** a fresh development environment with Go 1.21+ installed
**When** I run the initialization commands
**Then** a new `leanproxy-mcp` directory is created with `go.mod` initialized
**And** the cobra CLI library is properly imported
**And** the project follows idiomatic Go structure (`cmd/`, `pkg/`)

**Given** the project structure is initialized
**When** I run `go build ./cmd/leanproxy`
**Then** the binary compiles without errors
**And** running `./leanproxy --help` displays the help message
**And** the binary size is under 20MB (NFR3)

### Story 1.2: Implement JSON-RPC Streaming Proxy Core

**As a** developer,
**I want to** implement a streaming JSON-RPC 2.0 proxy that can intercept and forward messages,
**So that** the proxy can sit between the IDE and MCP servers while adding minimal latency (<50ms).

**Acceptance Criteria:**

**Given** a running leanproxy instance with stdio transport configured
**When** the IDE sends a valid JSON-RPC request through stdio
**Then** the proxy captures the request without blocking
**And** forwards it to the appropriate MCP server based on tool name
**And** returns the server's response back to the IDE
**And** the round-trip latency is under 50ms (NFR1)

**Given** a JSON-RPC request with a batch of calls
**When** the proxy receives the batch
**Then** it processes each call sequentially maintaining order
**And** returns a batch response matching the request structure

**Given** a malformed JSON-RPC message
**When** the proxy receives it
**Then** it returns a valid JSON-RPC error response
**And** it does not crash or hang
**And** it logs the error to stderr using slog

### Story 1.3: Implement MCP Server Lifecycle Management

**As a** developer,
**I want to** implement server process management (start/stop/restart),
**So that** each MCP server runs in its own isolated subprocess with process health monitoring.

**Acceptance Criteria:**

**Given** a configured MCP server definition with command and args
**When** the proxy starts
**Then** it spawns the server process with proper stdin/stdout streams
**And** each server runs in its own subprocess (NFR6)
**And** the process is monitored for health

**Given** a running MCP server process
**When** the process terminates unexpectedly
**Then** the proxy detects the failure within 1 second (NFR8)
**And** logs the failure to stderr
**And** attempts to restart the server with exponential backoff
**And** reports the failure status via the health endpoint

**Given** a running MCP server process
**When** the user issues a stop command via CLI
**Then** the proxy sends the appropriate shutdown signal
**And** waits up to 5 seconds for graceful shutdown
**And** forcefully kills the process if it doesn't stop
**And** confirms the stop to the user

### Story 1.4: Implement Shadow Manifesting (Config Merging)

**As a** developer,
**I want to** merge global and project-specific MCP configurations automatically,
**So that** users get seamless tool discovery without manual configuration.

**Acceptance Criteria:**

**Given** a global config at `~/.config/mcp.json` with server definitions
**And** a local project config at `./mcp.json` or `./leanproxy.yaml`
**When** the proxy starts
**Then** it reads both configuration files
**And** merges them into a single runtime registry
**And** local config takes priority over global config for conflicts

**Given** conflicting server definitions (same name in both configs)
**When** the merge occurs
**Then** the local config definition is used
**And** a warning is logged to stderr noting the override

**Given** only a global config exists
**When** the proxy starts
**Then** it uses the global config exclusively
**And** functions normally without requiring a local config

**Given** neither global nor local config exists
**When** the proxy starts
**Then** it starts in passthrough mode
**And** logs a warning that no servers are configured

### Story 1.5: Implement Dynamic Server Registry

**As a** developer,
**I want to** dynamically add, remove, list, enable, and disable MCP servers via CLI commands,
**So that** users can manage their server registry without editing config files.

**Acceptance Criteria:**

**Given** a running leanproxy instance
**When** the user runs `leanproxy server add <name> <command> [args...]`
**Then** the server is added to the active registry
**And** the server process is started immediately
**And** the change persists to the local config file

**Given** a running leanproxy instance
**When** the user runs `leanproxy server remove <name>`
**Then** the server process is stopped gracefully
**And** the server is removed from the active registry
**And** the local config is updated to remove the server

**Given** a running leanproxy instance
**When** the user runs `leanproxy server list`
**Then** a table of all configured servers is displayed
**And** each row shows: name, status (enabled/disabled), command
**And** the output is formatted as markdown for IDE display

**Given** a running leanproxy instance
**When** the user runs `leanproxy server enable <name>`
**Then** the server is marked as enabled in the config
**And** the server becomes available for routing requests

**Given** a running leanproxy instance
**When** the user runs `leanproxy server disable <name>`
**Then** the server is marked as disabled in the config
**And** the server is excluded from routing until re-enabled

**Given** an invalid server name or command
**When** the user attempts to add it
**Then** an error message explains the issue
**And** the command returns exit code 1

## Epic 2: Security & Data Governance (The Bouncer)

Security & Data Governance goal: Users are protected from secret leaks and PII exposure with real-time, in-memory redaction.

### Story 2.1: Implement Core Redaction Engine

**As a** developer,
**I want to** implement a streaming regex-based redaction engine,
**So that** sensitive data is intercepted and redacted in real-time before leaving the machine.

**Acceptance Criteria:**

**Given** outgoing JSON-RPC traffic containing sensitive patterns
**When** the traffic passes through the Bouncer
**Then** API keys matching known patterns are replaced with `[SECRET_REDACTED]`
**And** environment variable values are replaced with `[SECRET_REDACTED]`
**And** the redaction happens inline without buffering entire messages
**And** the processing adds less than 50ms overhead (NFR1)

**Given** a JSON-RPC message with multiple secrets
**When** the Bouncer processes it
**Then** all matching secrets are redacted
**And** the message structure remains valid JSON
**And** the redacted message length is approximately the same as the original

**Given** a message with no secrets
**When** the Bouncer processes it
**Then** the message passes through unchanged
**And** no false positives are introduced

### Story 2.2: Implement Allow-List Redaction Patterns

**As a** developer,
**I want to** implement an allow-list approach for core redaction patterns,
**So that** we minimize false negatives while ensuring high confidence redaction.

**Acceptance Criteria:**

**Given** standard secret formats (AWS keys, GitHub tokens, Stripe keys, .env values)
**When** they appear in JSON-RPC traffic
**Then** they are detected and redacted with 100% accuracy (NFR5)
**And** the allow-list is documented and extensible

**Given** a false negative (secret not caught)
**When** the user reports it
**Then** the pattern can be added to the allow-list
**And** a new release includes the updated pattern

**Given** an unknown pattern that looks like a secret
**When** it doesn't match any allow-list pattern
**Then** it is NOT redacted (no false positives)

### Story 2.3: Implement Custom Redaction Patterns

**As a** user,
**I want to** define custom regex patterns for redaction in my local config,
**So that** I can redact project-specific sensitive data beyond the built-in patterns.

**Acceptance Criteria:**

**Given** a local `leanproxy.yaml` with custom redaction patterns
**When** the proxy starts
**Then** it loads the custom patterns from the config
**And** applies them in addition to built-in patterns

**Given** a custom pattern `my-company-key-[A-Z0-9]{20}`
**When** a message containing `my-company-key-ABC123XYZ789012345678` is processed
**Then** the key is redacted to `[SECRET_REDACTED]`
**And** the user is notified via stderr

**Given** an invalid regex pattern in the config
**When** the proxy starts
**Then** it logs a warning about the invalid pattern
**And** continues startup with only valid patterns

### Story 2.4: Implement In-Memory Only Processing

**As a** developer,
**I want to** ensure all redaction and optimization happens in-memory only,
**So that** no sensitive data is ever written to disk (NFR4).

**Acceptance Criteria:**

**Given** intercepted JSON-RPC traffic with secrets
**When** the Bouncer processes it
**Then** no unredacted data is written to disk
**And** no network requests are made to external services
**And** all processing happens in memory

**Given** audit logging is disabled (default)
**When** redaction events occur
**Then** only the fact that redaction occurred is logged (not the content)
**And** no sensitive data appears in any log file

**Given** the proxy receives a large file read result (up to 50MB)
**When** the Bouncer processes it
**Then** it streams through without loading the entire payload into memory
**And** memory usage stays bounded (NFR2)

### Story 2.5: Implement Redaction Alerts via stderr

**As a** user,
**I want to** be alerted via stderr when redaction occurs,
**So that** I know my sensitive data was protected without polling logs.

**Acceptance Criteria:**

**Given** a redaction event occurring during JSON-RPC processing
**When** the Bouncer redacts a secret
**Then** a message is written to stderr (not stdout)
**And** the message includes the pattern that was matched
**And** the message does NOT include the actual secret value

**Given** multiple redactions in a single request
**When** processing completes
**Then** a summary is written to stderr
**And** it shows the count of redactions by type

**Given** verbose mode is enabled (`--verbose`)
**When** redaction occurs
**Then** additional context is provided in the stderr message
**And** the original message structure is hinted at (without exposing secrets)

## Epic 3: Context Optimization (JIT Discovery & Compactor)

Context Optimization goal: Users experience 50-80% token reduction through intelligent discovery and boilerplate pruning.

### Story 3.1: Implement Discovery Signatures

**As a** developer,
**I want to** register tools with minimal "Discovery Signatures" (name + description only),
**So that** the initial context overhead is dramatically reduced.

**Acceptance Criteria:**

**Given** a full MCP tool schema with name, description, and complex parameters
**When** the registry processes it for initial discovery
**Then** only the tool name and a brief description are stored
**And** the full JSON schema is NOT included in the initial manifest
**And** the resulting discovery payload is under 500 bytes per tool

**Given** 10 MCP servers with 50 tools each
**When** the IDE requests the tool list
**Then** the response includes all 50 tool names and descriptions
**And** the total payload is under 25KB (vs potentially 500KB+ with full schemas)

**Given** a tool's description needs updating
**When** the manifest is refreshed
**Then** the discovery signature is also updated

### Story 3.2: Implement JIT Schema Injection

**As a** developer,
**I want to** intercept `get_tool_schema` requests and inject full schemas on-demand,
**So that** full schema details are only loaded when a specific tool is actually called.

**Acceptance Criteria:**

**Given** an IDE request for `get_tool_schema` for a specific tool
**When** the request passes through the proxy
**Then** the proxy intercepts it
**And** looks up the cached full schema for that tool
**And** returns the complete schema in the response

**Given** an IDE request for `get_tool_schema` for an unknown tool
**When** the request passes through
**Then** the proxy forwards it to the MCP server
**And** returns the server's response

**Given** a tool schema hasn't been cached yet
**When** the first `get_tool_schema` request for it arrives
**Then** the proxy fetches the full schema from the server
**And** caches it for subsequent requests
**And** then returns the response

### Story 3.3: Implement Manifest Compactor (LLM Distillation)

**As a** developer,
**I want to** compact raw MCP manifests into token-dense signatures using LLM distillation,
**So that** even the full schemas are optimized for token efficiency.

**Acceptance Criteria:**

**Given** a raw MCP manifest with verbose descriptions
**When** the Compactor processes it
**Then** it sends the manifest to a configured cheap LLM (e.g., GPT-4o-mini)
**And** receives a distilled version with shorter descriptions
**And** preserves all parameter names and types exactly

**Given** a distilled manifest signature
**When** the IDE requests tool details
**Then** the distilled schema is used instead of the original
**And** the token count is reduced by 50-80% while preserving functionality

**Given** LLM distillation is configured but the LLM is unavailable
**When** a manifest needs compaction
**Then** the proxy falls back to the original manifest
**And** logs a warning to stderr
**And** continues operating without compaction

### Story 3.4: Implement Manual Re-Distillation Command

**As a** user,
**I want to** force re-distillation of a server manifest via CLI,
**So that** I can refresh stale discovery signatures when tool descriptions change.

**Acceptance Criteria:**

**Given** a configured MCP server with an existing distilled manifest
**When** the user runs `leanproxy compactor rebuild <server-name>`
**Then** a fresh distillation is triggered
**And** the new distilled manifest replaces the cached version
**And** a success message is displayed

**Given** a server that doesn't support distillation
**When** the rebuild command is run
**Then** an appropriate error is returned
**And** the existing manifest is preserved

**Given** a server with multiple tools
**When** the rebuild command is run
**Then** all tools are re-distilled
**And** the operation can take several seconds (logged to stderr)

### Story 3.5: Implement Boilerplate Blindness

**As a** developer,
**I want to** prune redundant imports and boilerplate from file-read results,
**So that** large file reads don't consume excessive tokens.

**Acceptance Criteria:**

**Given** a file read result containing import statements
**When** the result passes through the proxy
**Then** common import blocks are identified
**And** replaced with a compact `[IMPORTS_REDACTED]` marker
**And** the actual file content is preserved

**Given** a file read result containing copyright headers
**When** the result passes through the proxy
**Then** standard copyright blocks are identified
**And** replaced with `[LICENSE_REDACTED]`
**And** the actual code content is preserved

**Given** a file read result with no boilerplate
**When** it passes through the proxy
**Then** it passes through unchanged

**Given** boilerplate blindness is disabled in config
**When** file read results pass through
**Then** no modifications are made

## Epic 4: Developer Experience & CLI Interface

Developer Experience goal: Users can install, configure, and interact with the proxy via a polished POSIX CLI.

### Story 4.1: Implement Dry-Run Mode

**As a** user,
**I want to** run the proxy in dry-run mode to simulate savings and security alerts,
**So that** I can see the potential impact before enabling live mode.

**Acceptance Criteria:**

**Given** the proxy is started with `--dry-run` flag
**When** JSON-RPC requests pass through
**Then** they are analyzed but NOT forwarded to MCP servers
**And** simulated responses are generated
**And** token savings are calculated and logged to stderr

**Given** dry-run mode is active
**When** a request containing secrets is processed
**Then** the Bouncer still redacts in the analysis
**And** a security alert is logged showing what WOULD have been redacted
**And** no actual secrets leave the system

**Given** dry-run mode completes a session
**When** the proxy shuts down
**Then** a markdown report is generated summarizing:
**And** total simulated tokens processed
**And** estimated token savings
**And** security events that would have occurred

### Story 4.2: Implement POSIX-Compliant CLI

**As a** user,
**I want to** interact with the proxy via a standard POSIX CLI,
**So that** it works seamlessly in scripts and integrates with existing workflows.

**Acceptance Criteria:**

**Given** the leanproxy binary is installed
**When** the user runs any command
**Then** it returns appropriate exit codes (0 for success, 1 for errors)
**And** it supports `--help`, `--version` standard flags
**And** it works in both interactive shells and scripts

**Given** an invalid command is run
**When** the user runs `leanproxy invalid-cmd`
**Then** an error message is displayed
**And** exit code 1 is returned
**And** the help text suggests valid commands

**Given** the proxy is running
**When** the user sends SIGTERM or SIGINT
**Then** it shuts down gracefully
**And** all server processes are stopped cleanly
**And** exit code 0 is returned

### Story 4.3: Implement IDE Extension Socket

**As a** developer,
**I want to** provide a local Unix/Windows socket for IDE extension communication,
**So that** extensions can query proxy metrics and update configuration without disrupting the primary stdio stream.

**Acceptance Criteria:**

**Given** the proxy is running
**When** an IDE extension connects to the socket at `~/.leanproxy/socket`
**Then** a JSON-RPC connection is established
**And** the extension can query metrics, status, and configuration

**Given** an IDE extension sends a metrics request
**When** the socket receives it
**Then** it returns current token savings, server status, and health metrics
**And** the response does not interfere with the stdio stream

**Given** an IDE extension sends a config update request
**When** the socket receives it
**Then** the configuration is updated in memory
**And** the change is reflected immediately in proxy behavior
**And** the original config file is NOT modified (ephemeral change)

### Story 4.4: Implement Universal Installer

**As a** user,
**I want to** install leanproxy via a universal shell script or Homebrew,
**So that** I can get started in under 2 minutes.

**Acceptance Criteria:**

**Given** a Unix-like system with curl installed
**When** the user runs `curl -fsSL https://get.leanproxy.dev | sh`
**Then** the latest binary is downloaded for their platform
**And** it is installed to `/usr/local/bin/leanproxy`
**And** the binary is marked executable
**And** a success message is displayed with next steps

**Given** macOS with Homebrew installed
**When** the user runs `brew install leanproxy/tap/leanproxy`
**Then** Homebrew downloads and installs the correct version
**And** shell completions are automatically installed
**And** the user can run `leanproxy --help` immediately

**Given** Linux, macOS, or Windows
**When** the user downloads the correct binary for their platform
**Then** it runs without any additional dependencies
**And** it works on both x64 and ARM64 architectures

### Story 4.5: Implement Shell Completion

**As a** user,
**I want to** have automated shell completion for all management subcommands,
**So that** I can discover available commands and flags quickly.

**Acceptance Criteria:**

**Given** leanproxy is installed
**When** the user runs `leanproxy completion bash`
**Then** bash completion script is output to stdout
**And** the user can pipe it to a completion directory

**Given** leanproxy is installed on macOS with zsh
**When** the user runs `leanproxy completion zsh`
**Then** zsh completion functions are output
**And** they include all subcommands: server, compactor, context

**Given** leanproxy is installed with completion configured
**When** the user types `leanproxy <TAB><TAB>`
**Then** all available subcommands are shown
**And** when typing `leanproxy server <TAB><TAB>`
**Then** all server subcommands (add, remove, list) are shown

## Epic 5: Reporting & Insights

Reporting & Insights goal: Users can see real-time metrics on token savings and security events.

### Story 5.1: Implement Token Savings Calculator

**As a** developer,
**I want to** calculate and track token savings in real-time,
**So that** users can see the economic impact of using leanproxy.

**Acceptance Criteria:**

**Given** a JSON-RPC request passes through the proxy
**When** processing completes
**Then** the original token count is estimated
**And** the actual token count after optimization is calculated
**And** the difference is logged as "tokens saved"

**Given** a session with multiple requests
**When** the session ends or status is queried
**Then** the cumulative token savings is displayed
**And** it shows breakdown by MCP server (if multiple)

**Given** dry-run mode is active
**When** the user runs `leanproxy context rebuild --dry-run`
**Then** token savings are simulated and displayed
**And** they can be compared against actual savings later

### Story 5.2: Implement Markdown Report Generation

**As a** user,
**I want to** generate Markdown-formatted reports on tokens saved and risks intercepted,
**So that** I can share the impact with my team or include it in documentation.

**Acceptance Criteria:**

**Given** a completed session (or dry-run)
**When** the user runs `leanproxy report`
**Then** a Markdown-formatted report is output to stdout
**And** it includes a summary section with key metrics
**And** it includes a detailed breakdown by server

**Given** the report format includes:
**When** the report is generated
**Then** it shows "Total Tokens Saved: X" with percentage reduction
**And** it shows "Security Risks Intercepted: Y" with risk categories
**And** it shows "Session Duration: Z"
**And** it is formatted for display in IDE preview panels

### Story 5.3: Implement Real-Time Server Health Status

**As a** user,
**I want to** see real-time status of all active proxied servers,
**So that** I can monitor the health of my MCP integration.

**Acceptance Criteria:**

**Given** multiple MCP servers are running
**When** the user runs `leanproxy status`
**Then** a table is displayed showing all servers
**And** each row shows: name, status (running/error/stopped), uptime, last response time

**Given** a server process crashes
**When** the health monitor detects the failure
**Then** the status is updated to "error" within 1 second (NFR8)
**And** an alert is logged to stderr
**And** the restart attempts are shown in the status

**Given** the user runs `leanproxy status --watch`
**Then** the status updates are streamed continuously
**And** the display refreshes every second
**And** Ctrl+C exits the watch mode

**Given** verbose mode is enabled
**When** status is displayed
**Then** additional details are shown: memory usage, request count, error rate

## Epic 6: Server Configuration & Migration

Server Configuration & Migration goal: Users can define server entries with rich configuration and migrate from existing MCP tools.

### Story 6.1: Define LeanProxy Servers YAML Schema

**As a** developer,
**I want to** define a comprehensive `leanproxy_servers.yaml` schema,
**So that** users can configure MCP servers with transport type, command/args, env vars, and timeouts.

**Acceptance Criteria:**

**Given** a user configuring their MCP servers
**When** they create `~/.config/leanproxy_servers.yaml`
**Then** they can specify servers with: name, enabled flag, transport type (stdio/http/sse)
**And** for stdio transport: command, args, env variables, cwd
**And** for http/sse transport: url, headers
**And** common options: timeout, connect_timeout, cache settings, summarize settings

**Given** a minimal server entry
**When** only name and command are specified
**Then** defaults are applied for all other settings (enabled: true, timeout: 30s, etc.)

**Given** an invalid schema (missing required fields)
**When** the proxy starts
**Then** it reports the validation error
**And** exits with a helpful error message

### Story 6.2: Implement Auto-Detection and Migration

**As a** user,
**I want to** run `leanproxy migrate` to auto-detect and import all MCP configs,
**So that** I can move from OpenCode, Claude Code, VS Code, or Cursor without manual setup.

**Acceptance Criteria:**

**Given** existing MCP configurations on the system
**When** the user runs `leanproxy migrate`
**Then** the system scans known locations:
- `~/.config/opencode/opencode.json`
- `~/.claude.json` and `~/.config/claude/mcp_config.json`
- VS Code settings.json (MCP extensions section)
- `~/.cursor/mcp.json`
- `~/.config/mcp.json`

**Given** multiple MCP configs are found
**When** the scan completes
**Then** a summary is displayed showing:
- Number of configs found
- Servers to be imported per tool
- Total server count

**Given** the user confirms the migration
**When** the import proceeds
**Then** servers are merged into `leanproxy_servers.yaml`
**And** duplicate server names are handled with suffix (_opencode, _claude, etc.)
**And** a success message shows imported servers
**And** the `enabled` state from the source configuration is preserved during import

**Given** no MCP configs are found
**When** the migrate command runs
**Then** a message explains no configs were found

### Story 6.3: Validate Imported Server Configurations

**As a** user,
**I want to** see validation errors during migration,
**So that** I know which servers might not work and why.

**Acceptance Criteria:**

**Given** an imported server with a missing executable command
**When** the migration validates the config
**Then** an error is reported: "Server 'github': command 'npx' not found in PATH"

**Given** an imported server with invalid transport type
**When** the migration validates the config
**Then** an error is reported: "Server 'myserver': invalid transport 'ftp'. Must be stdio, http, or sse"

**Given** an imported server with missing required field
**When** the migration validates the config
**Then** an error is reported with the specific field missing

**Given** validation errors occur during migration
**When** the import completes
**Then** the summary shows: "Imported X servers, Y warnings"
**And** warnings are displayed but don't block import

**Given** the user runs `leanproxy migrate --validate-only`
**When** the command executes
**Then** only validation runs without importing
**And** all validation errors are reported

### Story 6.4: Add IDE Configuration Documentation

**As a** user,
**I want to** configure LeanProxy-MCP as an MCP server in my IDE,
**So that** I can use it with Claude Desktop, Cursor, OpenCode, or Windsurf.

**Acceptance Criteria:**

**Given** a user reading the README documentation
**When** they navigate to the IDE configuration section
**Then** they find instructions for:
- **Claude Desktop**: Adding `leanproxy` to `mcpServers` in `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Cursor**: Adding to `~/.cursor/mcp.json`
- **OpenCode**: Adding to `~/.config/opencode/mcp.json`
- **Windsurf**: Adding to `~/.windsurf/mcp.json`

**Given** the documentation for each IDE
**When** the user follows the steps
**Then** they see how to set the transport (stdio) and command path
**And** they see how to verify the connection works

**Given** a user migrating from another MCP tool
**When** they use the leanproxy migrate command
**Then** the resulting config is immediately usable by their IDE
**And** no manual editing of IDE config files is required

## Epic 7: Multi-Server Gateway Mode

Multi-Server Gateway Mode goal: LeanProxy-MCP acts as a unified gateway that proxies JSON-RPC requests to multiple configured MCP servers, routing each tool call to the correct server based on the merged tool registry, supporting 100+ concurrent MCP servers.

### Story 7.1: Implement Tool-to-Server Routing Engine

**As a** developer,
**I want to** parse JSON-RPC requests and route them to the correct MCP server based on tool name,
**So that** a single LeanProxy-MCP instance can proxy traffic to hundreds of MCP servers.

**Acceptance Criteria:**

**Given** an IDE sends a JSON-RPC request with method `github.create_issue`
**When** the proxy receives the request
**Then** it looks up `github.create_issue` in the tool registry
**And** routes the request to the `github` MCP server's stdin
**And** returns the response to the IDE

**Given** an IDE sends a batch of JSON-RPC requests for tools from different servers
**When** the proxy receives the batch
**Then** it parses each method name
**And** routes each request to the appropriate server in parallel
**And** collects responses and returns them in correct order

**Given** a request for an unknown tool
**When** the proxy receives it
**Then** it returns a JSON-RPC error with code -32601 (Method not found)
**And** logs a debug message noting the unmatched method

**Given** a server goes offline during active requests
**When** requests are pending for that server
**Then** the proxy returns an error indicating server unavailable
**And** does not block requests for other servers

### Story 7.2: Expose Gateway Tools to IDE

**As a** developer,
**I want to** expose internal gateway tools (list_servers, invoke_tool, search_tools) to the IDE,
**So that** the AI can discover and invoke tools across all configured MCP servers through a unified interface.

**Acceptance Criteria:**

**Given** LeanProxy-MCP is running as a gateway
**When** the IDE requests the tool list via `tools/list`
**Then** the response includes gateway tools: `list_servers`, `invoke_tool`, `search_tools`
**And** each gateway tool has a minimal discovery signature

**Given** the AI calls `list_servers()`
**When** the gateway receives the request
**Then** it returns a list of all configured MCP servers with their names and enabled status

**Given** the AI calls `invoke_tool(server_name, tool_name, params)`
**When** the gateway receives the request
**Then** it routes the request to the specified server
**And** returns the tool response

**Given** the AI calls `search_tools(query)`
**When** the gateway receives the request
**Then** it searches tool names and descriptions across all servers
**And** returns matching tools with server attribution

### Story 7.3: Implement Stdio Pool Manager

**As a** developer,
**I want to** manage a pool of stdio MCP server subprocesses,
**So that** multiple concurrent requests can be handled efficiently across 100+ servers.

**Acceptance Criteria:**

**Given** 100 MCP servers are configured in stdio mode
**When** the gateway starts
**Then** it spawns subprocesses for all enabled servers
**And** each subprocess runs in its own process group (NFR6)
**And** process health is monitored continuously

**Given** multiple concurrent requests for the same server
**When** requests arrive
**Then** they are queued and processed sequentially per server
**And** no request mixing occurs between different tool calls

**Given** a server's subprocess exits unexpectedly
**When** the lifecycle manager detects the exit
**Then** it restarts the subprocess with exponential backoff
**And** pending requests for that server return an error

**Given** server idle timeout is configured
**When** a server has no requests for the idle period
**Then** the subprocess is stopped to conserve resources
**And** the subprocess is restarted on the next request

### Story 7.4: Integrate Registry with Proxy for Dynamic Routing

**As a** developer,
**I want to** integrate the server registry with the proxy for dynamic server selection,
**So that** servers can be added, removed, and updated without restarting the gateway.

**Acceptance Criteria:**

**Given** a running gateway
**When** a new server is added via `leanproxy server add`
**Then** the server appears in the registry within 1 second
**And** the server's tools become available for routing
**And** the `list_servers` tool reflects the change

**Given** a running gateway
**When** a server is removed via `leanproxy server remove`
**Then** the server's subprocess is stopped
**And** pending requests return an error
**And** subsequent requests to that server's tools return method-not-found

**Given** the registry is updated externally
**When** the proxy checks the registry
**Then** it picks up changes without requiring restart
**And** routes requests based on the current registry state

### Story 7.5: Rewrite handleConnection for Multi-Server Routing

**As a** developer,
**I want to** rewrite the handleConnection function to support multi-server routing,
**So that** each incoming IDE connection is handled by routing requests to the appropriate MCP server.

**Acceptance Criteria:**

**Given** an IDE connects to LeanProxy-MCP's stdio endpoint
**When** the IDE sends a JSON-RPC request
**Then** handleConnection parses the method name
**And** looks up the target server in the registry
**And** forwards the request to that server's stdin
**And** streams the response back to IDE

**Given** handleConnection receives a notification (no ID)
**When** the notification is parsed
**Then** it is forwarded to the appropriate server
**And** no response is returned

**Given** handleConnection receives a batch request
**When** the batch is parsed
**Then** each request is routed to its target server
**And** responses are collected and returned as a batch

**Given** the connection is closed mid-stream
**When** handleConnection detects the close
**Then** it cleanly terminates server communication
**And** no zombie processes are left behind

### Story 7.6: Implement Concurrent Multi-Server Request Handling

**As a** developer,
**I want to** handle concurrent requests across multiple MCP servers efficiently,
**So that** the gateway can handle high-throughput scenarios with 100+ servers.

**Acceptance Criteria:**

**Given** 50 concurrent requests arrive for different servers
**When** the gateway processes them
**Then** each request is routed to its target server in parallel
**And** responses are returned as they complete
**And** no request ordering guarantees are broken for the same tool

**Given** a request with a very large payload (>10MB)
**When** the gateway receives it
**Then** it streams the payload without buffering entirely in memory
**And** processing overhead remains under 200ms (NFR2)

**Given** rate limiting is configured per server
**When** requests exceed the rate limit
**Then** excess requests are queued
**And** returned with a retry-after response when appropriate

**Given** concurrent requests for the same server
**When** they arrive simultaneously
**Then** they are serialized to prevent race conditions
**And** responses are matched to correct requests by ID

---

# NEW EPICS ADDED FROM MARKET RESEARCH (May 2026)

These epics were added based on market research findings identifying critical features for token savings and latency optimization.

## Epic 8: Token Optimization & Performance

Token Optimization goal: Users experience 6-7x token reduction through lazy-loading and connection reuse, with minimal latency overhead.

### Story 8.1: Implement Lazy-Loading Tool Schemas

**As a** developer,
**I want to** load tool schemas on-demand rather than at startup,
**So that** initial context overhead is dramatically reduced (6-7x token savings).

**Acceptance Criteria:**

**Given** 10 MCP servers with 100 tools total configured
**When** the proxy starts in lazy-loading mode
**Then** only compact tool stubs (~54 tokens each) are sent to the IDE
**And** full schemas are loaded only when a tool is actually invoked

**Given** an IDE requests `get_tool_schema` for a specific tool
**When** the lazy-loading proxy receives the request
**Then** it fetches the full schema from the MCP server
**And** caches it for subsequent requests
**And** returns the complete schema

**Given** a tool is not invoked within a session
**When** the session ends
**Then** the full schema was never loaded
**And** token savings are achieved

**Given** lazy-loading mode is disabled in config
**When** the proxy starts
**Then** all full schemas are loaded at startup (legacy behavior)

### Story 8.2: Implement Connection Pooling

**As a** developer,
**I want to** reuse MCP server sessions across multiple requests,
**So that** the 187x overhead issue (15s vs 80ms) is fixed.

**Acceptance Criteria:**

**Given** a stateless HTTP proxy setup
**When** multiple tool calls are made to the same server
**Then** a new client is NOT created on every call
**And** the same underlying session is reused
**And** latency overhead is reduced from 15s to under 100ms

**Given** connection pooling is enabled
**When** the proxy starts
**Then** initial connections are established proactively
**And** kept alive with keepalive heartbeats

**Given** a server connection is lost
**When** the proxy detects the failure
**Then** it automatically re-establishes the connection
**And** retries the pending request

**Given** connection pool size is configured (default: 5)
**When** more concurrent requests arrive
**Then** they are queued until a connection becomes available

### Story 8.3: Implement Minimal Session Re-Initialization

**As a** developer,
**I want to** avoid repeated MCP handshake overhead,
**So that** tool calls complete in under 100ms vs current 15s.

**Acceptance Criteria:**

**Given** a proxy session is established
**When** a new tool call arrives
**Then** the MCP initialize handshake is NOT repeated
**And** only the tool call is sent to the server

**Given** session state can be serialized
**When** the proxy restarts or reconnects
**Then** session state can be restored without full re-initialization

**Given** multiple clients connect to the same server
**When** requests arrive
**Then** session复用 is attempted before creating new sessions

### Story 8.4: Implement Cost Attribution Layer

**As a** user,
**I want to** track token usage per tool and per server,
**So that** I can see which tools consume the most tokens.

**Acceptance Criteria:**

**Given** a session is active
**When** tools are invoked
**Then** token counts are tracked per tool name
**And** per-MCP-server totals are accumulated

**Given** the user runs `leanproxy cost`
**When** the command executes
**Then** a breakdown is shown:
- Token count per tool
- Token count per server
- Total session tokens

**Given** cost attribution is enabled
**When** detailed tracking is available
**Then** the data is also available via the status socket

---

## Epic 9: Enterprise Transport & Architecture

Enterprise Transport goal: Support Streamable HTTP and hierarchical namespaces for enterprise deployments.

### Story 9.1: Implement Streamable HTTP Transport

**As a** enterprise user,
**I want to** use Streamable HTTP instead of SSE,
**So that** the proxy works with corporate proxies and load balancers.

**Acceptance Criteria:**

**Given** Streamable HTTP transport is configured
**When** the proxy starts
**Then** it listens on a single HTTP endpoint
**And** supports both synchronous and streaming responses

**Given** a client connects via Streamable HTTP
**When** the connection goes through a corporate proxy
**Then** the connection is not broken by proxy timeouts
**And** SSE stream buffering issues are avoided

**Given** both stdio and Streamable HTTP are configured
**When** the proxy starts
**Then** both transports are available
**And** clients can connect via either

**Given** Streamable HTTP is used
**When** the specification changes
**Then** the proxy can be updated to match spec

### Story 9.2: Implement Hierarchical Namespaces

**As a** enterprise user,
**I want to** organize MCP servers into hierarchical namespaces,
**So that** multi-team organizations can manage access cleanly.

**Acceptance Criteria:**

**Given** namespace configuration in leanproxy.yaml
**When** the proxy starts
**Then** servers are grouped under their namespaces
**And** tools are namespaced accordingly

**Given** a client requests tools from namespace "engineering"
**When** the request arrives
**Then** only servers in the engineering namespace are included
**And** other namespaces are excluded

**Given** nested namespaces are configured
**When** the proxy processes requests
**Then** the hierarchy is respected (parent includes child namespaces)

**Given** namespace-level access control is configured
**When** a client connects
**Then** access is restricted to their assigned namespaces

### Story 9.3: Implement Simple Federation

**As a** enterprise user,
**I want to** connect multiple LeanProxy instances,
**So that** servers can be federated across organizations.

**Acceptance Criteria:**

**Given** federation configuration is defined
**When** the proxy starts
**Then** it can discover and connect to other LeanProxy instances

**Given** a tool request for an unknown tool
**When** the proxy processes it
**Then** it looks up the tool in federated instances
**And** routes to the instance that has the tool

**Given** a federated instance goes offline
**When** requests are pending
**Then** the proxy detects the failure
**And** routes to backup instances if available

---

## FR Coverage Map (Updated)

New functional requirements from market research:

FR33: Epic 8.1 - Lazy-loading tool schemas
FR34: Epic 8.2 - Connection pooling  
FR35: Epic 9.1 - Streamable HTTP transport
FR36: Epic 8.4 - Cost attribution layer
FR37: Epic 8.3 - Minimal session re-initialization
FR38: Epic 9.2 - Hierarchical namespaces
FR39: Epic 9.3 - Simple federation

---

## Implementation Priority (Based on Market Research)

| Priority | Epic | Story | Expected KPI Impact |
|----------|------|-------|----------------|
| CRITICAL | Epic 8.1 | Lazy-loading tool schemas | 6-7x token reduction |
| CRITICAL | Epic 8.2 | Connection pooling | Fixes 187x latency |
| HIGH | Epic 8.3 | Minimal session re-init | Target <100ms |
| HIGH | Epic 9.1 | Streamable HTTP | Enterprise compat |
| HIGH | Epic 8.4 | Cost attribution | Differentiation |
| MEDIUM | Epic 9.2 | Hierarchical namespaces | Enterprise |
| MEDIUM | Epic 9.3 | Simple federation | Multi-org |
---

# NEW EPICS ADDED FROM BRAINSTORMING SESSION (June 2026)

These epics were added based on the 2026-06-22 brainstorming session that generated 102 market-trend-aligned feature ideas. The Top 10 RICE-prioritized ideas are decomposed into Epics 10-18 with full story breakdowns.

## Source

- **Brainstorming session:** `_bmad-output/brainstorming/brainstorming-session-2026-05-01.md` (extension section, 2026-06-22)
- **Ideas covered:** Top 10 of 102 generated (Anthropic Cache Bridge, MCP Registry Mirror, Semantic Cache, Injection Firewall v2, IDE Plugin, Model Router, Local LLM Sidecar, Cost Dashboard, First-Party Servers, Budget Governor)

## New Functional Requirements (FR40-FR49)

FR40: The system can auto-inject `cache_control: ephemeral` breakpoints into Anthropic API calls so the upstream prompt cache hits ≥70% of the time.
FR41: The system can mirror the official MCP Registry, allow one-click install of trusted servers, and surface trust/rating signals.
FR42: The system can cache tool-call payloads by semantic similarity (not exact match) and reuse responses across sessions.
FR43: The system can scan tool *results* for indirect prompt-injection patterns and quarantine, redact, block, or log based on policy.
FR44: The system can publish a `/metrics` JSON endpoint that exposes real-time token spend per server/tool/team for IDE plugin consumption.
FR45: The system can route individual tool calls to different LLM providers based on a per-tool "complexity tier" declared in the manifest.
FR46: The system can delegate redaction, summarization, and discovery to a local LLM (Ollama/MLX) to keep sensitive data on-device.
FR47: The system can ship first-party MCP servers (GitHub, Filesystem, Postgres, Redis) with secure defaults and built-in connection pooling.
FR48: The system can enforce per-team/per-project daily token budgets at the gateway, with auto-throttle or downgrade behavior.
FR49: The system can serve a web UI (TUI or HTTP) showing real-time cost dashboards with per-server/per-tool drill-down.

## New Non-Functional Requirements (NFR11-NFR13)

NFR11: All new caching features must add <10ms p95 overhead to the existing <50ms p95 proxy budget (NFR1).
NFR12: Any feature that calls an external LLM provider must be disable-able via config and fall back to a local or no-op mode.
NFR13: All new UI surfaces (web dashboard, IDE plugin) must respect LeanProxy's "local-only, no telemetry" principle (NFR4).

## Epic List (Updated)

| Epic | Title | FRs | Priority |
|:-----|:------|:----|:---------|
| 1 | Core Proxy Infrastructure | FR1-FR5 | CRITICAL |
| 2 | Security & Data Governance (The Bouncer) | FR11-FR15 | CRITICAL |
| 3 | Context Optimization (JIT Discovery & Compactor) | FR6-FR10 | HIGH |
| 4 | Developer Experience & CLI Interface | FR16-FR20 | HIGH |
| 5 | Reporting & Insights | FR21-FR23 | HIGH |
| 6 | Server Configuration & Migration | FR24-FR28 | HIGH |
| 7 | Multi-Server Gateway Mode | FR5, FR29-FR32 | CRITICAL |
| 8 | Token Optimization & Performance (lazy/pool/cost) | FR33-FR34, FR36-FR37 | CRITICAL |
| 9 | Enterprise Transport & Architecture | FR35, FR38-FR39 | MEDIUM |
| **10** | **Anthropic Prompt Caching Bridge** | **FR40** | **HIGH** |
| **11** | **MCP Registry Mirror & Discovery** | **FR41** | **HIGH** |
| **12** | **Semantic Prompt Cache** | **FR42** | **HIGH** |
| **13** | **AI Safety: Prompt-Injection Firewall v2** | **FR43** | **HIGH** |
| **14** | **IDE Plugin (VS Code / JetBrains) + Live Cost Sidebar** | **FR44** | **MEDIUM** |
| **15** | **Per-Tool Model Router & Local LLM Sidecar** | **FR45, FR46** | **MEDIUM** |
| **16** | **First-Party MCP Servers (GitHub / FS / DB)** | **FR47** | **MEDIUM** |
| **17** | **Token Budget Governor** | **FR48** | **HIGH** |
| **18** | **Cost Attribution Web Dashboard** | **FR49** | **MEDIUM** |

## FR Coverage Map (Updated)

| FR | Epic / Story |
|:---|:-------------|
| FR40 | Epic 10.1-10.3 |
| FR41 | Epic 11.1-11.3 |
| FR42 | Epic 12.1-12.3 |
| FR43 | Epic 13.1-13.3 |
| FR44 | Epic 14.1-14.3 |
| FR45 | Epic 15.1 |
| FR46 | Epic 15.2-15.3 |
| FR47 | Epic 16.1-16.3 |
| FR48 | Epic 17.1-17.2 |
| FR49 | Epic 18.1-18.3 |

---

## Epic 10: Anthropic Prompt Caching Bridge

**Goal:** Users automatically benefit from Anthropic's 90%-cheaper prompt cache without any config — LeanProxy transparently inserts `cache_control: ephemeral` breakpoints into stable tool-definition segments.

### Story 10.1: Detect Anthropic API calls in the proxy stream

**As a** developer,
**I want** LeanProxy to detect when an outgoing request is bound for the Anthropic API,
**So that** caching logic is only applied where it is supported.

**Acceptance Criteria:**

**Given** an outbound request whose URL matches an Anthropic endpoint pattern
**When** the proxy intercepts the request
**Then** it tags the request with `provider=anthropic` in the in-flight context
**And** logs a debug message to stderr (NFR9)

**Given** an outbound request whose URL is *not* Anthropic
**When** the proxy intercepts the request
**Then** it tags `provider=other` and skips caching logic
**And** no overhead is added beyond the existing proxy hop (NFR11)

**Given** the user has multiple providers configured
**When** the proxy starts
**Then** it loads the provider list from `leanproxy.yaml` and builds the matcher
**And** matcher reloads on SIGHUP without restart

### Story 10.2: Auto-inject `cache_control: ephemeral` breakpoints

**As a** developer,
**I want** LeanProxy to identify stable segments of the request (system prompt, tool definitions) and inject Anthropic cache breakpoints,
**So that** the upstream cache hits on subsequent requests.

**Acceptance Criteria:**

**Given** an Anthropic request containing a system block and a `tools` array
**When** the proxy processes the request
**Then** it appends `"cache_control": {"type": "ephemeral"}` to the last tool definition
**And** it appends the same to the last system block (if multiple)
**And** it leaves all other content unchanged

**Given** the request already contains a `cache_control` block
**When** the proxy processes it
**Then** it does NOT overwrite or duplicate the user's block
**And** it logs a debug message "cache_control: user-supplied, skipping"

**Given** caching strategy is set to `off` in config
**When** the proxy processes an Anthropic request
**Then** no cache breakpoints are injected
**And** the request is forwarded unchanged

**Given** caching strategy is set to `aggressive` (default)
**When** the proxy processes an Anthropic request
**Then** breakpoints are added to both system and tools
**And** if `balanced` is set, breakpoints are added only to the largest stable block

**Acceptance Criterion (NFR11):** Injection adds <1ms p95 to the existing proxy hop.

### Story 10.3: Report cache hit-rate via `leanproxy cache` command

**As a** user,
**I want** a CLI command that shows Anthropic cache hit rate, tokens saved, and dollar savings,
**So that** I can verify the feature is working and quantify the impact.

**Acceptance Criteria:**

**Given** at least one Anthropic request has been processed
**When** the user runs `leanproxy cache`
**Then** a Markdown table is printed to stdout showing: total requests, cache hits, hit rate %, tokens saved, estimated $ saved (using current Anthropic pricing)

**Given** no Anthropic requests have been processed
**When** the user runs `leanproxy cache`
**Then** a message "No Anthropic traffic observed" is printed and exit code is 0

**Given** the user runs `leanproxy cache --json`
**When** the command executes
**Then** the same data is emitted as a JSON object to stdout for piping

---

## Epic 11: MCP Registry Mirror & Discovery

**Goal:** Users can browse, install, and trust MCP servers from the emerging official registry in a single command, with curated ratings and token-cost previews.

### Story 11.1: Subscribe to the MCP Registry feed

**As a** developer,
**I want** LeanProxy to fetch the public MCP Registry index and cache it locally,
**So that** the user has an up-to-date catalog of available servers offline.

**Acceptance Criteria:**

**Given** the MCP Registry publishes an index (JSON or NDJSON) at a known URL
**When** the user runs `leanproxy marketplace sync`
**Then** the index is downloaded to `~/.leanproxy/registry/index.json`
**And** the timestamp of the last sync is recorded

**Given** the registry URL is unreachable
**When** the sync command runs
**Then** the existing cached index is preserved
**And** an error explains the network issue with retry guidance

**Given** the cache is older than 24 hours
**When** the proxy starts
**Then** it logs a notice that the registry cache is stale
**And** offers (via stderr) to run `leanproxy marketplace sync`

**Acceptance Criterion (NFR11):** Sync runs asynchronously; proxy startup is not blocked.

### Story 11.2: Implement `leanproxy add <server-id>` one-click install

**As a** user,
**I want** a single command to install and configure an MCP server from the registry,
**So that** I can add a tool to my workflow without writing YAML.

**Acceptance Criteria:**

**Given** the registry is synced
**When** the user runs `leanproxy add github`
**Then** the server definition is downloaded
**And** merged into `leanproxy_servers.yaml` under a unique name
**And** the server process is started (or scheduled for next session)
**And** a success message includes the server's tool count and a token-cost preview

**Given** the requested server does not exist
**When** the command runs
**Then** an error lists up to 5 similar server names
**And** exit code is non-zero

**Given** a server with the same name already exists locally
**When** the command runs
**Then** it prompts (or `--force` flag) before overwriting
**And** the existing process is gracefully stopped first

### Story 11.3: Surface trust score and maintenance status

**As a** user,
**I want** to see a trust score, last-updated date, and open-issue count for each registry server,
**So that** I can avoid installing abandoned or malicious tools.

**Acceptance Criteria:**

**Given** the user runs `leanproxy marketplace search <query>`
**When** the command executes
**Then** a table is displayed with columns: name, trust score (0-100), last release, open issues, downloads, estimated tokens/turn

**Given** a server has a trust score below 40
**When** the user runs `leanproxy add <low-trust-server>`
**Then** a warning is shown requiring `--i-understand-the-risks` flag to proceed
**And** the install is blocked by default

**Given** the trust score data is unavailable
**When** the command runs
**Then** a "—" placeholder is shown and no warning is raised

---

## Epic 12: Semantic Prompt Cache

**Goal:** Users get 60-80% additional token savings on repeated tool calls — beyond exact-string caches — by matching tool-call payloads by semantic similarity.

### Story 12.1: Embed tool-call payloads via local or remote model

**As a** developer,
**I want** LeanProxy to compute a semantic embedding of every tool-call payload,
**So that** semantically similar calls can be matched even when not textually identical.

**Acceptance Criteria:**

**Given** the semantic cache is enabled in config
**When** a tool call is processed
**Then** its structured payload (tool name + arguments) is embedded using the configured embedder
**And** the embedding is stored in the cache alongside the response

**Given** the embedder is configured as `local:ollama`
**When** a payload needs embedding
**Then** the request is made to the configured Ollama endpoint
**And** if Ollama is unreachable, the cache falls back to exact-match only and a warning is logged

**Given** the embedder is configured as `remote:openai`
**When** a payload needs embedding
**Then** the embedder API is called and the API key is read from the env (NFR12)
**And** if the API key is missing, startup fails with a clear message

**Acceptance Criterion (NFR11):** Embedding adds <5ms p95 to the request path.

### Story 12.2: Vector-store integration (pluggable backends)

**As a** developer,
**I want** the semantic cache to support multiple vector-store backends (SQLite-vec default; Qdrant/Pinecone optional),
**So that** the user can pick the right trade-off between simplicity and scale.

**Acceptance Criteria:**

**Given** config specifies `cache.vector_store: sqlite-vec` (default)
**When** the proxy starts
**Then** a SQLite database is created at `~/.leanproxy/cache/vectors.db`
**And** the `vec0` extension is loaded if available; otherwise a warning is logged

**Given** config specifies `cache.vector_store: qdrant`
**When** the proxy starts
**Then** a Qdrant client is initialized with the configured URL + API key
**And** connection failures cause startup to abort with a clear message

**Given** config specifies `cache.vector_store: pinecone`
**When** the proxy starts
**Then** the Pinecone client is initialized using the API key from env
**And** the index name from config is validated to exist

### Story 12.3: TTL, invalidation, and hit/miss dashboard

**As a** user,
**I want** cache entries to expire, schema changes to invalidate affected entries, and a hit-rate dashboard,
**So that** the cache is correct and observable.

**Acceptance Criteria:**

**Given** a cache entry older than the configured TTL (default: 24h)
**When** the cache is queried
**Then** the entry is treated as a miss
**And** a new entry is written after the response arrives

**Given** a tool's schema changes (via registry refresh)
**When** the change is detected
**Then** all cache entries for that tool are purged
**And** a log message records the invalidation count

**Given** the user runs `leanproxy cache --semantic`
**When** the command executes
**Then** a table is shown: total queries, exact hits, semantic hits, misses, hit rate %, avg similarity score

**Given** a cache hit occurs
**When** the proxy serves the cached response
**Then** a debug log records `cache=semantic similarity=0.92` to stderr
**And** a counter is incremented for the dashboard

---

## Epic 13: AI Safety — Prompt-Injection Firewall v2

**Goal:** Users are protected from indirect prompt injection — the top 2026 AI security threat — by scanning tool *results* (not just outgoing requests) and applying configurable actions.

### Story 13.1: Build a local prompt-injection classifier

**As a** developer,
**I want** a regex + heuristic-based local classifier for known injection patterns,
**So that** poisoned tool results are caught without calling a remote model.

**Acceptance Criteria:**

**Given** a tool result containing strings like "ignore previous instructions" or "you are now..."
**When** the classifier scans it
**Then** a `risk_score` (0-100) is computed based on the number and weight of matches
**And** the original payload is preserved alongside the score

**Given** the classifier detects no patterns
**When** the scan completes
**Then** the result is forwarded with `risk_score=0`
**And** no overhead is added (NFR11)

**Given** the classifier's pattern list is configurable
**When** a user adds a custom pattern in `leanproxy.yaml`
**Then** it is loaded on startup
**And** patterns can be enabled/disabled individually

**Acceptance Criterion:** Catches ≥95% of patterns in a red-team corpus of 200 known injection payloads.

### Story 13.2: Configurable actions (quarantine / redact / block / log)

**As a** user,
**I want** to choose what happens when a high-risk result is detected,
**So that** the policy matches my security posture.

**Acceptance Criteria:**

**Given** a tool result with `risk_score ≥ 80`
**When** the action policy is `block`
**Then** the result is dropped and an error returned to the LLM
**And** a critical alert is logged to stderr

**Given** a tool result with `risk_score ≥ 50` and `< 80`
**When** the action policy is `quarantine`
**Then** the result is moved to a side-channel (`~/.leanproxy/quarantine/<id>.json`)
**And** a stub `[CONTENT_QUARANTINED — review at ~/.leanproxy/quarantine/<id>.json]` is returned to the LLM
**And** a warning is logged

**Given** a tool result with `risk_score > 0` and `< 50`
**When** the action policy is `log`
**Then** the result is forwarded unchanged
**And** a debug entry is recorded

**Given** the user runs `leanproxy doctor --security`
**When** the command executes
**Then** a summary of recent injection attempts is shown with counts by action taken

### Story 13.3: Red-team corpus + continuous regression test

**As a** developer,
**I want** a red-team corpus of known injection payloads shipped with the binary,
**So that** the classifier is regression-tested on every release.

**Acceptance Criteria:**

**Given** a corpus of 200 known injection payloads at `tests/security/injection_corpus.json`
**When** `go test ./pkg/bouncer/...` runs
**Then** the classifier is run against every payload
**And** the test fails if recall drops below 95%

**Given** a new injection pattern is discovered
**When** the developer adds it to the corpus
**Then** the test reruns and the pattern is added to the default classifier list
**And** the changelog mentions the new pattern

---

## Epic 14: IDE Plugin (VS Code / JetBrains) + Live Cost Sidebar

**Goal:** Developers see the cost of every tool call in their IDE in real time, turning cost into a visceral feedback loop and a habit-forming adoption mechanism.

### Story 14.1: Publish `/metrics` JSON endpoint

**As a** developer,
**I want** LeanProxy to expose a real-time JSON metrics endpoint,
**So that** IDE plugins (and other consumers) can read spend data without parsing logs.

**Acceptance Criteria:**

**Given** the proxy is running with the metrics endpoint enabled (default: on)
**When** an HTTP client GETs `http://localhost:<port>/metrics`
**Then** a JSON object is returned with: per-server token counts, per-tool token counts, total session spend, top 5 most expensive tools

**Given** the metrics endpoint is disabled in config
**When** the proxy starts
**Then** the listener is not bound
**And** no port is occupied

**Given** the user sets `metrics.bind: 0.0.0.0:9090`
**When** the proxy starts
**Then** the endpoint is bound to all interfaces
**And** a warning is logged if the bind is non-loopback (security notice)

**Acceptance Criterion (NFR13):** The endpoint exposes only aggregated counts — no PII or prompt content.

### Story 14.2: VS Code extension (TypeScript) with status bar + webview

**As a** VS Code user,
**I want** a status-bar item showing the current session token cost, and a webview panel with a per-tool breakdown,
**So that** I can see the cost of my AI usage as I work.

**Acceptance Criteria:**

**Given** the VS Code extension is installed
**When** LeanProxy is running and reachable
**Then** the status bar shows `$0.00` initially
**And** updates within 1 second of each tool call

**Given** the user clicks the status bar item
**When** the webview opens
**Then** a table is displayed with: server, tool, calls, tokens, estimated cost
**And** the table refreshes every 2 seconds via a poll of `/metrics`

**Given** LeanProxy is not running
**When** the extension starts
**Then** the status bar shows "disconnected" with a tooltip explaining how to start the proxy
**And** the webview shows a "proxy offline" empty state

**Acceptance Criterion:** Extension installs from VS Code marketplace; first-run experience <60s.

### Story 14.3: JetBrains plugin (Kotlin) — parity with VS Code

**As a** JetBrains user (IntelliJ, PyCharm, GoLand),
**I want** the same live-cost experience as VS Code,
**So that** my team has consistent observability across IDEs.

**Acceptance Criteria:**

**Given** the JetBrains plugin is installed
**When** the IDE is open and LeanProxy is running
**Then** a status-bar widget shows the current session cost
**And** a tool window ("LeanProxy") displays the per-tool breakdown table

**Given** the user opens the tool window
**When** the view renders
**Then** data is polled from the same `/metrics` endpoint
**And** the refresh interval is configurable in plugin settings

**Acceptance Criterion:** Plugin published on JetBrains Marketplace with ≥4.5★ rating in first 90 days.

---

## Epic 15: Per-Tool Model Router & Local LLM Sidecar

**Goal:** Users route cheap tasks (list_tools, redaction) to local or cheap models, and premium reasoning to premium models, with zero config per call.

### Story 15.1: Per-tool model assignment via manifest

**As a** user,
**I want** to declare a `complexity_tier` per tool in `leanproxy_servers.yaml`,
**So that** LeanProxy automatically routes the call to the right model.

**Acceptance Criteria:**

**Given** a tool entry with `complexity_tier: low`
**When** the IDE invokes the tool
**Then** LeanProxy routes the call to the configured "cheap" provider (e.g., Haiku, GPT-4o-mini)
**And** the response is returned to the IDE unchanged

**Given** a tool entry with `complexity_tier: high`
**When** the IDE invokes the tool
**Then** LeanProxy routes the call to the configured "premium" provider
**And** the response is returned unchanged

**Given** the tool has no `complexity_tier`
**When** the call is processed
**Then** it defaults to `medium` (configurable global default)
**And** a debug log records the defaulting

**Acceptance Criterion (NFR12):** All routing logic is disable-able; disabled mode uses a single provider and behaves identically to the current proxy.

### Story 15.2: Ollama sidecar integration (re-routing to local LLM)

**As a** user,
**I want** redaction, summarization, and discovery to run on a local Ollama model,
**So that** sensitive data never leaves my machine for those tasks.

**Acceptance Criteria:**

**Given** config specifies `sidecar.provider: ollama` and `sidecar.model: llama3.1:8b`
**When** the Bouncer needs to redact a value (FR12) and the regex doesn't match
**Then** it sends the value to the local Ollama model for classification
**And** the redacted output is used

**Given** Ollama is unreachable
**When** the sidecar is needed
**Then** the Bouncer falls back to a "redact aggressively" mode (replace unknown values with `[VALUE_REDACTED]`)
**And** a stderr warning explains the fallback

**Given** the user has not configured a sidecar
**When** the proxy starts
**Then** the sidecar is disabled
**And** Bouncer uses regex-only mode (current behavior)

**Acceptance Criterion (NFR4):** When the sidecar is enabled, no payload is ever sent to a remote endpoint for redaction/discovery/summarization.

### Story 15.3: MLX / Apple Silicon support (experimental)

**As a** Apple Silicon user,
**I want** LeanProxy to use MLX-based local models for the sidecar,
**So that** I get faster inference on M-series Macs without Ollama.

**Acceptance Criteria:**

**Given** `sidecar.provider: mlx` is configured on macOS arm64
**When** the proxy starts
**Then** the MLX runtime is detected and loaded
**And** a model from `~/Library/Application Support/leanproxy/models/` is loaded

**Given** the configured model file is missing
**When** the proxy starts
**Then** a helpful error suggests `ollama pull <model>` or downloading from a provided URL
**And** startup is aborted

**Acceptance Criterion (NFR12):** MLX support is opt-in via build tag; absent the tag, the binary behaves identically.

---

## Epic 16: First-Party MCP Servers (GitHub / Filesystem / DB)

**Goal:** Users get battle-tested, security-hardened first-party MCP servers shipped with LeanProxy — removing third-party quality risk and creating a "batteries-included" experience.

### Story 16.1: First-party GitHub MCP server

**As a** user,
**I want** a LeanProxy-bundled GitHub MCP server with secure defaults,
**So that** I don't have to vet and install a third-party option.

**Acceptance Criteria:**

**Given** LeanProxy is installed
**When** the user runs `leanproxy add github` (or it's bundled by default)
**Then** the `leanproxy-mcp-github` server is registered
**And** it reads `GITHUB_TOKEN` from the environment (NFR4)
**And** rate limiting is enforced (5000 req/hour, GitHub's documented limit)

**Given** a GitHub API rate-limit error
**When** the server encounters it
**Then** it returns a structured error to the LLM with the reset time
**And** a warning is logged to stderr

**Given** the `GITHUB_TOKEN` is missing
**When** the server starts
**Then** it starts in "read-only public" mode with reduced tool set
**And** a notice is shown

**Acceptance Criterion:** Server passes an integration test that calls `list_repos`, `get_issue`, `create_pr` against the GitHub API.

### Story 16.2: First-party Filesystem MCP server with safe defaults

**As a** user,
**I want** a Filesystem MCP server that is restricted to a workspace root by default,
**So that** accidental `rm -rf` or path traversal is impossible.

**Acceptance Criteria:**

**Given** LeanProxy starts with a `filesystem.allowed_roots` config
**When** the Filesystem server is initialized
**Then** it only accepts paths under those roots
**And** path-traversal attempts (`../etc/passwd`) return a permission error

**Given** no `allowed_roots` are configured
**When** the server starts
**Then** it refuses to start with a clear error
**And** the user is directed to configure `filesystem.allowed_roots: ["/path/to/project"]`

**Given** a read of a 50MB file
**When** the server handles it
**Then** the response is streamed (NFR2)
**And** memory usage stays bounded

**Acceptance Criterion:** Server has zero CVEs in `gosec` static analysis on every release.

### Story 16.3: First-party Postgres / Redis servers with pooling

**As a** user,
**I want** first-party DB servers that use connection pooling by default,
**So that** I get high throughput without leaking connections.

**Acceptance Criteria:**

**Given** a Postgres MCP server is configured with `pool_size: 10`
**When** 50 concurrent tool calls arrive
**Then** no more than 10 database connections are opened
**And** the 11th-50th calls queue (per FR31/Epic 7 patterns)

**Given** the database becomes unreachable
**When** a query is in flight
**Then** the connection pool detects the failure within 1 second (NFR8)
**And** retries up to 3 times with exponential backoff

**Given** a long-running query exceeds the configured `statement_timeout`
**When** the timeout fires
**Then** the connection is released back to the pool
**And** a structured error is returned to the LLM

**Acceptance Criterion:** Throughput is ≥500 queries/sec on a 10-connection pool against a local Postgres.

---

## Epic 17: Token Budget Governor

**Goal:** Users enforce per-team/per-project daily token budgets at the gateway — not in every client — with auto-throttle or model downgrade when limits are approached.

### Story 17.1: Per-team and per-project budget configuration

**As a** user,
**I want** to set daily/monthly token budgets for teams and projects in config,
**So that** spend is governed centrally.

**Acceptance Criteria:**

**Given** config defines `budgets.teams.<team>.daily: 100000`
**When** the proxy processes a request from that team
**Then** the request's tokens are deducted from the team's daily budget
**And** cumulative spend is updated in an in-memory store

**Given** the team has a project sub-budget `budgets.teams.<team>.projects.<project>.monthly: 1000000`
**When** the project's monthly spend exceeds 80% of the limit
**Then** a warning is logged to stderr
**And** a webhook is fired (if configured)

**Given** no budget is configured
**When** the proxy runs
**Then** the governor is disabled
**And** no overhead is added (NFR11)

### Story 17.2: Auto-throttle and downgrade at threshold

**As a** user,
**I want** the governor to throttle or downgrade to a cheaper model when the budget is hit,
**So that** I never go over budget without explicit consent.

**Acceptance Criteria:**

**Given** a team's daily budget is 100% consumed
**When** the next request arrives
**Then** the request is rejected with a structured `budget_exceeded` error
**And** exit code 1 is returned for CLI use; JSON-RPC error is returned for gateway use

**Given** a team's daily budget is 90% consumed
**When** the next request arrives
**Then** the request is allowed but routed to the configured "budget" provider (e.g., local model or cheapest tier)
**And** a stderr notice explains the downgrade

**Given** a hard cap is configured (e.g., `hard_cap: true`)
**When** the budget is exceeded
**Then** requests are rejected regardless of model choice

**Given** a soft cap is configured (default)
**When** the budget is exceeded
**Then** requests are downgraded but allowed
**And** the user can override per-call with `--ignore-budget` (CLI) or `X-Ignore-Budget: true` (HTTP header)

**Acceptance Criterion (NFR13):** Budget state is in-memory only; not persisted to disk.

---

## Epic 18: Cost Attribution Web Dashboard

**Goal:** Users (and finance teams) see real-time and historical token spend per server, per tool, per team, with drill-down and export.

### Story 18.1: Web dashboard served from LeanProxy

**As a** user,
**I want** to open `http://localhost:9090` and see a real-time cost dashboard,
**So that** I don't need a separate tool to visualize spend.

**Acceptance Criteria:**

**Given** the dashboard is enabled in config (default: on)
**When** the user opens the dashboard URL
**Then** an HTML page loads within 500ms
**And** a summary card shows: today's spend, week-to-date, top server, top tool

**Given** the dashboard is accessed from a non-loopback address
**When** the request arrives
**Then** the dashboard requires a bearer token (configured in `dashboard.token`)
**And** 401 is returned without it

**Given** the dashboard is disabled in config
**When** the proxy starts
**Then** no HTTP listener is bound for the dashboard

**Acceptance Criterion (NFR13):** Dashboard reads only aggregated metrics from the in-memory store; no prompt content is ever rendered.

### Story 18.2: Per-server / per-tool drill-down

**As a** user,
**I want** to click a server in the dashboard and see its tool-level breakdown,
**So that** I can identify which tools drive the most cost.

**Acceptance Criteria:**

**Given** the dashboard is loaded
**When** the user clicks a server row
**Then** a drill-down view shows: tool name, call count, token count, avg tokens/call, last invoked
**And** the view sorts by token count descending by default

**Given** the user applies a date filter (e.g., "last 7 days")
**When** the filter is applied
**Then** all charts and tables update to reflect the date range
**And** the URL updates with the filter as a query param (for sharing)

**Given** the user wants to see per-prompt breakdown
**When** they click "Show prompts" (opt-in)
**Then** a list of prompt hashes is shown with their cost
**And** no prompt content is displayed (only hashes for privacy)

### Story 18.3: CSV/JSON export for finance

**As a** user,
**I want** to export cost data as CSV or JSON,
**So that** my finance team can include it in monthly reports.

**Acceptance Criteria:**

**Given** the user runs `leanproxy report --export csv --since 2026-01-01`
**When** the command executes
**Then** a CSV file is written to `leanproxy-report-<date>.csv` with columns: timestamp, team, project, server, tool, tokens, estimated_cost

**Given** the user passes `--export json`
**When** the command executes
**Then** a JSON file is written with the same data structured as an array of objects

**Given** the export covers a large range (e.g., 90 days, 1M+ rows)
**When** the export runs
**Then** it streams the output without buffering all rows in memory (NFR2)
**And** a progress indicator is shown

**Acceptance Criterion (NFR4):** Export contains only aggregated metrics; no PII, secrets, or prompt content is included.

---

## Implementation Priority (Updated — Top 10 from Brainstorming)

| Priority | Epic | Story | Source Idea | Expected KPI Impact |
|:---------|:-----|:------|:------------|:--------------------|
| 🥇 CRITICAL | Epic 10 | 10.1-10.3 | #3 Anthropic Cache Bridge | 90% cache-read discount |
| 🥇 CRITICAL | Epic 11 | 11.1-11.3 | #1 MCP Registry Mirror | First-mover advantage |
| 🥇 CRITICAL | Epic 12 | 12.1-12.3 | #2 Semantic Cache | 60-80% repeat-call savings |
| 🥇 CRITICAL | Epic 13 | 13.1-13.3 | #35 Injection Firewall v2 | 95%+ injection recall |
| HIGH | Epic 17 | 17.1-17.2 | #13 Budget Governor | Prevents bill shock |
| HIGH | Epic 14 | 14.1-14.3 | #17 IDE Plugin | Adoption flywheel |
| MEDIUM | Epic 15 | 15.1-15.3 | #4 Model Router + #7 Local LLM | Cost/privacy flex |
| MEDIUM | Epic 18 | 18.1-18.3 | #11 Cost Dashboard | Enterprise sales |
| MEDIUM | Epic 16 | 16.1-16.3 | #77-79 First-Party Servers | Quality + lock-in prevention |

## Roadmap Mapping (Cross-Reference to Brainstorming Roadmap)

| Brainstorming Phase | This Doc Epics |
|:--------------------|:---------------|
| Phase 1 — Q3 2026 Quick Wins | Epic 10, 11, 12, 17 (4 epics) |
| Phase 2 — Q4 2026 Trust & Safety | Epic 13, 15 (2 epics) |
| Phase 3 — Q1 2027 DX & Adoption | Epic 14, 18 (2 epics) |
| Phase 4 — Q2-Q3 2027 Platform | Epic 15, 16 (continued) |
| Phase 5 — Wildcards | Deferred — see brainstorming doc |
