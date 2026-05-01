---
title: "Product Brief Distillate: LeanProxy-MCP"
type: llm-distillate
source: "product-brief-LeanProxy-MCP.md"
created: "2026-05-01"
purpose: "Token-efficient context for downstream PRD creation"
---

## Technical Context & Constraints
- **Protocol Foundations**: Must support MCP's JSON-RPC over `stdio` and `http`. Deep content inspection is required for redaction, necessitating a robust, non-blocking parser to avoid high latency.
- **Local-First Architecture**: Standalone CLI tool. Must be portable and easy to install (e.g., single binary) to minimize adoption friction.
- **Provider Compatibility**: Designed to work with Anthropic (Claude Code), Google (Gemini CLI), OpenAI, and OpenCode (IDE). Should leverage provider-specific features like "Context Caching."
- **Performance Budget**: Target overhead of <50ms. High-aggression token stripping or model-based redaction (using a local sidecar) must be optimized to hit this target.

## Requirements Hints
- **JIT Tool Injection**: Signature-based discovery where the full JSON schema is only provided upon explicit intent or `get_tool_schema` call.
- **The Bouncer (Redaction)**: Configurable patterns for PII, AWS keys, `.env` values, and proprietary boilerplate.
- **Shadow Manifesting**: Automatic merging of global `~/.config/mcp.json` and project-local `mcp.json` without user manual intervention.
- **Budget Sentry**: Per-session token caps and real-time usage visualization (similar to "Dry Run" mode).
- **Offline Compactor**: One-time distillation of remote MCP manifests into local, token-dense signatures.

## Scope Signals & Roadmap
- **MVP (In-Scope)**: Core proxy, signature-based JIT discovery, basic regex/heuristic redaction, shadow manifesting, and Dry-Run mode.
- **Deferred (Out-of-Scope)**: The separate "Memory Service" (RAG/Persistent Memory), multi-model orchestration, and a GUI.
- **Strategic Direction**: Moving from a simple proxy to a "Security & Efficiency Gatehouse."

## Competitive Intelligence & Market Gaps
- **Enterprise Gateways**: Existing solutions (Portkey, Portway) focus on infrastructure/RBAC. LeanProxy-MCP fills the gap for **content-aware, developer-centric** context engineering.
- **Token Optimization**: 2026 state-of-the-art includes Dynamic Context Pruning (DyCP) and Hierarchical Summarization. LeanProxy-MCP should implement these as optional "Aggressive Mode" settings.
- **The "Lost-in-the-Middle" Problem**: Reducing long-session history to "Snapshot Sentences" preserves reasoning quality while slashing history costs.

## Rejected / Deferred Ideas
- **Aggressive Pass-through (Deferred)**: The decision to keep "Pass-through by Default" for MVP ensures 100% compatibility while allowing "Aggressive Mode" to be an opt-in configuration.
- **Manual Tool Loading**: Rejected in favor of "Shadow Manifesting" to remove developer friction.

## Open Questions
- **Sidecar Performance**: Can a local LLM (e.g., Llama-3-8B) perform redaction/validation within the 50ms latency budget?
- **Plugin Integration**: How much more context can an IDE-specific plugin (OpenCode) provide to the proxy compared to pure `stdio` monitoring?
- **Protocol Breakage**: How does the proxy handle binary streams or non-standard MCP extensions without failing the session?
