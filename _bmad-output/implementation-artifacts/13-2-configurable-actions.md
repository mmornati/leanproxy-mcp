---
baseline_commit: e43a46311bbd689f14ad14891b126b029f5fcfd0
---

# Story 13.2: Configurable actions (quarantine / redact / block / log)

Status: review

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 13.2 |
| **Key** | leanproxy-13-2 |
| **Epic** | epic-13 — AI Safety — Prompt-Injection Firewall v2 |
| **Title** | Configurable actions (quarantine / redact / block / log) |
| **Related FRs** | FR43 |
| **Related NFRs** | — |
| **Previous Story:** [13.1 injection-classifier](13-1-injection-classifier.md) |

## User Story

As a user, I want to choose what happens when a high-risk result is detected, so the policy matches my security posture.

## Acceptance Criteria (BDD Summary)

risk>=80 + action=block -> drop, error to LLM, critical stderr alert. risk>=50 & <80 + quarantine -> move to ~/.leanproxy/quarantine/<id>.json, return stub '[CONTENT_QUARANTINED - review at ...]', warn log. risk>0 & <50 + log -> forward unchanged, debug entry. leanproxy doctor --security -> counts by action taken.

## Developer Context

### Technical Notes

pkg/bouncer/injection/actions.go (NEW): action dispatcher w/ policy map; extend cmd/doctor.go with --security flag; quarantine dir under UserConfigDir.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-13-Story-13.2]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: AI Safety — Prompt-Injection Firewall v2

## Tasks/Subtasks

- [x] Create action dispatcher with policy map (pkg/bouncer/injection/actions.go)
- [x] Write unit tests for action dispatcher (block, quarantine, redact, log)
- [x] Wire config Policies to Dispatcher via BuildDispatcher
- [x] Create cmd/doctor.go with --security flag and doctor security subcommand
- [x] Fix import cycle: remove registry dependency from injection/actions.go
- [x] Run all tests and verify no regressions

## Dev Agent Record

### Implementation Notes
- actions.go already existed from story 13-1 with full Action types, Rule/Dispatcher/ActionResult structs, DefaultRules, and block/quarantine/redact/log implementations
- Fixed import cycle: removed registry.LeanProxyDir() dependency, inlined the logic using os.UserHomeDir()
- Created actions_test.go with 25 tests covering boundary conditions (risk thresholds), quarantine file writes, readonly fallback, custom rules, and dispatcher lifecycle
- Added BuildDispatcher() method to Config for wiring YAML policies to the Dispatcher
- Created cmd/doctor.go with doctor command and `doctor security` subcommand showing policy configuration and quarantine statistics
- Added test coverage for config policies including YAML loading with custom policies

### Completion Notes
- Actions dispatcher fully functional with default rules (80-100→block, 50-79→quarantine, 1-49→log)
- All exported functions have unit tests
- Config.Policies correctly wired to BuildDispatcher
- doctor security command shows policy config and quarantine dir status
- Import cycle resolved, build clean, go vet clean

## File List

- pkg/bouncer/injection/actions.go (modified: removed registry import cycle)
- pkg/bouncer/injection/actions_test.go (new: 25 tests)
- pkg/bouncer/injection/config.go (modified: added BuildDispatcher method)
- pkg/bouncer/injection/config_test.go (modified: added policy tests)
- cmd/doctor.go (new: doctor command with --security flag)

## Change Log

- Fixed import cycle: injection → registry → migrate → injection
- Added 25 unit tests for action dispatcher (boundary conditions, quarantine, custom rules)
- Added Config.BuildDispatcher() to wire YAML policies to Dispatcher
- Added cmd/doctor.go with doctor security subcommand
- All 1571 tests pass, go vet clean
