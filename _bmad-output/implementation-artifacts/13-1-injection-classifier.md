# Story 13.1: Build a local prompt-injection classifier

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 13.1 |
| **Key** | leanproxy-13-1 |
| **Epic** | epic-13 — AI Safety — Prompt-Injection Firewall v2 |
| **Title** | Build a local prompt-injection classifier |
| **Related FRs** | FR43 |
| **Related NFRs** | NFR11 |

## User Story

As a developer, I want a regex + heuristic-based local classifier for known injection patterns, so poisoned tool results are caught without calling a remote model.

## Acceptance Criteria (BDD Summary)

Result w/ 'ignore previous instructions' or 'you are now...' -> risk_score 0-100 based on weighted matches; preserve original payload. No matches -> risk_score=0, no overhead (NFR11). Custom pattern in leanproxy.yaml -> load on startup, individual enable/disable. >=95% recall on 200-payload corpus (FR43 AC).

## Developer Context

### Technical Notes

pkg/bouncer/injection/ classifier.go + patterns.go (NEW); regex + weighted heuristics; config-driven custom patterns; ship default pattern corpus in pkg/bouncer/injection/patterns_default.yaml.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-13-Story-13.1]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: AI Safety — Prompt-Injection Firewall v2

## File List

- See Technical Notes above
