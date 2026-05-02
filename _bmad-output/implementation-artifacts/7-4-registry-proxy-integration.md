# Story 7.4: Integrate Registry with Proxy for Dynamic Routing

Status: ready-for-dev

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 7.4 |
| **Key** | leanproxy-7-4 |
| **Epic** | epic-7 |
| **Title** | Integrate Registry with Proxy for Dynamic Routing |

## Story Requirements

### User Story

As a developer,
I want to integrate the server registry with the proxy for dynamic server selection,
So that servers can be added, removed, and updated without restarting the gateway.

### Acceptance Criteria (BDD Format)

```gherkin
Feature: Registry-Proxy Integration

  Scenario: Add new server via CLI updates routing
    Given a running gateway
    When a new server is added via "leanproxy server add"
    Then the server appears in the registry within 1 second
    And the server's tools become available for routing
    And the list_servers tool reflects the change

  Scenario: Remove server via CLI stops routing
    Given a running gateway
    When a server is removed via "leanproxy server remove"
    Then the server's subprocess is stopped
    And pending requests return an error
    And subsequent requests to that server's tools return method-not-found

  Scenario: Registry changes picked up without restart
    Given the registry is updated externally (e.g., config file change)
    When the proxy checks the registry
    Then it picks up changes without requiring restart
    And routes requests based on the current registry state

  Scenario: Server enabled/disabled toggling
    Given a server is running
    When the server is disabled via config update
    Then new requests to that server return error
    And existing requests complete or timeout
    And server process is eventually stopped

  Scenario: Tool registry stays in sync with server lifecycle
    Given a server's subprocess crashes and restarts
    When the server comes back online
    Then the tool registry is updated with current tool list
    And routing resumes to that server

  Scenario: Graceful degradation when registry unavailable
    Given the registry is temporarily unavailable
    When the proxy receives requests
    Then it returns error for new requests
    And it logs the unavailability
    And it retries registry connection periodically
```

## Developer Context

### Technical Requirements

1. **Dynamic Registry Interface**
   ```go
   type ToolRegistry interface {
       RegisterTool(serverID, toolName string, schema Schema) error
       UnregisterTool(serverID, toolName string) error
       GetToolServer(toolName string) (*ServerEntry, error)
       SearchTools(query string) []ToolMatch
       ListAllTools() []ToolEntry
   }
   ```

2. **Registry-Proxy Communication**
   - Proxy watches registry events via Subscribe channel
   - On EventRegistered: add to routing map
   - On EventUnregistered: remove from routing map, drain pending
   - On EventHealthChanged: update routing weights

3. **Event-Driven Updates**
   ```
   Server added → RegistryEvent{Type: EventRegistered} → Proxy updates routing
   Server removed → RegistryEvent{Type: EventUnregistered} → Proxy removes routing
   Server health changed → RegistryEvent{Type: EventHealthChanged} → Proxy updates weights
   ```

4. **Consistency Requirements**
   - Registry and routing map must be eventually consistent
   - No read-modify-write races in routing decisions
   - Registry operations must not block proxy hot path

### Architecture Compliance

- **Package**: `pkg/registry/` (existing - extend with ToolRegistry)
- **Interface**: `ToolRegistry` extending existing `Registry` interface
- **Naming**: camelCase for all exported symbols
- **Error Wrapping**: `fmt.Errorf("registry: context: %w", err)`
- **Logging**: All logs via `log/slog` to stderr

### File Structure

```
pkg/registry/
├── registry.go        # EXISTING - add ToolRegistry methods
├── tools.go           # NEW - tool registration and lookup
├── events.go          # EXISTING - already has Subscribe
└── doc.go             # EXISTING
```

### Dependencies

- Story 7.1 (Tool-to-Server Routing Engine) - routing needs registry
- Story 7.3 (Stdio Pool Manager) - pool needs registry for server lookup
- Existing `pkg/registry/registry.go` - extend with tool registration

### Testing Requirements

1. **Unit Tests**
   - Test tool registration and lookup
   - Test event publication on registry changes
   - Test search across multiple servers

2. **Integration Tests**
   - Test dynamic add/remove of servers
   - Test registry update propagation to routing

### Implementation Checklist

- [ ] Add ToolRegistry interface to pkg/registry
- [ ] Implement RegisterTool/UnregisterTool
- [ ] Implement GetToolServer for routing lookup
- [ ] Implement SearchTools for gateway search
- [ ] Add event subscription for tool changes
- [ ] Integrate with routing from Story 7.1
- [ ] Add unit tests

### Edge Cases

- Tool registered on non-existent server
- Tool name collision between servers
- Registry update during active routing
- Very large number of tools (10,000+) - search performance

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
- `pkg/registry/registry.go` (MODIFY - extend with ToolRegistry)
- `cmd/serve.go` (MODIFY - integrate registry with routing)
