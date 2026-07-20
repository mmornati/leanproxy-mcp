---
baseline_commit: b5514f51295502ba421497deb512fb99de298ad5
---

# Story 17.1: Per-team and per-project budget configuration

Status: in-progress

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 17.1 |
| **Key** | leanproxy-17-1 |
| **Epic** | epic-17 — Token Budget Governor |
| **Title** | Per-team and per-project budget configuration |
| **Related FRs** | FR48 |
| **Related NFRs** | NFR11 |

## User Story

As a user, I want to set daily/monthly token budgets for teams and projects in config, so spend is governed centrally.

## Acceptance Criteria (BDD Summary)

budgets.teams.<team>.daily: 100000 + request from team -> tokens deducted, in-memory cumulative updated. Sub-budget budgets.teams.<team>.projects.<project>.monthly > 80% -> stderr warn + webhook (if configured). No budget configured -> governor disabled, no overhead (NFR11).

## Tasks/Subtasks

- [x] 1. Define budget config schema (BudgetConfig, TeamBudget, ProjectBudget structs) in pkg/budget/config.go
- [x] 2. Implement in-memory BudgetStore with team daily and project monthly tracking in pkg/budget/store.go
- [x] 3. Implement Governor that enforces budget deduct, thresholds, and alert callbacks in pkg/budget/governor.go
- [x] 4. Implement webhook dispatcher in pkg/webhook/webhook.go
- [x] 5. Write unit tests for config types
- [x] 6. Write unit tests for BudgetStore (deduct, reset, threshold check)
- [x] 7. Write unit tests for Governor (deduct, disabled when no budget, exceed)
- [x] 8. Write unit tests for webhook dispatcher

## Developer Context

### Technical Notes

pkg/budget/ governor.go + store.go (NEW): in-memory token buckets keyed by team[/project]; webhook dispatcher pkg/webhook/; config schema budgets: section.

### File Structure

New files:
- pkg/budget/config.go — BudgetConfig, TeamBudget, ProjectBudget types
- pkg/budget/store.go — BudgetStore with in-memory token tracking
- pkg/budget/governor.go — Governor enforcing budgets
- pkg/webhook/webhook.go — Webhook dispatcher
- pkg/budget/config_test.go — Tests for config types
- pkg/budget/store_test.go — Tests for BudgetStore
- pkg/budget/governor_test.go — Tests for Governor
- pkg/webhook/webhook_test.go — Tests for webhook dispatcher

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

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-17-Story-17.1]
- [Source: _bmad-output/brainstorming/brainstorming-session-2026-05-01.md] (original market-trend idea)
- Related epic: Token Budget Governor

## Dev Agent Record

### Debug Log

- Created pkg/budget/config.go with BudgetConfig, TeamBudget, ProjectBudget types
- Created pkg/budget/store.go with BudgetStore: EnsureTeam, EnsureProject, DeductTeam, DeductProject, CheckProjectThreshold
- Created pkg/budget/governor.go with Governor: Enabled, Deduct methods, threshold alert callback
- Created pkg/webhook/webhook.go with Dispatcher: NewDispatcher, SendAlert
- Added methods to BudgetAlert struct to satisfy webhook.BudgetAlert interface
- Fixed DeductProject error handling in governor (was silently ignoring project budget exceed)

### Review Findings

- [ ] [Review][Patch] Per-team webhook URL never dispatches HTTP alerts [pkg/budget/governor.go:31-33, pkg/budget/governor.go:80-104] — `NewGovernor` creates a single `webhook.Dispatcher` only from `config.WebhookURL`. The per-team `webhook_url` is resolved in `Deduct()` and passed to `buildAlertCallback()`, but the closure uses `g.webhook` which is nil when only the team-level URL is set. Per-team webhook URLs silently never fire HTTP alerts. Fix: create a local dispatcher inside `buildAlertCallback` when `webhookURL != ""`.
- [ ] [Review][Patch] Monthly budget resets daily, not monthly [pkg/budget/store.go:82-86, pkg/budget/store.go:102-106] — Both `DeductTeam` and `DeductProject` use `time.Now().Truncate(24*time.Hour)` and day comparison for their reset logic. Monthly counters reset every day instead of at month boundaries. Fix: compare year/month of `now` vs `resetDay`.
- [ ] [Review][Patch] TOCTOU race in CheckProjectThreshold [pkg/budget/store.go:187-198] — `CheckProjectThreshold` releases the project mutex before checking the threshold percentage. Another goroutine can modify `monthlyUsed` between the unlock and the percentage check, causing stale/false threshold warnings. Fix: hold the lock through the read and percentage calculation.
- [ ] [Review][Patch] Zero daily limit silently clamped to 1 [pkg/budget/store.go:48-49] — `EnsureTeam` silently clamps `dailyLimit <= 0` to 1 without warning. A user setting `daily: 0` expecting unlimited gets a 1-token daily budget. Fix: log a warning or treat 0 as unlimited.
- [ ] [Review][Patch] Missing test for per-team webhook with HTTP dispatch [pkg/budget/governor_test.go] — No test validates that a team-level webhook URL triggers an actual HTTP alert when the global webhook URL is empty.
- [ ] [Review][Patch] Team deduct succeeds but project fails — no rollback [pkg/budget/governor.go:52-65] — Team daily deduct happens before project monthly deduct. If team succeeds but project fails, team tokens are consumed without rollback. Fix: reorder checks (project first) or add rollback.
- [ ] [Review][Patch] Duplicate log entry in buildAlertCallback webhook case [pkg/budget/governor.go:93-103] — The webhook closure also logs via `g.logger.Warn` in addition to the log in `CheckProjectThreshold`, producing duplicate entries. Fix: remove the redundant log from the webhook closure.
- [x] [Review][Defer] No integration wiring to proxy pipeline [entire diff] — Governor is created and tested but never instantiated in the proxy request handling path. Deferred: expected for a follow-up integration story.
- [x] [Review][Defer] Webhook dispatcher lacks retry logic [pkg/webhook/webhook.go:104] — HTTP POST has no retry on transient failure; alerts are silently dropped if endpoint is unavailable. Deferred: enhancement beyond spec scope.

### Completion Notes

Implemented per-team and per-project budget configuration. The BudgetConfig schema supports budgets.teams.<team>.daily and budgets.teams.<team>.projects.<project>.monthly limits. BudgetStore tracks cumulative usage in-memory using the existing TokenBucket for daily refills and simple counters for monthly usage. Governor enforces deducts, and when a project sub-budget exceeds 80% threshold it logs a warning to stderr and optionally dispatches a webhook alert. When no budgets are configured, the governor is disabled and adds zero overhead.

## File List

- pkg/budget/config.go (NEW)
- pkg/budget/store.go (NEW)
- pkg/budget/governor.go (NEW)
- pkg/webhook/webhook.go (NEW)
- pkg/budget/config_test.go (NEW)
- pkg/budget/store_test.go (NEW)
- pkg/budget/governor_test.go (NEW)
- pkg/webhook/webhook_test.go (NEW)
- _bmad-output/implementation-artifacts/17-1-budget-config.md (MODIFIED)

## Change Log

- Implemented budget config package (pkg/budget/) with config, store, and governor
- Implemented webhook dispatcher package (pkg/webhook/)
- 46 tests passing across new packages (zero regressions across all 1862 tests)
- Story status moved from "ready-for-dev" to "review"
