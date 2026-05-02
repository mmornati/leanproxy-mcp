# Story 3-1: Discovery Signatures

## Header

| Field | Value |
|-------|-------|
| ID | 3-1 |
| Key | discovery-signatures |
| Epic | Epic 3: Context Optimization (JIT Discovery & Compactor) |
| Title | Implement Discovery Signatures |
| Status | review |
| Estimated Points | 3 |

## User Story

**As a** developer,
**I want to** register tools with minimal "Discovery Signatures" (name + description only),
**So that** the initial context overhead is dramatically reduced.

## Acceptance Criteria (BDD Format)

### AC1: Minimal Discovery Payload

**Given** a full MCP tool schema with name, description, and complex parameters
**When** the registry processes it for initial discovery
**Then** only the tool name and a brief description are stored
**And** the full JSON schema is NOT included in the initial manifest
**And** the resulting discovery payload is under 500 bytes per tool

### AC2: Scaled Discovery Response

**Given** 10 MCP servers with 50 tools each
**When** the IDE requests the tool list
**Then** the response includes all 50 tool names and descriptions
**And** the total payload is under 25KB (vs potentially 500KB+ with full schemas)

### AC3: Signature Update on Refresh

**Given** a tool's description needs updating
**When** the manifest is refreshed
**Then** the discovery signature is also updated

## Developer Context

### Technical Requirements

1. **Discovery Signature Structure**
   - Create a `DiscoverySignature` struct in `pkg/registry/` with only `Name` and `Description` fields
   - Store full schemas separately in a `schemaCache` map indexed by tool name
   - Discovery signatures must be serializable for caching

2. **Registry Integration**
   - Modify existing `Tool` struct to support dual storage (signature vs full schema)
   - Add `GetDiscoverySignatures()` method that returns only signatures
   - Add `GetFullSchema(toolName string)` method for JIT schema retrieval

3. **MCP Protocol Compliance**
   - Discovery response must still be valid JSON-RPC 2.0
   - Tool list response format: `{"jsonrpc": "2.0", "result": {"tools": [{name, description}]}}`

4. **Configuration**
   - Add `registry.compact-by-default` config option (default: true)
   - Add `registry.max-signature-bytes` config option (default: 500)

### Architecture Compliance

- **Naming**: `camelCase` for Go functions/variables, `kebab-case` for CLI flags
- **Error Handling**: `fmt.Errorf("context: %w", err)` for error wrapping
- **Logging**: `log/slog` for structured logging to stderr
- **Project Structure**: `cmd/` for CLI, `pkg/registry/` for registry logic

### File Structure

```
pkg/
├── registry/
│   ├── registry.go           # Core registry types and interface
│   ├── signatures.go         # Discovery signature management
│   ├── signatures_test.go   # Unit tests for signatures
│   └── manifest.go           # Manifest loading and merging
```

### Testing Requirements

1. **Unit Tests**
   - Test `NewDiscoverySignature` creates signature under 500 bytes
   - Test `GetDiscoverySignatures` returns only name/description
   - Test `GetFullSchema` returns cached schema correctly
   - Test serialization/deserialization of signatures

2. **Integration Tests**
   - Test with mock MCP server providing 50+ tools
   - Verify discovery payload size constraint

3. **Performance Tests**
   - Verify signature generation adds <5ms overhead per tool
   - Verify 50-tool discovery response under 25KB

## Implementation Notes

### Discovery Signature Schema

```go
type DiscoverySignature struct {
    Name        string `json:"name"`
    Description string `json:"description"`
}
```

### Full Schema Storage

```go
type Tool struct {
    Signature   DiscoverySignature
    FullSchema  json.RawMessage  // Cached full JSON schema
}
```

### Key Methods

```go
// pkg/registry/signatures.go
func NewDiscoverySignature(name, description string, fullSchema json.RawMessage) (*DiscoverySignature, error)
func (r *Registry) GetDiscoverySignatures() []DiscoverySignature
func (r *Registry) GetFullSchema(toolName string) (json.RawMessage, error)
func (r *Registry) RegisterTool(tool Tool) error
```

## Tasks/Subtasks

- [x] Create DiscoverySignature struct in pkg/registry/signatures.go
- [x] Create Tool struct with dual storage (signature + full schema)
- [x] Implement GetDiscoverySignatures method
- [x] Implement GetFullSchema method for JIT schema retrieval
- [x] Add RegistryConfig with compact-by-default and max-signature-bytes options
- [x] Write unit tests for all signature functionality
- [x] Verify payload size constraints

## Dev Agent Record

### Debug Log

### Completion Notes

Implemented Discovery Signatures feature per story 3-1. Created:
- `pkg/registry/signatures.go` - Core discovery signature types and ToolSchemaRegistry interface
- `pkg/registry/signatures_test.go` - Comprehensive unit tests including 50-tool payload size verification
- `pkg/registry/config.go` - Registry configuration with compact-by-default and max-signature-bytes options

All acceptance criteria satisfied:
- AC1: Minimal discovery payload (<500 bytes per tool)
- AC2: 50-tool discovery response verified under 25KB
- AC3: RefreshManifest method available for signature updates

## File List

- pkg/registry/signatures.go (new)
- pkg/registry/signatures_test.go (new)
- pkg/registry/config.go (new)

## Change Log

- 2026-05-02: Implemented discovery signatures feature - Initial implementation with DiscoverySignature struct, Tool dual storage, GetDiscoverySignatures(), GetFullSchema(), and configuration options
