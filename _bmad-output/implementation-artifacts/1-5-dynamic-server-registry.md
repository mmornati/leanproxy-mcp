# Story 1-5: Implement Dynamic Server Registry

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 1-5 |
| **Key** | tokengate-mcp-1-5 |
| **Epic** | tokengate-mcp-epic-1 |
| **Title** | Implement Dynamic Server Registry |

## Story Requirements

### User Story

```
As a developer
I want to register, discover, and manage MCP servers dynamically
So that the proxy can route traffic to the appropriate server based on request content
```

### Acceptance Criteria (BDD Format)

```gherkin
Feature: Dynamic Server Registry

  Scenario: Register a new MCP server
    Given I have a server configuration
    When I call registry.Register(config)
    Then the server should be added to registry
    And the server should be discoverable by ID
    And the registry should return the server's address

  Scenario: Unregister a server
    Given a server is registered
    When I call registry.Unregister(serverID)
    Then the server should be removed from registry
    And subsequent lookups should return not found
    And active connections should be handled gracefully

  Scenario: Discover servers by capability
    Given multiple servers with different capabilities
    When I query registry.FindByCapability("code-complete")
    Then I should receive all servers supporting that capability
    And the results should not include servers without that capability

  Scenario: Discover server by transport type
    Given servers with different transport types
    When I query registry.FindByTransport("stdio")
    Then I should receive servers using stdio transport
    When I query registry.FindByTransport("http")
    Then I should receive servers using HTTP transport

  Scenario: Server health tracking
    Given a server is registered and running
    When the server becomes unhealthy
    Then registry should update server status
    And registry should optionally unregister unhealthy servers
    And registry should emit health change events

  Scenario: Concurrent access to registry
    Given multiple goroutines accessing registry
    When concurrent Register/Unregister/Find operations occur
    Then all operations should complete without race conditions
    And data consistency should be maintained

  Scenario: Registry persistence
    Given servers are registered
    When the application restarts
    Then previously registered servers should be recoverable
    And their configurations should be restored

  Scenario: Server lookup by request criteria
    Given multiple servers with overlapping capabilities
    When I send a request with specific requirements
    Then registry should return the most appropriate server
    And selection should consider load, health, and capability match
```

## Developer Context

### Technical Requirements

1. **Registry Storage**
   - In-memory map with thread-safe access
   - RWMutex for concurrent read/write
   - Optional persistent storage backend

2. **Server Metadata**
   ```go
   type ServerEntry struct {
       ID          string
       Config      *ServerConfig
       Address     string
       Transport   TransportType
       Capabilities []string
       Health      HealthStatus
       Stats       ServerStats
       RegisteredAt time.Time
       LastSeenAt   time.Time
   }

   type TransportType string
   const (
       TransportStdio TransportType = "stdio"
       TransportHTTP   TransportType = "http"
       TransportSSE    TransportType = "sse"
   )
   ```

3. **Query Capabilities**
   - Index by capability for fast lookup
   - Support capability wildcards
   - Rank results by relevance

4. **Health Monitoring**
   - Periodic health checks
   - Configurable thresholds
   - Graceful degradation

### Architecture Compliance

- **Package**: `pkg/registry/registry.go`
- **Interface**: `Registry` interface
- **Naming**: camelCase for all exported symbols
- **Error Wrapping**: `fmt.Errorf("registry: context: %w", err)`
- **Logging**: All logs via `log/slog` to stderr
- **Performance**: Lookup < 1ms, registration < 5ms

### File Structure

```
pkg/registry/
├── registry.go        # Server registry implementation
├── registry_test.go   # Unit tests
└── doc.go            # Package documentation
```

### API Design

```go
// Registry manages MCP server registration and discovery
type Registry interface {
    // Registration
    Register(ctx context.Context, entry ServerEntry) error
    Unregister(ctx context.Context, id string) error
    Update(ctx context.Context, entry ServerEntry) error

    // Discovery
    Get(ctx context.Context, id string) (*ServerEntry, error)
    List(ctx context.Context) ([]*ServerEntry, error)
    FindByCapability(ctx context.Context, capability string) ([]*ServerEntry, error)
    FindByTransport(ctx context.Context, transport TransportType) ([]*ServerEntry, error)
    FindBest(ctx context.Context, criteria MatchCriteria) (*ServerEntry, error)

    // Health
    UpdateHealth(ctx context.Context, id string, health HealthStatus) error
    ListUnhealthy(ctx context.Context) ([]*ServerEntry, error)

    // Persistence
    Save(ctx context.Context) error
    Load(ctx context.Context) error

    // Events
    Subscribe(ch chan<- RegistryEvent) func()
}

// MatchCriteria defines server selection requirements
type MatchCriteria struct {
    Capabilities []string
    Transport    TransportType
    MinHealth    HealthStatus
    MaxLoad      float64
}

// RegistryEvent emitted on registry changes
type RegistryEvent struct {
    Type    EventType
    Server  *ServerEntry
    Details string
}

type EventType int
const (
    EventRegistered EventType = iota
    EventUnregistered
    EventHealthChanged
)
```

### Testing Requirements

1. **Unit Tests**
   - Test register/unregister
   - Test discovery queries
   - Test capability matching
   - Test health updates

2. **Concurrency Tests**
   - Test with 10+ concurrent goroutines
   - Test race detector passes
   - Test data consistency

3. **Integration Tests**
   - Test with lifecycle manager integration
   - Test persistence round-trip
   - Test health check loop

### Implementation Checklist

- [ ] Create ServerEntry and related types
- [ ] Create Registry interface
- [ ] Create inMemoryRegistry struct
- [ ] Implement thread-safe storage with RWMutex
- [ ] Implement Register/Unregister/Update
- [ ] Implement Get and List
- [ ] Implement FindByCapability with index
- [ ] Implement FindByTransport
- [ ] Implement FindBest with ranking
- [ ] Implement health tracking
- [ ] Implement event subscription
- [ ] Implement persistence (JSON file)
- [ ] Add unit tests
- [ ] Add concurrency tests
- [ ] Verify race detector passes

### Edge Cases

- Register duplicate server ID
- Unregister non-existent server
- Concurrent register/unregister same ID
- Empty capability list query
- Server with all capabilities disabled
- Health check timeout
- Persistence file corruption
- Very large number of servers (> 1000)
- Rapid health status changes
- Event channel overflow

### Notes

- Use sync.RWMutex for thread safety
- Consider read-heavy optimization
- Implement capability index as map[string][]string
- Use buffered channels for events
- Consider eventual consistency for health
- Keep persistence format simple (JSON)
- Log all registry operations at debug level
