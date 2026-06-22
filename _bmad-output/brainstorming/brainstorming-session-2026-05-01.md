---
stepsCompleted: [1, 2, 3, 4]
inputDocuments: []
session_topic: 'LeanProxy-MCP: Token-optimized and Data-Secure MCP Proxy Service'
session_goals: 'Define architecture for minimal persistent context, lowest possible token usage, and data privacy/security for code/vibe-coding.'
selected_approach: 'AI-Recommended (First Principles, Resource Constraints, Reversal, Metaphor Mapping)'
techniques_used: ['First Principles Thinking', 'Resource Constraints', 'Metaphor Mapping', 'Anti-Solution', 'SCAMPER', 'Trend Riding', 'Jobs to be Done']
ideas_generated: [1, 2, 3, 4, 8, 9, 10, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 89, 90, 91, 92, 93, 94, 95, 96, 97, 98, 99, 100, 101, 102]
session_active: false
workflow_completed: true
session_continued: true
continuation_date: '2026-06-22'
extension_topic: 'New functionalities aligned with current MCP/AI market trends'
---

## Idea Organization and Prioritization

**Thematic Organization:**

**Theme 1: Context & Metadata Management (Priority)**
- **JIT Tool Definition (#1)**: Only send full schemas when the LLM is interested.
- **Shadow Manifesting (#2)**: Hide project/global config merging from the LLM; inject only relevant lists.
- **Smart Context Pruning (#18)**: Summarize history to keep the "hot" context lean.
- **Boilerplate Blindness (#20)**: Strip noise (imports/headers) from file reads.

**Theme 2: Protocol & Schema Optimization**
- **The Compactor (#8)**: Pre-distill MCP manifests during setup.
- **Tiered Schema Discovery (#4)**: Short signatures vs. Full manuals.

**Theme 3: Local Intelligence & Security (The Bouncer)**
- **The Bouncer (#16)**: Redact PII/Secrets as a side-effect of pruning.
- **Drafting Sidecar (#15)**: Use local models for tool discovery.

**Prioritization Results:**

- **Top Priority Ideas:** JIT Tool Definition, The Bouncer, and Shadow Manifesting. These form the core "LeanProxy-MCP" engine.
- **Quick Win Opportunities:** Shadow Manifesting and Shadow Tool List injection.
- **Breakthrough Concepts:** Boilerplate Blindness and Context Smuggling (using headers to bypass token counts).

## Session Summary and Insights

**Key Achievements:**
- Defined the "LeanProxy-MCP" architecture as a hybrid of Efficiency and Security.
- Solved the "Lazy Loading" friction through Shadow Manifesting.
- Identified "Offline Tool Compression" as a major token-saving strategy.

**Session Reflections:**
The shift from a "Simple Proxy" to a "Security & Efficiency Gatehouse" provided the necessary architectural "teeth" for the project. The First Principles approach was essential for stripping away standard agent assumptions.

[C] Complete - Generate final brainstorming session document

## Session Overview

**Topic:** LeanProxy-MCP: Token-optimized and Data-Secure MCP Proxy Service
**Goals:** Define architecture for minimal persistent context, lowest possible token usage, and data privacy/security for code/vibe-coding.

### Session Setup

The user wants to improve on `nexus-dev` by creating "LeanProxy-MCP"—a lightweight MCP proxy that acts as both a token firewall and a data privacy gate. The focus is on the simplest possible experience, lowest token overhead, and preventing data leaks (Secrets/PII) while reducing costs as inclusive plans disappear.

## Ideas Generated

...

**[Category #16]**: Content Redaction (The Bouncer)
*Concept*: A pre-flight filter that removes PII, secrets, and proprietary markers from outgoing messages.
*Novelty*: Saves tokens by omitting non-essential blocks rather than just masking, protecting data and budget simultaneously.

**[Category #17]**: The "Proprietary Pruner"
*Concept*: Configurable rules to prune internal documentation, boilerplate, or non-essential headers from source code or tool manifests.
*Novelty*: Direct token savings through data minimization.

**[Category #1]**: JIT (Just-In-Time) Tool Definition
*Concept*: The persistent context only contains a high-level "capability manifest". The Proxy only injects the full JSON schema for a specific tool *after* the LLM expresses interest.
*Novelty*: Moves heavy documentation out of the primary prompt, reducing static overhead by 70-90%.

**[Category #2]**: Shadow Manifesting
*Concept*: The Proxy dynamically injects the list of active MCP servers (Global + Project) directly into the `description` field of the gateway tools.
*Novelty*: Eliminates `list_servers` and "lazy loading" commands; handles configuration merging silently.

**[Category #3]**: Compressed Discovery
*Concept*: `search_tools` returns a "Lean Interface Definition" (YAML or custom shorthand) instead of full JSON Schema.
*Novelty*: Slashing discovery tokens by 60% by removing redundant JSON metadata.

**[Category #4]**: Tiered Schema Discovery
*Concept*: `search_tools` includes a `detail_level` parameter (`short` vs `full`).
*Novelty*: LLM manages its own token budget, only requesting full schemas when needed.

**[Category #8]**: The Compactor (Offline Tool Compression)
*Concept*: A one-time setup that uses a cheap model to "distill" raw MCP manifests into token-dense signatures and summaries.
*Novelty*: Moves the "understanding cost" to a setup phase, slashing discovery tokens by 50-80%.

**[Category #9]**: Cache-First Identity
*Concept*: The Proxy operates primarily from a distilled local cache, only querying live servers when the cache is stale.
*Novelty*: Instant discovery and zero-latency tool list injection.

**[Category #10]**: Stale Manifest Watcher
*Concept*: A background process that alerts the user/IDE when the tool configuration has changed and needs a "re-compression."
*Novelty*: Ensures the "Token-Efficient" view of the world stays in sync with real tool capabilities.

**[Category #13]**: Local Semantic Hashing
*Concept*: The Proxy prevents "Token Echo" by returning tiny references to data it has already sent in the same session.
*Novelty*: Eliminates redundant payment for the same file contents or tool outputs.

**[Category #14]**: Protocol Translation (JSON -> Compact)
*Concept*: The Proxy translates verbose IDE JSON into a custom, token-dense shorthand for the network trip to the provider.
*Novelty*: Slashing overhead by 30-50% through custom dialect compression.

**[Category #18]**: Smart Context Pruning
*Concept*: The Proxy automatically summarizes older conversation turns into "Snapshot Sentences," keeping only the most recent exchanges in high resolution.
*Novelty*: Prevents the "History Tax" from growing exponentially over long sessions.

**[Category #19]**: Intent-Gated Research
*Concept*: Blocks redundant or "just-in-case" tool calls from IDE agents unless the Proxy's local logic confirms they are strictly necessary for the current task.
*Novelty*: Stops "Token Leakage" caused by over-eager automated agents.

**[Category #20]**: Boilerplate Blindness
*Concept*: The Proxy strips standard imports, copyright headers, and repetitive boilerplate from file-read results before sending to the LLM.
*Novelty*: Focuses the LLM's "attention" (and your tokens) only on the functional code logic.

---

## Extension: New Functionalities Aligned with Current MCP/AI Market Trends (2026)

**Session Date:** 2026-06-22
**Techniques Used:** Cross-Pollination, SCAMPER, Anti-Solution (Black Swan)
**Ideas Generated:** 102 total (87 new in this extension)

### Phase 1 — Cross-Pollination (Market Pattern Borrowing)

**[Category #1]**: MCP Registry Mirror
*Concept*: LeanProxy subscribes to the official MCP Registry (2026 standard) and pre-caches curated, trusted servers — turning the gateway into a vetted tool store with one-click install.
*Novelty*: Combines discovery + curation + token-saving in one flow; beats manual YAML config.

**[Category #2]**: Semantic Prompt Cache (Helicone-style)
*Concept*: Hash tool-call payloads by semantic similarity (not exact match) so identical-intent requests reuse cached responses across sessions.
*Novelty*: Cuts token spend 60-80% on repeated workflows without exact-string matches.

**[Category #3]**: Anthropic Prompt Caching Bridge
*Concept*: Auto-inject cache_control: ephemeral breakpoints into tool definitions and system prompts so Anthropic's prompt cache hits ~90% of the time.
*Novelty*: Translates MCP-native output into cache-friendly structure for providers that support it.

**[Category #4]**: Model Router (LiteLLM-style)
*Concept*: Route sub-tasks to different providers based on cost/latency/quality — cheap model for list_tools, premium for invoke_tool reasoning.
*Novelty*: Per-tool model assignment based on declared "complexity tier" in the tool manifest.

**[Category #5]**: BYOK (Bring Your Own Key) Vault
*Concept*: Centralized encrypted key store for downstream API calls (OpenAI, Anthropic, custom) with usage-per-team attribution.
*Novelty*: Avoids re-entering keys per MCP server; integrates with 1Password/Bitwarden.

**[Category #6]**: Skills/Sub-Agent Host (Anthropic Skills pattern)
*Concept*: LeanProxy becomes a Skills runtime — packages of "tool + prompt + model" that activate on demand, with the prompt hidden from the LLM until needed.
*Novelty*: Reduces prompt overhead by lazy-loading skill definitions.

**[Category #7]**: Local LLM Sidecar (Ollama/MLX)
*Concept*: Embed a local LLM for pre-processing tool descriptions, summarization, and PII redaction — keeping sensitive data on-device.
*Novelty*: Privacy + cost: zero API spend on redaction and discovery.

**[Category #8]**: Computer-Use Guardrails (NeMo/LlamaGuard)
*Concept*: Intercept tool calls that touch the filesystem, shell, or browser and enforce policy (e.g., "no rm -rf", "no uploads outside /workspace").
*Novelty*: MCP-native guardrail layer.

**[Category #9]**: Code Sandbox (E2B integration)
*Concept*: invoke_tool for code-execution servers routes through an E2B sandbox by default, isolating host environment.
*Novelty*: Drop-in security for mcp-server-code-runner-style tools.

**[Category #10]**: Web Search Proxy (Tavily/Brave/Serper)
*Concept*: Standardize web-search MCP tools behind a single leanproxy.search() that picks the best provider per region/pricing.
*Novelty*: Multi-provider abstraction with cost-aware fallback.

### Phase 2 — Observability & Analytics

**[Category #11]**: Cost Attribution Dashboard (real-time)
**[Category #12]**: Prompt-level Token Breakdown
**[Category #13]**: Token Budget Governor
**[Category #14]**: Latency-aware Circuit Breaker v2
**[Category #15]**: A/B Test Router (Promptfoo/Braintrust integration)

### Phase 3 — Developer Experience

**[Category #16]**: leanproxy doctor CLI
**[Category #17]**: VS Code / JetBrains Plugin (live cost sidebar)
**[Category #18]**: Interactive Playground (leanproxy playground)
**[Category #19]**: Server Diff Tool
**[Category #20]**: Auto-Migration from Claude Desktop / Cursor / Windsurf / Zed

### Phase 4 — Multi-Agent Orchestration

**[Category #21]**: Sub-Agent Spawner (LangGraph/AutoGen bridge)
**[Category #22]**: Shared Memory Layer (mem0/Zep bridge)
**[Category #23]**: Agent-to-Agent (A2A) Tool Routing
**[Category #24]**: Hierarchical Task Decomposition

### Phase 5 — Multi-Modal & New Modalities

**[Category #25]**: Image-aware Tool Routing
**[Category #26]**: Voice Tool Bridge (Whisper/ElevenLabs)
**[Category #27]**: Browser-Use Proxy (Playwright/Computer Use)

### Phase 6 — Enterprise & Governance

**[Category #28]**: SSO + SCIM (Okta/Entra)
**[Category #29]**: Audit Log Streaming (Splunk/Datadog)
**[Category #30]**: PII Vault with Selective Disclosure

### Phase 7 — Open Ecosystem

**[Category #31]**: Server Marketplace (in-proxy, npm-style)
**[Category #32]**: Schema Sharing (P2P cache across teams)
**[Category #33]**: Plugin SDK (Go/TS/Python)
**[Category #34]**: Community Recipe Library

### Phase 8 — AI Safety & Guardrails

**[Category #35]**: Prompt-Injection Firewall v2 (output-side defense)
**[Category #36]**: Output Filter (LlamaGuard / NeMo Guardrails)
**[Category #37]**: Toxicity / Bias Scoring
**[Category #38]**: Deny-by-Default Mode (high-security posture)

### Phase 9 — Pricing & Business Model

**[Category #39]**: LeanProxy Cloud (managed SaaS)
**[Category #40]**: Cost Forecasting (ML-based)
**[Category #41]**: Per-Project Token Wallets (FinOps for AI)
**[Category #42]**: Vendor Marketplace Plugin (Cloudflare Workers/AWS Fargate/GCP Cloud Run)

### Phase 10 — Testing & Quality

**[Category #43]**: Replay Mode (VCR for MCP)
**[Category #44]**: Eval Hooks (Promptfoo/Braintrust)
**[Category #45]**: Regression Test Suite Generator

### Phase 11 — Schema Optimization

**[Category #46]**: Schema Minifier (production-grade)
**[Category #47]**: Example Generator (auto few-shot hints)
**[Category #48]**: Schema Versioning (drift detection)

### Phase 12 — Network & Edge

**[Category #49]**: Edge Mode (WASM, CDN worker)
**[Category #50]**: Geo-aware Provider Routing

### Phase 13 — Developer Delight

**[Category #51]**: leanproxy why (transparency for denials)
**[Category #52]**: Token Cost in IDE Inline Hints
**[Category #53]**: Slack/Discord Cost Notifier
**[Category #54]**: leanproxy bench (vs. native MCP)

### Phase 14 — Protocol & Framework Bridges

**[Category #55]**: OpenAI Function-Calling Bridge
**[Category #56]**: Anthropic tool_use Optimizer
**[Category #57]**: Google Gemini Function-Calling Bridge
**[Category #58]**: Azure OpenAI Bridge
**[Category #59]**: LangChain/LlamaIndex Tool Adapter
**[Category #60]**: Vercel AI SDK Adapter

### Phase 15 — Storage, State, Observability

**[Category #61]**: Stateful Session Replay (durability)
**[Category #62]**: Distributed Tracing (OpenTelemetry)
**[Category #63]**: Metrics Endpoint (Prometheus)

### Phase 16 — Resilience

**[Category #64]**: Smart Retry with Cost Awareness
**[Category #65]**: Fallback Tool Resolver
**[Category #66]**: Tool Dependency Graph

### Phase 17 — DevOps & Lifecycle

**[Category #67]**: leanproxy spec (OpenAPI-style for MCP)
**[Category #68]**: GitOps Config (PR-driven)
**[Category #69]**: Schema Linter

### Phase 18 — Privacy & Sovereignty

**[Category #70]**: On-Prem Air-Gapped Mode
**[Category #71]**: Region Pinning (GDPR/sovereign cloud)
**[Category #72]**: Differential Privacy Mode (telemetry)

### Phase 19 — Productivity

**[Category #73]**: Hot-Reload Config
**[Category #74]**: leanproxy trace (Interactive Replay)
**[Category #75]**: Config Templates Library
**[Category #76]**: YAML/JSON/TOML Auto-detection

### Phase 20 — First-Party Servers

**[Category #77]**: First-Party GitHub Server
**[Category #78]**: First-Party Filesystem Server
**[Category #79]**: First-Party Postgres/Redis Server

### Phase 21 — Output Handling

**[Category #80]**: Streaming Response Aggregator
**[Category #81]**: Auto-Summarization of Large Outputs
**[Category #82]**: Citation / Source Tracking

### Phase 22 — Market Positioning

**[Category #83]**: "Token Firewall" Trademark Campaign
**[Category #84]**: Public Token-Savings Leaderboard
**[Category #85]**: Annual "State of MCP Token Economy" Report

### Phase 23 — Vertical Specialization

**[Category #86]**: Healthcare Pack (HIPAA-compliant)
**[Category #87]**: Finance Pack (SOX/PCI)
**[Category #88]**: Legal Pack (privileged-document detection)

### Phase 24 — AI Evals

**[Category #89]**: SWE-bench-style Benchmark for MCP Gateways
**[Category #90]**: Cost-Quality Pareto Frontier Visualizer

### Phase 25 — Future-Proofing

**[Category #91]**: MCP Spec Compliance Tracker
**[Category #92]**: Experimental Features Flag
**[Category #93]**: Compatibility Matrix Page

### Phase 26 — Black-Swan / Anti-Solution (Competitive Defense)

**[Category #94]**: Schema Compiler (defends against smarter upstream schemas)
**[Category #95]**: Cross-Provider Cache Sharing (defends against provider-native caching)
**[Category #96]**: Hybrid Router (defends against local LLMs replacing cloud)
**[Category #97]**: Tool Broker (defends against IDE-native tools)
**[Category #98]**: Provider-Agnostic Neutrality (defends against first-party gateways)

### Phase 27 — Wildcards

**[Category #99]**: MCP Server Health Score Marketplace Signal
**[Category #100]**: leanproxy prompt (Interactive Tool Description Editor)
**[Category #101]**: Auto-Generated "Lean Schema" by Local LLM
**[Category #102]**: Token-Economy Simulator (leanproxy sim)

---

## Idea Organization and Prioritization (Extension)

### Thematic Clustering (102 ideas → 8 themes)

| Theme | # Ideas | Examples |
|:------|:-------:|:---------|
| **T1. Cost Optimization & Caching** | 12 | #2 Semantic Cache, #3 Anthropic Cache Bridge, #13 Budget Governor, #40 Forecasting |
| **T2. AI Safety & Guardrails** | 8 | #35 Injection Firewall v2, #36 Output Filter, #37 Toxicity Scoring, #38 Deny-by-Default |
| **T3. Multi-Provider & Protocol Bridges** | 9 | #4 Model Router, #5 BYOK Vault, #55-60 OpenAI/Gemini/LangChain/Vercel adapters |
| **T4. Observability & Analytics** | 9 | #11 Cost Dashboard, #12 Prompt Breakdown, #62 OTel, #63 Prometheus |
| **T5. Multi-Agent & Orchestration** | 6 | #21 Sub-Agent Spawner, #22 Shared Memory, #23 A2A Routing, #24 Task Decomposition |
| **T6. Developer Experience & DX** | 14 | #16 doctor CLI, #17 IDE Plugin, #18 Playground, #73 Hot-Reload, #74 trace |
| **T7. Enterprise & Governance** | 11 | #28 SSO/SCIM, #29 Audit Stream, #30 PII Vault, #70 Air-Gap, #86-88 Vertical Packs |
| **T8. Open Ecosystem & Marketplace** | 10 | #1 Registry Mirror, #31 Marketplace, #33 Plugin SDK, #34 Recipes |
| **Cross-cutting / Wildcards** | 23 | First-party servers (#77-79), edge mode (#49), output handling (#80-82), marketing (#83-85) |

### Prioritization Results (Top 10 by RICE-style scoring)

| Rank | # | Feature | Impact | Feasibility | Timing | Total |
|:----:|:--|:--------|:------:|:-----------:|:------:|:-----:|
| 1 | #3 | Anthropic Prompt Caching Bridge | 5 | 5 | Now | 15 |
| 2 | #1 | MCP Registry Mirror | 5 | 4 | Now | 14 |
| 3 | #2 | Semantic Prompt Cache | 5 | 4 | Now | 14 |
| 4 | #35 | Prompt-Injection Firewall v2 | 5 | 4 | Now | 14 |
| 5 | #17 | VS Code / JetBrains Plugin | 4 | 5 | Soon | 14 |
| 6 | #4 | Model Router (per-tool) | 4 | 4 | Soon | 13 |
| 7 | #7 | Local LLM Sidecar | 4 | 4 | Soon | 13 |
| 8 | #11 | Cost Attribution Dashboard | 4 | 4 | Soon | 13 |
| 9 | #77-79 | First-Party Servers | 4 | 3 | Later | 12 |
| 10 | #13 | Token Budget Governor | 4 | 4 | Soon | 12 |

### Phased Roadmap

#### Phase 1 — Quick Wins (Q3 2026, 6-8 weeks)
#3 Anthropic Cache Bridge, #2 Semantic Cache, #1 Registry Mirror, #13 Budget Governor

#### Phase 2 — Trust & Safety (Q4 2026, 8-10 weeks)
#35 Injection Firewall v2, #8 Computer-Use Guardrails, #7 Local LLM Sidecar, #70 Air-Gapped Mode

#### Phase 3 — DX & Adoption (Q1 2027, 10-12 weeks)
#17 IDE Plugin, #18 Playground, #4 Model Router, #11 Cost Dashboard, #75 Templates

#### Phase 4 — Platform & Scale (Q2-Q3 2027)
#77-79 First-Party Servers, #39 LeanProxy Cloud SaaS, #62-63 OTel/Prometheus, #55-60 Protocol Bridges

#### Phase 5 — Wildcards (Opportunistic)
#31 Marketplace, #42 Serverless Deploy, #84 Public Leaderboard, #21 Sub-Agent Spawner

### Top 5 Action Plans

**#3 Anthropic Prompt Caching Bridge**
- Week 1: Spike — detect Anthropic calls, find stable segments
- Week 2: Implement `cache_control: ephemeral` injection
- Week 3: Report cache hit rate via `leanproxy report`
- Week 4: Docs + blog post "Save 90% on Anthropic"
- Success: >70% cache hit rate, 1k stars from blog

**#1 MCP Registry Mirror**
- Week 1: Subscribe to MCP registry spec; design sync protocol
- Week 2: `leanproxy add <server-id>`; local schema cache
- Week 3: Trust scoring for unmaintained servers
- Week 4: `leanproxy marketplace` TUI
- Success: 100+ servers mirrored, community PRs

**#2 Semantic Prompt Cache**
- Week 1: Embed payloads (local Ollama or remote API)
- Week 2: Vector store (SQLite-vec default; Qdrant/Pinecone optional)
- Week 3: TTL + invalidation on schema change
- Week 4: Cache hit/miss dashboard
- Success: 60% hit rate on GitHub MCP after 1 week

**#35 Prompt-Injection Firewall v2**
- Week 1: Threat model — injection patterns in tool results
- Week 2: Local classifier (regex + heuristics)
- Week 3: Configurable actions (quarantine/redact/block/log)
- Week 4: Red-team corpus + tests
- Success: 95% catch rate on known patterns

**#17 VS Code + JetBrains Plugin**
- Week 1: Define plugin API (use #63 /metrics endpoint)
- Weeks 2-3: VS Code extension (TS) — status bar + webview
- Week 4: JetBrains plugin (Kotlin)
- Week 5: Polish + publish to marketplaces
- Success: 1k installs, 4.5★ rating in month 1

### Breakthrough Concepts

- **Provider-Agnostic Neutrality (#98)** — defends against first-party gateways from OpenAI/Anthropic
- **Cross-Provider Cache Sharing (#95)** — unique value no single provider can replicate
- **Public Token-Savings Leaderboard (#84)** — viral community flywheel
- **MCP Server Health Score (#99)** — ecosystem-wide reputation signal

### Implementation-Ready Ideas (next 90 days)

1. #3 Anthropic Cache Bridge (4 weeks)
2. #1 MCP Registry Mirror (4 weeks)
3. #2 Semantic Cache (4 weeks)
4. #13 Token Budget Governor (2 weeks)
5. #73 Hot-Reload Config (1 week)
6. #16 `leanproxy doctor` CLI (2 weeks)

## Session Summary and Insights (Extension)

**Key Achievements:**
- Generated 102 new feature ideas for LeanProxy-MCP aligned with 2026 MCP/AI market trends
- Identified 4 "Now-or-Never" features riding current market waves (Anthropic caching, MCP Registry, semantic cache, prompt injection defense)
- Built 5-phase roadmap spanning Q3 2026 → Q3 2027
- Surfaced 5 black-swan defensive features (Hybrid Router, Cross-Provider Cache, etc.) that protect against market disruption

**Session Reflections:**
The cross-pollination technique was most productive — borrowing patterns from Helicone (semantic cache), LiteLLM (model router), Anthropic Skills (lazy-loaded packages), and E2B (sandboxing) created features that feel inevitable rather than speculative. The anti-solution phase was crucial for identifying defensive moats (#95-98) that protect LeanProxy from market consolidation by upstream players.

**Key Strategic Insight:**
LeanProxy's moat is NOT in any single feature — it's in being the **provider-agnostic, neutral, OSS-governed layer** between agents and the chaotic MCP/tool ecosystem. Every feature should reinforce that neutrality. Features that lock users into one provider (e.g., a deep Anthropic-only optimization) should be balanced with cross-provider counterparts (e.g., cross-provider cache sharing).
