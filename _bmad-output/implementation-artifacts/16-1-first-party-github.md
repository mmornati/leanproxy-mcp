# Story 16.1: First-party GitHub MCP server

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 16.1 |
| **Key** | leanproxy-16-1 |
| **Epic** | epic-16 — First-Party MCP Servers (GitHub / Filesystem / DB) |
| **Title** | First-party GitHub MCP server |
| **Related FRs** | FR47 |
| **Related NFRs** | NFR4 |

## User Story

As a user, I want a LeanProxy-bundled GitHub MCP server with secure defaults, so I don't have to vet and install a third-party option.

## Acceptance Criteria (BDD Summary)

LeanProxy installed + leanproxy add github (or bundled by default) -> leanproxy-mcp-github registered; reads GITHUB_TOKEN from env (NFR4); rate limit 5000 req/h enforced. Rate-limit error -> structured error with reset time + stderr warn. Missing token -> 'read-only public' mode w/ reduced tool set + notice. Integration test: list_repos, get_issue, create_pr against GitHub API.

## Developer Context

### Technical Notes

servers/github/ (NEW): main.go stdio MCP server using github.com/google/go-github/v62; tools in pkg/githubtools/; rate limiter pkg/ratelimit/tokenbucket.go; integration test in tests/integration/github_test.go (gated by GITHUB_TOKEN env).

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-16-Story-16.1]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: First-Party MCP Servers (GitHub / Filesystem / DB)

## File List

- See Technical Notes above
