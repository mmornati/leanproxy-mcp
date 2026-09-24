# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack

delegated: the documentation site stays on MkDocs Material (GitHub Pages via `.github/workflows/docs.yml`, `mkdocs build --strict`). The user allowed any stack; MkDocs was kept because it already gives search, navigation, edit links and a strict link check. The landing page is a Material theme override (`docs/overrides/home.html`) with its own CSS, so the homepage is not limited by the docs template.

## Users

Individual developers who run an AI coding client (Claude Code, Claude Desktop, Cursor, OpenCode, VS Code, Windsurf) with several MCP servers connected. They install and configure tools themselves, pay per token, and hand third-party MCP servers their credentials and filesystem.

## Product Purpose

LeanProxy-MCP is a single local Go binary that sits between the AI client and every MCP server. It cuts the tokens MCP costs (tool schemas and tool results) and adds a local security layer in front of third-party servers. Success: a developer installs it in minutes, sees a measured drop in tokens, and trusts it not to leak secrets.

## Positioning

"Token firewall": cost reduction and local security in one small binary, with no call to a third-party service. The combination is the claim: response-side token governor plus request/response injection classifier, tool pinning, per-tool policy and redaction. Numbers are measured by `make harness` against the real binary and published with their methodology and caveats (upper bounds, extra LLM turns, passthrough mode for clients that already search tools).

## Operating Context

- Installed via Homebrew, a release tarball, or `go install`; configured with `~/.config/leanproxy_servers.yaml` and added to the client as a stdio MCP server (`leanproxy-mcp server run --stdio`) or as an HTTP gateway (`serve`).
- Evaluated by reading the docs site at https://mmornati.github.io/leanproxy-mcp and the README benchmark table.
- Terminal-first users; the docs are read beside an editor.

## Capabilities and Constraints

Router (4 tools, ~318 tokens) or passthrough exposure; `search_tools` BM25 discovery; response token governor (projection, truncation, dedup, spill-store paging, optional Ollama summarization); secret redaction; prompt injection classifier; tool pinning; per-tool policy; stdio server sandboxing; `doctor security` OWASP MCP Top 10 report; web dashboard; OpenTelemetry; auditable savings report; marketplace; VS Code and JetBrains extensions. Code mode (`execute_code`) is a spike behind a build tag, not a shipped feature.

## Brand Commitments

Name: LeanProxy-MCP. Honest, measured voice: every number traceable to the harness, caveats stated next to claims. No logo exists yet.

## Evidence on Hand

- Benchmarks: `docs/benchmark-results.md`, README "Latest Benchmark" table (`make harness`).
- Comparison table vs MCPProxy, ToolHive, Docker MCP Gateway, LiteLLM (README, 2026-09).
- No customer logos, testimonials, pricing, or user counts exist. Do not fabricate any.

## Product Principles

1. Measured, not claimed: a number ships only with its method and its caveat.
2. Local-first: nothing leaves the machine unless the user opts in.
3. Safe by default, powerful by opt-in.
4. One binary, minutes to value.
