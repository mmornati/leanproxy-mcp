//go:build integration
// +build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests need a real, reachable PostgreSQL database: set
// LEANPROXY_POSTGRES_TEST_DSN to a connection string for a throwaway
// database (e.g. postgres://postgres:postgres@127.0.0.1:5432/leanproxy_test)
// before running them. They are skipped otherwise — there is no bundled
// Postgres in CI, so this mirrors the "skipped without Docker" pattern used
// for the other first-party server integration tests in this package.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("LEANPROXY_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("LEANPROXY_POSTGRES_TEST_DSN not set — skipping Postgres integration test")
	}
	return dsn
}

func buildPostgresServer(t *testing.T) string {
	t.Helper()

	repoRoot := os.Getenv("LEANPROXY_REPO_ROOT")
	if repoRoot == "" {
		repoRoot = findRepoRoot(t)
	}
	if repoRoot == "" {
		t.Skip("could not determine repo root — skipping integration test")
	}

	binaryPath := filepath.Join(repoRoot, "dist", "postgres-mcp-server-test")
	mainPath := filepath.Join(repoRoot, "servers", "postgres", "main.go")

	cmd := exec.Command("go", "build", "-o", binaryPath, mainPath)
	cmd.Dir = repoRoot
	cmd.Env = os.Environ()

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build postgres server: %v\noutput: %s", err, string(output))
	}

	return binaryPath
}

// pgServerHarness starts the built postgres server, sends `initialize`, and
// hands back stdin/stdout/stderr plumbing plus a request-id counter so tests
// can drive tools/list and tools/call.
type pgServerHarness struct {
	t       *testing.T
	writer  func(v interface{})
	decoder *json.Decoder
	stderr  *bytes.Buffer
	nextID  int
}

func startPostgresServer(t *testing.T, dsn string, extraEnv ...string) *pgServerHarness {
	t.Helper()

	serverPath := buildPostgresServer(t)

	cmd := exec.CommandContext(context.Background(), serverPath)
	cmd.Env = append(os.Environ(), "LEANPROXY_POSTGRES_CONNECTION="+dsn)
	cmd.Env = append(cmd.Env, extraEnv...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	h := &pgServerHarness{
		t: t,
		writer: func(v interface{}) {
			b, _ := json.Marshal(v)
			fmt.Fprintln(stdin, string(b))
		},
		decoder: json.NewDecoder(stdout),
		stderr:  stderr,
	}

	initReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "integration-test", "version": "1.0"},
		},
		"id": h.nextRequestID(),
	}
	h.writer(initReq)

	var initResp map[string]interface{}
	if err := h.decoder.Decode(&initResp); err != nil {
		t.Fatalf("decode init: %v\nstderr: %s", err, stderr.String())
	}
	if errVal, ok := initResp["error"]; ok {
		t.Fatalf("initialize error: %v\nstderr: %s", errVal, stderr.String())
	}

	return h
}

func (h *pgServerHarness) nextRequestID() int {
	h.nextID++
	return h.nextID
}

func (h *pgServerHarness) toolsList() map[string]interface{} {
	h.t.Helper()
	h.writer(map[string]interface{}{"jsonrpc": "2.0", "method": "tools/list", "id": h.nextRequestID()})
	var resp map[string]interface{}
	if err := h.decoder.Decode(&resp); err != nil {
		h.t.Fatalf("decode tools/list: %v\nstderr: %s", err, h.stderr.String())
	}
	return resp
}

func (h *pgServerHarness) toolsCall(name string, args map[string]interface{}) map[string]interface{} {
	h.t.Helper()
	h.writer(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": name, "arguments": args},
		"id":      h.nextRequestID(),
	})
	var resp map[string]interface{}
	if err := h.decoder.Decode(&resp); err != nil {
		h.t.Fatalf("decode tools/call(%s): %v\nstderr: %s", name, err, h.stderr.String())
	}
	return resp
}

func setupUsersTable(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect for setup: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS issue318_users"); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if _, err := pool.Exec(ctx, "CREATE TABLE issue318_users (id serial primary key, name text not null)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO issue318_users (name) VALUES ('alice'), ('bob')"); err != nil {
		t.Fatalf("seed table: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, "DROP TABLE IF EXISTS issue318_users")
	})
	return pool
}

func rowCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM issue318_users").Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

func toolsCallErrMsg(t *testing.T, resp map[string]interface{}) (string, bool) {
	t.Helper()
	if errVal, ok := resp["error"]; ok {
		errMap, ok := errVal.(map[string]interface{})
		if !ok {
			return fmt.Sprintf("%v", errVal), true
		}
		return fmt.Sprintf("%v", errMap["message"]), true
	}
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		return "", false
	}
	content, ok := result["content"].([]interface{})
	if !ok || len(content) == 0 {
		return "", false
	}
	block, ok := content[0].(map[string]interface{})
	if !ok {
		return "", false
	}
	text, _ := block["text"].(string)
	// A JSON-RPC-level success can still carry an application-level error
	// string in its text content in this server's protocol, but for the
	// query tool a genuine success always looks like {"columns":...}; any
	// text that doesn't parse as that shape is treated as an error message
	// for the purposes of these tests only if it's not valid QueryResult
	// JSON — in practice this server always reports failures via
	// resp["error"], so this branch is not expected to fire.
	return text, false
}

