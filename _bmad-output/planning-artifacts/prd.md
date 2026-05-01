---
stepsCompleted: [step-01-init, step-02-discovery, step-02b-vision, step-02c-executive-summary, step-03-success, step-04-journeys, step-05-domain, step-06-innovation, step-07-project-type, step-08-scoping, step-09-functional, step-10-nonfunctional, step-11-polish, step-12-complete]
releaseMode: phased
inputDocuments:
  - product-brief-LeanProxy-MCP.md
  - product-brief-LeanProxy-MCP-distillate.md
  - brainstorming-session-2026-05-01.md
documentCounts:
  briefCount: 1
  researchCount: 0
  brainstormingCount: 1
  projectDocsCount: 0
workflowType: 'prd'
classification:
  projectType: 'CLI Tool'
  domain: 'Developer Tools'
  complexity: 'Medium'
  projectContext: 'greenfield'
---

# Product Requirements Document - LeanProxy-MCP

**Author:** mmornati
**Date:** 2026-05-01

## Executive Summary

LeanProxy-MCP is a specialized "Token Firewall" and local proxy designed to make AI-assisted development economically sustainable and secure. It addresses the "Token Tax" inherent in modern Model Context Protocol (MCP) integrations, where verbose schemas and repetitive context significantly inflate LLM provider bills. By positioning itself as a transparent layer between the IDE and Model Provider, LeanProxy-MCP allows independent developers to maintain high-velocity "vibe-coding" workflows while reducing persistent context overhead by 50-80%.

### What Makes This Special

While standard gateways focus on infrastructure-level management, LeanProxy-MCP is **content-aware** and protocol-specific. It treats privacy and efficiency as the same architectural problem: by redacting sensitive data (secrets, PII) and pruning boilerplate "noise" (imports, headers), it simultaneously secures the codebase and slashes the token bill. Its "Shadow Manifesting" capability removes the friction of "Lazy Loading" by silently merging global and project configurations, providing a better developer experience than native MCP integrations at a fraction of the cost.

## Project Classification

- **Project Type:** CLI Tool
- **Domain:** Developer Tools
- **Complexity:** Medium
- **Project Context:** Greenfield

## Success Criteria

### User Success
- **Billing Impact**: Users see a measurable 50-80% reduction in token consumption for repetitive tasks (e.g., tool discovery and boilerplate reads).
- **Setup Speed**: A developer can go from `npm install` (or equivalent) to a running proxy with their first MCP server in under 2 minutes.
- **Silent Utility**: The tool merges global and project configs so seamlessly that the user "forgets" they ever had a lazy-loading problem.

### Business Success
- **Adoption**: Achieve 500+ GitHub Stars within the first 6 months of launch.
- **Contribution**: At least 5 community-contributed "Tool Compression Signatures" for popular MCP servers (e.g., AWS, Slack, Confluence).
- **Recognition**: Featured as a recommended "Context Engineering" tool in the MCP ecosystem.

### Technical Success
- **Performance**: Maintain an average processing overhead of <50ms per request.
- **Reliability**: 100% successful redaction of standard secret patterns (API keys, .env values) with zero crashes during high-volume JSON-RPC streams.
- **Protocol Integrity**: Zero breakage of the standard MCP JSON-RPC protocol for all supported `stdio` servers.

### Measurable Outcomes
- **The "Savings Report"**: The `dry-run` mode accurately predicts token savings within a 10% margin of error compared to live execution.
- **Portability**: The binary runs consistently across macOS, Linux, and Windows with zero platform-specific config required for the core proxy.

## Product Scope

### MVP - Minimum Viable Product
- **Local CLI Proxy**: Native support for the `stdio` protocol.
- **The Bouncer**: Pre-configured regex/heuristic redaction for secrets and PII.
- **Shadow Manifesting**: Automatic merging of global and local `mcp.json` files.
- **Dry-Run Mode**: CLI flag to simulate and report potential token savings.
- **JIT Discovery**: signature-based tool registration to minimize initial context.

### Growth Features (Post-MVP)
- **The Compactor**: Automated "distillation" of remote manifests using a cheap LLM.
- **HTTP/SSE Support**: Extending proxy capabilities to remote MCP servers.
- **Budget Sentry**: Hard-capping token spend per session with immediate termination.
- **Boilerplate Blindness**: Intelligent stripping of common imports and headers from file reads.

