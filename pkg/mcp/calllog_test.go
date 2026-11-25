package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestCallLogKeepsTheMostRecentCalls(t *testing.T) {
	log := NewCallLog(3)
	for i := 1; i <= 5; i++ {
		server := "a"
		if i%2 == 0 {
			server = "b"
		}
		log.Add(CallRecord{Server: server, Tool: fmt.Sprintf("t%d", i)})
	}
	if log.Total() != 5 {
		t.Fatalf("Total = %d, want 5", log.Total())
	}
	got := log.Recent(10, "")
	if len(got) != 3 || got[0].Tool != "t5" || got[1].Tool != "t4" || got[2].Tool != "t3" {
		t.Fatalf("Recent = %+v, want t5 t4 t3", got)
	}
	if got := log.Recent(10, "b"); len(got) != 1 || got[0].Tool != "t4" {
		t.Fatalf("Recent(b) = %+v, want t4", got)
	}
	if got := log.Recent(1, ""); len(got) != 1 || got[0].Tool != "t5" {
		t.Fatalf("Recent(1) = %+v, want t5", got)
	}
	var nilLog *CallLog
	nilLog.Add(CallRecord{})
	if nilLog.Recent(5, "") != nil || nilLog.Total() != 0 {
		t.Fatal("a nil log must be inert")
	}
}

func TestTruncateTextCutsOnARuneBoundary(t *testing.T) {
	got := truncateText(strings.Repeat("é", 10), 5)
	if got != "éé…" {
		t.Fatalf("got %q", got)
	}
	if truncateText("short", 10) != "short" {
		t.Fatal("short text changed")
	}
}

func callRequest(t *testing.T, name string, args any) *Request {
	t.Helper()
	rawArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	params, err := json.Marshal(ToolsCallParams{Name: name, Arguments: rawArgs})
	if err != nil {
		t.Fatal(err)
	}
	return &Request{JSONRPC: JSONRPCVersion, ID: 1, Method: MethodToolsCall, Params: params}
}

func TestCallLogMiddlewareRecordsWhoCalledWhat(t *testing.T) {
	pl := newMockPool()
	pl.servers["alpha"] = "running"
	h := NewHandler(pl, nil)
	log := NewCallLog(10)
	session, closeSession := h.OpenSession(nil)
	defer closeSession()
	session.setInitialized(LatestProtocolVersion, InitializeParams{ClientInfo: ClientInfo{Name: "claude-code", Version: "2.1"}}, nil, "")
	ctx := WithClientSession(context.Background(), session)

	results := map[string]func() (*Response, error){
		"alpha.ok":   func() (*Response, error) { return &Response{Result: json.RawMessage(`{"content":[]}`)}, nil },
		"alpha.tool": func() (*Response, error) { return &Response{Result: json.RawMessage(`{"isError":true}`)}, nil },
		"alpha.rpc":  func() (*Response, error) { return &Response{Error: NewError(-32000, "denied by policy")}, nil },
		"alpha.err":  func() (*Response, error) { return nil, errors.New("transport down") },
	}
	next := func(_ context.Context, req *Request) (*Response, error) {
		var p ToolsCallParams
		_ = json.Unmarshal(req.Params, &p)
		if fn, ok := results[p.Name]; ok {
			return fn()
		}
		return &Response{Result: json.RawMessage(`{}`)}, nil
	}
	mw := h.CallLogMiddleware(log)(next)

	for _, name := range []string{"alpha.ok", "alpha.tool", "alpha.rpc", "alpha.err"} {
		_, _ = mw(ctx, callRequest(t, name, map[string]string{"secret": "hunter2"}))
	}
	// A router tool and invoke_tool.
	_, _ = mw(ctx, callRequest(t, "search_tools", map[string]string{"query": "x"}))
	_, _ = mw(ctx, callRequest(t, "invoke_tool", map[string]any{"server": "alpha", "tool": "ok", "arguments": map[string]string{}}))
	// Not a tool call: not recorded.
	_, _ = mw(ctx, &Request{JSONRPC: JSONRPCVersion, ID: 2, Method: "tools/list"})

	got := log.Recent(10, "")
	if len(got) != 6 {
		t.Fatalf("recorded %d calls, want 6: %+v", len(got), got)
	}
	want := []struct{ server, tool, status string }{
		{"alpha", "ok", CallOK},
		{proxyServerLabel, "search_tools", CallOK},
		{"alpha", "err", CallError},
		{"alpha", "rpc", CallError},
		{"alpha", "tool", CallToolError},
		{"alpha", "ok", CallOK},
	}
	for i, w := range want {
		g := got[i]
		if g.Server != w.server || g.Tool != w.tool || g.Status != w.status {
			t.Errorf("call %d = %s.%s %s, want %s.%s %s", i, g.Server, g.Tool, g.Status, w.server, w.tool, w.status)
		}
		if g.Client != "claude-code" || g.SessionID != session.id {
			t.Errorf("call %d not attributed to the session: %+v", i, g)
		}
	}
	if got[3].Error != "denied by policy" || got[2].Error != "transport down" {
		t.Errorf("error text not kept: %q / %q", got[3].Error, got[2].Error)
	}
	// Arguments never reach the log.
	dump, _ := json.Marshal(got)
	if strings.Contains(string(dump), "hunter2") {
		t.Fatal("the call log holds call arguments")
	}

	sessions := h.Sessions()
	if len(sessions) != 1 || sessions[0].Calls != 6 || sessions[0].Client != "claude-code" || sessions[0].LastCallAt.IsZero() {
		t.Fatalf("Sessions = %+v", sessions)
	}
}

func TestHandlerServerRemovedDropsItsTools(t *testing.T) {
	pl := newMockPool()
	pl.servers["alpha"] = "running"
	h := NewHandler(pl, nil)
	h.setServerTools("alpha", []Tool{{Name: "one"}, {Name: "two"}})
	if n, known := h.ToolCount("alpha"); n != 2 || !known {
		t.Fatalf("ToolCount = %d, %v", n, known)
	}
	h.ServerRemoved("alpha")
	if n, known := h.ToolCount("alpha"); n != 0 || known {
		t.Fatalf("after removal ToolCount = %d, %v", n, known)
	}
}
