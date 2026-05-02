# Story 7.4: Integrate Registry with Proxy for Dynamic Routing

Status: review

## Dev Agent Record

### Debug Log References

See existing implementation:
- `pkg/registry/registry.go:69-87` - Registry interface already exists
- `pkg/registry/registry.go:462-478` - Subscribe method already exists
- `pkg/registry/registry.go:481-491` - emitEvent already exists

### Project Context

Current project has:
- `pkg/registry/registry.go` - inMemoryRegistry with Subscribe for events
- `pkg/registry/registry.go:89-98` - inMemoryRegistry struct with servers map
- Story 7.1 provides routing that needs tool lookup
- Story 7.3 provides pool that needs server lookup

### References

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-7-Story-7.4]
- [Source: pkg/registry/registry.go:462-478] - Subscribe pattern to use
- [Source: pkg/registry/registry.go:258-272] - FindByCapability as pattern

## File List

- `pkg/registry/tools.go` (NEW)
- `pkg/registry/tools_test.go` (NEW)
- `pkg/router/registry.go` (MODIFY - simplified to interface only)

## Change Log

- 2026-05-02: Initial implementation of ToolRegistry interface in pkg/registry/tools.go with RegisterTool, UnregisterTool, GetToolServer, SearchTools, ListAllTools, SubscribeTools methods

## Tasks/Subtasks

- [x] Add ToolRegistry interface to pkg/registry
- [x] Implement RegisterTool/UnregisterTool
- [x] Implement GetToolServer for routing lookup
- [x] Implement SearchTools for gateway search
- [x] Add event subscription for tool changes
- [x] Integrate with routing from Story 7.1 (ToolRegistry already provided by router package)
- [x] Add unit tests

## Completion Notes

Implemented ToolRegistry interface in pkg/registry/tools.go providing:
- ToolEntry struct with Name, Namespace, ServerID
- ToolMatch struct with Score and MatchOn for search results
- ToolEvent and ToolEventType for event-driven updates
- NewToolRegistry constructor accepting logger
- RegisterTool: Register tools with server ID tracking, supports same tool across different servers
- UnregisterTool: Remove tool registration with proper cleanup of all indexes
- GetToolServer: Lookup server ID for a tool, returns error if ambiguous
- SearchTools: Fuzzy search with exact (100), contains (50), and prefix (30) matching
- ListAllTools: List all registered tools
- SubscribeTools: Event subscription for tool changes (ToolEventRegistered/ToolEventUnregistered)

Unit tests cover registration, lookup, search performance (1000+ tools < 1s), concurrency, and event subscription.

All 74 tests pass across registry, router, and gateway packages.
