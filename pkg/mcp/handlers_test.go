package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/internal/version"
	"github.com/mmornati/leanproxy-mcp/pkg/errors"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/toolstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockPool struct {
	servers         map[string]string
	tools           map[string][]Tool
	requestResult   *MockRequestResult
	requestError    error
	sendRequestFunc func(ctx context.Context, name, method string, params json.RawMessage, timeout time.Duration) (*pool.Response, error)
}

type MockRequestResult struct {
	Result json.RawMessage
	Error  *errors.JSONRPCError
}

func newMockPool() *mockPool {
	return &mockPool{
		servers: make(map[string]string),
		tools:   make(map[string][]Tool),
	}
}

func (m *mockPool) SendRequestToServer(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration) (*pool.Response, error) {
	if m.sendRequestFunc != nil {
		return m.sendRequestFunc(ctx, name, method, params, timeout)
	}
	if m.requestError != nil {
		return nil, m.requestError
	}
	if m.requestResult != nil {
		return &pool.Response{
			Result: m.requestResult.Result,
			Error:  m.requestResult.Error,
		}, nil
	}
	if method == MethodToolsList {
		if tools, ok := m.tools[name]; ok {
			toolsJSON, _ := json.Marshal(map[string]interface{}{"tools": tools})
			return &pool.Response{
				Result: toolsJSON,
			}, nil
		}
		return &pool.Response{
			Result: json.RawMessage(`{"tools": []}`),
		}, nil
	}
	return &pool.Response{
		Result: json.RawMessage(`{}`),
	}, nil
}

func (m *mockPool) SendRequestToServerWithID(ctx context.Context, name string, method string, params json.RawMessage, timeout time.Duration, id int) (*pool.Response, error) {
	return m.SendRequestToServer(ctx, name, method, params, timeout)
}

func (m *mockPool) SendServerNotification(ctx context.Context, name string, method string, params map[string]interface{}) error {
	return nil
}

func (m *mockPool) ListServers() []string {
	var result []string
	for k := range m.servers {
		result = append(result, k)
	}
	return result
}

func (m *mockPool) GetServerState(name string) (pool.ServerState, error) {
	state, ok := m.servers[name]
	if !ok {
		return "", fmt.Errorf("server not found")
	}
	return pool.ServerState(state), nil
}

func (m *mockPool) GetServerTransport(name string) (string, error) {
	if _, ok := m.servers[name]; !ok {
		return "", fmt.Errorf("server not found")
	}
	return "stdio", nil
}

func (m *mockPool) RestartServer(ctx context.Context, name string) error {
	m.servers[name] = string(pool.StateIdle)
	return nil
}

func (m *mockPool) Close() error {
	return nil
}

func (m *mockPool) SetServerState(name string, state pool.ServerState) {
	m.servers[name] = string(state)
}

func (m *mockPool) SetTools(serverName string, tools []Tool) {
	m.tools[serverName] = tools
}

func (m *mockPool) SetRequestResult(result json.RawMessage, err *errors.JSONRPCError) {
	m.requestResult = &MockRequestResult{Result: result, Error: err}
}

func (m *mockPool) SetRequestError(err error) {
	m.requestError = err
}

func TestNewHandler(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	pool := newMockPool()

	h := NewHandler(pool, logger)

	assert.NotNil(t, h)
	assert.Equal(t, pool, h.pool)
	assert.Equal(t, logger, h.logger)
	assert.Equal(t, 30*time.Second, h.timeout)
	assert.NotNil(t, h.toolCache)
}

func TestHandlerTimeoutFor_PerServer(t *testing.T) {
	h := NewHandler(newMockPool(), nil)

	// default fallback applies for unknown servers
	assert.Equal(t, 30*time.Second, h.timeoutFor("missing"))

	// per-server override wins
	h.SetTimeout("garmin", 60*time.Second)
	assert.Equal(t, 60*time.Second, h.timeoutFor("garmin"))
	assert.Equal(t, 30*time.Second, h.timeoutFor("intervals"))

	// zero/negative durations clear the override (and fall back to default)
	h.SetTimeout("garmin", 0)
	assert.Equal(t, 30*time.Second, h.timeoutFor("garmin"))
}

func TestHandlerSetDefaultTimeout(t *testing.T) {
	h := NewHandler(newMockPool(), nil)

	h.SetDefaultTimeout(45 * time.Second)
	assert.Equal(t, 45*time.Second, h.timeoutFor("any"))

	// non-positive values are ignored (default remains)
	h.SetDefaultTimeout(0)
	assert.Equal(t, 45*time.Second, h.timeoutFor("any"))
	h.SetDefaultTimeout(-1 * time.Second)
	assert.Equal(t, 45*time.Second, h.timeoutFor("any"))

	// per-server still wins over the custom default
	h.SetTimeout("garmin", 60*time.Second)
	assert.Equal(t, 60*time.Second, h.timeoutFor("garmin"))
	assert.Equal(t, 45*time.Second, h.timeoutFor("intervals"))
}

