package mockmcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestServerToolsList(t *testing.T) {
	srv := New(Config{ToolCount: 3, ToolNamePrefix: "x"})
	resp, err := srv.HandleRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	if err != nil {
		t.Fatalf("HandleRequest: %v", err)
	}
	var parsed struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", parsed.JSONRPC)
	}
	if got := len(parsed.Result.Tools); got != 3 {
		t.Errorf("tools len = %d, want 3", got)
	}
	if parsed.Result.Tools[0].Name != "x_0" {
		t.Errorf("first tool = %q, want x_0", parsed.Result.Tools[0].Name)
	}
}

func TestServerInitialize(t *testing.T) {
	srv := New(Config{ToolCount: 1})
	resp, err := srv.HandleRequest(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if err != nil {
		t.Fatalf("HandleRequest: %v", err)
	}
	if !strings.Contains(resp, `"protocolVersion":"2024-11-05"`) {
		t.Errorf("initialize response missing protocolVersion: %s", resp)
	}
}

func TestServerNotificationNoResponse(t *testing.T) {
	srv := New(Config{ToolCount: 1})
	resp, err := srv.HandleRequest(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if err != nil {
		t.Fatalf("HandleRequest: %v", err)
	}
	if resp != "" {
		t.Errorf("notification should produce no response, got: %s", resp)
	}
}

func TestServerMethodNotFound(t *testing.T) {
	srv := New(Config{ToolCount: 1})
	resp, err := srv.HandleRequest(`{"jsonrpc":"2.0","id":9,"method":"does/not/exist","params":{}}`)
	if err != nil {
		t.Fatalf("HandleRequest: %v", err)
	}
	if !strings.Contains(resp, `"error"`) || !strings.Contains(resp, `-32601`) {
		t.Errorf("expected method-not-found error, got: %s", resp)
	}
}

func TestServerToolsCall(t *testing.T) {
	srv := New(Config{ToolCount: 1, ResponseBytes: 128})
	resp, err := srv.HandleRequest(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tool_0","arguments":{}}}`)
	if err != nil {
		t.Fatalf("HandleRequest: %v", err)
	}
	if !strings.Contains(resp, `"result"`) {
		t.Errorf("tools/call response missing result: %s", resp)
	}
}

func TestServerCount(t *testing.T) {
	srv := New(Config{ToolCount: 1})
	for i := 0; i < 5; i++ {
		_, _ = srv.HandleRequest(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	}
	if got := srv.Count(); got != 5 {
		t.Errorf("Count = %d, want 5", got)
	}
}

func TestServerInvalidJSON(t *testing.T) {
	srv := New(Config{ToolCount: 1})
	_, err := srv.HandleRequest("not json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestServerWrongProtocol(t *testing.T) {
	srv := New(Config{ToolCount: 1})
	_, err := srv.HandleRequest(`{"jsonrpc":"1.0","id":1,"method":"ping"}`)
	if err == nil {
		t.Error("expected error for wrong jsonrpc version")
	}
}
