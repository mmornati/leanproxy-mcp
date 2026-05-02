# Story 7.2: Expose Gateway Tools to IDE

Status: review

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 7.2 |
| **Key** | leanproxy-7-2 |
| **Epic** | epic-7 |
| **Title** | Expose Gateway Tools to IDE |

## Story Requirements

### User Story

As a developer,
I want to expose internal gateway tools (list_servers, invoke_tool, search_tools) to the IDE,
So that the AI can discover and invoke tools across all configured MCP servers through a unified interface.

### Acceptance Criteria (BDD Format)

```gherkin
Feature: Gateway Tools Exposure

  Scenario: IDE requests tool list via tools/list
    Given LeanProxy-MCP is running as a gateway
    When the IDE sends a JSON-RPC request for tools/list
    Then the response includes gateway tools: list_servers, invoke_tool, search_tools
    And each gateway tool has a minimal discovery signature (name + description)
    And gateway tools do NOT include underlying MCP server tools (to reduce initial context)

  Scenario: AI calls list_servers
    Given the AI calls list_servers()
    When the gateway receives the request
    Then it returns a list of all configured MCP servers with:
    - name: server identifier
    - status: running/stopped/error
    - transport: stdio/http/sse
    - tool_count: number of tools available

  Scenario: AI calls invoke_tool
    Given the AI calls invoke_tool(server_name, tool_name, params)
    When the gateway receives the request
    Then it validates server_name exists and is running
    And it validates tool_name exists on that server
    And it routes the request to the specified server
    And it returns the tool response

  Scenario: AI calls search_tools
    Given the AI calls search_tools(query)
    When the gateway receives the request
    Then it searches tool names and descriptions across all servers
    And it returns matching tools with server attribution
    And results include: tool_name, server_name, description snippet

  Scenario: invoke_tool with invalid server
    Given the AI calls invoke_tool("nonexistent", "create_issue", {})
    When the gateway receives the request
    Then it returns JSON-RPC error with code -32602 (Invalid params)
    And error message indicates server not found

  Scenario: Gateway tools are discoverable first
    Given the IDE connects to LeanProxy-MCP
    When the IDE requests available tools
    Then gateway tools appear FIRST in the list
    And underlying server tools appear only when explicitly requested via search_tools
```

## Developer Context

### Technical Requirements

1. **Gateway Tool Registration**
   - On gateway startup, register three gateway tools in the registry
   - Gateway tools are pseudo-tools that get translated to actual server calls
   - They follow MCP tool format: `{ name, description, inputSchema }`

2. **Gateway Tools Schema**
   ```go
   // list_servers tool
   ListServersTool = Tool{
       Name: "list_servers",
       Description: "List all MCP servers configured in this gateway",
       InputSchema: empty object
   }

   // invoke_tool tool
   InvokeToolTool = Tool{
       Name: "invoke_tool",
       Description: "Invoke a tool on a specific MCP server",
       InputSchema: {
           type: "object",
           properties: {
               server_name: { type: "string" },
               tool_name: { type: "string" },
               arguments: { type: "object" }
           },
           required: ["server_name", "tool_name"]
       }
   }

   // search_tools tool
   SearchToolsTool = Tool{
       Name: "search_tools",
       Description: "Search for tools across all configured MCP servers",
       InputSchema: {
           type: "object",
           properties: {
               query: { type: "string" }
           },
           required: ["query"]
       }
   }
   ```

3. **Tool Execution Flow**
   ```
   IDE request: tools/call with invoke_tool
   → Gateway receives request
   → Gateway parses: { server_name, tool_name, arguments }
   → Gateway looks up target server in registry
   → Gateway forwards actual tool call to target server
   → Gateway returns response to IDE
   ```

4. **Tool Name Collision Handling**
   - If multiple servers have the same tool name, search_tools returns all matches
   - invoke_tool requires fully qualified name OR disambiguation via server_name
   - Error returned if tool exists on multiple servers without server_name

