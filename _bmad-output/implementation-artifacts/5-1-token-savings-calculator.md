---
id: 5-1
key: token-savings-calculator
epic: epic-5-reporting-insights
title: Implement Token Savings Calculator
status: ready-for-dev
developer: Amelia
---

# Story 5-1: Implement Token Savings Calculator

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 5-1 |
| **Key** | `token-savings-calculator` |
| **Epic** | Epic 5: Reporting & Insights |
| **Title** | Implement Token Savings Calculator |
| **Status** | `ready-for-dev` |
| **Story Points** | 3 |
| **Implementation Order** | 1 |

---

## Story Requirements

### User Story

**As a** developer,
**I want to** calculate and track token savings in real-time,
**So that** users can see the economic impact of using leanproxy.

### FR Coverage

- **FR21**: The system can calculate and report real-time token savings per session.
- **NFR9**: The system shall output real-time health and savings metrics (tokens saved, secrets redacted) to stderr to avoid polluting the primary protocol stream.

---

## Acceptance Criteria

### BDD Format

```
Scenario: Token savings calculation for single request
  Given a JSON-RPC request passes through the proxy
  When processing completes
  Then the original token count is estimated
  And the actual token count after optimization is calculated
  And the difference is logged as "tokens saved"

Scenario: Cumulative token savings across session
  Given a session with multiple requests
  When the session ends or status is queried
  Then the cumulative token savings is displayed
  And it shows breakdown by MCP server (if multiple)

Scenario: Dry-run mode token simulation
  Given dry-run mode is active
  When the user runs `leanproxy context rebuild --dry-run`
  Then token savings are simulated and displayed
  And they can be compared against actual savings later

Scenario: Token estimation accuracy
  Given a JSON-RPC request with known content
  When the calculator estimates token usage
  Then the estimation follows OpenAI's tiktoken tokenization rules approximately
  And the estimation is suitable for display purposes (not billing)

Scenario: Savings calculated from multiple optimization techniques
  Given optimization techniques are applied (discovery signatures, JIT schema, boilerplate blindness)
  When a request is processed
  Then each technique's individual savings contribution is tracked
  And the total reflects the sum of all optimizations
```

---

## Developer Context

### Technical Requirements

#### Core Functionality

1. **TokenEstimator Interface**
   - Located in `pkg/utils/token_estimator.go`
   - Method: `EstimateTokens(content string) int`
   - Uses character-based approximation aligned with tiktoken tokenization (1 token ≈ 4 characters)
   - Method: `CalculateSavings(original, optimized string) SavingsResult`

2. **SavingsTracker Struct**
   - Located in `pkg/utils/savings_tracker.go`
   - Thread-safe using mutex protection for concurrent JSON-RPC processing
   - Fields:
     - `sessionStart time.Time`
     - `totalOriginalTokens int64`
     - `totalOptimizedTokens int64`
     - `serverSavings map[string]ServerSavings`
     - `mu sync.Mutex`
   - Methods:
     - `RecordRequest(serverName string, original, optimized string)`
     - `GetCumulativeSavings() CumulativeSavings`
     - `GetServerBreakdown() map[string]ServerSavings`
     - `Reset()`

3. **SavingsResult Struct**
   - Fields:
     - `OriginalTokens int`
     - `OptimizedTokens int`
     - `SavedTokens int`
     - `SavingsPercentage float64`
     - `Breakdown map[string]int` // optimization technique -> tokens saved

4. **CumulativeSavings Struct**
   - Fields:
     - `TotalOriginal int64`
     - `TotalOptimized int64`
     - `TotalSaved int64`
     - `SessionDuration time.Duration`
     - `RequestsProcessed int`

5. **ServerSavings Struct**
   - Fields:
     - `ServerName string`
     - `OriginalTokens int64`
     - `OptimizedTokens int64`
     - `SavedTokens int64`

#### CLI Integration

- New command: `leanproxy savings` (subcommand of root)
  - Flags:
    - `--reset` (bool): Reset cumulative counters
    - `--server` (string): Filter savings by server name
    - `--json` (bool): Output in JSON format

#### Real-Time Output

- Token savings logged to stderr using `log/slog` at `Info` level
- Format: `{"level":"INFO","msg":"token_savings","server":"<name>","original":<n>,"optimized":<n>,"saved":<n>,"pct":<f>}`
- Summary logged on session end or status query

### Architecture Compliance

| Requirement | Implementation |
|-------------|----------------|
| Go with cobra CLI | CLI commands in `cmd/leanproxy/` |
| camelCase for functions/variables | `calculateSavings`, `recordRequest`, `getCumulativeSavings` |
| kebab-case for CLI flags | `--dry-run`, `--reset`, `--server`, `--json` |
| `fmt.Errorf("context: %w", err)` | Used for all error wrapping |
| `log/slog` for structured logging | All savings output via `slog.Info` |
| Real-time calculator | Updated per-request, not batched |
| pkg/ structure | `pkg/utils/savings_tracker.go`, `pkg/utils/token_estimator.go` |

### File Structure

```
tokengate-mcp/
├── cmd/
│   └── leanproxy/
│       ├── main.go
│       └── savings.go          # New: savings CLI command
├── pkg/
│   ├── utils/
│   │   ├── token_estimator.go  # New: token estimation logic
│   │   └── savings_tracker.go  # New: cumulative savings tracking
│   └── ... (existing files)
```

### Integration Points

1. **With `pkg/bouncer/`**: Record redaction events that affect token counts
2. **With `pkg/registry/`**: Associate savings with specific servers
3. **With `pkg/proxy/`**: Hook into request/response cycle for measurement
4. **With `cmd/leanproxy/status.go`**: Display savings in status output

### Testing Requirements

#### Unit Tests

- `pkg/utils/token_estimator_test.go`
  - Test token estimation accuracy against known strings
  - Test savings percentage calculation
  - Test empty and nil input handling

- `pkg/utils/savings_tracker_test.go`
  - Test thread-safety with concurrent requests
  - Test cumulative calculations
  - Test server breakdown accuracy
  - Test reset functionality

#### Integration Tests

- Test savings calculation end-to-end with mock JSON-RPC traffic
- Verify savings are accurately calculated when all optimization techniques are active

### Error Handling

- If token estimation fails, log warning and return 0 for that request
- If savings tracking fails (e.g., memory pressure), log error but don't block request processing
- All errors wrapped with `fmt.Errorf("token savings: context: %w", err)`

### Edge Cases

1. **Zero-length content**: Return 0 tokens for both original and optimized
2. **Optimized > Original**: Log warning, record 0 savings (negative savings not possible)
3. **Very large payloads**: Stream through without loading entire content into memory
4. **Session with no requests**: Display 0 savings with appropriate message
5. **Server removed mid-session**: Preserve its savings in historical record

---

## Definition of Done

- [ ] TokenEstimator interface implemented with character-based approximation
- [ ] SavingsTracker struct implemented with thread-safety
- [ ] `leanproxy savings` CLI command functional with `--reset`, `--server`, `--json` flags
- [ ] Real-time savings logged to stderr via slog
- [ ] Server breakdown tracking working correctly
- [ ] Cumulative savings displayed on session end
- [ ] Unit tests pass with >80% coverage
- [ ] Integration tests verify end-to-end functionality
- [ ] Architecture compliance verified (naming, error handling, logging)
