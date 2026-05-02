# Story 7.5: Rewrite handleConnection for Multi-Server Routing

Status: review

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 7.5 |
| **Key** | leanproxy-7-5 |
| **Epic** | epic-7 |
| **Title** | Rewrite handleConnection for Multi-Server Routing |

## Story Requirements

### User Story

As a developer,
I want to rewrite the handleConnection function to support multi-server routing,
So that each incoming IDE connection is handled by routing requests to the appropriate MCP server.

### Acceptance Criteria (BDD Format)

```gherkin
Feature: Multi-Server handleConnection

  Scenario: IDE sends single request to gateway
    Given an IDE connects to LeanProxy-MCP's stdio endpoint
    When the IDE sends a JSON-RPC request
    Then handleConnection parses the method name
    And looks up the target server in the registry
    And forwards the request to that server's stdin
    And streams the response back to IDE

  Scenario: Handle notification (no ID)
    Given handleConnection receives a notification (no ID)
    When the notification is parsed
    Then it is forwarded to the appropriate server
    And no response is returned to IDE

  Scenario: Handle batch request
    Given handleConnection receives a batch request
    When the batch is parsed
    Then each request is routed to its target server
    And responses are collected and returned as a batch
    And batch response order matches request order

  Scenario: Connection closed mid-stream
    Given the connection is closed mid-stream
    When handleConnection detects the close
    Then it cleanly terminates server communication
    And no zombie processes are left behind

  Scenario: Gateway tools routed internally
    Given IDE sends request for gateway tool (list_servers/invoke_tool/search_tools)
    When handleConnection parses the method
    Then the request is handled internally
    And not forwarded to any MCP server
    And the gateway tool response is returned

  Scenario: Malformed JSON-RPC handled gracefully
    Given handleConnection receives malformed data
    When parsing fails
    Then it returns JSON-RPC error -32700 (Parse error)
    And it does not crash or hang
```

## Developer Context

### Technical Requirements

1. **New handleConnection Implementation**
   ```go
   func handleConnection(conn io.ReadWriter) error {
       reader := bufio.NewReader(conn)
       writer := bufio.NewWriter(conn)

       for {
           // Read line-based JSON-RPC message
           line, err := reader.ReadBytes('\n')
           if err != nil {
               if err == io.EOF {
                   return nil // Normal close
               }
               return fmt.Errorf("read: %w", err)
           }

           // Parse request
           req, err := proxy.ParseJSONRPCRequest(line)
           if err != nil {
               // Return parse error
               writeError(writer, -32700, "Parse error")
               continue
           }

           // Check if it's a gateway tool
           if isGatewayTool(req.Method) {
               handleGatewayTool(req, writer)
               continue
           }

           // Route to MCP server
           server, err := router.RouteRequest(req.Method)
           if err != nil {
               writeError(writer, -32601, "Method not found")
               continue
           }

           // Forward to server and get response
           resp, err := pool.SendRequest(server, req)
           if err != nil {
               writeError(writer, -32603, err.Error())
               continue
           }

           // Write response
           writeResponse(writer, resp)
       }
   }
   ```

2. **Line-Based Protocol**
   - MCP over stdio uses newline-delimited JSON-RPC messages
   - Each request/response is one line (ending with '\n')
   - Batch requests use JSON arrays

3. **Request/Response Matching**
   - Match responses to requests by ID
   - Notifications have no ID (send and forget)
   - Batch requests may have mixed IDs

4. **Stdin/Stdout Per Server**
   - Each server has dedicated stdin/stdout
   - Requests are serialized per server (no interleaving)
   - Responses matched by JSON-RPC ID

### Architecture Compliance

- **Package**: `cmd/` (modify serve.go)
- **Interface**: Uses router.RouteRequest, pool.SendRequest
- **Naming**: kebab-case for CLI, camelCase for Go
- **Error Wrapping**: `fmt.Errorf("serve: context: %w", err)`
- **Logging**: All logs via `log/slog` to stderr

### File Structure

