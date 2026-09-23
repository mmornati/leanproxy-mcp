package postgresql

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	toolQuery      = "postgresql_query"
	toolExecute    = "postgresql_execute"
	toolListTables = "postgresql_list_tables"
	toolPGDescribe = "postgresql_describe"

	maxRetries      = 3
	baseBackoff     = 50 * time.Millisecond
	DefaultPoolSize = 10
)

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type ToolHandler func(ctx context.Context, args json.RawMessage) (interface{}, error)

type PostgresClient struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
	config Config
	tools  map[string]ToolHandler
}

type Config struct {
	ConnectionString string        `json:"connection_string"`
	PoolSize         int           `json:"pool_size"`
	StatementTimeout time.Duration `json:"statement_timeout"`
	// ReadOnly disables postgresql_execute entirely and forces every
	// postgresql_query call to run inside a real "BEGIN ... READ ONLY"
	// transaction, rolled back afterwards. Defaults to true: the server
	// only becomes able to mutate data when an operator explicitly opts
	// out via LEANPROXY_POSTGRES_READ_ONLY=false.
	ReadOnly bool `json:"read_only"`
}

func DefaultConfig() Config {
	return Config{
		PoolSize:         DefaultPoolSize,
		StatementTimeout: 30 * time.Second,
		ReadOnly:         true,
	}
}

func NewPostgresClient(logger *slog.Logger, cfg Config) (*PostgresClient, error) {
	if cfg.PoolSize <= 0 {
		cfg.PoolSize = DefaultPoolSize
	}
	if cfg.StatementTimeout <= 0 {
		cfg.StatementTimeout = 30 * time.Second
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.ConnectionString)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.PoolSize > math.MaxInt32 {
		return nil, fmt.Errorf("pool size %d exceeds maximum", cfg.PoolSize)
	}
	poolCfg.MaxConns = int32(cfg.PoolSize)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	client := &PostgresClient{
		pool:   pool,
		logger: logger,
		config: cfg,
	}

	client.tools = map[string]ToolHandler{
		toolQuery:      client.handleQuery,
		toolListTables: client.handleListTables,
		toolPGDescribe: client.handleDescribe,
	}
	if !cfg.ReadOnly {
		client.tools[toolExecute] = client.handleExecute
	}

	logger.Info("postgres client initialized",
		"pool_size", cfg.PoolSize,
		"statement_timeout", cfg.StatementTimeout,
		"read_only", cfg.ReadOnly,
	)
	return client, nil
}

func (c *PostgresClient) Close() {
	c.pool.Close()
}

func (c *PostgresClient) GetTools() []ToolDefinition {
	tools := []ToolDefinition{
		{
			Name:        toolQuery,
			Description: "Run a read-only SELECT/EXPLAIN/WITH query against the PostgreSQL database (always executed inside a read-only transaction) and return results as a JSON array of rows.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "SQL SELECT/EXPLAIN/WITH query to execute"}
				},
				"required": ["query"]
			}`),
		},
	}

	if !c.config.ReadOnly {
		tools = append(tools, ToolDefinition{
			Name:        toolExecute,
			Description: "Execute an INSERT, UPDATE, DELETE, or DDL statement against the PostgreSQL database.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"statement": {"type": "string", "description": "SQL statement to execute (INSERT, UPDATE, DELETE, DDL)"}
				},
				"required": ["statement"]
			}`),
		})
	}

	tools = append(tools, []ToolDefinition{
		{
			Name:        toolListTables,
			Description: "List all tables in the PostgreSQL database with schema name and row count estimates.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"schema": {"type": "string", "description": "Schema filter (default: public)"}
				},
				"required": []
			}`),
		},
		{
			Name:        toolPGDescribe,
			Description: "Describe a PostgreSQL table schema including column names, types, and nullability.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"table": {"type": "string", "description": "Table name (can include schema, e.g. public.users)"}
				},
				"required": ["table"]
			}`),
		},
	}...)

	return tools
}

