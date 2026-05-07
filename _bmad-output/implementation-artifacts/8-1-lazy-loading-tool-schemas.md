---
story_id: 8.1
story_key: 8-1-lazy-loading-tool-schemas
epic_num: 8
story_num: 1
story_title: "Implement Lazy-Loading Tool Schemas"
status: ready-for-dev
created: 2026-05-07
source: market-research-2026-05-07
priority: CRITICAL
kpi_impact: "6-7x token reduction"
---

## Story

**As a** Developer building LeanProxy-MCP,
**I want to** implement lazy-loading tool schemas that load on-demand rather than at startup,
**so that** initial context overhead is dramatically reduced (6-7x token savings).

## Acceptance Criteria

### AC1: Stub Schema Generation
**Given** 10 MCP servers with 100 tools total configured
**When** the proxy starts in lazy-loading mode
**Then** only compact tool stubs (~54 tokens each) are sent to the IDE
**And** full schemas are loaded only when a tool is actually invoked

### AC2: On-Demand Schema Loading
**Given** an IDE requests `get_tool_schema` for a specific tool
**When** the lazy-loading proxy receives the request
**Then** it fetches the full schema from the MCP server
**And** caches it for subsequent requests
**And** returns the complete schema

### AC3: Session Token Savings
**Given** a tool is NOT invoked within a session
**When** the session ends
**Then** the full schema was never loaded
**And** token savings are achieved

### AC4: Legacy Mode Support
**Given** lazy-loading mode is disabled in config
**When** the proxy starts
**Then** all full schemas are loaded at startup (legacy behavior)

## Technical Requirements

### Implementation Location
- **Package:** `pkg/registry/lazy.go` (NEW FILE)
- **Integration:** Modify existing `pkg/registry/` for lazy-loading integration

### Data Structures

```go
// ToolStub represents minimal tool info sent at startup
type ToolStub struct {
    Name        string `json:"name"`
    Description string `json:"description"` // ~54 tokens, 1-line
    Category    string `json:"category,omitempty"`
}

// LazySchemaCache in-memory cache for full schemas
type LazySchemaCache struct {
   mu sync.RWMutex
    // toolName -> cached full schema
    cache map[string]ToolSchema
    // toolName -> last access time (for TTL)
    lastAccess map[string]time.Time
    // TTL configuration
    ttl time.Duration
}
```

### Configuration

```yaml
# leanproxy.yaml
optimization:
  lazy_loading:
    enabled: true  # default: true
    stub_tokens: 54  # tokens per stub
    cache_ttl: 24h   # cache validity
    prewarm: []     # tools to pre-load
```

### API Changes

#### `tools/list` Handler (MODIFY)
- Current: Returns full tool schemas
- New: Returns ToolStub only, not full schema

#### `get_tool_schema` Handler (MODIFY)
- Current: Returns full schema
- New: Check cache → fetch if missing → cache → return

### Key Patterns

1. **Cache Hit:** Return cached schema (no MCP call)
2. **Cache Miss:** Fetch from MCP server → Cache → Return
3. **Cache Expired:** Re-fetch on next request

## Developer Context

### Architecture Integration

| Component | File | Action |
|-----------|------|--------|
| Registry | `pkg/registry/registry.go` | MODIFY - Add lazy loading |
| Proxy | `pkg/proxy/server.go` | MODIFY - Wire lazy registry |
| Config | `pkg/migrate/config.go` | MODIFY - Add lazy_loading config |

### Existing Code to Reference

- `pkg/registry/registry.go` - Current tool registration
- `pkg/proxy/server.go` - JSON-RPC handlers

### Testing Requirements

- Unit: LazySchemaCache operations
- Integration: Stub vs full schema comparison
- Token count verification: ~54 tokens vs ~500 tokens

## Web Research Reference

**Based on:** `mcp-lazy-proxy` npm package patterns

**Key findings:**
- Lazy-loading achieves 6-7x token reduction
- Disk caching with 24h TTL
- Per-session savings proof logging

**Library:** `mcp-lazy-proxy` (npm) - Reference implementation
- Language: Node.js/npm
- Mechanism: Lazy-load on call
- Schema caching: Disk (24h TTL)

## Implementation Tasks

- [ ] 1. Create `pkg/registry/lazy.go` with LazySchemaCache
  - [ ] 1.1 Define ToolStub and LazySchemaCache structs
  - [ ] 1.2 Implement NewLazySchemaCache() constructor  
  - [ ] 1.3 Implement GetStub() method
  - [ ] 1.4 Implement GetFullSchema() with cache lookup
  - [ ] 1.5 Implement CacheWithTTL() for expiration
- [ ] 2. Modify `pkg/registry/registry.go` 
  - [ ] 2.1 Add lazy loading configuration
  - [ ] 2.2 Wire LazySchemaCache into registry
  - [ ] 2.3 Handle stub-only list responses
- [ ] 3. Modify `pkg/proxy/server.go`
  - [ ] 3.1 Update tools/list handler for stubs
  - [ ] 3.2 Update get_tool_schema handler for lazy loading
- [ ] 4. Add configuration parsing in `pkg/migrate/config.go`
  - [ ] 4.1 Add lazy_loading config section
  - [ ] 4.2 Parse YAML configuration
- [ ] 5. Testing
  - [ ] 5.1 Unit tests for LazySchemaCache
  - [ ] 5.2 Integration tests with mock MCP server
  - [ ] 5.3 Token count verification test

## Dev Notes

### Key Patterns from Market Research

1. **Compression:** ~54 tokens per stub (vs 344+ full)
2. **Caching:** In-memory with TTL, disk backup optional
3. **Trigger:** First get_tool_schema call loads full schema

### Trade-offs Considered

| Aspect | Legacy | Lazy-loading |
|--------|--------|-------------|
| Initial load | 100% tokens | ~15% tokens |
| First call | N/A | +1 call latency |
| Memory | Low | ~50MB for 100 servers |
| Complexity | None | Medium |

### Success Metrics

- Token reduction: Target 6-7x (verified by mcp-lazy-proxy)
- Latency: First call adds <100ms (acceptable)
- Memory: <100MB for 100 servers (acceptable)

## References

- [Source: /planning-artifacts/epics.md#Epic-8-Story-8.1]
- [Source: /planning-artifacts/architecture.md#Epic-8-Token-Optimization]
- [Source: /planning-artifacts/research/market-mcp-proxy-server-features-token-savings-latency-2026-research-2026-05-07.md]

---

## Story Completion Status

**Status:** ready-for-dev  
**Created:** 2026-05-07  
**Source:** Market Research findings  
**Priority:** CRITICAL - Your primary KPI (token minimization)