# Story 15.2: Ollama sidecar integration (re-routing to local LLM)

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 15.2 |
| **Key** | leanproxy-15-2 |
| **Epic** | epic-15 — Per-Tool Model Router & Local LLM Sidecar |
| **Title** | Ollama sidecar integration (re-routing to local LLM) |
| **Related FRs** | FR46 |
| **Related NFRs** | NFR4 |
| **Previous Story:** [15.1 per-tool-model-routing](15-1-per-tool-model-routing.md) |

## User Story

As a user, I want redaction, summarization, and discovery to run on a local Ollama model, so sensitive data never leaves my machine for those tasks.

## Acceptance Criteria (BDD Summary)

sidecar.provider=ollama + sidecar.model=llama3.1:8b + Bouncer needs redaction (FR12) and regex misses -> send to Ollama, use redacted output. Ollama unreachable -> fall back to 'redact aggressively' ([VALUE_REDACTED]) + stderr warn. No sidecar config -> disabled, Bouncer uses regex only. When enabled, no payload ever sent to remote for redaction/discovery/summarization (NFR4).

## Developer Context

### Technical Notes

pkg/sidecar/ollama.go (NEW): HTTP client to /api/generate; hook into pkg/bouncer redaction path; telemetry flag for fallback count; detect runtime via existing pkg/health.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-15-Story-15.2]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: Per-Tool Model Router & Local LLM Sidecar

## File List

- See Technical Notes above