// TestPostgresServer_QueryToolIsAlwaysReadOnlyTransaction is the core
// regression test for the story: even queries whose text passes the prefix
// hint (EXPLAIN ..., WITH ...) must fail with a real Postgres read-only
// transaction error when they try to mutate data, and the table must be
// left untouched.
func TestPostgresServer_QueryToolIsAlwaysReadOnlyTransaction(t *testing.T) {
	dsn := testDSN(t)
	pool := setupUsersTable(t, dsn)
	before := rowCount(t, pool)

	h := startPostgresServer(t, dsn)

	cases := []struct {
		name  string
		query string
	}{
		{"explain_analyze_delete", "EXPLAIN ANALYZE DELETE FROM issue318_users"},
		{"with_delete", "WITH deleted AS (DELETE FROM issue318_users RETURNING *) SELECT * FROM deleted"},
		{"select_into", "SELECT * INTO issue318_users_copy FROM issue318_users"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := h.toolsCall("postgresql_query", map[string]interface{}{"query": tc.query})
			msg, isErr := toolsCallErrMsg(t, resp)
			if !isErr {
				t.Fatalf("expected an error for %q, got success: %v", tc.query, resp)
			}
			lower := strings.ToLower(msg)
			if !strings.Contains(lower, "read-only") && !strings.Contains(lower, "read only") {
				t.Errorf("expected a read-only-transaction error for %q, got: %s", tc.query, msg)
			}
		})
	}

	after := rowCount(t, pool)
	if after != before {
		t.Errorf("table row count changed: before=%d after=%d — a read-only bypass mutated data", before, after)
	}
}

// TestPostgresServer_QueryToolRejectsMultiStatement covers AC #3: pgx's
// extended query protocol must reject a ";"-separated multi-statement
// injection attempt.
func TestPostgresServer_QueryToolRejectsMultiStatement(t *testing.T) {
	dsn := testDSN(t)
	pool := setupUsersTable(t, dsn)
	before := rowCount(t, pool)

	h := startPostgresServer(t, dsn)

	resp := h.toolsCall("postgresql_query", map[string]interface{}{
		"query": "SELECT 1; DELETE FROM issue318_users",
	})
	msg, isErr := toolsCallErrMsg(t, resp)
	if !isErr {
		t.Fatalf("expected multi-statement query to be rejected, got success: %v", resp)
	}
	if msg == "" {
		t.Error("expected a non-empty error message")
	}

	after := rowCount(t, pool)
	if after != before {
		t.Errorf("table row count changed: before=%d after=%d — multi-statement injection mutated data", before, after)
	}
}

// TestPostgresServer_ReadOnlyMode_ToolsListExcludesExecute covers AC #2:
// with the default (read-only) configuration, tools/list must not advertise
// postgresql_execute at all.
func TestPostgresServer_ReadOnlyMode_ToolsListExcludesExecute(t *testing.T) {
	dsn := testDSN(t)
	h := startPostgresServer(t, dsn) // LEANPROXY_POSTGRES_READ_ONLY unset -> defaults to true

	resp := h.toolsList()
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result object, got %T: %v", resp["result"], resp)
	}
	tools, ok := result["tools"].([]interface{})
	if !ok {
		t.Fatal("expected tools array")
	}

	for _, tRaw := range tools {
		tool, ok := tRaw.(map[string]interface{})
		if !ok {
			continue
		}
		if tool["name"] == "postgresql_execute" {
			t.Error("postgresql_execute must not be listed in read-only mode")
		}
	}
}

// TestPostgresServer_NotReadOnlyMode_ToolsListIncludesExecute is the
// counterpart: opting out via LEANPROXY_POSTGRES_READ_ONLY=false restores
// postgresql_execute.
func TestPostgresServer_NotReadOnlyMode_ToolsListIncludesExecute(t *testing.T) {
	dsn := testDSN(t)
	h := startPostgresServer(t, dsn, "LEANPROXY_POSTGRES_READ_ONLY=false")

	resp := h.toolsList()
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result object, got %T: %v", resp["result"], resp)
	}
	tools, ok := result["tools"].([]interface{})
	if !ok {
		t.Fatal("expected tools array")
	}

	found := false
	for _, tRaw := range tools {
		tool, ok := tRaw.(map[string]interface{})
		if !ok {
			continue
		}
		if tool["name"] == "postgresql_execute" {
			found = true
		}
	}
	if !found {
		t.Error("postgresql_execute must be listed when LEANPROXY_POSTGRES_READ_ONLY=false")
	}
}

// TestPostgresServer_QueryToolAllowsRealSelect makes sure the read-only
// transaction wrapping did not break the common case: a plain SELECT must
// still return rows.
func TestPostgresServer_QueryToolAllowsRealSelect(t *testing.T) {
	dsn := testDSN(t)
	setupUsersTable(t, dsn)
	h := startPostgresServer(t, dsn)

	resp := h.toolsCall("postgresql_query", map[string]interface{}{
		"query": "SELECT id, name FROM issue318_users ORDER BY id",
	})
	if errVal, ok := resp["error"]; ok {
		t.Fatalf("unexpected error: %v", errVal)
	}
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result object, got %T", resp["result"])
	}
	content, ok := result["content"].([]interface{})
	if !ok || len(content) == 0 {
		t.Fatal("expected content array")
	}
	block, ok := content[0].(map[string]interface{})
	if !ok {
		t.Fatal("expected content block")
	}
	text, _ := block["text"].(string)
	var qr struct {
		RowCount int64 `json:"row_count"`
	}
	if err := json.Unmarshal([]byte(text), &qr); err != nil {
		t.Fatalf("unmarshal query result: %v\ntext: %s", err, text)
	}
	if qr.RowCount != 2 {
		t.Errorf("row_count = %d, want 2", qr.RowCount)
	}
}