func TestHandlerToolsCallUsesPerServerTimeout(t *testing.T) {
	mp := newMockPool()
	mp.servers["garmin"] = "running"
	var capturedTimeout time.Duration
	mp.sendRequestFunc = func(_ context.Context, name, _ string, _ json.RawMessage, timeout time.Duration) (*pool.Response, error) {
		capturedTimeout = timeout
		return &pool.Response{ID: 1, Result: json.RawMessage(`{"ok":true}`)}, nil
	}
	h := NewHandler(mp, nil)
	h.SetTimeout("garmin", 60*time.Second)

	resp, err := h.HandleRequest(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		Method:  MethodToolsCall,
		ID:      1,
		Params:  json.RawMessage(`{"name":"garmin_get_activity_fit_data","arguments":{}}`),
	})
	assert.NoError(t, err)
	assert.Nil(t, resp.Error)
	assert.Equal(t, 60*time.Second, capturedTimeout,
		"per-server timeout must be honored end-to-end (regression: handler used to hardcode 30s)")
}

func TestNewHandlerWithNilLogger(t *testing.T) {
	pool := newMockPool()

	h := NewHandler(pool, nil)

	assert.NotNil(t, h)
	assert.Equal(t, slog.Default(), h.logger)
}

func TestNewHandlerWithToolStore(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	pool := newMockPool()
	cache := toolstore.NewNoOpCache()

	h := NewHandlerWithToolStore(pool, logger, cache)

	assert.NotNil(t, h)
	assert.Equal(t, pool, h.pool)
	assert.Equal(t, logger, h.logger)
	assert.Equal(t, cache, h.toolStore)
}

func TestNewHandlerWithToolStoreNilLogger(t *testing.T) {
	pool := newMockPool()
	cache := toolstore.NewNoOpCache()

	h := NewHandlerWithToolStore(pool, nil, cache)

	assert.NotNil(t, h)
	assert.Equal(t, slog.Default(), h.logger)
}

func TestHandleInitialize(t *testing.T) {
	tests := []struct {
		name          string
		params        *InitializeParams
		expectError   bool
		expectedProto string
		expectedName  string
		expectedVer   string
	}{
		{
			name: "basic initialize",
			params: &InitializeParams{
				ProtocolVersion: "2024-11-05",
				ClientInfo: ClientInfo{
					Name:    "test-client",
					Version: "1.0.0",
				},
			},
			expectError:   false,
			expectedProto: "2024-11-05",
			expectedName:  "leanproxy-mcp",
			expectedVer:   version.Get().Version,
		},
		{
			name:          "nil params",
			params:        nil,
			expectError:   false,
			expectedProto: "2024-11-05",
			expectedName:  "leanproxy-mcp",
			expectedVer:   version.Get().Version,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
			h := NewHandler(newMockPool(), logger)

			var paramsBytes json.RawMessage
			if tt.params != nil {
				paramsBytes, _ = json.Marshal(tt.params)
			}

			req := &Request{
				JSONRPC: "2.0",
				Method:  MethodInitialize,
				Params:  paramsBytes,
				ID:      1,
			}

			resp, err := h.HandleRequest(context.Background(), req)

			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.Nil(t, resp.Error)

			var result InitializeResult
			err = json.Unmarshal(resp.Result, &result)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedProto, result.ProtocolVersion)
			assert.Equal(t, tt.expectedName, result.ServerInfo.Name)
			assert.Equal(t, tt.expectedVer, result.ServerInfo.Version)
			assert.NotNil(t, result.Capabilities.Tools)
			assert.NotNil(t, result.Capabilities.Resources)
			assert.NotNil(t, result.Capabilities.Prompts)
		})
	}
}

