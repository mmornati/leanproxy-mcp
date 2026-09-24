# Savings Report (issue #324)

`leanproxy-mcp report` produces an auditable savings report built entirely
from counters the proxy actually records while it runs. It never estimates
or extrapolates a number that was not measured, and every figure is
labelled **measured** or **estimated** with its estimator named. This page
is the report's methodology: what each category counts, its baseline, and
how it maps to [`docs/benchmark-results.md`](benchmark-results.md)'s harness
numbers.

See `docs/commands.md`'s [`report`](commands.md#report-auditable-savings-report)
section for the CLI flags and sample output.

## Why a rewrite

Before issue #324, `leanproxy-mcp report`/`savings`/`cost` computed a
"native cost" by **simulating** what an unoptimized client would have sent,
from a model, not a measurement. Nothing in the live request pipeline fed
those trackers (`pkg/utils.SavingsTracker`, `reporter.CostTracker`'s
`Track`/`TrackCostFromStrings`), so their numbers were always the
estimated/zero placeholder state. `savings` and `cost` are kept for
backward compatibility, marked deprecated in `--help`, and print a stderr
notice pointing here. `report` was rewritten to read only real counters.

## Where the numbers come from

Every front end (`server run --stdio`, `server run --http`, `serve`) already
maintains the same process-wide counters, exposed unconditionally on the
`/metrics` JSON endpoint (`pkg/metrics.Snapshot`, token-protected since
issue #316, available on both `server run` and `serve` via
`--metrics-bind`): the response governor's `GovernorStats`
(`pkg/mcp/middleware_governor.go`, issues #319-#321) and the schema/
discovery telemetry counters (`pkg/mcp/telemetry.go`, this issue). Each
front end also appends a snapshot of `pkg/metrics.Snapshot()` to an
append-only, per-UTC-day JSONL store (`pkg/usage`) under
`~/.leanproxy/usage/`, mode `0600`:

- once at startup,
- every 5 seconds while running (the same tick the status file already
  uses),
- and once more at shutdown (SIGINT/SIGTERM or, for `server run --stdio`,
  EOF on stdin),

so even a short-lived session leaves at least one record behind. Files
older than 90 days are pruned on every process start (configurable with
`LEANPROXY_USAGE_RETENTION_DAYS`, a positive integer number of days).

`report` reads that store — `pkg/usage.Store.Load(since)` — and builds the
report (`pkg/usage.BuildSavingsReport`) from it. It needs no running proxy.
Records are grouped by session id, and only the **latest** snapshot of
each session is used (each `Record.Snapshot` is that process's own
cumulative total, so summing every snapshot of the same session would
double count).

### The dashboard, `/metrics` and the IDE extensions

The [web dashboard](dashboard.md), the `/metrics` endpoint's `usage`
section and the [IDE extensions](extensions.md) read the same store, from
a running proxy (`server run` or `serve` with `--dashboard-bind` /
`--metrics-bind`). They show two fixed windows, **today** (since 00:00 UTC)
and **week to date** (since Monday 00:00 UTC), computed with the same
`BuildSavingsReport`, so their totals are `report`'s `total_*` numbers.
There is one difference in how a window is cut:

- `report --since X` keeps each session whose latest snapshot is at or
  after `X` and uses that snapshot's cumulative total, including anything
  the session recorded before `X`.
- A dashboard window uses, per session, the latest snapshot **minus** the
  session's last snapshot before the window started
  (`pkg/usage.Summarize` / `pkg/usage.Live`), so a long-running proxy only
  contributes what it recorded inside the window.

For a session that started inside the window the two agree. The
dashboard's per-server and per-tool rows are the same governor data as
`report --by server|tool` (empty while the governor is off). The old
dashboard/metrics fields fed by `reporter.CostTracker` (`total_spend`,
`by_tool`, `by_server`, `top_5_expensive_tools`, prompt hashes) were
always empty and have been removed.

## The estimator

Every `*_tokens` field in every category uses the same heuristic:
`reporter.DefaultCharsPerToken` — 1 token ≈ 4 characters of the relevant
payload's JSON encoding, rounded up (`pkg/reporter.Estimator`,
`pkg/mcp/governor.Tokens`). This is:

- the same estimator the response governor uses to enforce `max_tokens`
  and to compute its own `GovernorStats`,
- the same estimator `tests/bench` and `tests/harness` use (`tokens()`,
  the "Token unit" row of `docs/benchmark-results.md`),

