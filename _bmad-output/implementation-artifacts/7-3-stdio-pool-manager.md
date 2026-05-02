# Story 7.3: Stdio Pool Manager

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 7.3 |
| **Key** | leanproxy-7-3 |
| **Epic** | epic-7 |
| **Title** | Implement Stdio Pool Manager |

## Story Requirements

### User Story

As a developer,
I want to manage a pool of stdio MCP server subprocesses,
So that multiple concurrent requests can be handled efficiently across 100+ servers.

### Acceptance Criteria (BDD Format)

```gherkin
Feature: Stdio Pool Manager

  Scenario: Spawn all configured servers on startup
    Given 100 MCP servers are configured in stdio mode
    When the gateway starts
    Then it spawns subprocesses for all enabled servers
    And each subprocess runs in its own process group (NFR6)
    And process health is monitored continuously

  Scenario: Handle concurrent requests for same server
    Given multiple concurrent requests for the same server
    When requests arrive
    Then they are queued and processed sequentially per server
    And no request mixing occurs between different tool calls

  Scenario: Detect and restart crashed server
    Given a server's subprocess exits unexpectedly
    When the lifecycle manager detects the exit
    Then it restarts the subprocess with exponential backoff
    And pending requests for that server return an error
    And health status is updated to "error"

  Scenario: Idle timeout stops unused servers
    Given server idle timeout is configured (e.g., 5 minutes)
    When a server has no requests for the idle period
    Then the subprocess is stopped to conserve resources
    And the subprocess is restarted on the next request

  Scenario: Connection reuse within pool
    Given a server is running and idle
    When a new request arrives for that server
    Then the existing connection is reused
    And no new subprocess is spawned

  Scenario: Per-server request queue
    Given server has max_concurrent_requests = 5 configured
    When 10 concurrent requests arrive for that server
    Then 5 are processed, 5 are queued
    And queued requests wait for available slot
    And queue has timeout - requests timeout if wait exceeds limit
```

## Developer Context

### Technical Requirements

1. **Process Pool Structure**
   ```go
   type StdioPool struct {
       servers    map[string]*StdioServer // server name -> server handle
       mu         sync.RWMutex
       maxPerServer int
       idleTimeout time.Duration
   }

   type StdioServer struct {
       name       string
       config     *ServerConfig
       process    *os/exec.Cmd
       stdin      io.WriteCloser
       stdout     io.Reader
       mu         sync.Mutex
       requestCh  chan Request
       responseCh chan Response
       state      ServerState
       restartCount int
   }
   ```

2. **Request/Response Protocol**
   ```
   Request: { method, params, id }
   Response: { result, id } or { error, id }
   ```

3. **Health Monitoring**
   - Goroutine per server watching process exit
   - Periodic ping/pong to detect stuck processes
   - Memory/CPU monitoring if available

4. **Connection Pooling**
   - Reuse stdin/stdout for same server across requests
   - Serialize requests through mutex + channel
   - Return connection to pool after request completes

5. **Restart Logic**
   - Exponential backoff: 1s, 2s, 4s, 8s, max 60s
   - Reset backoff on successful request
   - Max restarts before giving up (configurable)

### Architecture Compliance

- **Package**: `pkg/pool/` (new package for stdio pool management)
- **Interface**: `StdioPool` interface with `GetServer()`, `PutRequest()`, `Close()`
- **Naming**: camelCase for all exported symbols
- **Error Wrapping**: `fmt.Errorf("pool: context: %w", err)`
- **Logging**: All logs via `log/slog` to stderr
- **Performance**: Sub-50ms request dispatch (NFR1)

### File Structure

```
pkg/pool/
├── pool.go            # StdioPool implementation
├── server.go         # Per-server handling
├── queue.go          # Request queue per server
├── health.go         # Health monitoring
├── pool_test.go      # Unit tests
└── doc.go            # Package documentation
```

### Dependencies

- Story 7.1 (Tool-to-Server Routing Engine) - provides routing
- `pkg/registry/lifecycle.go` - existing process management (extend, not replace)
- Uses `pkg/migrate/config.go` - ServerConfig for stdio transport

### Testing Requirements

1. **Unit Tests**
   - Test server spawn and cleanup
   - Test request queue serialization
   - Test idle timeout trigger
   - Test restart with backoff

2. **Integration Tests**
   - Test concurrent requests across multiple servers
   - Test server crash detection and restart
   - Test 100+ servers scenario

### Implementation Checklist

- [ ] Create `pkg/pool/pool.go` with StdioPool
- [ ] Implement server spawn with stdin/stdout pipes
- [ ] Implement request serialization per server
- [ ] Implement connection reuse
- [ ] Implement health monitoring goroutine
- [ ] Implement idle timeout and server stop
- [ ] Implement restart with exponential backoff
- [ ] Add unit tests

### Edge Cases

- Server executable not found
- Server process hangs (no output within timeout)
- Very long response (> 50MB - NFR2)
- stdin/stdout pipe breaks
- Zombie processes (children of killed server)
- Out of file descriptors (ulimit)

## Dev Agent Record

### Debug Log References

See existing implementation:
- `pkg/registry/lifecycle.go` - existing lifecycle manager (EXTEND, not replace)
- `pkg/proxy/proxy.go:60-92` - io.Copy patterns for forwarding

### Project Context

Current project has:
- `pkg/registry/lifecycle.go` - ServerConfig, LifecycleManager interface
- `cmd/serve.go` - process spawning for single server (legacy, replace)
- `pkg/migrate/config.go:16-28` - TransportStdio, ServerConfig struct

### References

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-7-Story-7.3]
- [Source: pkg/registry/lifecycle.go] - Existing lifecycle patterns
- [Source: _bmad-output/planning-artifacts/architecture.md#Decision-Manifest-Management]

## File List

- `pkg/pool/pool.go` (NEW)
- `pkg/pool/server.go` (NEW)
- `pkg/pool/queue.go` (NEW)
- `pkg/pool/health.go` (NEW)
- `pkg/pool/pool_test.go` (NEW)
- `pkg/pool/doc.go` (NEW)
- `cmd/serve.go` (MODIFY - integrate pool)
- `pkg/registry/lifecycle.go` (EXTEND - add pooling methods)
