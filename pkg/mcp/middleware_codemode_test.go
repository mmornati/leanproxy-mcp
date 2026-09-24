//go:build codemode

package mcp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/codemode"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
)

// The test binary doubles as the code mode sandbox process.
const codeModeTestWorker = "LEANPROXY_MCP_CODEMODE_TEST_WORKER"

func init() {
	if os.Getenv(codeModeTestWorker) == "1" {
		os.Exit(codemode.ServeWorker(os.Stdin, os.Stdout))
	}
}

func testCodeMode(t *testing.T, h *Handler, cfg *codemode.Config) *CodeMode {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return NewCodeMode(h, cfg, codemode.Options{Command: exe, Args: []string{"-test.run=^$"}, Env: []string{codeModeTestWorker + "=1"}})
}

// recorder is an outer middleware that records every request it sees.
type recorder struct {
	mu   sync.Mutex
	seen []string
}

func (r *recorder) middleware() Middleware {
	return func(next Next) Next {
		return func(ctx context.Context, req *Request) (*Response, error) {
			var p ToolsCallParams
			_ = json.Unmarshal(req.Params, &p)
			entry := req.Method + " " + p.Name
			if ctx.Value(codeModeCallKey{}) != nil {
				entry += " (from code)"
			}
			r.mu.Lock()
			r.seen = append(r.seen, entry)
			r.mu.Unlock()
			return next(ctx, req)
		}
	}
}

func codeModeHandler(t *testing.T) (*Handler, *recorder) {
	t.Helper()
	mp := newMockPool()
	mp.SetServerState("gh", pool.StateRunning)
	secret := "AKIA" + "IOSFODNN7EXAMPLE"
	mp.sendRequestFunc = func(_ context.Context, _, method string, params json.RawMessage, _ time.Duration) (*pool.Response, error) {
		if method != MethodToolsCall {
			return &pool.Response{Result: json.RawMessage(`{"tools":[]}`)}, nil
		}
		var p ToolsCallParams
		_ = json.Unmarshal(params, &p)
		text := `[{"n":1,"c":3},{"n":2,"c":9},{"n":3,"c":1}]`
		if p.Name == "leak" {
			text = "key " + secret
		}
		body, _ := json.Marshal(map[string]interface{}{"content": []map[string]string{{"type": "text", "text": text}}})
		return &pool.Response{Result: body}, nil
	}
	h := NewHandler(mp, nil)
	rec := &recorder{}
	fw := NewFirewall(nil, nil)
	h.Use(append([]Middleware{rec.middleware()}, fw.Middlewares()...)...)
	return h, rec
}

func callExecuteCode(t *testing.T, h *Handler, code string) (string, bool) {
	t.Helper()
	params, _ := json.Marshal(map[string]interface{}{"name": ExecuteCodeToolName, "arguments": map[string]string{"code": code}})
	resp, err := h.HandleRequest(context.Background(), &Request{JSONRPC: JSONRPCVersion, ID: 1, Method: MethodToolsCall, Params: params})
	if err != nil || resp == nil || resp.Error != nil {
		t.Fatalf("execute_code: %v %+v", err, resp)
	}
	var res struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp.Result, &res); err != nil || len(res.Content) == 0 {
		t.Fatalf("result %s", resp.Result)
	}
	return res.Content[len(res.Content)-1].Text, res.IsError
}

// Every call a program makes re-enters the pipeline from the outermost
// middleware, marked as coming from code.
func TestCodeMode_CallsGoThroughTheWholePipeline(t *testing.T) {
	h, rec := codeModeHandler(t)
	h.Use(testCodeMode(t, h, &codemode.Config{Enabled: true}).Middleware())

	text, isErr := callExecuteCode(t, h, `
const issues = await tools.gh.list_issues({repo: "a"});
const leak = await tools.gh.leak({});
return {top: issues.sort((a, b) => b.c - a.c)[0].n, leak};`)
	if isErr {
		t.Fatalf("program failed: %s", text)
	}
	if !strings.Contains(text, `"top":2`) || strings.Contains(text, "IOSFODNN7") || !strings.Contains(text, "[SECRET_REDACTED]") {
		t.Fatalf("output %s", text)
	}
	want := []string{
		"tools/call execute_code",
		"tools/call invoke_tool (from code)",
		"tools/call invoke_tool (from code)",
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if strings.Join(rec.seen, "|") != strings.Join(want, "|") {
		t.Fatalf("the outer middleware saw %q, want %q", rec.seen, want)
	}
}

func TestCodeMode_ToolsListAndGuards(t *testing.T) {
	h, _ := codeModeHandler(t)
	cm := testCodeMode(t, h, &codemode.Config{Enabled: true})
	h.Use(cm.Middleware())

	resp, err := h.HandleRequest(context.Background(), &Request{JSONRPC: JSONRPCVersion, ID: 1, Method: MethodToolsList})
	if err != nil || resp.Error != nil {
		t.Fatalf("tools/list: %v %+v", err, resp)
	}
	var list ToolsListResult
	if err := json.Unmarshal(resp.Result, &list); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range list.Tools {
		if tool.Name == ExecuteCodeToolName {
			found = true
			if !strings.Contains(tool.Description, "servers: gh") {
				t.Errorf("description %q", tool.Description)
			}
		}
	}
	if !found {
		t.Fatalf("execute_code not listed: %s", resp.Result)
	}

	// No nested sandboxes: execute_code from a call made from code.
	ctx := context.WithValue(context.Background(), codeModeCallKey{}, true)
	params, _ := json.Marshal(map[string]interface{}{"name": ExecuteCodeToolName, "arguments": map[string]string{"code": "return 1"}})
	resp, _ = h.HandleRequest(ctx, &Request{JSONRPC: JSONRPCVersion, ID: 2, Method: MethodToolsCall, Params: params})
	if resp == nil || !strings.Contains(string(resp.Result), "cannot be called from code") {
		t.Fatalf("nested execute_code: %+v", resp)
	}

	// Only JavaScript.
	params, _ = json.Marshal(map[string]interface{}{"name": ExecuteCodeToolName, "arguments": map[string]string{"code": "print(1)", "language": "python"}})
	resp, _ = h.HandleRequest(context.Background(), &Request{JSONRPC: JSONRPCVersion, ID: 3, Method: MethodToolsCall, Params: params})
	if resp == nil || resp.Error == nil || resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("python: %+v", resp)
	}

	// A failing program is a tool error the model can read.
	text, isErr := callExecuteCode(t, h, `throw new Error("nope")`)
	if !isErr || !strings.Contains(text, "execute_code failed (error)") || !strings.Contains(text, "nope") {
		t.Fatalf("failure: %v %q", isErr, text)
	}
	if runs, failures, _ := cm.Stats(); runs != 1 || failures != 1 {
		t.Fatalf("stats: %d runs, %d failures", runs, failures)
	}
}