### Architecture Compliance

- **Package**: `pkg/gateway/` (new package for gateway-specific logic)
- **Interface**: `GatewayTools` interface with `ListTools()`, `InvokeTool()`, `SearchTools()`
- **Naming**: camelCase for all exported symbols
- **Error Wrapping**: `fmt.Errorf("gateway: context: %w", err)`
- **Logging**: All logs via `log/slog` to stderr

### File Structure

```
pkg/gateway/
├── tools.go           # Gateway tool definitions and registration
├── executor.go        # Tool execution logic
├── search.go          # Tool search implementation
├── gateway_test.go    # Unit tests
└── doc.go             # Package documentation
```

### Dependencies

- Story 7.1 (Tool-to-Server Routing Engine) MUST be completed first
- Uses `pkg/router/` for actual request routing
- Uses `pkg/registry/` for server lookup

### Testing Requirements

1. **Unit Tests**
   - Test list_servers returns correct server list
   - Test invoke_tool routes correctly
   - Test search_tools finds matching tools
   - Test error handling for invalid inputs

2. **Integration Tests**
   - Test gateway tools appear in tool list
   - Test round-trip invoke_tool through multiple servers

### Implementation Checklist

- [x] Create `pkg/gateway/tools.go` with tool definitions
- [x] Implement ListTools() returning gateway tool list
- [x] Implement InvokeTool() with routing to target server
- [x] Implement SearchTools() with cross-server search
- [x] Integrate with router from Story 7.1
- [x] Add unit tests

### Edge Cases

- invoke_tool with server_name that exists but is stopped
- invoke_tool with tool_name that doesn't exist on server
- search_tools with empty query (return all tools)
- search_tools with special characters
- Very large number of servers (100+)

## Dev Agent Record

### Debug Log References

See nexus-dev gateway implementation for reference patterns:
- Nexus-Dev exposes: `search_tools`, `invoke_tool`, `list_servers`, `get_gateway_prompt`, `get_gateway_metrics`

### Project Context

Current project has:
- `pkg/registry/registry.go` - ServerEntry structure to use for list_servers response
- `pkg/proxy/proxy.go:199-217` - JSONRPCRequest/Response types for parsing/building
- Story 7.1 provides routing mechanism this depends on

### References

- [Source: _bmad-output/planning-artifacts/epics.md#Epic-7-Story-7.2]
- [Source: nexus-dev README.md#MCP-Gateway-Mode] - Reference implementation
- [Source: pkg/registry/registry.go:245-256] - List() method for server enumeration

## File List

- `pkg/gateway/tools.go` (NEW)
- `pkg/gateway/executor.go` (NEW)
- `pkg/gateway/search.go` (NEW)
- `pkg/gateway/gateway_test.go` (NEW)
- `pkg/gateway/doc.go` (NEW)
- `cmd/serve.go` (MODIFY - integrate gateway tools)

## Dev Agent Record

### Implementation Plan

Created `pkg/gateway/` package with three gateway tools exposed to IDE:
- `list_servers`: Returns list of all configured MCP servers with status, transport, and tool count
- `invoke_tool`: Routes tool calls to appropriate server after validation
- `search_tools`: Searches tool names across all servers

### Completion Notes

Implemented all acceptance criteria:
- Gateway tools (list_servers, invoke_tool, search_tools) return minimal discovery signatures
- list_servers returns name, status, transport, and tool_count for each server
- invoke_tool validates server_name and tool_name, routes to target server
- search_tools returns matching tools with server attribution
- Invalid server returns JSON-RPC error -32602
- All edge cases handled: stopped servers, missing tools, empty query

### Technical Decisions

1. Used direct tool registry lookup instead of router for InvokeTool to properly validate server ownership
2. Tool matching considers both exact names and server-qualified names (e.g., "github.create_issue")
3. All logging via log/slog as per architecture requirements

## Change Log

- 2026-05-02: Initial implementation of gateway tools package (story 7-2)
