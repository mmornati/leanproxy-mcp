# Story 13.2: Configurable actions (quarantine / redact / block / log)

Status: ready-for-dev

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

## File List

- See Technical Notes above
