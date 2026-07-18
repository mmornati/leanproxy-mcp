---
story_id: 8.2
story_key: 8-2-connection-pooling
epic_num: 8
story_num: 2
story_title: "Implement Connection Pooling"
status: done
created: 2026-05-07
source: market-research-2026-05-07
priority: CRITICAL
kpi_impact: "Fixes 187x latency overhead (15s → <100ms)"
---

## Story

**As a** Developer building LeanProxy-MCP,
**I want to** implement connection pooling to reuse MCP sessions across multiple requests,
**so that** the 187x latency issue (15s vs 80ms) is fixed.

## Acceptance Criteria

### AC1: Session Reuse
**Given** a stateless HTTP proxy setup
**When** multiple tool calls are made to the same server
**Then** a new client is NOT created on every call
**And** the same underlying session is reused
**And** latency overhead is reduced from 15s to under 100ms

### AC2: Pool Initialization
**Given** connection pooling is enabled
**When** the proxy starts
**Then** initial connections are established proactively
**And** kept alive with keepalive heartbeats

### AC3: Connection Recovery
**Given** a server connection is lost
**When** the proxy detects the failure
**Then** it automatically re-establishes the connection
**And** retries the pending request

### AC4: Queue Handling
**Given** connection pool size is configured (default: 5)
**When** more concurrent requests arrive
**Then** they are queued until a connection becomes available

## Technical Requirements

### Implementation Location
- **Package:** `pkg/proxy/pool.go` (NEW FILE)
- **Integration:** Modify existing `pkg/proxy/` for connection pooling

### Data Structures

```go
// ConnectionPool manages reusable MCP server connections
type ConnectionPool struct {
    mu sync.Mutex
    // Server name -> pool of connections
    pools map[string]*ServerPool
    // Configuration
    config PoolConfig
}

// ServerPool represents a pool for a single server
type ServerPool struct {
    mu sync.Mutex
    available chan *Client  // available connections
    pending    int           // pending request count
    maxSize    int           // max pool size
}

// PoolConfig pool configuration
type PoolConfig struct {
    MaxSize       int           // connections per server (default: 5)
    MaxWaitTime    time.Duration // max wait for connection
    IdleTimeout   time.Duration // keepalive timeout
    HealthCheck  time.Duration // health check interval
}
```

### Key Methods

```go
// GetClient gets a pooled client or creates new one
func (p *ConnectionPool) GetClient(serverName string) (*Client, error)

// ReturnClient returns a client to the pool
func (p *ConnectionPool) ReturnClient(serverName string, client *Client)

// Close shuts down all pools gracefully
func (p *ConnectionPool) Close() error
```

### Configuration

```yaml
# leanproxy.yaml
proxy:
  connection_pool:
    enabled: true  # default: true
    max_size: 5    # connections per server
    max_wait: 30s  # max wait for available connection
    idle_timeout: 5m  # keepalive timeout
    health_check: 10s # health check interval
```

### Integration Points

| Component | File | Action |
|-----------|------|--------|
| Proxy Server | `pkg/proxy/server.go` | MODIFY - Wrap with pool |
| MCP Client | `pkg/proxy/client.go` | MODIFY - Add pooling interface |
| Health | `pkg/health/health.go` | MODIFY - Check pooled connections |

## Developer Context

### Architecture Integration

Based on `maxim-ai/bifrost` patterns (11µs overhead):

1. **sync.Pool Pattern:** Use Go's sync.Pool for connection reuse
2. **Queue Pattern:** Requests queue when pool exhausted
3. **Health Check:** Periodic ping to detect dead connections

### Existing Code to Reference

- `pkg/proxy/client.go` - Current MCP client implementation
- `pkg/proxy/server.go` - Proxy server

### Testing Requirements

- Unit: Pool operations (Get, Return, queue)
- Performance: 15s → <100ms verification
- Concurrency: Multiple simultaneous requests

## Web Research Reference

**Based on:** Bifrost gateway patterns

**Key findings:**
- 11µs overhead at 5,000 RPS
- Connection reuse eliminates handshake overhead
- Queue when pool exhausted

**Reference:** maxim-ai/bifrost - High-performance Go gateway

## Implementation Tasks

- [x] 1. Create `pkg/connpool/pool.go`
  - [x] 1.1 Define ConnectionPool and ServerPool structs
  - [x] 1.2 Implement NewConnectionPool() constructor
  - [x] 1.3 Implement GetClient() with queue handling
  - [x] 1.4 Implement ReturnClient() 
  - [x] 1.5 Implement HealthCheck()
  - [x] 1.6 Implement Close() graceful shutdown
- [x] 2. Modify `pkg/pool/http_pool.go`
  - [x] 2.1 Add PoolClient wrapper
  - [x] 2.2 Implement pooling interface
- [x] 3. Modify `pkg/proxy/server.go`
  - [x] 3.1 Wire ConnectionPool into server
  - [x] 3.2 Update request handling to use pooled clients
- [x] 4. Modify `pkg/health/health.go`
  - [x] 4.1 Add pool health metrics
  - [x] 4.2 Track pool hit/miss rates
- [x] 5. Testing
  - [x] 5.1 Unit tests for ConnectionPool
  - [x] 5.2 Latency benchmark (15s → <100ms target)
  - [x] 5.3 Concurrency test

## Dev Notes

### Problem Being Solved

**Current issue (from FastMCP):**
```
100 Requests.
Proxy: Average of 15s
Direct to MCP: Average of 80ms
```

**Root cause:** New ProxyClient created on every method call, causing multiple MCP initialization handshakes.

### Solution

Connection pooling reuses the same client/session across requests, avoiding repeated handshakes.

### Success Metrics

| Metric | Before | After | Improvement |
|--------|--------|-------|-------------|
| Latency | 15,000ms | <100ms | 150x faster |
| Requests/sec | ~0.07 | ~10 | 150x throughput |

## References

- [Source: /planning-artifacts/epics.md#Epic-8-Story-8.2]
- [Source: /planning-artifacts/architecture.md#Epic-8-Token-Optimization]
- [Source: /planning-artifacts/research/market-mcp-proxy-server-features-token-savings-latency-2026-research-2026-05-07.md]

---

## Story Completion Status

**Status:** ready-for-dev  
**Created:** 2026-05-07  
**Source:** Market Research findings  
**Priority:** CRITICAL - Your latency KPI