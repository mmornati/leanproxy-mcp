# Story 13.3: Red-team corpus + continuous regression test

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 13.3 |
| **Key** | leanproxy-13-3 |
| **Epic** | epic-13 — AI Safety — Prompt-Injection Firewall v2 |
| **Title** | Red-team corpus + continuous regression test |
| **Related FRs** | FR43 |
| **Related NFRs** | — |
| **Previous Story:** [13.2 configurable-actions](13-2-configurable-actions.md) |

## User Story

As a developer, I want a red-team corpus of known injection payloads shipped with the binary, so the classifier is regression-tested on every release.

## Acceptance Criteria (BDD Summary)

tests/security/injection_corpus.json (200 payloads) -> go test ./pkg/bouncer/... runs classifier against all; fails if recall <95%. New pattern -> add to corpus, test reruns, pattern appended to default list, changelog updated.

## Developer Context

### Technical Notes

tests/security/injection_corpus.json (NEW, 200 entries); pkg/bouncer/injection/classifier_test.go extended; CI gate via existing scripts/ci.sh.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-13-Story-13.3]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: AI Safety — Prompt-Injection Firewall v2

## File List

- See Technical Notes above
