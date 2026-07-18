# Story 11.2: Implement 'leanproxy add <server-id>' one-click install

Status: done

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 11.2 |
| **Key** | leanproxy-11-2 |
| **Epic** | epic-11 — MCP Registry Mirror & Discovery |
| **Title** | Implement 'leanproxy add <server-id>' one-click install |
| **Related FRs** | FR41 |
| **Related NFRs** | — |
| **Previous Story:** [11.1 registry-sync](11-1-registry-sync.md) |

## User Story

As a user, I want a single command to install and configure an MCP server from the registry, so I can add a tool without writing YAML.

## Acceptance Criteria (BDD Summary)

Registry synced + leanproxy add github -> download def, merge into leanproxy_servers.yaml, schedule start, show success with tool count + token-cost preview. Unknown server -> list up to 5 similar, non-zero exit. Existing name -> prompt or --force, graceful stop first.

## Developer Context

### Technical Notes

cmd/add.go (NEW): uses pkg/registry/feed + pkg/migrate to merge YAML; pkg/registry/lifecycle.go for graceful stop; token-cost preview from pkg/bouncer/snapshot.go.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-11-Story-11.2]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: MCP Registry Mirror & Discovery

## File List

- See Technical Notes above
