# Deferred Work

## Deferred from: code review of 10-3-cache-hit-rate-report (2026-06-23)

- [Review][Defer] Hardcoded pricing values with no update mechanism [pkg/cache/pricing.go] — acceptable for initial release; prices change over time
- [Review][Defer] float64 used for financial calculations [pkg/cache/pricing.go:64] — acceptable for display purposes

## Deferred from: code review of 13-1-injection-classifier (2026-07-18)

- [Review][Defer] **No recall corpus test for FR43 AC** — Story AC requires ≥95% recall on a 200-payload corpus. No corpus file or recall test exists. Deferred: requires labeled dataset, out of scope for this PR.

## Deferred from: code review of 15-1-per-tool-model-routing (2026-07-19)

- [Review][Defer] ComplexityTier validation at config load — `pkg/migrate/config.go:90` lacks validation; invalid values silently fall back to medium. Pre-existing pattern (other fields also unvalidated at parse time).
- [Review][Defer] GetComplexityTier dot-less method handling — `pkg/router/router.go:38-42` constructs `method.method` for dot-less names. Pre-existing issue inherited from `Route()`.

## Deferred from: code review of 15-2-ollama-sidecar (2026-07-19)

- [Review][Defer] No telemetry exposure for fallback count — metrics endpoint for sidecar is out of scope for first implementation
- [Review][Defer] No large-content guard for sidecar — model-specific context windows are outside this story's scope
- [Review][Defer] `pkg/health` integration not implemented — sidecar `Healthy()` is sufficient for v1

## Deferred from: code review of 15-3-mlx-apple-silicon (2026-07-19)

- [Review][Defer] Story file `baseline_commit` frontmatter is misleading — story claims `baseline_commit: 42c06c67...` but changes are uncommitted on main. Defer: dev should commit before tagging review complete.
- [Review][Defer] Story Dev Agent Record claims "All acceptance criteria satisfied" — but A1/A2/A4 are unmet. Defer: amend Dev Agent Record on next edit.

## Deferred from: code review of 16-2-first-party-filesystem (2026-07-20)

- [Review][Defer] Multi-root validation name mismatch — `resolvePathWithinRoots` suggests multi-root checks but only single root via `os.OpenRoot` is used. Pre-existing design limitation.
- [Review][Defer] Unbounded content size in write_file — out of scope for this story.
- [Review][Defer] No concurrency protection on FilesystemClient — not triggered by serial stdin architecture.

## Deferred from: code review of 16-3-first-party-db-servers (2026-07-20)

- [Review][Defer] Missing LEANPROXY_REDIS_DB env var [servers/redis/main.go] — `Config.DB` field exists but has no corresponding env var. Not part of spec requirements.
- [Review][Defer] go.sum has stale testify entries [go.sum] — Old `stretchr/testify v1.9.0` entries remain after upgrade. `go mod tidy` would clean this up.
- [Review][Defer] isFatalError uses string matching instead of pgx error codes [pkg/postgresql/tools.go:196-201] — Works for common cases but may not match all pgx structured errors.
