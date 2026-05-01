---
id: 5-3
key: real-time-server-health
epic: epic-5-reporting-insights
title: Implement Real-Time Server Health Status
status: ready-for-dev
developer: Amelia
---

# Story 5-3: Implement Real-Time Server Health Status

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 5-3 |
| **Key** | `real-time-server-health` |
| **Epic** | Epic 5: Reporting & Insights |
| **Title** | Implement Real-Time Server Health Status |
| **Status** | `ready-for-dev` |
| **Story Points** | 5 |
| **Implementation Order** | 3 |

---

## Story Requirements

### User Story

**As a** user,
**I want to** see real-time status of all active proxied servers,
**So that** I can monitor the health of my MCP integration.

### FR Coverage

- **FR23**: The system can provide real-time status of all active proxied servers and their health.
- **NFR8**: The proxy shall detect and report the failure of any underlying MCP process within 1 second and provide a graceful recovery path for the IDE session.
- **NFR9**: The system shall output real-time health and savings metrics (tokens saved, secrets redacted) to stderr.

---

## Acceptance Criteria

### BDD Format

```
Scenario: Display status for running servers
  Given multiple MCP servers are running
  When the user runs `leanproxy status`
  Then a table is displayed showing all servers
  And each row shows: name, status (running/error/stopped), uptime, last response time

Scenario: Server process failure detection
  Given a server process crashes
  When the health monitor detects the failure
  Then the status is updated to "error" within 1 second (NFR8)
  And an alert is logged to stderr
  And the restart attempts are shown in the status

Scenario: Watch mode for continuous monitoring
  Given the user runs `leanproxy status --watch`
  Then the status updates are streamed continuously
  And the display refreshes every second
  And Ctrl+C exits the watch mode

Scenario: Verbose status output
  Given verbose mode is enabled
  When status is displayed
  Then additional details are shown: memory usage, request count, error rate

Scenario: Filter status by server name
  Given multiple servers are running
  When the user runs `leanproxy status --server <name>`
  Then only the specified server is shown in the output

Scenario: Server restart recovery display
  Given a server is in restart recovery state
  When status is displayed
  Then it shows: restart attempt count, backoff duration, next retry time

Scenario: Health check on unresponsive server
  Given a server is running but not responding
  When health check triggers
  Then it marks server as "unresponsive"
  And logs warning to stderr
  And initiates restart if configured
```

---

## Developer Context

### Technical Requirements

#### Health Monitor

