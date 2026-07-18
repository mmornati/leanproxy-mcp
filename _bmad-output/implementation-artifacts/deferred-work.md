# Deferred Work

## Deferred from: code review of 10-3-cache-hit-rate-report (2026-06-23)

- [Review][Defer] Hardcoded pricing values with no update mechanism [pkg/cache/pricing.go] — acceptable for initial release; prices change over time
- [Review][Defer] float64 used for financial calculations [pkg/cache/pricing.go:64] — acceptable for display purposes

## Deferred from: code review of 13-1-injection-classifier (2026-07-18)

- [Review][Defer] **No recall corpus test for FR43 AC** — Story AC requires ≥95% recall on a 200-payload corpus. No corpus file or recall test exists. Deferred: requires labeled dataset, out of scope for this PR.
