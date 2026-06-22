# Story 16.2: First-party Filesystem MCP server with safe defaults

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 16.2 |
| **Key** | leanproxy-16-2 |
| **Epic** | epic-16 — First-Party MCP Servers (GitHub / Filesystem / DB) |
| **Title** | First-party Filesystem MCP server with safe defaults |
| **Related FRs** | FR47 |
| **Related NFRs** | NFR2 |
| **Previous Story:** [16.1 first-party-github](16-1-first-party-github.md) |

## User Story

As a user, I want a Filesystem MCP server restricted to a workspace root by default, so accidental rm -rf or path traversal is impossible.

## Acceptance Criteria (BDD Summary)

filesystem.allowed_roots config + server init -> only paths under roots accepted; ../etc/passwd returns permission error. No allowed_roots -> refuses to start, directs to configure. Read 50MB file -> streamed (NFR2), bounded memory. Zero CVEs in gosec static analysis on every release.

## Developer Context

### Technical Notes

servers/filesystem/ (NEW): root containment via filepath.Clean + os.Root (Go 1.24+); streaming via io.Pipe; gosec in CI scripts/ci.sh.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-16-Story-16.2]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: First-Party MCP Servers (GitHub / Filesystem / DB)

## File List

- See Technical Notes above
