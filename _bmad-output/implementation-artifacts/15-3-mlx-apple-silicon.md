# Story 15.3: MLX / Apple Silicon support (experimental)

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 15.3 |
| **Key** | leanproxy-15-3 |
| **Epic** | epic-15 — Per-Tool Model Router & Local LLM Sidecar |
| **Title** | MLX / Apple Silicon support (experimental) |
| **Related FRs** | FR46 |
| **Related NFRs** | NFR12 |
| **Previous Story:** [15.2 ollama-sidecar](15-2-ollama-sidecar.md) |

## User Story

As an Apple Silicon user, I want LeanProxy to use MLX-based local models for the sidecar, so I get faster inference on M-series Macs without Ollama.

## Acceptance Criteria (BDD Summary)

sidecar.provider=mlx + macOS arm64 -> MLX runtime detected and loaded; model from ~/Library/Application Support/leanproxy/models/ loaded. Model file missing -> helpful error suggests 'ollama pull <model>' or download URL, abort startup. Opt-in via build tag; absent tag, binary behaves identically (NFR12).

## Developer Context

### Technical Notes

pkg/sidecar/mlx.go (NEW, build tag mlx): CGO binding to mlx-c via cgo; model dir under os.UserConfigDir() on darwin; feature detection in cmd/serve.go.

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-15-Story-15.3]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: Per-Tool Model Router & Local LLM Sidecar

## File List

- See Technical Notes above