```
cmd/
├── serve.go          # MODIFY - replace handleConnection
├── root.go           # EXISTING
└── migrate.go        # EXISTING

pkg/router/           # Story 7.1
pkg/pool/             # Story 7.3
pkg/gateway/          # Story 7.2
```

### Dependencies

- Story 7.1 (Tool-to-Server Routing Engine) - router
- Story 7.2 (Gateway Tools) - isGatewayTool, handleGatewayTool
- Story 7.3 (Stdio Pool Manager) - pool.SendRequest
- `pkg/proxy/proxy.go` - JSON-RPC parsing utilities

### Testing Requirements

1. **Unit Tests**
   - Test single request routing
   - Test notification handling
   - Test batch request routing
   - Test error responses

2. **Integration Tests**
   - Test full round-trip with mock MCP server
   - Test gateway tools vs server routing
   - Test connection close handling

### Implementation Checklist

- [x] Replace handleConnection in cmd/serve.go
- [x] Implement request parsing from line
- [x] Implement gateway tool detection
- [x] Implement server routing via router
- [x] Implement request forwarding via pool
- [x] Implement response writing
- [x] Implement batch request handling
- [x] Add integration tests

### Edge Cases

- Request without valid ID
- Response with mismatched ID
- Server times out
- Server returns error response
- Very large request (> 50MB)
- Binary data in request parameters
- Null bytes in request

## Dev Agent Record

### Debug Log References

See existing implementation:
- `pkg/proxy/proxy.go:94-165` - ForwardLoopWithJSONRPC (reference pattern)
- `pkg/proxy/proxy.go:167-197` - ParseJSONRPCRequest/Response functions
- `cmd/serve.go:95-98` - CURRENT handleConnection (replace this)

### Project Context

Current project has:
- `cmd/serve.go:95-98` - stub handleConnection that just prints "ready"
- `pkg/proxy/proxy.go` - JSON-RPC types and parsing
- `pkg/registry/registry.go` - registry interface

### Implementation Plan

1. Replaced `handleConnection` with full multi-server routing implementation
2. Added `Router` and `Pool` interfaces for dependency injection and testability
3. Implemented `handleSingleRequest` for processing individual JSON-RPC requests
4. Implemented `handleBatchRequest` for batch request processing
5. Added gateway tool handling (`list_servers`, `invoke_tool`, `search_tools`)
6. Added `SendRequest` method to `StdioPool`
7. Created comprehensive unit tests (26 tests)

### Completion Notes

Implemented story 7.5 - Multi-Server handleConnection:

**Changes Made:**
- `cmd/serve.go`: Complete rewrite of `handleConnection` function
  - Added `Router` and `Pool` interfaces for testability
  - Line-based JSON-RPC message parsing
  - Gateway tool detection and internal routing
  - Server routing via `router.Route()`
  - Request forwarding via `pool.SendRequest()`
  - Batch request handling with order preservation
  - Proper error handling (parse errors, method not found, internal errors)

- `pkg/pool/pool.go`: Added `SendRequest` method to `StdioPool`
  - Simplified request forwarding interface
  - Handles timeout and context cancellation

- `cmd/serve_test.go`: 26 unit tests covering:
  - `isGatewayTool` - gateway tool detection
  - `trimNewline` - newline trimming
  - `isBatchRequest` - batch detection
  - `writeResponse` / `writeError` - response writing
  - `handleConnection` - full connection handling
  - `handleSingleRequest` - single request routing
  - `handleBatchRequest` - batch request handling
  - `handleGatewayToolSync` - gateway tool processing

**Tests:** All 219 tests pass (26 new tests in cmd package)

### References

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-7-Story-7.5]
- [Source: cmd/serve.go:95-98] - Current stub handleConnection
- [Source: pkg/proxy/proxy.go:199-217] - JSONRPCRequest type
- [Source: _bmad-output/planning-artifacts/architecture.md#Decision-Manifest-Management]

## File List

- `cmd/serve.go` (MODIFY - replaced handleConnection and runServe logic)
- `cmd/serve_test.go` (NEW - 26 unit tests)
- `pkg/pool/pool.go` (MODIFY - added SendRequest method)

## Change Log

- 2026-05-02: Initial implementation complete - all tasks completed, tests passing