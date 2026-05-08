package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/errors"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/toolstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockPool struct {
	servers       map[string]string
	tools         map[string][]Tool
	requestResult *MockRequestResult
	requestError  error
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
			expectedVer:   "1.0.0",
		},
		{
			name:          "nil params",
			params:        nil,
			expectError:   false,
			expectedProto: "2024-11-05",
			expectedName:  "leanproxy-mcp",
			expectedVer:   "1.0.0",
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

	h.loadFromPersistentCache(context.Background())
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
			h := NewHandler(newMockPool(), logger)
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