// TestHandleInitialize_ReportsBuildVersion simulates the -ldflags injected
// build version (internal/version.Version, e.g. set via
// -X .../internal/version.Version=v9.9.9) and asserts serverInfo.version
// reflects it, per #300 acceptance criteria (no more hard-coded "1.0.0").
func TestHandleInitialize_ReportsBuildVersion(t *testing.T) {
	original := version.Version
	version.Version = "v9.9.9"
	t.Cleanup(func() { version.Version = original })

	h := NewHandler(newMockPool(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodInitialize,
		ID:      1,
	}

	resp, err := h.HandleRequest(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Nil(t, resp.Error)

	var result InitializeResult
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	assert.Equal(t, "v9.9.9", result.ServerInfo.Version)
	assert.Equal(t, "leanproxy-mcp", result.ServerInfo.Name)
}

func TestHandleInitialized(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	h := NewHandler(newMockPool(), logger)

	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodInitialized,
		ID:      nil,
	}

	resp, err := h.HandleRequest(context.Background(), req)

	assert.NoError(t, err)
	assert.Nil(t, resp)
}

func TestHandleToolsList(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	h := NewHandler(newMockPool(), logger)

	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodToolsList,
		ID:      1,
	}

	resp, err := h.HandleRequest(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Nil(t, resp.Error)

	var result ToolsListResult
	err = json.Unmarshal(resp.Result, &result)
	require.NoError(t, err)
	assert.NotEmpty(t, result.Tools)

	for _, tool := range result.Tools {
		assert.NotEmpty(t, tool.Name)
		assert.NotEmpty(t, tool.Description)
	}
}

func TestHandleResourcesList(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	h := NewHandler(newMockPool(), logger)

	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodResourcesList,
		ID:      1,
	}

	resp, err := h.HandleRequest(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Nil(t, resp.Error)

	var result ResourcesListResult
	err = json.Unmarshal(resp.Result, &result)
	require.NoError(t, err)
	assert.NotNil(t, result.Resources)
	assert.Empty(t, result.Resources)
}

func TestHandlePromptsList(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	h := NewHandler(newMockPool(), logger)

	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodPromptsList,
		ID:      1,
	}

	resp, err := h.HandleRequest(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Nil(t, resp.Error)

	var result PromptsListResult
	err = json.Unmarshal(resp.Result, &result)
	require.NoError(t, err)
	assert.NotNil(t, result.Prompts)
	assert.Empty(t, result.Prompts)
}

func TestHandlePing(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	h := NewHandler(newMockPool(), logger)

	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodPing,
		ID:      1,
	}

	resp, err := h.HandleRequest(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Nil(t, resp.Error)

	var result map[string]string
	err = json.Unmarshal(resp.Result, &result)
	require.NoError(t, err)
	assert.Equal(t, "ok", result["status"])
}

func TestHandleShutdown(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	pool := newMockPool()
	h := NewHandler(pool, logger)

	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodShutdown,
		ID:      1,
	}

	resp, err := h.HandleRequest(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Nil(t, resp.Error)

	var result map[string]string
	err = json.Unmarshal(resp.Result, &result)
	require.NoError(t, err)
	assert.Equal(t, "shutdown", result["status"])
}

func TestHandleRequestUnknownMethod(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	h := NewHandler(newMockPool(), logger)

	req := &Request{
		JSONRPC: "2.0",
		Method:  "unknown/method",
		ID:      1,
	}

	resp, err := h.HandleRequest(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, ErrCodeMethodNotFound, resp.Error.Code)
}

func TestHandleToolsCall(t *testing.T) {
	tests := []struct {
		name        string
		params      ToolsCallParams
		poolSetup   func(*mockPool)
		expectError bool
		errorCode   int
	}{
		{
			name: "missing tool name",
			params: ToolsCallParams{
				Name: "",
			},
			expectError: true,
			errorCode:   ErrCodeInvalidParams,
		},
		{
			name: "builtin list_tools",
			params: ToolsCallParams{
				Name:      "list_tools",
				Arguments: json.RawMessage(`{"server_name": "github"}`),
			},
			poolSetup: func(mp *mockPool) {
				mp.SetServerState("github", pool.StateIdle)
				mp.SetTools("github", []Tool{
					{Name: "list_issues", Description: "List issues"},
				})
			},
			expectError: false,
		},
		{
			name: "builtin invoke_tool",
			params: ToolsCallParams{
				Name:      "invoke_tool",
				Arguments: json.RawMessage(`{"server": "github", "tool": "list_issues"}`),
			},
			poolSetup: func(mp *mockPool) {
				mp.SetServerState("github", pool.StateIdle)
				mp.SetTools("github", []Tool{
					{Name: "list_issues", Description: "List issues"},
				})
				mp.SetRequestResult(json.RawMessage(`{"content": []}`), nil)
			},
			expectError: false,
		},
		{
			name: "external tool call",
			params: ToolsCallParams{
				Name:      "github_list_issues",
				Arguments: json.RawMessage(`{"owner": "test"}`),
			},
			poolSetup: func(mp *mockPool) {
				mp.SetServerState("github", pool.StateIdle)
				mp.SetTools("github", []Tool{
					{Name: "list_issues", Description: "List issues"},
				})
				mp.SetRequestResult(json.RawMessage(`{"content": []}`), nil)
			},
			expectError: false,
		},
		{
			name: "invalid tool name format",
			params: ToolsCallParams{
				Name: "invalid_no_server",
			},
			expectError: true,
			errorCode:   ErrCodeServerError,
			poolSetup: func(mp *mockPool) {
				mp.SetRequestError(fmt.Errorf("server not found"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
			pool := newMockPool()

			if tt.poolSetup != nil {
				tt.poolSetup(pool)
			}

			h := NewHandler(pool, logger)

			paramsBytes, _ := json.Marshal(tt.params)
			req := &Request{
				JSONRPC: "2.0",
				Method:  MethodToolsCall,
				Params:  paramsBytes,
				ID:      1,
			}

			resp, err := h.HandleRequest(context.Background(), req)

			require.NoError(t, err)
			require.NotNil(t, resp)

			if tt.expectError {
				assert.NotNil(t, resp.Error)
				if tt.errorCode > 0 {
					assert.Equal(t, tt.errorCode, resp.Error.Code)
				}
			} else {
				assert.Nil(t, resp.Error)
			}
		})
	}
}

func TestHandleListTools(t *testing.T) {
	tests := []struct {
		name        string
		serverName  string
		maxDesc     int
		poolSetup   func(*mockPool)
		expectText  string
		expectEmpty bool
	}{
		{
			name:       "empty server_name",
			serverName: "",
			poolSetup: func(mp *mockPool) {
				mp.SetServerState("github", pool.StateIdle)
				mp.SetTools("github", []Tool{
					{
						Name:        "list_issues",
						Description: "List GitHub issues",
						InputSchema: json.RawMessage(`{}`),
					},
				})
			},
			expectEmpty: true,
			expectText:  "server_name parameter is required",
		},
		{
			name:       "valid server with results",
			serverName: "github",
			maxDesc:    200,
			poolSetup: func(mp *mockPool) {
				mp.SetServerState("github", pool.StateIdle)
				mp.SetTools("github", []Tool{
					{
						Name:        "list_issues",
						Description: "List GitHub issues",
						InputSchema: json.RawMessage(`{"type": "object", "properties": {"owner": {"type": "string"}}}`),
					},
				})
			},
			expectText: "github tools (1):",
		},
		{
			name:       "unknown server",
			serverName: "unknown",
			poolSetup: func(mp *mockPool) {
				mp.SetServerState("github", pool.StateIdle)
				mp.SetTools("github", []Tool{
					{
						Name:        "list_issues",
						Description: "List GitHub issues",
						InputSchema: json.RawMessage(`{}`),
					},
				})
			},
			expectText: "not found",
		},
		{
			name:       "server with no tools",
			serverName: "github",
			maxDesc:    200,
			poolSetup: func(mp *mockPool) {
				mp.SetServerState("github", pool.StateIdle)
			},
			expectText: "No tools available",
		},
		{
			name:       "custom max_description_chars",
			serverName: "github",
			maxDesc:    20,
			poolSetup: func(mp *mockPool) {
				mp.SetServerState("github", pool.StateIdle)
				mp.SetTools("github", []Tool{
					{
						Name:        "list_issues",
						Description: "List GitHub issues from repository",
						InputSchema: json.RawMessage(`{}`),
					},
				})
			},
			expectEmpty: true,
			expectText:  "max_description_chars must be between 50 and 500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
			pool := newMockPool()

			if tt.poolSetup != nil {
				tt.poolSetup(pool)
			}

			h := NewHandler(pool, logger)

			args := map[string]interface{}{"server_name": tt.serverName}
			if tt.maxDesc > 0 {
				args["max_description_chars"] = float64(tt.maxDesc)
			}
			argsBytes, _ := json.Marshal(args)

			params := ToolsCallParams{
				Name:      "list_tools",
				Arguments: argsBytes,
			}
			paramsBytes, _ := json.Marshal(params)

			req := &Request{
				JSONRPC: "2.0",
				Method:  MethodToolsCall,
				Params:  paramsBytes,
				ID:      1,
			}

			resp, err := h.HandleRequest(context.Background(), req)

			require.NoError(t, err)
			require.NotNil(t, resp)

			if tt.expectText != "" {
				if resp.Error != nil {
					assert.Contains(t, resp.Error.Message, tt.expectText)
				} else {
					var result map[string]interface{}
					err = json.Unmarshal(resp.Result, &result)
					require.NoError(t, err)
					content := result["content"].([]interface{})
					textBlock := content[0].(map[string]interface{})
					text := textBlock["text"].(string)
					assert.Contains(t, text, tt.expectText)
				}
			}
		})
	}
}

// TestHandleListServers covers the #300 acceptance criterion: tools/call
// list_servers returns every configured server with transport, state and
// tool count, and an unreachable server is labeled "unreachable".
func TestHandleListServers(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	p := newMockPool()
	p.SetServerState("github", pool.StateIdle)
	p.SetServerState("jira", pool.StateError)
	p.SetTools("github", []Tool{
		{Name: "list_issues", Description: "List GitHub issues", InputSchema: json.RawMessage(`{}`)},
	})

	h := NewHandler(p, logger)
	// Seed the handler's own tool cache directly: list_servers reads from
	// it (kept current by the background refresh), not from the mock
	// pool's per-test tool map.
	h.toolCache.tools["github"] = []Tool{
		{Name: "list_issues", Description: "List GitHub issues", InputSchema: json.RawMessage(`{}`)},
	}

	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodToolsCall,
		Params:  mustMarshal(t, ToolsCallParams{Name: "list_servers"}),
		ID:      1,
	}

	resp, err := h.HandleRequest(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Nil(t, resp.Error)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	content := result["content"].([]interface{})
	textBlock := content[0].(map[string]interface{})
	text := textBlock["text"].(string)

	assert.Contains(t, text, "github (stdio, healthy, 1 tools)")
	assert.Contains(t, text, "jira (stdio, unreachable, 0 tools)")
}

func TestHandleListServers_NoServers(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	h := NewHandler(newMockPool(), logger)

	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodToolsCall,
		Params:  mustMarshal(t, ToolsCallParams{Name: "list_servers"}),
		ID:      1,
	}

	resp, err := h.HandleRequest(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Nil(t, resp.Error)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	content := result["content"].([]interface{})
	textBlock := content[0].(map[string]interface{})
	text := textBlock["text"].(string)

	assert.Contains(t, text, "No servers configured")
}

func mustMarshal(t *testing.T, params ToolsCallParams) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(params)
	require.NoError(t, err)
	return b
}

func TestHandleInvokeTool(t *testing.T) {
	tests := []struct {
		name        string
		server      string
		tool        string
		args        map[string]interface{}
		poolSetup   func(*mockPool)
		expectError bool
	}{
		{
			name:        "missing server",
			server:      "",
			tool:        "list_issues",
			expectError: true,
		},
		{
			name:        "missing tool",
			server:      "github",
			tool:        "",
			expectError: true,
		},
		{
			name:   "successful invocation",
			server: "github",
			tool:   "list_issues",
			args:   map[string]interface{}{"owner": "test"},
			poolSetup: func(mp *mockPool) {
				mp.SetServerState("github", pool.StateIdle)
				mp.SetTools("github", []Tool{
					{Name: "list_issues", Description: "List issues"},
				})
				mp.SetRequestResult(json.RawMessage(`{"content": [{"type": "text", "text": "done"}]}`), nil)
			},
			expectError: false,
		},
		{
			name:   "server not running",
			server: "github",
			tool:   "list_issues",
			args:   map[string]interface{}{"owner": "test"},
			poolSetup: func(mp *mockPool) {
				mp.SetServerState("github", pool.StateStopped)
				mp.SetTools("github", []Tool{
					{Name: "list_issues", Description: "List issues"},
				})
				mp.SetRequestResult(json.RawMessage(`{"content": []}`), nil)
			},
			expectError: false,
		},
		{
			name:   "tool already prefixed with server",
			server: "github",
			tool:   "github_list_issues",
			args:   map[string]interface{}{"owner": "test"},
			poolSetup: func(mp *mockPool) {
				mp.SetServerState("github", pool.StateIdle)
				mp.SetTools("github", []Tool{
					{Name: "list_issues", Description: "List issues"},
				})
				mp.SetRequestResult(json.RawMessage(`{"content": []}`), nil)
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
			pool := newMockPool()

			if tt.poolSetup != nil {
				tt.poolSetup(pool)
			}

			h := NewHandler(pool, logger)

			args := map[string]interface{}{
				"server": tt.server,
				"tool":   tt.tool,
			}
			if tt.args != nil {
				for k, v := range tt.args {
					args[k] = v
				}
			}
			argsBytes, _ := json.Marshal(args)

			params := ToolsCallParams{
				Name:      "invoke_tool",
				Arguments: argsBytes,
			}
			paramsBytes, _ := json.Marshal(params)

			req := &Request{
				JSONRPC: "2.0",
				Method:  MethodToolsCall,
				Params:  paramsBytes,
				ID:      1,
			}

			resp, err := h.HandleRequest(context.Background(), req)

			require.NoError(t, err)
			require.NotNil(t, resp)

			if tt.expectError {
				assert.NotNil(t, resp.Error)
			} else {
				assert.Nil(t, resp.Error)
			}
		})
	}
}

// TestHandleInvokeTool_LosslessBigIntArguments is the regression test for
// #296 point 5: invoke_tool must forward `arguments` byte-for-byte instead
// of round-tripping through map[string]interface{} (which turns a large
// integer into a float64 and loses precision).
func TestHandleInvokeTool_LosslessBigIntArguments(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	mp := newMockPool()
	mp.SetServerState("github", pool.StateIdle)

	var capturedParams json.RawMessage
	mp.sendRequestFunc = func(_ context.Context, _ string, method string, params json.RawMessage, _ time.Duration) (*pool.Response, error) {
		if method == MethodToolsCall {
			capturedParams = params
		}
		return &pool.Response{ID: 1, Result: json.RawMessage(`{"content":[]}`)}, nil
	}

	h := NewHandler(mp, logger)

	invokeArgs := json.RawMessage(`{"server":"github","tool":"list_issues","arguments":{"n":12345678901234567,"owner":"test"}}`)
	params := ToolsCallParams{Name: "invoke_tool", Arguments: invokeArgs}
	paramsBytes, _ := json.Marshal(params)

	resp, err := h.HandleRequest(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		Method:  MethodToolsCall,
		ID:      1,
		Params:  paramsBytes,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Nil(t, resp.Error)
	require.NotNil(t, capturedParams)

	var forwarded ToolsCallParams
	require.NoError(t, json.Unmarshal(capturedParams, &forwarded))

	if !bytes.Contains(forwarded.Arguments, []byte("12345678901234567")) {
		t.Fatalf("expected forwarded arguments to contain the big integer byte-identical, got: %s", forwarded.Arguments)
	}
}

// TestHandleInvokeTool_ServerPrefixedToolNames: invoke_tool strips a
// repeated server prefix ("slack_list_channels" on server "slack" when the
// server's tool is "list_channels"), but keeps it when the server's tool is
// really named with that prefix ("slack_post_message", as in the Slack MCP
// server). Found by the end-to-end harness (#301).
func TestHandleInvokeTool_ServerPrefixedToolNames(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	tests := []struct {
		name, tool, want string
	}{
		{"real prefixed tool name is kept", "slack_post_message", "slack_post_message"},
		{"repeated server prefix is stripped", "slack_list_channels", "list_channels"},
		{"unprefixed name is unchanged", "list_channels", "list_channels"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mp := newMockPool()
			mp.SetServerState("slack", pool.StateIdle)
			var forwarded ToolsCallParams
			mp.sendRequestFunc = func(_ context.Context, _ string, method string, params json.RawMessage, _ time.Duration) (*pool.Response, error) {
				if method == MethodToolsCall {
					_ = json.Unmarshal(params, &forwarded)
				}
				return &pool.Response{ID: 1, Result: json.RawMessage(`{"content":[]}`)}, nil
			}
			h := NewHandler(mp, logger)
			h.toolCache.tools["slack"] = []Tool{{Name: "slack_post_message"}, {Name: "list_channels"}}

			args, _ := json.Marshal(map[string]string{"server": "slack", "tool": tt.tool})
			paramsBytes, _ := json.Marshal(ToolsCallParams{Name: "invoke_tool", Arguments: args})
			resp, err := h.HandleRequest(context.Background(), &Request{JSONRPC: JSONRPCVersion, Method: MethodToolsCall, ID: 1, Params: paramsBytes})
			require.NoError(t, err)
			require.Nil(t, resp.Error)
			if forwarded.Name != tt.want {
				t.Fatalf("forwarded tool %q, want %q", forwarded.Name, tt.want)
			}
		})
	}
}

// TestHandleInvokeTool_RejectsNonObjectArguments covers #296 point 5: a
// non-object `arguments` payload is rejected with -32602 instead of being
// silently dropped.
func TestHandleInvokeTool_RejectsNonObjectArguments(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	mp := newMockPool()
	mp.SetServerState("github", pool.StateIdle)
	h := NewHandler(mp, logger)

	invokeArgs := json.RawMessage(`{"server":"github","tool":"list_issues","arguments":"not-an-object"}`)
	params := ToolsCallParams{Name: "invoke_tool", Arguments: invokeArgs}
	paramsBytes, _ := json.Marshal(params)

	resp, err := h.HandleRequest(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		Method:  MethodToolsCall,
		ID:      1,
		Params:  paramsBytes,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Error)
	assert.Equal(t, ErrCodeInvalidParams, resp.Error.Code)
}

// TestHandleInvokeTool_PreservesUpstreamErrorCodeAndMessage is the
// regression test for #296 acceptance criterion: when the upstream returns
// {"error":{"code":-32602,"message":"missing owner"}}, the client receives
// the same code and message.
func TestHandleInvokeTool_PreservesUpstreamErrorCodeAndMessage(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	mp := newMockPool()
	mp.SetServerState("github", pool.StateIdle)
	mp.sendRequestFunc = func(_ context.Context, _ string, method string, _ json.RawMessage, _ time.Duration) (*pool.Response, error) {
		if method == MethodInitialize {
			return &pool.Response{ID: 1, Result: json.RawMessage(`{}`)}, nil
		}
		return &pool.Response{ID: 1, Error: &errors.JSONRPCError{Code: -32602, Message: "missing owner"}}, nil
	}

	h := NewHandler(mp, logger)

	invokeArgs := json.RawMessage(`{"server":"github","tool":"list_issues","arguments":{}}`)
	params := ToolsCallParams{Name: "invoke_tool", Arguments: invokeArgs}
	paramsBytes, _ := json.Marshal(params)

	resp, err := h.HandleRequest(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		Method:  MethodToolsCall,
		ID:      1,
		Params:  paramsBytes,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Error)
	assert.Equal(t, -32602, resp.Error.Code)
	assert.Equal(t, "missing owner", resp.Error.Message)
}

// TestHandleToolsCall_PreservesUpstreamErrorCodeAndMessage covers the
// direct `<server>_<tool>` tools/call path for the same acceptance
// criterion.
func TestHandleToolsCall_PreservesUpstreamErrorCodeAndMessage(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	mp := newMockPool()
	mp.SetServerState("github", pool.StateIdle)
	mp.sendRequestFunc = func(_ context.Context, _ string, method string, _ json.RawMessage, _ time.Duration) (*pool.Response, error) {
		if method == MethodInitialize {
			return &pool.Response{ID: 1, Result: json.RawMessage(`{}`)}, nil
		}
		return &pool.Response{ID: 1, Error: &errors.JSONRPCError{Code: -32602, Message: "missing owner"}}, nil
	}

	h := NewHandler(mp, logger)

	params := ToolsCallParams{Name: "github_list_issues", Arguments: json.RawMessage(`{}`)}
	paramsBytes, _ := json.Marshal(params)

	resp, err := h.HandleRequest(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		Method:  MethodToolsCall,
		ID:      1,
		Params:  paramsBytes,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Error)
	assert.Equal(t, -32602, resp.Error.Code)
	assert.Equal(t, "missing owner", resp.Error.Message)
}

// TestHandleRequest_GuardsAgainstEmptyResponse is the unit test required by
// #296's acceptance criteria: a response with neither result nor error is
// never written to the client; HandleRequest replaces it with an internal
// error instead.
func TestHandleRequest_GuardsAgainstEmptyResponse(t *testing.T) {
	h := NewHandler(newMockPool(), nil)
	h.Use(func(next Next) Next {
		return func(ctx context.Context, req *Request) (*Response, error) {
			// Simulate a buggy handler/middleware that drops the upstream
			// error and returns neither a result nor an error.
			return &Response{JSONRPC: JSONRPCVersion, ID: req.ID}, nil
		}
	})

	resp, err := h.HandleRequest(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		Method:  MethodPing,
		ID:      7,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Nil(t, resp.Result)
	require.NotNil(t, resp.Error, "a response with neither result nor error must be replaced by an internal error")
	assert.Equal(t, ErrCodeInternalError, resp.Error.Code)
	assert.Equal(t, 7, resp.ID)
}

// TestHandleRequest_NotificationStaysNil ensures the empty-response guard
// does not turn a legitimate "nothing to write" notification response
// (nil) into an error.
func TestHandleRequest_NotificationStaysNil(t *testing.T) {
	h := NewHandler(newMockPool(), nil)
	resp, err := h.HandleRequest(context.Background(), &Request{
		JSONRPC: JSONRPCVersion,
		Method:  MethodInitialized,
	})
	require.NoError(t, err)
	assert.Nil(t, resp)
}

func TestPopulateToolCache(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	mockPool := newMockPool()
	cache := toolstore.NewNoOpCache()

	mockPool.SetServerState("github", pool.StateIdle)
	mockPool.SetTools("github", []Tool{
		{Name: "list_issues", Description: "List issues"},
	})
	mockPool.SetRequestResult(json.RawMessage(`{"tools": [{"name": "list_repos", "description": "List repos"}]}`), nil)

	h := NewHandlerWithToolStore(mockPool, logger, cache)

	h.PopulateToolCache(context.Background())

	h.toolCache.mu.RLock()
	tools, ok := h.toolCache.tools["github"]
	h.toolCache.mu.RUnlock()

	assert.True(t, ok)
	assert.NotEmpty(t, tools)
}

func TestLoadFromPersistentCache(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	mockPool := newMockPool()
	cache := toolstore.NewNoOpCache()

	mockPool.SetServerState("github", pool.StateIdle)

	h := NewHandlerWithToolStore(mockPool, logger, cache)

	h.loadFromPersistentCache()
}

func TestParseInputSchema(t *testing.T) {
	tests := []struct {
		name             string
		schema           string
		expectedReqCount int
		expectedOptCount int
	}{
		{
			name:             "empty schema",
			schema:           `{}`,
			expectedReqCount: 0,
			expectedOptCount: 0,
		},
		{
			name:             "schema with required fields",
			schema:           `{"type": "object", "properties": {"owner": {"type": "string"}, "repo": {"type": "string"}}, "required": ["owner", "repo"]}`,
			expectedReqCount: 2,
			expectedOptCount: 0,
		},
		{
			name:             "schema with optional fields",
			schema:           `{"type": "object", "properties": {"owner": {"type": "string", "description": "Owner name"}, "per_page": {"type": "number", "description": "Per page"}}}`,
			expectedReqCount: 0,
			expectedOptCount: 2,
		},
		{
			name:             "schema with mixed fields",
			schema:           `{"type": "object", "properties": {"owner": {"type": "string"}, "per_page": {"type": "number", "description": "Per page"}}, "required": ["owner"]}`,
			expectedReqCount: 1,
			expectedOptCount: 1,
		},
		{
			name:             "invalid schema",
			schema:           `not json`,
			expectedReqCount: 0,
			expectedOptCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			required, optional := parseInputSchema(json.RawMessage(tt.schema))

			assert.Equal(t, tt.expectedReqCount, len(required))
			assert.Equal(t, tt.expectedOptCount, len(optional))
		})
	}
}

func TestFormatToolSearchResult(t *testing.T) {
	tests := []struct {
		name          string
		serverName    string
		toolName      string
		description   string
		required      []ParamInfo
		Optional      []ParamInfo
		maxDescChars  int
		expectedParts []string
	}{
		{
			name:          "basic tool",
			serverName:    "github",
			toolName:      "list_issues",
			description:   "List issues",
			required:      nil,
			Optional:      nil,
			maxDescChars:  200,
			expectedParts: []string{"github_list_issues:", "List issues"},
		},
		{
			name:          "tool with required params",
			serverName:    "github",
			toolName:      "list_issues",
			description:   "List issues",
			required:      []ParamInfo{{Name: "owner", Type: "string"}, {Name: "repo", Type: "string"}},
			Optional:      nil,
			maxDescChars:  200,
			expectedParts: []string{"github_list_issues:", "[owner: string, repo: string]"},
		},
		{
			name:          "tool with optional params",
			serverName:    "github",
			toolName:      "list_issues",
			description:   "List issues",
			required:      nil,
			Optional:      []ParamInfo{{Name: "per_page", Type: "number"}},
			maxDescChars:  200,
			expectedParts: []string{"github_list_issues:", "{per_page: number}"},
		},
		{
			name:          "truncated description",
			serverName:    "github",
			toolName:      "list_issues",
			description:   "List all issues from repository with pagination",
			required:      nil,
			Optional:      nil,
			maxDescChars:  20,
			expectedParts: []string{"github_list_issues:", "github_list_issues: List all issues f..."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatToolSearchResult(tt.serverName, tt.toolName, tt.description, tt.required, tt.Optional, tt.maxDescChars)

			for _, part := range tt.expectedParts {
				assert.Contains(t, result, part)
			}
		})
	}
}

func TestTruncateDescription(t *testing.T) {
	tests := []struct {
		name        string
		description string
		maxChars    int
		expected    string
	}{
		{
			name:        "nil or zero max",
			description: "Test description",
			maxChars:    0,
			expected:    "Test description",
		},
		{
			name:        "negative max",
			description: "Test description",
			maxChars:    -1,
			expected:    "Test description",
		},
		{
			name:        "description shorter than max",
			description: "Short",
			maxChars:    100,
			expected:    "Short",
		},
		{
			name:        "exact match",
			description: "Exact",
			maxChars:    5,
			expected:    "Exact",
		},
		{
			name:        "truncate with ellipsis",
			description: "Long description here",
			maxChars:    10,
			expected:    "Long de...",
		},
		{
			name:        "very small max",
			description: "Long description",
			maxChars:    2,
			expected:    "Lo",
		},
		{
			name:        "max 3 chars",
			description: "Long description",
			maxChars:    3,
			expected:    "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateDescription(tt.description, tt.maxChars)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLookupToolSchema(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	pool := newMockPool()
	h := NewHandler(pool, logger)

	testSchema := json.RawMessage(`{"type": "object", "properties": {"owner": {"type": "string"}}}`)
	h.toolCache.mu.Lock()
	h.toolCache.tools["github"] = []Tool{
		{Name: "list_issues", Description: "List issues", InputSchema: testSchema},
	}
	h.toolCache.mu.Unlock()

	result := h.lookupToolSchema("github", "list_issues")
	assert.NotNil(t, result)
	assert.Equal(t, testSchema, result)

	result = h.lookupToolSchema("github", "nonexistent")
	assert.Nil(t, result)

	result = h.lookupToolSchema("nonexistent", "list_issues")
	assert.Nil(t, result)
}

func TestCollectTools(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	h := NewHandler(newMockPool(), logger)

	manifest, err := h.collectTools(context.Background())

	require.NoError(t, err)
	assert.NotNil(t, manifest)
	assert.NotNil(t, manifest.Tools)
	assert.NotNil(t, manifest.Resources)
	assert.NotNil(t, manifest.Prompts)
}

func TestParseToolName(t *testing.T) {
	tests := []struct {
		name         string
		fullName     string
		expectedSrv  string
		expectedTool string
		expectError  bool
	}{
		{
			name:         "valid tool name",
			fullName:     "github_list_issues",
			expectedSrv:  "github",
			expectedTool: "list_issues",
			expectError:  false,
		},
		{
			name:         "valid with underscores",
			fullName:     "github_my_tool",
			expectedSrv:  "github",
			expectedTool: "my_tool",
			expectError:  false,
		},
		{
			name:         "invalid no underscore",
			fullName:     "github",
			expectedSrv:  "",
			expectedTool: "",
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
			mp := newMockPool()
			mp.servers["github"] = "running"
			h := NewHandler(mp, logger)
			srv, tool, err := h.parseToolName(tt.fullName)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedSrv, srv)
				assert.Equal(t, tt.expectedTool, tool)
			}
		})
	}
}

