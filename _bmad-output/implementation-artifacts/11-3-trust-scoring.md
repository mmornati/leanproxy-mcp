# Story 11.3: Surface trust score and maintenance status

Status: done

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 11.3 |
| **Key** | leanproxy-11-3 |
| **Epic** | epic-11 — MCP Registry Mirror & Discovery |
| **Title** | Surface trust score and maintenance status |
| **Related FRs** | FR41 |
| **Related NFRs** | — |
| **Previous Story:** [11.2 one-click-install](11-2-one-click-install.md) |

## User Story

As a user, I want to see a trust score, last-updated date, and open-issue count for each registry server, so I avoid installing abandoned/malicious tools.

## Acceptance Criteria (BDD Summary)

leanproxy marketplace search <query> -> table: name, trust (0-100), last release, open issues, downloads, est tokens/turn. Trust<40 + leanproxy add -> warning requiring --i-understand-the-risks. Unavailable data -> '-' placeholder, no warning.

## Developer Context

### Technical Notes

pkg/registry/trust.go (NEW): heuristic score from registry metadata (release recency, issue count, downloads); cmd/marketplace_search.go (NEW).

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-11-Story-11.3]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: MCP Registry Mirror & Discovery

## File List

- See Technical Notes above