### Vision (Future)
- **Drafting Sidecar**: Using a local, tiny LLM (Llama-3-8B) for zero-cost redaction and discovery.
- **IDE Extensions**: Deep integration with OpenCode/VS Code to provide rich UI-based usage metrics and "1-click" config management.

## User Journeys

### Journey 1: Alex's "Aha!" Moment (The Success Path)
- **Persona**: Alex, an Independent Vibe-Coder.
- **Opening Scene**: Alex is juggling three different microservices. Every time he switches projects, he loses "vibe" because his IDE's MCP tools are either stale or bloated, and his monthly token bill has just hit $300.
- **Rising Action**: Alex installs the **LeanProxy-MCP** CLI and runs it in `dry-run` mode. He continues his normal workflow, watching the proxy log its "potential savings" in a side terminal.
- **Climax**: After one hour, the proxy reports: *"Simulated Savings: 65,000 tokens ($0.98 saved). 4 potential secret leaks intercepted."*
- **Resolution**: Alex flips to live mode. The proxy silently handles all project-specific config merges. He codes faster, the IDE feels snappier due to JIT discovery, and his token spend is finally under control.

### Journey 2: Sam's "Sleepless Night" (The Security/Edge Case)
- **Persona**: Sam, an Enterprise Security Engineer.
- **Opening Scene**: A junior developer on Sam's team accidentally includes a `.env` file in a large file-read request to a cloud-based LLM.
- **Rising Action**: Because the team uses **LeanProxy-MCP** as a mandatory local layer, "The Bouncer" intercepts the `stdio` JSON-RPC stream before it's encrypted and sent to the provider.
- **Climax**: The proxy identifies the `STRIPE_SECRET_KEY` pattern and instantly replaces it with `[SECRET_REDACTED_BY_LEANPROXY]`.
- **Resolution**: The secret never reaches the cloud. Sam sees the local log entry the next morning and is relieved that the "Token Firewall" did its job.

### Journey 3: The "Silent Setup" (Admin/Ops Journey)
- **Persona**: A Developer setting up a complex multi-MCP environment.
- **Opening Scene**: The user has 5 global MCP servers (Git, Slack, Jira, etc.) and a project-specific "Internal API" MCP server.
- **Rising Action**: They `cd` into the project directory. **LeanProxy-MCP** detects both the `~/.config/mcp.json` and the local `./mcp.json`.
- **Climax**: The "Shadow Manifesting" engine merges the two lists and "Compacts" the discovery signatures into a single, token-dense manifest.
- **Resolution**: When the IDE starts, the developer sees all 6 servers available. There was no manual loading, no terminal commands to run, and no "discovery bloat" in the initial system prompt.

### Journey Requirements Summary
- **Capability 1**: High-fidelity JSON-RPC parsing for `stdio` and `http` to enable "The Bouncer."
- **Capability 2**: A robust configuration engine that merges global and local manifests with conflict resolution.
- **Capability 3**: A `dry-run` simulation engine that calculates token counts without forwarding requests.
- **Capability 4**: A JIT tool injection system that keeps the initial system prompt lightweight.

## Innovation & Novel Patterns

### Detected Innovation Areas
- **JIT Tool Discovery signatures**: A novel "Tiered Discovery" pattern that replaces verbose JSON schemas with lightweight signatures, only injecting full definitions when intent is detected.
- **The Bouncer (Semantic Redaction)**: An intelligent interception layer that treats privacy and token efficiency as a single problem, redacting secrets while simultaneously pruning boilerplate "noise."
- **Shadow Manifesting**: A silent configuration engine that eliminates "Lazy Loading" friction by automatically merging multi-level MCP manifests in the background.

### Market Context & Competitive Landscape
- **Content-Aware Proxying**: While existing gateways focus on infrastructure (rate limiting, auth), LeanProxy-MCP operates at the content level of the JSON-RPC protocol, filling a gap for a developer-centric "Token Firewall."
- **Paradigm Shift**: Moving from "Prompt Engineering" (managed by the LLM) to "Context Engineering" (managed by the Proxy) to ensure maximum signal-to-noise ratio in every request.

### Validation Approach
- **Savings Simulation**: The `dry-run` mode provides an empirical validation of token savings before a developer commits to the live proxy.
- **Sidecar Benchmarking**: Testing the latency impact of local model-based redaction against a strict <50ms performance budget.

