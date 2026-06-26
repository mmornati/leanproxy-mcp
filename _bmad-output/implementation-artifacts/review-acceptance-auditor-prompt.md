# Acceptance Auditor — Code Review

**Instructions:** You receive the code diff below, the implementation spec, and any loaded context docs. Review the diff against the spec and context docs. Check for: violations of acceptance criteria, deviations from spec intent, missing implementation of specified behavior, and contradictions between spec constraints and actual code.

## Spec File

Content of `_bmad-output/implementation-artifacts/11-2-one-click-install.md`:

```
# Story 11.2: Implement 'leanproxy add <server-id>' one-click install

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 11.2 |
| **Key** | leanproxy-11-2 |
| **Epic** | epic-11 — MCP Registry Mirror & Discovery |
| **Title** | Implement 'leanproxy add <server-id>' one-click install |
| **Related FRs** | FR41 |
| **Previous Story:** 11.1 registry-sync |

## User Story

As a user, I want a single command to install and configure an MCP server from the registry, so I can add a tool without writing YAML.

## Acceptance Criteria (BDD Summary)

Registry synced + leanproxy add github -> download def, merge into leanproxy_servers.yaml, schedule start, show success with tool count + token-cost preview. Unknown server -> list up to 5 similar, non-zero exit. Existing name -> prompt or --force, graceful stop first.

## Developer Context

### Technical Notes
cmd/add.go (NEW): uses pkg/registry/feed + pkg/migrate to merge YAML; pkg/registry/lifecycle.go for graceful stop; token-cost preview from pkg/bouncer/snapshot.go.

### Architecture Compliance
- camelCase Go, kebab-case CLI flags
- log/slog to stderr; errors wrapped with fmt.Errorf %w
- Static binary <20MB; Homebrew + curl|sh install preserved
- Backward compatibility: existing endpoints and flags unchanged

### Testing Requirements
- Unit tests for all new exported functions
- Integration tests for any HTTP/MCP wire changes
- gosec clean for any new server code (Epic 16)
```

## Output format

Markdown list. Each finding: one-line title, which AC/constraint it violates, and evidence from the diff.

```
- [acceptance] Title of the finding
  AC: Which specific acceptance criterion is violated
  Evidence: ```go
  relevant code line(s)
  ```
  Explanation: ...
```

If all acceptance criteria are met, output: "All acceptance criteria satisfied."

---

**Diff file:** `_bmad-output/implementation-artifacts/story-11.2-review-diff.txt`

Paste the contents of that file below this line and run your audit against the spec above.
