# Story 10.1: Detect Anthropic API calls in the proxy stream

Status: review

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 10.1 |
| **Key** | leanproxy-10-1 |
| **Epic** | epic-10 — Anthropic Prompt Caching Bridge |
| **Title** | Detect Anthropic API calls in the proxy stream |
| **Related FRs** | FR40 |
| **Related NFRs** | NFR9,NFR11 |

## User Story

As a developer, I want LeanProxy to detect when an outgoing request is bound for the Anthropic API, so caching logic is only applied where supported.

## Acceptance Criteria (BDD Summary)

Given an outbound URL matching an Anthropic endpoint -> tag provider=anthropic and log to stderr (NFR9). Non-Anthropic -> tag provider=other, skip caching, no overhead. Multi-provider config -> matcher loads from leanproxy.yaml and reloads on SIGHUP.

## Developer Context

### Technical Notes

pkg/cache/provider_detector.go (NEW): pattern matcher per provider; slog debug; SIGHUP hot-reload via existing cmd/serve.go signal handling.

### File Structure

New files listed in technical notes; modify existing files only where required.

### Architecture Compliance

- camelCase Go, kebab-case CLI flags
- log/slog to stderr; errors wrapped with fmt.Errorf %w
- Static binary <20MB; Homebrew + curl|sh install preserved
- Backward compatibility: existing endpoints and flags unchanged

### Testing Requirements

- Unit tests for all new exported functions
- Integration tests for any HTTP/MCP wire changes
- Benchmark for any new hot path (target <1ms p95 overhead unless otherwise stated)
- gosec clean for any new server code (Epic 16)

## References

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-10-Story-10.1]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: Anthropic Prompt Caching Bridge

## File List

- See Technical Notes above

## Review Findings

Code review performed via the bmad-code-review workflow (PR #238). Findings triaged into patch / defer / dismiss categories.

### Patch — fixed in this review

- [x] [Review][Patch] Prefix matching treats raw URL strings, allowing `https://api.anthropic.com.evil.com` to be misclassified as Anthropic [pkg/cache/provider_detector.go:101-107]
- [x] [Review][Patch] Empty pattern entry (`patterns: [""]`) silently matches every URL via `HasPrefix(x,"")` [pkg/cache/provider_detector.go:133-142]
- [x] [Review][Patch] `WithConfigReader(nil)` causes nil-deref panic on `Reload()` [pkg/cache/provider_detector.go:59-63]
- [x] [Review][Patch] No size limit on YAML input — DoS risk via huge SIGHUP-triggered reload [pkg/cache/provider_detector.go:123-127]
- [x] [Review][Patch] Detect result unused — no downstream cache gating wired [cmd/serve.go x4]
- [x] [Review][Patch] SIGHUP reload panic crashes the process (no recover) [cmd/serve.go:187-211, pkg/cache/provider_detector.go:150-160]
- [x] [Review][Patch] `TestConcurrentAccess` asserts nothing — false safety [pkg/cache/provider_detector_test.go:92-114]
- [x] [Review][Patch] Global `providerDetector` reassigned without synchronization [cmd/serve.go:47,154-161]
- [x] [Review][Patch] Provider name has no validation — accepts arbitrary content [pkg/cache/provider_detector.go:133-141]
- [x] [Review][Patch] URL with port `https://api.anthropic.com:8443` falls through [pkg/cache/provider_detector.go:86-96]
- [x] [Review][Patch] Four duplicated detection blocks in handlers (drift risk) [cmd/serve.go x4]
- [x] [Review][Patch] SIGHUP arriving during shutdown mishandled [cmd/serve.go:184-211]
- [x] [Review][Patch] Provider name collision with built-in ("anthropic" custom override) [pkg/cache/provider_detector.go:132-142]
- [x] [Review][Patch] Patterns in YAML not trimmed/validated [pkg/cache/provider_detector.go:133-142]
- [x] [Review][Patch] Provider name "other" from YAML shadows `ProviderOther` [pkg/cache/provider_detector.go:133-142]
- [x] [Review][Patch] Dry-run preview omits `providers_config` flag [cmd/serve.go:82-87]
- [x] [Review][Patch] `prefixes` sub-slices pin yaml-decoded memory [pkg/cache/provider_detector.go:132-142]
- [x] [Review][Patch] Missing tests: empty patterns, whitespace patterns, huge config, panic recovery, WithConfigReader, Load() positive path [pkg/cache/provider_detector_test.go]
- [x] [Review][Patch] SIGHUP reload no-op when `--providers-config` unset — spec says matcher reloads from leanproxy.yaml [cmd/serve.go:190-192, pkg/cache/provider_detector.go:150-159]
- [x] [Review][Patch] `providerPattern` struct field alignment cosmetic [pkg/cache/provider_detector.go:21-24]
- [x] [Review][Patch] `TestWithLogger` only exercises no-config-path branch [pkg/cache/provider_detector_test.go:131-139]
- [x] [Review][Patch] `Load()` nil reader close panic [pkg/cache/provider_detector.go:111-121]
- [x] [Review][Patch] `providerDetector` package-level var shared mutable state in tests [cmd/serve.go:47]
- [x] [Review][Patch] No test asserting reload error keeps old patterns [pkg/cache/provider_detector_test.go]

### Decision-needed — resolved

- [x] [Review][Decision] Multi-provider config file path — spec says "leanproxy.yaml" but the implementation uses a separate file via `--providers-config`. Resolved: keep separate file (cleaner separation, matches PR description), but wire SIGHUP-aware `Reload()` to handle the no-config case with an explicit warning so reload behavior is documented. The downstream stories (10.2/10.3) will use the same flag.

### Defer — pre-existing or out-of-scope for this PR

- [x] [Review][Defer] Production `server.Address` may be empty because pools don't register entries with the registry — pre-existing wiring concern [cmd/serve.go:293 + pkg/pool] — deferred, downstream integration
- [x] [Review][Defer] NFR11 target mismatch (spec <1ms vs PR description <10ms) — deferred, align with product
- [x] [Review][Defer] NFR9 stderr destination not explicitly verified in this diff — deferred, covered by existing `initLogger` convention
- [x] [Review][Defer] Double-SIGHUP — channel buffer may drop signals — deferred, edge case in operator behavior
- [x] [Review][Defer] Directory/symlink config-path edge cases — deferred, common Go filesystem semantics
- [x] [Review][Defer] Logger captured at construction time, not lookup — deferred, by design
- [x] [Review][Defer] Case sensitivity of URL matching undocumented — deferred, intentional based on existing test