1. **HealthMonitor Interface**
   - Located in `pkg/proxy/health_monitor.go`
   - Method: `Start()` error`
   - Method: `Stop()`
   - Method: `GetStatus() ServerStatusList`
   - Method: `WatchStatus(ctx context.Context, interval time.Duration) <-chan ServerStatusList`

2. **ServerStatus Struct**
   - Fields:
     - `Name string`
     - `Status ServerHealthStatus` // "running", "error", "stopped", "starting", "unresponsive"
     - `Uptime time.Duration`
     - `LastResponseTime time.Time`
     - `LastError string`
     - `RestartCount int`
     - `RequestCount int64`
     - `ErrorRate float64`

3. **ServerStatusList Struct**
   - Fields:
     - `Timestamp time.Time`
     - `Servers []ServerStatus`

4. **ServerHealthStatus Enum**
   - Values: `StatusRunning`, `StatusError`, `StatusStopped`, `StatusStarting`, `StatusUnresponsive`

5. **HealthConfig Struct**
   - Fields:
     - `CheckInterval time.Duration` // default: 1 second
     - `ResponseTimeout time.Duration` // default: 30 seconds
     - `MaxRestartAttempts int` // default: 3
     - `RestartBackoff time.Duration` // default: 5 seconds (exponential)

#### Process Health Tracking

1. **ProcessHealthChecker**
   - Located in `pkg/proxy/process_health.go`
   - Monitors MCP server subprocess health
   - Detects crashes within 1 second (NFR8)
   - Tracks memory usage via `/proc/<pid>/status` on Linux, `sysctl` on macOS
   - Method: `CheckProcessHealth(pid int) ProcessHealth`

2. **ProcessHealth Struct**
   - Fields:
     - `PID int`
     - `MemoryMB int64`
     - `CPUPercent float64`
     - `Status string`
     - `IsAlive bool`

#### Status Display

1. **StatusDisplay Struct**
   - Located in `pkg/utils/status_display.go`
   - Method: `RenderTable(statusList ServerStatusList) string`
   - Method: `RenderVerbose(statusList ServerStatusList) string`
   - Method: `RenderCompact(status ServerStatus) string`

2. **Table Format**
   ```
   NAME           STATUS      UPTIME     LAST RESPONSE   RESTARTS
   ──────────────────────────────────────────────────────────────
   server-1      running     2m34s      120ms           0
   server-2      error       5m12s      -               2
   server-3      stopped     -          -               0
   ```

3. **Verbose Format**
   ```
   Server: server-1
     Status: running
     Uptime: 2m34s
     Last Response: 120ms
     Memory: 45MB
     Requests: 1,234
     Error Rate: 0.1%
     Restarts: 0

   Server: server-2
     Status: error
     Uptime: 5m12s
     Last Error: process exited with code 1
     Restart Attempts: 2/3
     Next Retry: 10s
   ```

#### CLI Integration

- New command: `leanproxy status` (subcommand of root)
  - Flags:
    - `--watch` (bool): Continuously update status every second
    - `--verbose` (bool): Show additional details (memory, request count, error rate)
    - `--server` (string): Filter by specific server name
    - `--json` (bool): Output in JSON format

#### Real-Time Updates

- Watch mode streams status updates to stdout
- Updates rendered every second via ticker
- Graceful shutdown on SIGINT/SIGTERM
- Uses ANSI escape codes for terminal clearing (or compatible alternative)

### Architecture Compliance

| Requirement | Implementation |
|-------------|----------------|
| Go with cobra CLI | CLI commands in `cmd/leanproxy/` |
| camelCase for functions/variables | `healthMonitor`, `getStatus`, `watchStatus`, `serverStatus` |
| kebab-case for CLI flags | `--watch`, `--verbose`, `--server`, `--json` |
| `fmt.Errorf("context: %w", err)` | Used for all error wrapping |
| `log/slog` for structured logging | Health alerts logged via `slog.Warn`, status via `slog.Info` |
| 1-second failure detection (NFR8) | Health check interval set to 1 second |
| pkg/ structure | `pkg/proxy/health_monitor.go`, `pkg/proxy/process_health.go`, `pkg/utils/status_display.go` |

### File Structure

```
tokengate-mcp/
├── cmd/
│   └── leanproxy/
│       ├── main.go
│       └── status.go          # New: status CLI command
├── pkg/
│   ├── proxy/
│   │   ├── health_monitor.go  # New: health monitoring logic
│   │   └── process_health.go   # New: process-level health checks
│   └── utils/
│       └── status_display.go   # New: status rendering
```

### Integration Points

1. **With `pkg/proxy/server.go`**: Hook into server lifecycle events
2. **With `pkg/registry/`**: Get list of configured servers
3. **With `cmd/leanproxy/savings.go`**: Include health in combined status output
4. **With process management**: Receive notifications on server crash/restart

### Testing Requirements

#### Unit Tests

- `pkg/proxy/health_monitor_test.go`
  - Test status detection for all health states
  - Test watch channel produces updates at correct interval
  - Test health check timeout detection
  - Test restart backoff calculation

- `pkg/proxy/process_health_test.go`
  - Test memory usage retrieval
  - Test CPU percentage calculation
  - Test process alive/dead detection

- `pkg/utils/status_display_test.go`
  - Test table formatting with various column widths
  - Test verbose output formatting
  - Test empty server list handling
  - Test ANSI terminal compatibility

#### Integration Tests

- Test health monitoring with mock server processes
- Verify 1-second detection requirement (NFR8)
- Test watch mode continuous updates
- Test graceful shutdown during watch

### Error Handling

- If health check fails for a server, mark as "unknown" status and continue
- If process info cannot be retrieved, log warning and show "?" for memory
- If watch mode encounters error, display error and attempt to continue
- All errors wrapped with `fmt.Errorf("health monitor: context: %w", err)`

### Edge Cases

1. **No servers configured**: Display "No servers configured" message
2. **Server starting up**: Show "starting" status with spinner
3. **Server in crash loop**: Show "error" with restart attempt count
4. **Max restarts exceeded**: Show "stopped" with "max restarts reached" message
5. **Very long server names**: Truncate with ellipsis in table view
6. **Terminal not supporting ANSI**: Fall back to simple text output
7. **High server count (>20)**: Paginate output in watch mode

---

## Definition of Done

- [ ] HealthMonitor interface implemented with Start/Stop/GetStatus/WatchStatus
- [ ] ServerStatus struct tracks all required fields (name, status, uptime, last response, restarts)
- [ ] Process health tracking with memory and CPU monitoring
- [ ] 1-second failure detection implemented (NFR8)
- [ ] `leanproxy status` CLI command functional with `--watch`, `--verbose`, `--server`, `--json` flags
- [ ] Watch mode streams updates every second
- [ ] Table display format implemented with proper column alignment
- [ ] Verbose mode shows memory, request count, error rate
- [ ] Health alerts logged to stderr via slog within 1 second of detection
- [ ] Unit tests pass with >80% coverage
- [ ] Integration tests verify end-to-end health monitoring
- [ ] Architecture compliance verified (naming, error handling, logging)