func (c *PostgresClient) CallTool(ctx context.Context, name string, args json.RawMessage) (interface{}, error) {
	handler, ok := c.tools[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return handler(ctx, args)
}

func (c *PostgresClient) withRetry(ctx context.Context, fn func(context.Context) error) error {
	var lastErr error
	for attempt := uint(0); attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := baseBackoff * (1 << (attempt - 1))
			c.logger.Debug("retrying after error", "attempt", attempt+1, "backoff", backoff, "error", lastErr)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		queryCtx, cancel := context.WithTimeout(ctx, c.config.StatementTimeout)
		err := fn(queryCtx)
		cancel()

		if err == nil {
			return nil
		}

		lastErr = err

		if isFatalError(err) {
			return err
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}

func isFatalError(err error) bool {
	errStr := err.Error()
	return strings.Contains(errStr, "syntax error") ||
		strings.Contains(errStr, "permission denied") ||
		strings.Contains(errStr, "column") && strings.Contains(errStr, "does not exist") ||
		strings.Contains(errStr, "relation") && strings.Contains(errStr, "does not exist")
}

type QueryResult struct {
	Columns  []string                 `json:"columns"`
	Rows     []map[string]interface{} `json:"rows"`
	RowCount int64                    `json:"row_count"`
}

func (c *PostgresClient) handleQuery(ctx context.Context, args json.RawMessage) (interface{}, error) {
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Query == "" {
		return nil, fmt.Errorf("'query' is required")
	}

	// This prefix check is a UX hint only, to reject obvious misuse (INSERT,
	// UPDATE, DELETE, DDL...) with a clearer error than Postgres would give.
	// It is NOT the security boundary: a prefix like EXPLAIN or SELECT can
	// still hide a side effect (EXPLAIN ANALYZE executes the statement,
	// SELECT ... INTO creates a table, SELECT pg_terminate_backend(...) has
	// side effects, a WITH ... can wrap a DELETE ... RETURNING). The real
	// protection is that runReadOnlyQuery below always runs the query inside
	// a genuine "BEGIN ... READ ONLY" transaction, which Postgres itself
	// enforces regardless of what the query text says.
	upper := strings.TrimSpace(strings.ToUpper(params.Query))
	if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "EXPLAIN") && !strings.HasPrefix(upper, "WITH") {
		return nil, fmt.Errorf("only SELECT, EXPLAIN and WITH queries are allowed here; use execute for DML/DDL")
	}

	var result QueryResult
	err := c.withRetry(ctx, func(qctx context.Context) error {
		return c.runReadOnlyQuery(qctx, params.Query, &result)
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// runReadOnlyQuery executes query inside a real read-only transaction and
// always rolls it back afterwards (a read-only transaction cannot commit any
// change, so rollback vs. commit makes no difference to the database, but
// rollback is what the story asks for and avoids leaving anything open on
// error paths). pgx's default query-execution mode uses the extended
// protocol (parse/bind/execute), which also rejects a query string containing
// more than one SQL statement, closing the classic ";"-separated
// multi-statement injection vector.
func (c *PostgresClient) runReadOnlyQuery(ctx context.Context, query string, result *QueryResult) error {
	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin read-only transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if timeoutMS := c.config.StatementTimeout.Milliseconds(); timeoutMS > 0 {
		if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", timeoutMS)); err != nil {
			return fmt.Errorf("set statement_timeout: %w", err)
		}
	}

	rows, err := tx.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	columns := make([]string, len(rows.FieldDescriptions()))
	for i, fd := range rows.FieldDescriptions() {
		columns[i] = string(fd.Name)
	}
	result.Columns = columns
	result.Rows = nil

	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return fmt.Errorf("read row: %w", err)
		}
		row := make(map[string]interface{})
		for i, col := range columns {
			row[col] = values[i]
		}
		result.Rows = append(result.Rows, row)
	}

	result.RowCount = int64(len(result.Rows))
	return rows.Err()
}

type ExecuteResult struct {
	CommandTag   string `json:"command_tag"`
	RowsAffected int64  `json:"rows_affected"`
}

func (c *PostgresClient) handleExecute(ctx context.Context, args json.RawMessage) (interface{}, error) {
	var params struct {
		Statement string `json:"statement"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Statement == "" {
		return nil, fmt.Errorf("'statement' is required")
	}

	upper := strings.TrimSpace(strings.ToUpper(params.Statement))
	if strings.HasPrefix(upper, "SELECT") {
		return nil, fmt.Errorf("SELECT queries should use the query tool; execute is for DML/DDL")
	}

	var result ExecuteResult
	err := c.withRetry(ctx, func(qctx context.Context) error {
		tag, err := c.pool.Exec(qctx, params.Statement)
		if err != nil {
			return fmt.Errorf("execute: %w", err)
		}
		result.CommandTag = tag.String()
		result.RowsAffected = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

type TableInfo struct {
	Schema      string `json:"schema"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	RowEstimate int64  `json:"row_estimate,omitempty"`
}

func (c *PostgresClient) handleListTables(ctx context.Context, args json.RawMessage) (interface{}, error) {
	var params struct {
		Schema string `json:"schema"`
	}
	if args != nil {
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if params.Schema == "" {
		params.Schema = "public"
	}

	var tables []TableInfo
	err := c.withRetry(ctx, func(qctx context.Context) error {
		rows, err := c.pool.Query(qctx,
			`SELECT schemaname, tablename, tableowner,
					COALESCE((SELECT reltuples::bigint FROM pg_class WHERE oid = (schemaname||'.'||tablename)::regclass), 0) as row_estimate
			 FROM pg_tables
			 WHERE schemaname = $1
			 ORDER BY tablename`,
			params.Schema,
		)
		if err != nil {
			return fmt.Errorf("list tables: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var t TableInfo
			if err := rows.Scan(&t.Schema, &t.Name, &t.Type, &t.RowEstimate); err != nil {
				return fmt.Errorf("scan table: %w", err)
			}
			tables = append(tables, t)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	if tables == nil {
		tables = []TableInfo{}
	}

	return map[string]interface{}{
		"schema": params.Schema,
		"tables": tables,
	}, nil
}

type ColumnInfo struct {
	ColumnName string `json:"column_name"`
	DataType   string `json:"data_type"`
	NotNull    bool   `json:"not_null"`
	Default    string `json:"default,omitempty"`
}

func (c *PostgresClient) handleDescribe(ctx context.Context, args json.RawMessage) (interface{}, error) {
	var params struct {
		Table string `json:"table"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Table == "" {
		return nil, fmt.Errorf("'table' is required")
	}

	var columns []ColumnInfo
	err := c.withRetry(ctx, func(qctx context.Context) error {
		schema, table := "public", params.Table
		if parts := strings.SplitN(params.Table, ".", 2); len(parts) == 2 {
			schema = parts[0]
			table = parts[1]
		}
		rows, err := c.pool.Query(qctx,
			`SELECT column_name, data_type, is_nullable, column_default
			 FROM information_schema.columns
			 WHERE table_schema = $1
			   AND table_name = $2
			 ORDER BY ordinal_position`,
			schema, table,
		)
		if err != nil {
			return fmt.Errorf("describe table: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var cCol ColumnInfo
			var nullable string
			var defaultVal *string
			if err := rows.Scan(&cCol.ColumnName, &cCol.DataType, &nullable, &defaultVal); err != nil {
				return fmt.Errorf("scan column: %w", err)
			}
			cCol.NotNull = nullable == "NO"
			if defaultVal != nil {
				cCol.Default = *defaultVal
			}
			columns = append(columns, cCol)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	if columns == nil {
		columns = []ColumnInfo{}
	}

	return map[string]interface{}{
		"table":   params.Table,
		"columns": columns,
	}, nil
}