// TestParseToolName_OverlappingServerNames is the regression test for #296:
// parseToolName matches the longest configured server name that is a
// prefix, so overlapping names like "git" and "github" resolve to the
// correct owner instead of the first partial match, and a server literally
// named "my_srv" is reachable via both "my_srv_tool" and "my_srv.tool".
func TestParseToolName_OverlappingServerNames(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	mp := newMockPool()
	for _, name := range []string{"git", "github", "my_srv"} {
		mp.servers[name] = "running"
	}
	h := NewHandler(mp, logger)

	tests := []struct {
		fullName     string
		expectedSrv  string
		expectedTool string
	}{
		{"git_status", "git", "status"},
		{"github_search_issues", "github", "search_issues"},
		{"github.search_issues", "github", "search_issues"},
		{"my_srv_tool", "my_srv", "tool"},
		{"my_srv.tool", "my_srv", "tool"},
	}
	for _, tt := range tests {
		t.Run(tt.fullName, func(t *testing.T) {
			srv, tool, err := h.parseToolName(tt.fullName)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedSrv, srv)
			assert.Equal(t, tt.expectedTool, tool)
		})
	}
}

func TestToolsToCachedTools(t *testing.T) {
	tools := []Tool{
		{
			Name:        "list_issues",
			Description: "List issues",
			InputSchema: json.RawMessage(`{"type": "object"}`),
		},
		{
			Name:        "create_issue",
			Description: "Create issue",
			InputSchema: json.RawMessage(`{"type": "object"}`),
		},
	}

	result := toolsToCachedTools(tools)

	assert.Equal(t, len(tools), len(result))
	for i, ct := range result {
		assert.Equal(t, tools[i].Name, ct.Name)
		assert.Equal(t, tools[i].Description, ct.Description)
		assert.Equal(t, tools[i].InputSchema, ct.InputSchema)
	}
}

func TestHandleInitializeInvalidParams(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	h := NewHandler(newMockPool(), logger)

	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodInitialize,
		Params:  json.RawMessage(`invalid json`),
		ID:      1,
	}

	resp, err := h.HandleRequest(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, ErrCodeInvalidParams, resp.Error.Code)
}

func TestHandleToolsCallInvalidParams(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	h := NewHandler(newMockPool(), logger)

	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodToolsCall,
		Params:  json.RawMessage(`invalid json`),
		ID:      1,
	}

	resp, err := h.HandleRequest(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, ErrCodeInvalidParams, resp.Error.Code)
}

func TestMatchesQuery(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		words    []string
		expected bool
	}{
		{
			name:     "all words present",
			text:     "github_list_issues list github issues",
			words:    []string{"github", "issues"},
			expected: true,
		},
		{
			name:     "empty words",
			text:     "github_list_issues",
			words:    []string{},
			expected: true,
		},
		{
			name:     "case insensitive",
			text:     "github_list_issues",
			words:    []string{"github"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesQuery(tt.text, tt.words)
			assert.Equal(t, tt.expected, result)
		})
	}
}
