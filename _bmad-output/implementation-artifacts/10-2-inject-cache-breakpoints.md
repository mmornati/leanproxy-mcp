# Story 10.2: Auto-inject cache_control: ephemeral breakpoints

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 10.2 |
| **Key** | leanproxy-10-2 |
| **Epic** | epic-10 — Anthropic Prompt Caching Bridge |
| **Title** | Auto-inject cache_control: ephemeral breakpoints |
| **Related FRs** | balanced |
| **Related NFRs** | aggressive).|FR40|NFR11 |
| **Previous Story:** [10.1 detect-anthropic-calls](10-1-detect-anthropic-calls.md) |

## User Story

As a developer, I want LeanProxy to identify stable segments (system prompt, tool definitions) and inject Anthropic cache breakpoints, so the upstream cache hits on subsequent requests.

## Acceptance Criteria (BDD Summary)

Anthropic request w/ system + tools -> append cache_control:{type:ephemeral} to last tool and last system block. User-supplied cache_control -> skip + log debug. Strategy=off -> no injection. Strategy=aggressive (default) -> both; balanced -> largest block only. <1ms p95 overhead (NFR11).

## Developer Context

### Technical Notes

pkg/cache/breakpoint_injector.go (NEW): post-parse JSON transformer preserving user blocks; config-driven strategy enum (off

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

## Tasks/Subtasks

- [x] Task 1: Define `InjectStrategy` type and `BreakpointInjector` struct with functional options
  - [x] Write failing tests for strategy enum, constructor, and option pattern
  - [x] Implement `InjectStrategy` type, constants, and `BreakpointInjector` struct
- [x] Task 2: Implement `Inject` method with strategy-driven cache_control injection
  - [x] Write failing tests for aggressive strategy (both system + tools)
  - [x] Write failing tests for balanced strategy (largest block only)
  - [x] Write failing tests for off strategy (no injection)
  - [x] Write failing tests for user-supplied cache_control (skip + log debug)
  - [x] Implement `Inject` method with full strategy logic
- [x] Task 3: Add edge case handling
  - [x] Write tests for empty tools, empty system, malformed JSON, nil body
  - [x] Implement edge case handling
- [x] Task 4: Wire injector into `cmd/serve.go` with `--cache-strategy` flag
  - [x] Write integration tests for request pipeline integration
  - [x] Add `--cache-strategy` CLI flag to serve command
  - [x] Wire injector call after provider detection for Anthropic requests
- [x] Task 5: Benchmark for NFR11 (<1ms p95 overhead)
  - [x] Write and run benchmarks
  - [x] Verify benchmark results meet threshold

## References

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-10-Story-10.2]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: Anthropic Prompt Caching Bridge

## Dev Agent Record

### Debug Log

### Implementation Plan

1. Created `pkg/cache/breakpoint_injector.go` (NEW) with `InjectStrategy` type (`off`, `aggressive`, `balanced`), `BreakpointInjector` struct with functional options pattern, and `Inject([]byte)` method that post-parse JSON transforms Anthropic request bodies.
2. Created `pkg/cache/breakpoint_injector_test.go` (NEW) with 26 unit tests covering all ACs: aggressive (system+tools), balanced (largest block only), off (pass-through), user-supplied cache_control skip, edge cases, and 5 benchmarks.
3. Modified `cmd/serve.go` to add `--cache-strategy` CLI flag, initialize `breakpointInjector` at startup, and call `injectBreakpoints()` in all four request paths (sync, async, batch, batch-async) after provider detection.

### Completion Notes

- **26 unit tests** all passing (TDD: RED → GREEN confirmed)
- **Full regression suite**: 1118 tests pass, 0 failures
- **Benchmarks**: aggressive ~8µs, balanced ~3.4µs, off ~1.2ns, user-supplied ~3µs, large payload ~120µs — all well under 1ms p95 (NFR11 ✅)
- **go vet**: clean, no issues

## File List

- pkg/cache/breakpoint_injector.go (NEW)
- pkg/cache/breakpoint_injector_test.go (NEW)
- cmd/serve.go (MODIFY)

## Change Log

- 2026-06-23: Story initialized with task breakdown for implementation
- 2026-06-23: Implementation complete — all ACs satisfied, 26 tests passing, benchmarks under 1ms

## Status

Status: review
