---
stepsCompleted: [1, 2, 3, 4, 5, 6, 7]
inputDocuments:
  - /Users/mmornati/Projects/tokengate-mcp/_bmad-output/planning-artifacts/prd.md
  - /Users/mmornati/Projects/tokengate-mcp/_bmad-output/planning-artifacts/product-brief-LeanProxy-MCP.md
workflowType: 'architecture'
project_name: 'tokengate-mcp'
user_name: 'mmornati'
date: '2026-05-01'
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
mkdir tokengate-mcp && cd tokengate-mcp
go mod init github.com/mmornati/tokengate-mcp
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
tokengate-mcp/
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

## Architecture Validation Results

### Coherence Validation ✅
- **Decision Compatibility:** Verified. All technology choices (Go, cobra, manual stream parsing) are aligned.
- **Pattern Consistency:** Verified. Structure, naming, and communication patterns align with Go idioms.
- **Structure Alignment:** Verified. The proposed directory structure fully supports the architecture.

### Requirements Coverage Validation ✅
- **Epic/Feature Coverage:** All core proxy and security features are supported.
- **Functional Requirements:** All 23 FRs are supported.
- **Non-Functional Requirements:** Performance (<50ms), security (in-memory), and distribution (<20MB) are prioritized.

### Implementation Readiness Validation ✅
- **Decision Completeness:** All critical decisions documented.
- **Structure Completeness:** Complete project tree defined.
- **Pattern Completeness:** All naming/structure/communication patterns defined.

### Architecture Readiness Assessment
- **Overall Status:** READY FOR IMPLEMENTATION
- **Confidence Level:** High
