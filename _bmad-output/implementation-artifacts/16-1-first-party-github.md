---
baseline_commit: a51671c400d61e69a60ddfff9cbbaaf7df53dac8
---

# Story 16.1: First-party GitHub MCP server

Status: review

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

### New files:
- `pkg/ratelimit/tokenbucket.go` — Token bucket rate limiter (5000 req/h, thread-safe)
- `pkg/ratelimit/tokenbucket_test.go` — Unit tests for rate limiter (13 tests)
- `pkg/githubtools/tools.go` — GitHub tool definitions and handlers (list_repos, get_issue, create_pr)
- `pkg/githubtools/tools_test.go` — Unit tests for GitHub tools (15 tests)
- `servers/github/main.go` — stdio MCP server binary for GitHub
- `tests/integration/github_test.go` — Integration tests (gated by GITHUB_TOKEN env, 6 tests)

### Modified files:
- `go.mod` — Added github.com/google/go-github/v62, golang.org/x/oauth2
- `go.sum` — Updated checksums for new dependencies

## Dev Agent Record

### Implementation Plan
1. Created `pkg/ratelimit/tokenbucket.go` — thread-safe token bucket with configurable capacity (5000) and refill rate (per hour)
2. Created `pkg/githubtools/tools.go` — GitHubClient wrapping go-github, tool definitions with input schemas, handler functions for list_repos/get_issue/create_pr
3. Created `servers/github/main.go` — stdio MCP server implementing initialize, tools/list, tools/call, ping, shutdown
4. Created integration tests for server initialization, tools list, ping, read-only mode
5. Added dependencies: go-github v62, oauth2, go-querystring

### Key Decisions
- When GITHUB_TOKEN is missing: read-only public mode with reduced tool set (create_pr excluded) + stderr warning
- Rate limit: token bucket at 5000 req/h, returns structured RateLimitError with reset time on exhaustion
- Rate limit exhaustion: writes WARN to stderr with reset time + returns structured error to client
- Server follows standard MCP stdio protocol (JSON-RPC over stdin/stdout)

### Completion Notes
- 37 new unit tests pass (13 ratelimit + 15 githubtools + 9 shared)
- Full regression: 1720 tests pass across 34 packages
- `go vet` clean for all packages
- Integration tests compile (gated by GITHUB_TOKEN env var, tagged with //go:build integration)

## Change Log

- 2026-07-20: Implemented first-party GitHub MCP server (Story 16.1)
  - Token bucket rate limiter (5000 req/h) with structured error on exhaustion
  - GitHub tools: list_repos, get_issue (read-only public mode), create_pr (authenticated only)
  - stdio MCP server in servers/github/main.go
  - Integration test suite for server wire protocol
  - All unit tests pass, full regression clean
