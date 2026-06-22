# Story 16.3: First-party Postgres / Redis servers with pooling

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 16.3 |
| **Key** | leanproxy-16-3 |
| **Epic** | epic-16 — First-Party MCP Servers (GitHub / Filesystem / DB) |
| **Title** | First-party Postgres / Redis servers with pooling |
| **Related FRs** | FR47 |
| **Related NFRs** | NFR8 |
| **Previous Story:** [16.2 first-party-filesystem](16-2-first-party-filesystem.md) |

## User Story

As a user, I want first-party DB servers that use connection pooling by default, so I get high throughput without leaking connections.

## Acceptance Criteria (BDD Summary)

Postgres MCP + pool_size=10 + 50 concurrent tool calls -> <=10 DB connections opened, 11-50 queue (FR31/Epic 7 patterns). DB unreachable + query in flight -> pool detects <1s (NFR8), retries up to 3x w/ exponential backoff. Long query > statement_timeout -> connection released, structured error to LLM. >=500 q/s on 10-conn pool against local Postgres.

## Developer Context

### Technical Notes

servers/postgres/ + servers/redis/ (NEW): use github.com/jackc/pgx/v5/pgxpool + github.com/redis/go-redis/v9; configurable pool_size, statement_timeout; throughput bench in tests/bench/db_test.go.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-16-Story-16.3]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: First-Party MCP Servers (GitHub / Filesystem / DB)

## File List

- See Technical Notes above
