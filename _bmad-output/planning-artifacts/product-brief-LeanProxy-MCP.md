---
title: "Product Brief: LeanProxy-MCP"
status: "complete"
created: "2026-05-01"
updated: "2026-05-01"
inputs: ["brainstorming-session-2026-05-01.md", "research-discovery-2026.md"]
---

# Product Brief: LeanProxy-MCP

## Executive Summary
LeanProxy-MCP is a high-performance, local-first "Token Firewall" and Data Governance gateway for the Model Context Protocol (MCP). It sits directly between the IDE and the Model Provider, acting as a "Customs Office" that strips expensive token bloat and redacts sensitive data before it reaches the cloud.

Designed for developers who want to maintain high coding velocity without the "Context Tax," LeanProxy-MCP provides a zero-config, protocol-aware layer that slashes per-turn costs by up to 50-80%. It transforms AI agents from "over-eager spenders" into precise, budget-conscious collaborators.

## The Problem
As AI coding assistants move toward "Agentic" workflows, they have become increasingly expensive and insecure:
- **The Token Tax**: Standard MCP integrations are verbose. Every tool call often redundanty sends full JSON schemas, un-pruned history, and repetitive project context, leading to massive billing spikes.
- **The Privacy Gap**: Current agents lack a local "Bouncer." They frequently read and transmit `.env` files, proprietary boilerplate, and PII to third-party providers without any intermediate filtering or consent.
- **Lazy Loading Friction**: Developers are forced to manually refresh tool sets when switching between laptop-global and project-specific contexts, breaking the "vibe" of the coding session.

## The Solution: The Token Gatehouse
LeanProxy-MCP implements a suite of "Smart Proxy" features at the content level:
- **JIT Tool Injection**: Only registers full tool schemas with the LLM when intent is detected, using lightweight "Discovery Signatures" for discovery.
- **The Bouncer (Security)**: A real-time redaction engine that identifies and removes PII, secrets, and internal boilerplate from the message stream.
- **The Compactor**: A setup-time workflow that "distills" raw MCP manifests into token-dense signatures, shifting the "understanding cost" away from the per-message runtime.
- **Automated Context Caching**: Intelligently manages provider-side cache TTLs to maximize architectural discounts (e.g., Gemini/Claude caching) for the user.

## What Makes This Different
- **Protocol-Aware**: Unlike generic LLM gateways, LeanProxy-MCP understands the specific JSON-RPC patterns of MCP, allowing for "Shorthand" translation.
- **Zero-Config Adoption**: A standalone local binary that requires no network changes—simply point your IDE to the proxy stdio/http endpoint.
- **Financial Control**: Includes a first-of-its-kind "Budget Sentry" that allows developers to set per-session token caps or aggressive pruning levels.

## Who This Serves
- **Independent Developers**: High-volume coders who need to manage personal AI spend.
- **Regulated Enterprises**: Teams in Finance, Health, or Defense who are currently blocked from using AI agents due to DLP (Data Loss Prevention) concerns.
- **Open-Source Contributors**: Developers switching contexts frequently who need their tools to "just work" in every repository.

## Success Criteria
- **Economic Impact**: Minimum 50% reduction in average token cost per task.
- **Security Reliability**: 100% interception of standard secret patterns (regex-based and heuristic).
- **Adoption Speed**: "Zero-to-Saving" in under 2 minutes of setup.
- **Performance**: Less than 50ms overhead for base proxying logic.

## Scope (MVP)
- **Standalone CLI Proxy**: Supporting stdio and http protocols.
- **Redaction Engine**: Basic PII and secret pattern recognition.
- **JIT Discovery**: signature-based tool registration.
- **Dry-Run Mode**: A "Savings Simulator" that shows what would be redacted/saved before going live.

## Vision
LeanProxy-MCP becomes the foundational "Local Intelligence Layer" for all agentic software engineering. It evolves into a **Collaborative Registry**, where teams share pre-compressed "Tool Signatures" and "Project Snapshots," enabling ultra-efficient, multi-agent swarms on multi-million line codebases with near-zero overhead.