### Risk Mitigation
- **Pass-through Safety**: The "Pass-through by Default" strategy ensures 100% protocol compatibility, with aggressive optimizations being opt-in to mitigate the risk of breaking tool logic.

## CLI & Developer Tool Specific Requirements

### Project-Type Overview
LeanProxy-MCP is a performance-optimized Go CLI that serves as a specialized MCP proxy. It is designed to be primarily scriptable for standard JSON-RPC communication while providing a rich set of management commands for context optimization and server orchestration.

### Technical Architecture Considerations
- **Orchestration Layer**: The system must manage a lifecycle of multiple sub-processes (local stdio MCP servers) and coordinate their I/O streams into a single optimized model-facing interface.
- **IDE-Proxy Communication**: A local Unix/Windows socket will be established to allow IDE extensions (like OpenCode) to query proxy metrics, force compactions, and update configurations without interrupting the primary `stdio` stream.

### Command Structure & Scripting
- **Primary Mode**: Standard `leanproxy` start command defaults to `stdio` JSON-RPC mode.
- **Management Commands**:
  - `compactor`: Triggers manual re-distillation of server manifests.
  - `server [add|remove|list]`: Manages the local/remote server registry.
  - `context [rebuild|prune]`: Manages local context snapshots and history summaries.
- **Scriptability**: All commands must adhere to POSIX standards for return codes and support non-interactive execution.

### Output Formats
- **Protocol**: Strict adherence to the Model Context Protocol (JSON-RPC 2.0).
- **Reports**: `dry-run` and `savings` reports will be formatted in **Markdown** by default to allow for high-fidelity rendering within IDE \"system message\" or \"preview\" panels.

### Configuration Schema
- **Global Config**: Reads from `~/.config/mcp_config.json` for laptop-wide tool access.
- **Local Config**: Supports a project-level `leanproxy.yaml` (or `mcp_local.json`) to provide a clean separation between standard MCP data and proxy-specific optimization settings (redaction rules, token caps).
- **Redaction Rules**: Custom regex patterns for \"The Bouncer\" can be defined in the local config or via environment variables.

### Distribution & Installation
- **Universal Access**: Provided as a standalone binary via a `curl | sh` universal installer.
- **Package Management**: Official support for `homebrew` (macOS/Linux) and `go install` for developer convenience.
- **Platform Matrix**: Native builds for Intel/Silicon macOS, x64/ARM64 Linux, and Windows.

## Project Scoping & Phased Development

### MVP Strategy & Philosophy

**MVP Approach**: **Problem-Solving MVP**. We are laser-focused on solving the \"Token Tax\" and \"Secret Leak\" problems for independent developers using Go's performance to ensure zero friction.
**Resource Requirements**: Single Go Developer / Open-Source Lead.

### MVP Feature Set (Phase 1)

**Core User Journeys Supported**:
- Alex's Success Path (Dry-run and live savings).
- Sam's Security Path (Standard secret redaction).
- The Silent Setup (Shadow config merging).

**Must-Have Capabilities**:
- **Core CLI Engine**: Go binary with `stdio` proxying.
- **JIT Discovery signatures**: signature-based tool registration that defers full JSON schema injection.
- **The Bouncer (V1)**: Real-time regex-based redaction of common secret patterns and PII.
- **Shadow Manifesting**: Silent merging of global and project-specific configuration files.
- **The Compactor (V1)**: Automatic distillation of raw MCP manifests into token-dense discovery signatures (using a cheap LLM model).
- **Dry-Run Mode**: CLI flag and simulation engine to report potential savings and security alerts in Markdown.

### Post-MVP Features

**Phase 2 (Growth)**:
- **`http/sse` Support**: Extending the \"Token Firewall\" to remote and web-based MCP servers.
- **Budget Sentry**: Hard-capping and session termination based on real-time token spend.
- **Boilerplate Blindness**: Content-aware stripping of common imports, headers, and license blocks.
- **Local Persistence**: A caching layer for distilled signatures.

**Phase 3 (Vision)**:
- **Drafting Sidecar**: Offloading redaction and discovery to a local, tiny LLM (e.g., Llama-3-8B).
- **IDE Extensions**: Deep integration with OpenCode and VS Code for rich UI metrics.

### Risk Mitigation Strategy

