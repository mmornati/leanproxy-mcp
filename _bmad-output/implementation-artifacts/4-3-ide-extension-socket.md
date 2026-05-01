# Story 4-3: Implement IDE Extension Socket

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 4-3 |
| **Key** | ide-extension-socket |
| **Epic** | Epic 4 - CLI Installation and Interaction |
| **Title** | Implement IDE Extension Socket |
| **Priority** | High |
| **Status** | ready-for-dev |

## Story Requirements

### User Story

As an IDE extension, I want to communicate with tokengate via a Unix domain socket so that I can provide token gating features directly in the developer's editor.

### Acceptance Criteria (BDD Format)

```gherkin
Feature: IDE Extension Socket Communication
  Scenario: Socket server starts on configured path
    Given tokengate is running with socket enabled
    And socket path is set to "/tmp/tokengate.sock"
    When an IDE extension connects to the socket
    Then a connection is established
    And the server accepts the connection

  Scenario: Socket handles JSON-RPC requests
    Given tokengate is running with socket enabled
    And a client connects to the socket
    When the client sends '{"jsonrpc":"2.0","method":"token.resolve","params":{"uri":"api://example"},"id":1}'
    Then the server responds with '{"jsonrpc":"2.0","result":{...},"id":1}'
    And the token is resolved from registry

  Scenario: Socket supports multiple concurrent connections
    Given tokengate is running with socket enabled
    When multiple IDE extensions connect simultaneously
    Then all connections are handled concurrently
    And requests are processed in parallel

  Scenario: Socket cleans up on graceful shutdown
    Given tokengate is running with socket enabled
    When the user sends SIGTERM
    Then the socket file is removed
    And all connections are closed gracefully

  Scenario: Socket rejects invalid requests
    Given tokengate is running with socket enabled
    And a client connects to the socket
    When the client sends malformed JSON
    Then the server responds with JSON-RPC error
    And the server continues to accept new requests

  Scenario: Windows named pipe fallback
    Given tokengate is running on Windows
    And socket is enabled
    Then a named pipe is created instead of Unix socket
    And the same JSON-RPC protocol is used
```

## Developer Context

### Technical Requirements

1. **Socket Implementation**
   - Use `net.Listen()` with `unix` or `tcp` network types
   - Unix socket path: `~/.tokengate/tokengate.sock` (configurable)
   - Windows: Use `\\.\pipe\tokengate` named pipe
   - Socket permissions: `0700` (owner only) for Unix sockets
   - Auto-create parent directory if missing

2. **Protocol**
   - JSON-RPC 2.0 specification compliance
   - Transport: persistent socket connection
   - Encoding: UTF-8 JSON
   - Message framing: newline-delimited JSON (JSON-RPC batch supported)

3. **Socket Server**
   - Implement in `pkg/proxy/socket/server.go`
   - Use goroutine per connection pattern
   - Implement graceful shutdown with `context.Context`
   - Handle SIGHUP for socket refresh (development mode)

4. **Supported Methods**
   - `token.resolve`: Resolve token from URI
   - `token.validate`: Validate token with policy
   - `proxy.status`: Get proxy status
   - `proxy.restart`: Restart proxy
   - `config.get`: Get configuration value
   - `config.set`: Set configuration value (runtime)
   - `shutdown`: Graceful shutdown

5. **Socket Security**
   - Validate socket file permissions on startup
   - Reject connections from other users on Unix (optional, configurable)
   - Rate limiting: 100 requests/second per connection
   - Max message size: 1MB

### Architecture Compliance

- All Go code uses camelCase for functions and variables
- CLI flags use kebab-case (e.g., `--socket-path`)
- Error wrapping: `fmt.Errorf("socket: %w", err)`
- Structured logging via `log/slog` to stderr
- Interface: `SocketServer` interface in `pkg/proxy/socket/socket.go`

### File Structure

```
cmd/
  tokengate/
    main.go                    # Entry point, register socket commands
    serve.go                   # Background serve command for socket

pkg/
  proxy/
    socket/
      socket.go                # SocketServer interface
      server.go                # Server implementation
      server_test.go           # Unit tests
      handler.go               # JSON-RPC request handler
      handler_test.go           # Handler tests
      transport_unix.go         # Unix socket transport
      transport_windows.go      # Windows named pipe transport
      examples_test.go          # Example usage tests
```

### Testing Requirements

1. **Unit Tests**
   - `pkg/proxy/socket/server_test.go`: Test server lifecycle
   - `pkg/proxy/socket/handler_test.go`: Test JSON-RPC parsing
   - Test each JSON-RPC method handler

2. **Integration Tests**
   - Test socket creation and deletion
   - Test JSON-RPC request/response cycle
   - Test concurrent connections
   - Test graceful shutdown
   - Test malformed request handling

3. **Test Patterns**
   ```go
   func TestSocketServer(t *testing.T) {
       tmpDir := t.TempDir()
       socketPath := filepath.Join(tmpDir, "test.sock")
       
       server, err := socket.NewServer(socketPath)
       assert.NoError(t, err)
       
       go server.Serve()
       defer server.Shutdown()
       
       // Connect and send request
       conn, err := net.Dial("unix", socketPath)
       assert.NoError(t, err)
       defer conn.Close()
       
       // Send JSON-RPC request
       req := `{"jsonrpc":"2.0","method":"token.resolve","params":{"uri":"api://example"},"id":1}`
       _, err = fmt.Fprintf(conn, "%s\n", req)
       assert.NoError(t, err)
       
       // Read response
       resp := make([]byte, 4096)
       n, err := conn.Read(resp)
       assert.NoError(t, err)
       
       var rpcResp map[string]interface{}
       json.Unmarshal(resp[:n], &rpcResp)
       assert.Equal(t, float64(1), rpcResp["id"])
   }
   ```

### Implementation Notes

1. Use `golang.org/x/sys/unix` for socket-specific operations
2. Implement socket abstract namespace option for Linux (optional)
3. Use `sync.WaitGroup` for graceful connection draining
4. Socket should be configurable as startup flag or via config file
5. Implement `tokengate serve` command for background mode
6. Support socket over TCP for containerized environments (configurable)