so a number from `report` and a number from `make harness` for the same
kind of payload are directly comparable — see
[§11 of the benchmark results](benchmark-results.md#11-auditable-savings-report-324).

## Categories

Every category below is a row of `report`'s `mechanisms` array
(`pkg/usage.MechanismSavings`). Each row's `saved_tokens` is
`original_tokens - resulting_tokens` where both are tracked; rows that
only track a delta (projection, dedup, summarization) carry `saved_tokens`
alone. Every non-cost row is `measured: true` — nothing in this report is
modeled — and the non-cost rows sum to `total_saved_tokens`.

### `schema` — router vs. passthrough `tools/list`

- **What is counted:** `schema_native_tokens` and `schema_sent_tokens`,
  from the real marshaled `tools/list` JSON-RPC response
  (`pkg/mcp/handlers.go`'s `handleToolsList`), on every `tools/list` call.
- **Baseline:** in **router** exposure mode (the default for an unknown
  client), `schema_native_tokens` is the same real payload
  **passthrough** mode would have sent for that client — every upstream
  tool the security layers (tool pinning, per-tool policy) let it see,
  rendered without waiting for a cold or unpinned server
  (`Handler.exposedToolsSnapshot`, which never blocks a request) — and
  `schema_sent_tokens` is the router's compact gateway-tool list actually
  sent. In **passthrough**/**hybrid** mode, both are the same real
  payload: the measured saving there is legitimately zero, not "n/a" —
  the router's compaction is what creates the gap, and passthrough
  clients defer differently (see `docs/benchmark-results.md` §10).
- **Corresponds to:** `docs/benchmark-results.md` §4's "Tokens: per
  server" and §10's exposure-mode comparison.

### `discovery` — `search_tools`/`list_tools`/`list_servers` (a cost, not a saving)

- **What is counted:** `discovery_calls` and `discovery_tokens`, from the
  real result text of every call to `search_tools`, `list_tools` or
  `list_servers` (`toolListingResult`, shared by all three).
- **Baseline:** none — this is a token **cost** the router pays to look up
  a tool it doesn't have inline, in exchange for the schema saving above.
  It is reported as `is_cost: true` and excluded from `total_saved_tokens`
  so it can never quietly offset the mechanisms above.
- **Corresponds to:** `docs/benchmark-results.md` §4's discovery-payload
  table.

### `response_truncation` / `response_projection` / `response_dedup` / `response_summarization`

- **What is counted:** the response governor's own accounting
  (`GovernorStats`, `Governor.account`), on every governed tool result:
  - `response_truncation`: `original_tokens`/`resulting_tokens` are the
    governor's `OriginalTokens`/`ReturnedTokens` (the whole governed
    result, before/after every mechanism); its own `saved_tokens` is the
    residual after the three mechanisms below are subtracted out, so the
    four rows sum to the governor's total saving.
  - `response_projection`: `ProjectionSavedTokens` (issue #320) — fields a
    projection rule removed, before truncation. The projected part's own
    before/after size is not retained separately from the whole result, so
    only the saved delta is shown.
  - `response_dedup`: `DedupSavedTokens` (issue #321) — a byte-identical
    result in the same session replaced by a short stub.
  - `response_summarization`: `SummarizeSavedTokens` (issue #321) — a
    result replaced by a local-LLM summary; `notes` also carries the
    fallback count (summarization attempts that fell back to ordinary
    truncation).
- **Baseline:** the same result's size before the governor touched it
  (`OriginalTokens`), from the real upstream response.
- **Off by default:** every number here is zero unless `response.enabled`
  (and, for dedup/summarize, their own config) is on.
- **Corresponds to:** `docs/benchmark-results.md` §7 (truncation), §8
  (projection), §9 (dedup); summarization is deliberately not in the
  harness table (it needs a real local model — see §9's note) but is
  covered here from the real counters of whatever session ran it.

### Extra turns (not a token figure)

- **What is counted:** `extra_turns_estimate` = the measured
  `discovery_calls` count.
- **Why it's labelled estimated:** the call count itself is measured;
  reading it as "roughly one extra LLM turn per discovery call" is the
  estimate. It is never converted into a token number, per the issue's
  explicit requirement ("do not convert it into tokens silently") — it is
  its own field, always reported separately from every `*_tokens` value.

## Privacy and reproducibility

- **Offline.** `report` never makes a network call; it only reads local
  files.
- **No payloads.** The usage store and every report output carry only
  names (server, tool, session id), counts and timestamps — never a tool
  argument, a tool result, or a secret. `pkg/usage`'s and the harness's
  and e2e's tests assert this directly (a canary "secret"/payload string
  is checked to never appear in any output format).
- **File permissions.** Usage files and `report --output` are written with
  mode `0600`.
- **Reproducible.** Given the same usage store, `report` always produces
  the same numbers — it does no live measurement or randomized sampling.
  To reproduce a number cited from the benchmark harness instead, run
  `make harness` (see `docs/benchmark-results.md` §1); the two are
  computed with the same estimator but are not the same measurement (the
  harness runs a synthetic, fixed catalog with no network; `report` reads
  whatever real sessions the store has).

## Output formats

- **Text** (default): a fixed-width human summary.
- **`--export json`**: the stable schema documented above and in
  `docs/commands.md` (`pkg/usage.SavingsReport`, `encoding/json` tags are
  the field names).
- **`--export md`**: a Markdown table, suitable for pasting into a PR or
  wiki page.
- **`--export csv`**: one row per mechanism plus a `TOTAL` row, for a
  spreadsheet or BI tool.

`--by tool|server|session` selects which extra breakdown table is shown
(top tools/servers by response size — a hint for writing projection rules,
issue #320 — or a per-session totals table). `--price-per-mtok` computes an
estimated cost saved at the given price; there is no built-in price table,
so that number is always clearly derived from the caller's own input, never
presented as measured.
