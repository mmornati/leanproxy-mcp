package postgresql

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testClient builds a client with ReadOnly: false so existing tests that
// exercise postgresql_execute keep working; TestGetTools_ReadOnly and
// TestCallTool_ExecuteDisabledInReadOnlyMode cover the ReadOnly: true case.
func testClient() *PostgresClient {
	return testClientWithConfig(func(cfg *Config) { cfg.ReadOnly = false })
}

func testClientWithConfig(mutate func(cfg *Config)) *PostgresClient {
	cfg := DefaultConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	c := &PostgresClient{
		logger: discardLogger(),
		config: cfg,
		tools:  make(map[string]ToolHandler),
	}
	c.tools[toolQuery] = c.handleQuery
	c.tools[toolListTables] = c.handleListTables
	c.tools[toolPGDescribe] = c.handleDescribe
	if !cfg.ReadOnly {
		c.tools[toolExecute] = c.handleExecute
	}
	return c
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, DefaultPoolSize, cfg.PoolSize)
	assert.Equal(t, 30*time.Second, cfg.StatementTimeout)
	assert.Empty(t, cfg.ConnectionString)
	assert.True(t, cfg.ReadOnly, "read-only mode must default to true")
}

func TestIsFatalError_Actual(t *testing.T) {
	tests := []struct {
		msg   string
		fatal bool
	}{
		{"syntax error at or near \"SELECT\"", true},
		{"permission denied for table users", true},
		{"column \"email\" does not exist", true},
		{"relation \"users\" does not exist", true},
		{"connection refused", false},
		{"deadlock detected", false},
		{"timeout expired", false},
		{"context deadline exceeded", false},
	}
	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			err := &testError{msg: tt.msg}
			got := isFatalError(err)
			assert.Equal(t, tt.fatal, got, "isFatalError(%q) = %v, want %v", tt.msg, got, tt.fatal)
		})
	}
}

type testError struct {
	msg string
}

func (e *testError) Error() string { return e.msg }

func TestGetTools(t *testing.T) {
	client := testClient()
	tools := client.GetTools()
	require.Len(t, tools, 4)

	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Name] = true
	}
	assert.True(t, names["postgresql_query"])
	assert.True(t, names["postgresql_execute"])
	assert.True(t, names["postgresql_list_tables"])
	assert.True(t, names["postgresql_describe"])
}

func TestCallTool_Unknown(t *testing.T) {
	client := testClient()
	_, err := client.CallTool(context.Background(), "unknown", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown tool")
}

func TestGetTools_ReadOnly_ExcludesExecute(t *testing.T) {
	client := testClientWithConfig(func(cfg *Config) { cfg.ReadOnly = true })
	tools := client.GetTools()
	require.Len(t, tools, 3, "postgresql_execute must not be advertised in read-only mode")

	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Name] = true
	}
	assert.True(t, names[toolQuery])
	assert.True(t, names[toolListTables])
	assert.True(t, names[toolPGDescribe])
	assert.False(t, names[toolExecute])
}

func TestGetTools_NotReadOnly_IncludesExecute(t *testing.T) {
	client := testClientWithConfig(func(cfg *Config) { cfg.ReadOnly = false })
	tools := client.GetTools()
	require.Len(t, tools, 4)

	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Name] = true
	}
	assert.True(t, names[toolExecute])
}

func TestCallTool_ExecuteDisabledInReadOnlyMode(t *testing.T) {
	client := testClientWithConfig(func(cfg *Config) { cfg.ReadOnly = true })
	args, _ := json.Marshal(map[string]string{"statement": "DELETE FROM users"})
	_, err := client.CallTool(context.Background(), toolExecute, args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown tool")
}

func TestHandleQuery_EmptyQuery(t *testing.T) {
	client := testClient()
	args, _ := json.Marshal(map[string]string{"query": ""})
	_, err := client.CallTool(context.Background(), toolQuery, args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "'query' is required")
}

func TestHandleQuery_NonSelect(t *testing.T) {
	client := testClient()
	args, _ := json.Marshal(map[string]string{"query": "INSERT INTO users (name) VALUES ('test')"})
	_, err := client.CallTool(context.Background(), toolQuery, args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "only SELECT")
}

func TestHandleQuery_WithPrefixAllowedByHint(t *testing.T) {
	// WITH must no longer be rejected by the prefix hint: the real
	// protection is the read-only transaction runReadOnlyQuery opens, not
	// this check. With no live pool configured, the call still fails, but
	// it must fail trying to reach the database (a nil-pointer/connection
	// error), never with the old "WITH queries are not allowed" message.
	client := testClient()
	args, _ := json.Marshal(map[string]string{"query": "WITH x AS (DELETE FROM users RETURNING *) SELECT * FROM x"})
	assert.Panics(t, func() {
		_, _ = client.CallTool(context.Background(), toolQuery, args)
	}, "expected the call to reach the (nil) pool rather than being rejected by the prefix hint")
}

func TestHandleExecute_EmptyStatement(t *testing.T) {
	client := testClient()
	args, _ := json.Marshal(map[string]string{"statement": ""})
	_, err := client.CallTool(context.Background(), toolExecute, args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "'statement' is required")
}

func TestHandleExecute_SelectBlocked(t *testing.T) {
	client := testClient()
	args, _ := json.Marshal(map[string]string{"statement": "SELECT * FROM users"})
	_, err := client.CallTool(context.Background(), toolExecute, args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SELECT queries should use the query tool")
}

func TestToolDefinitions(t *testing.T) {
	client := testClient()
	tools := client.GetTools()
	for _, tool := range tools {
		assert.NotEmpty(t, tool.Name, "tool name should not be empty")
		assert.NotEmpty(t, tool.Description, "tool description should not be empty")
		assert.NotEmpty(t, tool.InputSchema, "tool input schema should not be empty")

		var schema map[string]interface{}
		err := json.Unmarshal(tool.InputSchema, &schema)
		assert.NoError(t, err, "tool %s has invalid JSON schema", tool.Name)
		assert.Equal(t, "object", schema["type"], "tool %s should have object type schema", tool.Name)
	}
}

func TestWithRetry_RetriesOnNonFatal(t *testing.T) {
	client := testClient()

	attempts := 0
	err := client.withRetry(context.Background(), func(ctx context.Context) error {
		attempts++
		return fmt.Errorf("connection refused: attempt %d", attempts)
	})
	assert.Error(t, err)
	assert.Equal(t, maxRetries, attempts)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestWithRetry_SuccessAfterRetry(t *testing.T) {
	client := testClient()

	attempts := 0
	err := client.withRetry(context.Background(), func(ctx context.Context) error {
		attempts++
		if attempts < 2 {
			return fmt.Errorf("connection refused: attempt %d", attempts)
		}
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 2, attempts)
}

func TestWithRetry_FatalNoRetry(t *testing.T) {
	client := testClient()

	attempts := 0
	err := client.withRetry(context.Background(), func(ctx context.Context) error {
		attempts++
		return fmt.Errorf("syntax error at or near \"SELECT\"")
	})
	assert.Error(t, err)
	assert.Equal(t, 1, attempts, "fatal error should not retry")
}
