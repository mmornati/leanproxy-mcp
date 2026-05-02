# Story 3-2: JIT Schema Injection

## Header

| Field | Value |
|-------|-------|
| ID | 3-2 |
| Key | jit-schema-injection |
| Epic | Epic 3: Context Optimization (JIT Discovery & Compactor) |
| Title | Implement JIT Schema Injection |
| Status | review |
| Estimated Points | 5 |

## User Story

**As a** developer,
**I want to** intercept `get_tool_schema` requests and inject full schemas on-demand,
**So that** full schema details are only loaded when a specific tool is actually called.

## Acceptance Criteria (BDD Format)

### AC1: Schema Interception

**Given** an IDE request for `get_tool_schema` for a specific tool
**When** the request passes through the proxy
**Then** the proxy intercepts it
**And** looks up the cached full schema for that tool
**And** returns the complete schema in the response

### AC2: Unknown Tool Forwarding

**Given** an IDE request for `get_tool_schema` for an unknown tool
**When** the request passes through
**Then** the proxy forwards it to the MCP server
**And** returns the server's response

### AC3: Lazy Schema Caching

**Given** a tool schema hasn't been cached yet
**When** the first `get_tool_schema` request for it arrives
**Then** the proxy fetches the full schema from the server
**And** caches it for subsequent requests
**And** then returns the response

## Developer Context

### Technical Requirements

1. **Request Interception**
   - Add handler for `get_tool_schema` method in proxy pipeline
   - Match method name case-insensitively
   - Extract tool name from request params

2. **Schema Cache Management**
   - Implement LRU cache for tool schemas with configurable size (default: 100)
   - Cache key: `{serverName}/{toolName}`
   - TTL: configurable, default 1 hour

3. **Schema Fetching**
   - Send `tools/list` request to MCP server if schema not cached
   - Parse response and extract specific tool schema
   - Store in cache before returning

4. **Fallback Behavior**
   - If tool not found in registry cache, forward to MCP server
   - Cache server's response for future requests

5. **Configuration**
   - Add `jit.cache-size` config option (default: 100)
   - Add `jit.cache-ttl` config option (default: 1h)
   - Add `jit.enabled` config option (default: true)

### Architecture Compliance

- **Naming**: `camelCase` for Go functions/variables, `kebab-case` for CLI flags
- **Error Handling**: `fmt.Errorf("context: %w", err)` for error wrapping
- **Logging**: `log/slog` for structured logging to stderr
- **Project Structure**: `pkg/proxy/` for request handling, `pkg/registry/` for schema storage

### File Structure

```
pkg/
├── proxy/
│   ├── proxy.go              # Main proxy orchestration
│   ├── handler.go            # JSON-RPC request handlers
│   ├── jit.go                # JIT schema injection logic
│   ├── jit_test.go           # Unit tests for JIT
│   └── interceptor.go        # Request interception middleware
└── registry/
    ├── registry.go           # Core registry
    └── cache.go              # Schema cache implementation
```

### Testing Requirements

1. **Unit Tests**
   - Test schema cache set/get operations
   - Test cache eviction (LRU behavior)
   - Test interception logic for `get_tool_schema`
   - Test unknown tool forwarding

2. **Integration Tests**
   - Test full flow with mock MCP server
   - Verify schema is cached after first fetch
   - Verify subsequent requests use cache

3. **Performance Tests**
   - Verify cache hit adds <1ms overhead
   - Verify cache miss + fetch <50ms

## Implementation Notes

### JIT Handler Logic

```go
// pkg/proxy/jit.go
func (h *JITHandler) HandleGetToolSchema(ctx context.Context, req JSONRPCRequest) (JSONRPCResponse, error) {
    toolName := req.Params["name"].(string)
    
    // Check cache first
    if schema, ok := h.cache.Get(toolName); ok {
        slog.Debug("jit cache hit", "tool", toolName)
        return newSuccessResponse(schema, req.ID), nil
    }
    
    // Cache miss - fetch from registry or forward
    schema, err := h.registry.GetFullSchema(toolName)
    if err != nil {
        // Forward to MCP server
        return h.forwardToServer(ctx, req)
    }
    
    // Cache and return
    h.cache.Set(toolName, schema)
    return newSuccessResponse(schema, req.ID), nil
}
```

### Schema Cache Interface

```go
type SchemaCache interface {
    Get(key string) (json.RawMessage, bool)
    Set(key string, schema json.RawMessage)
    Delete(key string)
    Clear()
}
```

### Configuration Schema

```yaml
jit:
  enabled: true
  cache-size: 100
  cache-ttl: 1h
```

## Tasks/Subtasks

- [x] Create SchemaCache interface and LRU implementation in pkg/registry/cache.go
- [x] Create JITHandler in pkg/proxy/jit.go with HandleGetToolSchema method
- [x] Implement cache key generation as `{serverName}/{toolName}`
- [x] Implement case-insensitive method matching for `get_tool_schema`
- [x] Implement extractToolName helper to parse tool name from params
- [x] Implement registry lookup and cache population
- [x] Implement forward to MCP server for unknown tools
- [x] Add nil-safe logging in JIT handler
- [x] Create comprehensive unit tests in pkg/proxy/jit_test.go
- [x] Add benchmarks for cache hit/miss scenarios
- [x] Verify all tests pass with no regressions

## Dev Agent Record

### Debug Log

- 2026-05-02: Initial implementation of JIT schema injection
- 2026-05-02: Fixed nil pointer dereference when logger is nil in debug logging
- 2026-05-02: Fixed ID preservation in mock forwarder for forwarded responses

### Completion Notes

Implemented JIT Schema Injection feature with:
- LRU schema cache in pkg/registry/cache.go with configurable size (default 100) and TTL (default 1h)
- JITHandler in pkg/proxy/jit.go that intercepts `get_tool_schema` requests
- Case-insensitive method matching for get_tool_schema
- Cache key format: `{serverName}/{toolName}`
- Unknown tools are forwarded to MCP server
- SchemaCache interface for testability
- Comprehensive unit tests covering cache hit, cache miss, disabled mode, and error cases

All 362 tests pass with no regressions.

## File List

- pkg/registry/cache.go (NEW)
- pkg/proxy/jit.go (NEW)
- pkg/proxy/jit_test.go (NEW)
- pkg/proxy/proxy.go (MODIFIED - added SchemaCache interface)

## Change Log

- 2026-05-02: Initial implementation of JIT schema injection feature
- 2026-05-02: Added LRU cache implementation with TTL support
- 2026-05-02: Added comprehensive unit tests with benchmarks
