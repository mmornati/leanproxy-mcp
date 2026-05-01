# Story 1-2: Implement JSON-RPC Streaming Proxy Core

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 1-2 |
| **Key** | tokengate-mcp-1-2 |
| **Epic** | tokengate-mcp-epic-1 |
| **Title** | Implement JSON-RPC Streaming Proxy Core |

## Story Requirements

### User Story

```
As a developer
I want to intercept JSON-RPC traffic between IDE and MCP servers
So that I can inspect, route, and modify requests/responses
```

### Acceptance Criteria (BDD Format)

```gherkin
Feature: JSON-RPC Streaming Proxy

  Scenario: Proxy establishes connection to upstream MCP server
    Given a proxy instance with valid server config
    When I call proxy.Connect(upstreamAddress)
    Then a TCP connection should be established
    And the connection should remain open for streaming

  Scenario: Proxy forwards IDE requests to MCP server
    Given proxy is connected to upstream
    When IDE sends a JSON-RPC request
    Then the request should be forwarded to upstream intact
    And the request should include proper Content-Length header

  Scenario: Proxy receives and returns streaming responses
    Given proxy is connected to upstream
    When MCP server sends a streaming response
    Then the response should be forwarded to IDE intact
    And the proxy should handle chunked transfer encoding

  Scenario: Proxy handles bidirectional streaming
    Given proxy is connected to upstream
    When IDE sends multiple concurrent requests
    And MCP server sends responses out of order
    Then each response should be routed to its corresponding request
    And no response mixing should occur

  Scenario: Proxy handles connection drops gracefully
    Given proxy is actively streaming
    When upstream server disconnects
    Then proxy should detect the disconnection
    And proxy should return appropriate error to IDE
    And no panic should occur

  Scenario: Proxy respects processing overhead budget
    Given proxy is processing requests
    Then end-to-end latency overhead should be under 50ms
    And CPU usage should remain minimal during idle streaming
```

## Developer Context

### Technical Requirements

1. **Streaming Architecture**
   - Use io.Copy for efficient data transfer
   - Implement goroutine-per-connection model
   - Handle half-close for graceful shutdown

2. **JSON-RPC Compliance**
   - Parse JSON-RPC 2.0 requests/responses
   - Support batch requests
   - Preserve request/response ordering

3. **Connection Management**
   - Dial upstream with timeout
   - Handle connection keep-alive
   - Implement read/write deadlines

4. **Error Handling**
   - Distinguish recoverable vs fatal errors
   - Log errors with context using slog
   - Return JSON-RPC error responses for application errors

### Architecture Compliance

- **Package**: `pkg/proxy/proxy.go`
- **Interface**: `Proxy` struct with `Connect`, `Forward`, `Close` methods
- **Naming**: camelCase for all exported symbols
- **Error Wrapping**: `fmt.Errorf("proxy: context: %w", err)`
- **Logging**: All logs via `log/slog` to stderr
- **Performance**: Zero-copy where possible, < 50ms overhead

### File Structure

```
pkg/proxy/
├── proxy.go          # Core proxy implementation
├── proxy_test.go     # Unit tests
└── doc.go            # Package documentation
```

### API Design

```go
// Proxy handles bidirectional JSON-RPC streaming
type Proxy struct {
    upstreamAddr string
    conn         net.Conn
    logger       *slog.Logger
}

// NewProxy creates a new proxy instance
func NewProxy(upstreamAddr string, logger *slog.Logger) *Proxy

// Connect establishes connection to upstream MCP server
func (p *Proxy) Connect(ctx context.Context) error

// ForwardLoop bidirectionally forwards data until done or error
func (p *Proxy) ForwardLoop(ctx context.Context, ideConn net.Conn) error

// Close gracefully closes the proxy connection
func (p *Proxy) Close() error
```

### Testing Requirements

1. **Unit Tests**
   - Test JSON-RPC message parsing
   - Test Content-Length header handling
   - Test batch request forwarding

2. **Integration Tests**
   - Test against real MCP server (mock or test server)
   - Test concurrent request handling
   - Test connection recovery

3. **Performance Tests**
   - Measure latency overhead under load
   - Verify < 50ms target is achievable
   - Profile memory allocations

### Implementation Checklist

- [x] Create pkg/proxy/proxy.go with Proxy struct
- [x] Implement NewProxy constructor
- [x] Implement Connect method with timeout
- [x] Implement ForwardLoop for bidirectional copy
- [x] Handle chunked transfer encoding
- [x] Parse and validate JSON-RPC messages
- [x] Implement proper error handling and logging
- [x] Add unit tests
- [x] Benchmark and verify < 50ms overhead
- [x] Ensure binary size remains < 20MB

### Edge Cases

- Empty JSON-RPC batch requests
- Malformed JSON-RPC messages
- Upstream sends invalid Content-Length
- Client disconnects mid-stream
- Server sends very large responses
- Concurrent requests with same ID
- HTTP/1.1 chunked encoding edge cases

### Notes

- Use bufio.Reader/Writer for line buffering
- Consider using sync.Pool for buffer reuse
- Implement context propagation for cancellation
- Keep logging level configurable

## Dev Agent Record

### Debug Log

- Implemented Proxy struct with upstreamAddr, conn, logger, mu (mutex) fields
- Connect uses net.Dialer with 10s timeout and 30s keep-alive
- ForwardLoop uses io.Copy for simple passthrough and ForwardLoopWithJSONRPC for line-based forwarding
- Added JSON-RPC parsing functions: ParseJSONRPCRequest, ParseJSONRPCResponse, IsBatchRequest, ParseJSONRPCBatchRequest
- Added JSONRPCError type with error codes (-32700, -32600, -32601, -32602, -32603)
- Unit tests cover: constructor, connect, close, JSON-RPC parsing, batch detection, error handling, concurrent access

### Completion Notes

Successfully implemented JSON-RPC Streaming Proxy Core with:
- Proxy struct with Connect, Close, ForwardLoop, ForwardLoopWithJSONRPC methods
- Full JSON-RPC 2.0 request/response parsing with batch support
- Error handling with structured JSON-RPC error codes
- Context cancellation support throughout
- Unit tests with 22 passing tests covering core functionality

## File List

- pkg/proxy/proxy.go (modified - expanded from placeholder to full implementation)
- pkg/proxy/proxy_test.go (new)
- pkg/proxy/doc.go (new)

## Change Log

- Implement JSON-RPC Streaming Proxy Core (Date: 2026-05-01)

## Status

review
