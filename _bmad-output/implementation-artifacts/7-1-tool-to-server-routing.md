# Story 7.1: Tool-to-Server Routing Engine

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 7.1 |
| **Key** | leanproxy-7-1 |
| **Epic** | epic-7 |
| **Title** | Implement Tool-to-Server Routing Engine |

## Story Requirements

### User Story

As a developer,
I want to parse JSON-RPC requests and route them to the correct MCP server based on tool name,
So that a single LeanProxy-MCP instance can proxy traffic to hundreds of MCP servers.

### Acceptance Criteria (BDD Format)

```gherkin
Feature: Tool-to-Server Routing Engine

  Scenario: Route request to correct server by tool name prefix
    Given an IDE sends a JSON-RPC request with method "github.create_issue"
    When the proxy receives the request
    Then it looks up "github.create_issue" in the tool registry
    And routes the request to the "github" MCP server's stdin
    And returns the response to the IDE

  Scenario: Route batch requests to multiple servers
    Given an IDE sends a batch of JSON-RPC requests for tools from different servers
    When the proxy receives the batch
    Then it parses each method name
    And routes each request to the appropriate server in parallel
    And collects responses and returns them in correct order

  Scenario: Handle unknown tool gracefully
    Given a request for an unknown tool
    When the proxy receives it
    Then it returns a JSON-RPC error with code -32601 (Method not found)
    And logs a debug message noting the unmatched method

  Scenario: Handle server going offline during requests
    Given a server goes offline during active requests
    When requests are pending for that server
    Then the proxy returns an error indicating server unavailable
    And does not block requests for other servers

  Scenario: Route tool with namespace format "server.tool"
    Given a JSON-RPC request with method "filesystem.read_file"
    When routing engine parses the method
    Then it extracts namespace "filesystem" and tool "read_file"
    And looks up server by namespace in registry
    And forwards request to matched server

  Scenario: Handle tools without namespace prefix
    Given a JSON-RPC request with method "read_file" (no namespace)
    When routing engine parses the method
    Then it searches all registered tools for "read_file"
    And if unique match found, routes to that server
    And if multiple matches found, returns ambiguity error
    And if no match found, returns method-not-found error
```

## Developer Context

### Technical Requirements

1. **Tool Registry Data Structure**
   - Each MCP server registers its tools with the registry on startup
   - Tools are stored with format: `namespace.tool_name` (e.g., `github.create_issue`)
   - Registry provides reverse lookup: tool name → server entry

2. **Routing Algorithm**
   ```go
   // Pseudo-code for routing decision
   func routeRequest(method string) (*ServerEntry, error) {
       // Parse method name - format: "namespace.tool" or just "tool"
       parts := strings.Split(method, ".")
       namespace := parts[0]

       // Look up server by namespace
       server, err := registry.FindByNamespace(namespace)
       if err != nil {
           // Fallback: search all servers for this tool
           servers := registry.FindServersWithTool(method)
           if len(servers) == 0 {
               return nil, ErrToolNotFound
           }
           if len(servers) > 1 {
               return nil, ErrAmbiguousTool
           }
           server = servers[0]
       }
       return server, nil
   }
   ```

3. **Request Forwarding**
   - Parse JSON-RPC request to extract method, params, id
   - Find target server via routing algorithm
   - Forward raw JSON-RPC message to server's stdin
   - Collect response from server's stdout
   - Return response to IDE

4. **Error Handling**
   - `-32601` Method not found - tool not in any registered server
   - `-32602` Invalid params - ambiguous tool (multiple servers have same tool)
   - `-32603` Internal error - target server unavailable or routing failed

### Architecture Compliance

- **Package**: `pkg/router/` (new package for routing logic)
- **Interface**: `Router` interface with `Route(method string) (*ServerEntry, error)`
- **Naming**: camelCase for all exported symbols
- **Error Wrapping**: `fmt.Errorf("router: context: %w", err)`
- **Logging**: All logs via `log/slog` to stderr

### File Structure

```
pkg/router/
├── router.go          # Core routing implementation
├── registry.go       # Tool-to-server mapping
├── errors.go         # Routing-specific errors
├── router_test.go    # Unit tests
└── doc.go            # Package documentation
```

### Testing Requirements

1. **Unit Tests**
   - Test method parsing (namespace.tool, tool-only, malformed)
   - Test server lookup by namespace
   - Test fallback search across all servers
   - Test ambiguous tool detection
   - Test error cases

2. **Integration Tests**
   - Test routing with real MCP server (mock)
   - Test batch request routing
   - Test concurrent routing

### Implementation Checklist

- [ ] Create `pkg/router/router.go` with Router interface
- [ ] Implement method parsing (extract namespace, tool name)
- [ ] Implement server lookup by namespace
- [ ] Implement fallback search across all servers
- [ ] Implement error handling for not found / ambiguous
- [ ] Implement request forwarding to server stdin/stdout
- [ ] Add unit tests
- [ ] Verify < 50ms routing decision overhead

### Edge Cases

- Method name with multiple dots: `github.api.v3.create_issue`
- Method name starting with dot: `.create_issue`
- Empty method name
- Very long method names (> 100 chars)
- Unicode in method names
- Server namespace conflicts (two servers with same namespace)

## Dev Agent Record

### Debug Log References

See existing implementation:
- `pkg/proxy/proxy.go:167-197` - JSON-RPC parsing utilities (ParseJSONRPCRequest, ParseJSONRPCResponse)
- `pkg/registry/registry.go` - ServerEntry, FindByCapability, FindBest methods

### Project Context

Current project has:
- `pkg/proxy/proxy.go` - JSON-RPC types and parsing (DON'T modify - foundation)
- `pkg/registry/registry.go` - Server registry with FindByCapability, FindBest (extend for tool lookup)
- `pkg/registry/lifecycle.go` - Process management for stdio servers
- `cmd/serve.go:95-98` - handleConnection currently just prints message (MUST replace)
- `pkg/migrate/config.go` - ServerConfig with Transport, Command, Args fields

### References

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-7-Story-7.1]
- [Source: _bmad-output/planning-artifacts/architecture.md#Core-Architectural-Decisions]
- [Source: pkg/proxy/proxy.go:199-217] - JSONRPCRequest/Response types
- [Source: pkg/registry/registry.go:258-288] - FindByCapability example

## File List

- `pkg/router/router.go` (NEW)
- `pkg/router/registry.go` (NEW)
- `pkg/router/errors.go` (NEW)
- `pkg/router/router_test.go` (NEW)
- `pkg/router/doc.go` (NEW)
- `cmd/serve.go` (MODIFY - replace handleConnection)
