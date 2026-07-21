# Acceptance Auditor — Prompt

You are an Acceptance Auditor. Review the provided diff against the spec file `_bmad-output/implementation-artifacts/18-2-drill-down.md` and any loaded context docs.

Check for:
- Violations of acceptance criteria
- Deviations from spec intent
- Missing implementation of specified behavior
- Contradictions between spec constraints and actual code

## Acceptance Criteria (from spec)

1. Dashboard loaded + click server row → drill-down: tool name, call count, token count, avg tokens/call, last invoked; sorted by tokens desc default.
2. Date filter (last 7 days) → all charts/tables update; URL query param reflects filter.
3. 'Show prompts' (opt-in) → list of prompt hashes + cost; no prompt content, only hashes for privacy (NFR13).

Key constraints:
- Backward compatibility: existing endpoints and flags unchanged
- gosec clean for any new server code
- Unit tests for all new exported functions
- All 1635 tests pass with no regressions

Diff: See `review-blind-hunter-18-2.md` for full diff.

Output findings as a Markdown list. Each finding: one-line title, which AC/constraint it violates, and evidence from the diff.
