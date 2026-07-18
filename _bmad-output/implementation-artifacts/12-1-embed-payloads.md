# Story 12.1: Embed tool-call payloads via local or remote model

Status: done

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 12.1 |
| **Key** | leanproxy-12-1 |
| **Epic** | epic-12 — Semantic Prompt Cache |
| **Title** | Embed tool-call payloads via local or remote model |
| **Related FRs** | FR42 |
| **Related NFRs** | NFR11,NFR12 |

## User Story

As a developer, I want LeanProxy to compute a semantic embedding of every tool-call payload, so semantically similar calls match even when not textually identical.

## Acceptance Criteria (BDD Summary)

Cache enabled -> embed (tool name + args) using configured embedder; store alongside response. local:ollama -> call Ollama; unreachable -> fall back to exact-match + warn. remote:openai -> call embedder API, key from env (NFR12); missing key -> fail startup clearly. <5ms p95 (NFR11).

## Developer Context

### Technical Notes

pkg/cache/embedder/ interface + ollama.go, openai.go (NEW); integrate into pkg/bouncer for outbound payloads; lazy embedding with goroutine pool.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-12-Story-12.1]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: Semantic Prompt Cache

## File List

- See Technical Notes above
