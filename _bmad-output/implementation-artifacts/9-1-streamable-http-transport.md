---
story_id: 9.1
story_key: 9-1-streamable-http-transport
epic_num: 9
story_num: 1
story_title: "Implement Streamable HTTP Transport"
status: done
created: 2026-05-07
source: market-research-2026-05-07
priority: HIGH
kpi_impact: "Enterprise compatibility (SSE deprecated)"
---

## Story

**As an** Enterprise User of LeanProxy-MCP,
**I want to** use Streamable HTTP instead of SSE,
**So that** the proxy works with corporate proxies and load balancers.

## Acceptance Criteria

### AC1: Streamable HTTP Listener
**Given** Streamable HTTP transport is configured
**When** the proxy starts
**Then** it listens on a single HTTP endpoint
**And** supports both synchronous and streaming responses

### AC2: Corporate Proxy Compatibility
**Given** a client connects via Streamable HTTP
**When** the connection goes through a corporate proxy
**Then** the connection is not broken by proxy timeouts
**And** SSE stream buffering issues are avoided

### AC3: Multi-Transport Support
**Given** both stdio and Streamable HTTP are configured
**When** the proxy starts
**Then** both transports are available
**And** clients can connect via either

### AC4: Specification Compliance
**Given** Streamable HTTP is used
**When** the specification changes
**Then** the proxy can be updated to match spec

## Technical Requirements

### Implementation Location
- **Package:** `pkg/proxy/http.go` (NEW FILE)
- **Integration:** Modify existing proxy for HTTP transport

### Data Structures

```go
// HTTPTransport implements Streamable HTTP for MCP
type HTTPTransport struct {
    addr string
    handler HTTPHandler
    server *http.Server
    // Configuration
    config HTTPTransportConfig
}

// HTTPTransportConfig HTTP transport configuration
type HTTPTransportConfig struct {
    Port         string        // default: 8080
    ReadTimeout  time.Duration // default: 30s
    WriteTimeout time.Duration // default: 30s
    MaxHeaderBytes int        // default: 1MB
}
```

### Endpoint Design

```
# Streamable HTTP (primary - MCP 2026 spec)
POST /mcp - JSON-RPC requests
GET /mcp - Streamable responses
GET /health - Health check

# SSE (legacy - for backward compatibility)
GET /sse - SSE stream
POST /sse - JSON-RPC for SSE
```

### Headers

```go
// Required headers for Streamable HTTP
req.Header.Set("Content-Type", "application/json")
req.Header.Set("Accept", "application/json, text/event-stream")
```

## Web Research Reference

**From MCP 2026 spec:**

- SSE creates long-lived HTTP connections
- Fight with load balancers (timeout/redirect issues)
- Corporate proxies buffer SSE streams
- **Streamable HTTP** is the replacement
- Spec has deprecated SSE in favor

**Migration:**
- Keboola discontinued SSE April 2026
- Atlassian Rovo MCP Server June 2026 deadline

## Implementation Tasks

- [ ] 1. Create `pkg/proxy/http.go`
  - [ ] 1.1 Define HTTPTransport struct
  - [ ] 1.2 Implement ListenAndServe()
  - [ ] 1.3 Implement Streamable HTTP handler
  - [ ] 1.4 Implement SSE handler (legacy)
- [ ] 2. Add HTTP endpoints
  - [ ] 2.1 POST /mcp - JSON-RPC
  - [ ] 2.2 GET /mcp - Streamable
  - [ ] 2.3 GET /sse - Legacy compatibility
  - [ ] 2.4 GET /health - Health
- [ ] 3. Configuration in `config.go`
  - [ ] 3.1 Add transport config section
  - [ ] 3.2 Parse stdio/http/sse transports
- [ ] 4. Testing
  - [ ] 4.1 HTTP transport test
  - [ ] 4.2 Corporate proxy simulation
  - [ ] 4.3 Backward compatibility test

## Dev Notes

### Why Streamable HTTP?

1. **SSE Problems:**
   - Load balancer timeout issues
   - Corporate proxy buffering
   - No multiplex (single stream)

2. **Streamable HTTP Benefits:**
   - Single endpoint
   - Both sync/async responses
   - Works with modern proxies

### Success Metrics

- Both transports: ✓ stdio + HTTP
- Legacy compatibility: ✓ SSE fallback
- Enterprise ready: ✓ Corporate proxy tested

## References

- [Source: /planning-artifacts/epics.md#Epic-9-Story-9.1]
- [Source: /planning-artifacts/architecture.md#Epic-9-Enterprise-Transport]

---

**Status:** review