**Technical Risks**: JSON-RPC over `stdio` can be brittle; mitigation via a robust state machine and strict pass-through by default.
**Market Risks**: Adoption depends on zero friction; mitigation via `curl | sh` installer and Markdown savings reports.
Resource Risks**: Open-source maintenance; mitigation via clear documentation and \"Distillation Signatures\" registry for community contributions.

## Functional Requirements

### 1. Tool Orchestration & Proxying
- **FR1**: The system can intercept and route JSON-RPC traffic between an IDE and multiple local `stdio` MCP servers.
- **FR2**: The system can manage the lifecycle (start/stop/restart) of configured MCP sub-processes.
- **FR3**: The system can merge global and project-specific MCP manifests into a single runtime registry.
- **FR4**: Users can dynamically add or remove MCP servers from the active proxy registry via CLI commands.
- **FR5**: The system can route specific tool calls to the correct underlying MCP server based on the merged registry.

### 2. Context Optimization
- **FR6**: The system can register tools with the Model Provider using \"Discovery Signatures\" (minimal name/description).
- **FR7**: The system can intercept `get_tool_schema` requests and inject full JSON schemas only for requested tools (JIT Discovery).
- **FR8**: The system can \"compact\" raw third-party MCP manifests into token-dense signatures using a distillation workflow.
- **FR9**: Users can force a re-distillation of any MCP server manifest to refresh stale discovery signatures.
- **FR10**: The system can prune redundant imports and copyright boilerplate from file-read results (Boilerplate Blindness).

### 3. Data Governance (The Bouncer)
- **FR11**: The system can scan outgoing JSON-RPC messages for sensitive data patterns (API keys, secrets, PII).
- **FR12**: The system can redact identified sensitive data with a standardized placeholder (`[SECRET_REDACTED]`).
- **FR13**: Users can define custom redaction patterns using regex in their local project configuration.
- **FR14**: The system can operate entirely in-memory to prevent local persistence of sensitive intercepted data.
- **FR15**: The system can alert the user via an out-of-band channel (stderr) when a redaction event occurs.

### 4. Developer Experience & Interface
- **FR16**: Users can run the proxy in a non-destructive `dry-run` mode to simulate savings and security alerts.
- **FR17**: Users can interact with the proxy via a standard POSIX-compliant CLI (Go binary).
- **FR18**: The system provides a local Unix/Windows socket for high-fidelity communication with IDE extensions.
- **FR19**: Users can install the system via a universal shell script or platform-specific package managers (e.g., Homebrew).
- **FR20**: The system provides automated shell completion for all management subcommands.

### 5. Reporting & Insights
- **FR21**: The system can calculate and report real-time token savings per session.
- **FR22**: The system can generate Markdown-formatted reports summarizing \"Total Tokens Saved\" and \"Security Risks Intercepted.\"
- **FR23**: The system can provide real-time status of all active proxied servers and their health.

## Non-Functional Requirements

### Performance
- **Latency**: The system shall add an average processing overhead of **<50ms** per JSON-RPC request.
- **Throughput**: The system shall handle JSON payloads up to **50MB** (common in large file reads) without crashing or exceeding 200ms of latency.
- **Resource Footprint**: The standalone binary shall remain **<20MB** in size to ensure fast distribution and minimal memory usage.

### Security
- **Local-Only Processing**: The system shall execute all redaction and optimization logic **locally in-memory**. No unredacted user data shall ever be persisted to disk or sent to LeanProxy-MCP's own servers.
- **Redaction Integrity**: The \"Bouncer\" shall use an **allow-list approach** for its core redaction patterns to minimize false negatives and ensure 100% interception of standard secret formats.
- **Process Isolation**: The proxy shall run each MCP server in its own sub-process to prevent cross-server data leakage or state interference.

### Reliability
- **Protocol Fidelity**: The system shall ensure **bit-perfect pass-through** for all non-intercepted JSON-RPC messages, ensuring zero breakage of the standard MCP protocol.
- **Process Health**: The proxy shall detect and report the failure of any underlying MCP process within **1 second** and provide a graceful recovery path for the IDE session.

### Observability
- **Operational Transparency**: The system shall output real-time health and savings metrics (tokens saved, secrets redacted) to **stderr** to avoid polluting the primary protocol stream.
- **Audit Logging**: Users can enable a local, rotated JSON log file to audit redaction events for enterprise compliance.


