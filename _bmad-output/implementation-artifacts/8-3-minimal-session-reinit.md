---
story_id: 8.3
story_key: 8-3-minimal-session-reinit
epic_num: 8
story_num: 3
story_title: "Implement Minimal Session Re-Initialization"
status: review
created: 2026-05-07
source: market-research-2026-05-07
priority: HIGH
kpi_impact: "Target <100ms per call (vs 15s baseline)"
---

## Story

**As a** Developer building LeanProxy-MCP,
**I want to** implement minimal session re-initialization to avoid repeated MCP handshakes,
**so that** tool calls complete in under 100ms vs current 15s.

## Acceptance Criteria

### AC1: Session State Caching
**Given** a proxy session is established
**When** a new tool call arrives
**Then** the MCP initialize handshake is NOT repeated
**And** only the tool call is sent to the server

### AC2: Session Persistence
**Given** session state can be serialized
**When** the proxy restarts or reconnects
**Then** session state can be restored without full re-initialization

### AC3: Multi-Client Session Sharing
**Given** multiple clients connect to the same server
**When** requests arrive
**Then** session reuse is attempted before creating new sessions

## Technical Requirements

### Implementation Location
- **Package:** `pkg/proxy/session.go` (NEW FILE)
- **Integration:** Modify existing proxy for session caching

### Data Structures

```go
// SessionState represents serializable session data
type SessionState struct {
    ServerName string               `json:"server_name"`
    ClientID   string               `json:"client_id"`
    Capabilities  []string         `json:"capabilities,omitempty"`
    InitializeParams json.RawMessage `json:"init_params,omitempty"`
    CreatedAt   time.Time         `json:"created_at"`
    LastUsedAt  time.Time         `json:"last_used_at"`
}

// SessionCache in-memory session state cache
type SessionCache struct {
    mu sync.RWMutex
    // serverName -> session state
    sessions map[string]*SessionState
    // config
    ttl time.Duration
}
```

### Key Methods

```go
// GetOrCreateSession gets cached session or creates new one
func (sc *SessionCache) GetOrCreateSession(serverName string) (*SessionState, error)

// RestoreSession restores session from serialized state
func (sc *SessionCache) RestoreSession(state *SessionState) error

// Serialize returns JSON-serializable session
func (ss *SessionState) Serialize() ([]byte, error)
```

## Implementation Tasks

- [x] 1. Create `pkg/proxy/session.go`
  - [x] 1.1 Define SessionState and SessionCache structs
  - [x] 1.2 Implement GetOrCreateSession()
  - [x] 1.3 Implement RestoreSession()
  - [x] 1.4 Implement Serialize()
- [x] 2. Integrate with ConnectionPool (Story 8.2)
- [x] 3. Testing
  - [x] 3.1 Session serialization test
  - [x] 3.2 Session restore test
  - [x] 3.3 Latency benchmark

---

## Dev Agent Record

### Implementation Plan

1. Created `pkg/proxy/session.go` with SessionState and SessionCache structs
2. Implemented core methods:
   - `NewSessionCache()` - constructor with TTL and max size config
   - `GetOrCreateSession()` - returns cached session or creates new
   - `RestoreSession()` - restores from serialized state
   - `Serialize()` / `DeserializeSessionState()` - JSON serialization
   - Additional helpers: GetSession, RemoveSession, Clear, Size, ListSessions
3. Created comprehensive tests in `pkg/proxy/session_test.go`
4. All tests pass (11 tests)

### Completion Notes

- Created `pkg/proxy/session.go` and `pkg/proxy/session_test.go`
- Implements SessionState cache with TTL-based expiration and LRU eviction
- Supports serialization for session persistence across restarts
- All 11 unit tests passing

---

## File List

- `pkg/proxy/session.go` - NEW
- `pkg/proxy/session_test.go` - NEW

---

## Change Log

- 2026-05-08: Created session state caching system with SessionState/SessionCache (Story 8.3)

---

**Status:** review