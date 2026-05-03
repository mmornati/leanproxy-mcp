# Story 4-3: Implement IDE Extension Socket

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 4-3 |
| **Key** | ide-extension-socket |
| **Epic** | Epic 4 - CLI Installation and Interaction |
| **Title** | Implement IDE Extension Socket |
| **Priority** | High |
| **Status** | review |

## Story Requirements

### User Story

As an IDE extension, I want to communicate with leanproxy via a Unix domain socket so that I can provide token gating features directly in the developer's editor.

### Acceptance Criteria (BDD Format)

```gherkin
Feature: IDE Extension Socket Communication
  Scenario: Socket server starts on configured path
    Given leanproxy is running with socket enabled
    And socket path is set to "/tmp/leanproxy.sock"
    When an IDE extension connects to the socket
    Then a connection is established
    And the server accepts the connection

  Scenario: Socket handles JSON-RPC requests
    Given leanproxy is running with socket enabled
    And a client connects to the socket
    When the client sends '{"jsonrpc":"2.0","method":"token.resolve","params":{"uri":"api://example"},"id":1}'
    Then the server responds with '{"jsonrpc":"2.0","result":{...},"id":1}'
    And the token is resolved from registry

  Scenario: Socket supports multiple concurrent connections
    Given leanproxy is running with socket enabled
    When multiple IDE extensions connect simultaneously
    Then all connections are handled concurrently
    And requests are processed in parallel

  Scenario: Socket cleans up on graceful shutdown
    Given leanproxy is running with socket enabled
    When the user sends SIGTERM
    Then the socket file is removed
    And all connections are closed gracefully

  Scenario: Socket rejects invalid requests
    Given leanproxy is running with socket enabled
    And a client connects to the socket
    When the client sends malformed JSON
    Then the server responds with JSON-RPC error
    And the server continues to accept new requests

  Scenario: Windows named pipe fallback
    Given leanproxy is running on Windows
    And socket is enabled
    Then a named pipe is created instead of Unix socket
    And the same JSON-RPC protocol is used
```

## Developer Context

### Technical Requirements

1. **Socket Implementation**
   - Use `net.Listen()` with `unix` or `tcp` network types
   - Unix socket path: `~/.leanproxy/leanproxy.sock` (configurable)
   - Windows: Use `\\.\pipe\leanproxy` named pipe
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
  leanproxy/
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
5. Implement `leanproxy serve` command for background mode
6. Support socket over TCP for containerized environments (configurable)

## Dev Agent Record

### Implementation Notes

Implemented IDE Extension Socket feature with the following components:

1. **SocketServer Interface** (`pkg/proxy/socket/socket.go`)
   - Defined `SocketServer` interface with `Serve`, `Shutdown`, and `Addr` methods
   - Added `ServerConfig` struct with configurable path, permissions, max message size, and rate limit

2. **Socket Server** (`pkg/proxy/socket/server.go`)
   - Implemented JSON-RPC 2.0 compliant server using goroutine per connection pattern
   - Uses `bufio.Reader` for line-delimited message parsing
   - Supports concurrent connections with atomic connection counter
   - Implements graceful shutdown with `sync.WaitGroup` for connection draining
   - Handles SIGHUP, SIGINT, SIGTERM for socket refresh and shutdown

3. **JSON-RPC Handler** (`pkg/proxy/socket/handler.go`)
   - Registered methods: `token.resolve`, `token.validate`, `proxy.status`, `proxy.restart`, `config.get`, `config.set`, `shutdown`
   - Interfaces for TokenResolver, ProxyStatusProvider, ConfigGetter, ConfigSetter

4. **Transport Layers**
   - `transport_unix.go`: Unix socket transport with permission validation
   - `transport_windows.go`: Windows named pipe transport with build tags

5. **CLI Command** (`cmd/leanproxy/serve.go`)
   - `leanproxy serve` command for background socket server
   - Flags: `--socket-path`, `--socket-perm`, `--enable`

6. **Tests**
   - `server_test.go`: Server lifecycle, JSON-RPC parsing, malformed requests, concurrent connections, message size limits
   - `handler_test.go`: Handler method tests
   - `testing.go`: Shared mock implementations

### Debug Log

- Fixed missing imports in server.go (added "bufio", "runtime")
- Fixed os.Dir usage to filepath.Dir in transport_unix.go
- Fixed syntax errors in transport_unix.go (missing assignment operator)
- Resolved test file conflicts by moving mockTokenResolver to testing.go

### Completion Notes

All acceptance criteria implemented:
- Socket server starts on configured path with proper permissions
- JSON-RPC request/response handling working
- Concurrent connections supported via goroutine per connection pattern
- Graceful shutdown cleans up socket file
- Invalid JSON returns proper JSON-RPC error responses
- Windows named pipe transport implemented with build tags

## File List

- pkg/proxy/socket/socket.go (new)
- pkg/proxy/socket/server.go (new)
- pkg/proxy/socket/handler.go (new)
- pkg/proxy/socket/transport_unix.go (new)
- pkg/proxy/socket/transport_windows.go (new)
- pkg/proxy/socket/testing.go (new)
- pkg/proxy/socket/server_test.go (new)
- pkg/proxy/socket/handler_test.go (new)
- cmd/leanproxy/serve.go (new)

## Change Log

- 2026-05-03: Initial implementation of IDE Extension Socket feature
  - Created socket package with Server, Handler, and transport implementations
  - Added JSON-RPC 2.0 support with all specified methods
  - Implemented concurrent connection handling
  - Added graceful shutdown with context cancellation
  - Created CLI serve command
  - Added comprehensive unit tests
