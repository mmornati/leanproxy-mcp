package cmd

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp/exposure"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
)

func TestNewExposureResolver(t *testing.T) {
	r, err := newExposureResolver(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := r.ModeFor("claude-code"); m != exposure.ModePassthrough {
		t.Fatalf("default claude-code = %q", m)
	}
	if m, _ := r.ModeFor("unknown"); m != exposure.ModeRouter {
		t.Fatalf("default unknown = %q", m)
	}

	r, err = newExposureResolver(&migrate.Config{Exposure: &exposure.Config{Mode: exposure.ModeHybrid}}, "router")
	if err != nil {
		t.Fatal(err)
	}
	if m, why := r.ModeFor("claude-code"); m != exposure.ModeRouter || why != "--exposure" {
		t.Fatalf("--exposure router: %q (%s)", m, why)
	}

	if _, err := newExposureResolver(nil, "everything"); err == nil || !strings.Contains(err.Error(), "--exposure") {
		t.Fatalf("invalid flag: %v", err)
	}
}

func TestRunFlagsDeclareExposure(t *testing.T) {
	f := runCmd.Flags().Lookup("exposure")
	if f == nil || !strings.Contains(f.Usage, "router, passthrough or hybrid") {
		t.Fatalf("server run --exposure: %+v", f)
	}
}

// TestHandlerAnswersListing: serve answers tools/list (and hybrid's
// search_tools) through the shared handler only for sessions that list the
// upstream tools; a router session keeps serve's own gateway.
func TestHandlerAnswersListing(t *testing.T) {
	sp := pool.NewStdioPool(1, time.Minute, slog.Default())
	t.Cleanup(func() { _ = sp.Close() })
	h := mcp.NewHandler(pool.NewUnifiedPool(sp, pool.NewHTTPClientPool(slog.Default()), pool.NewSSEPool(slog.Default()), slog.Default()), slog.Default())
	r, err := exposure.NewResolver(&exposure.Config{Clients: []exposure.ClientRule{{Match: "hy", Mode: exposure.ModeHybrid}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	h.SetExposure(r)
	session := func(client string) context.Context {
		s, closeS := h.OpenSession(nil)
		t.Cleanup(closeS)
		ctx := mcp.WithClientSession(context.Background(), s)
		params, _ := json.Marshal(map[string]interface{}{"protocolVersion": "2025-06-18", "capabilities": map[string]interface{}{}, "clientInfo": map[string]string{"name": client}})
		if _, err := h.HandleRequest(ctx, &mcp.Request{JSONRPC: "2.0", ID: 1, Method: mcp.MethodInitialize, Params: params}); err != nil {
			t.Fatal(err)
		}
		return ctx
	}
	list := &proxy.JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: "tools/list"}
	search := &proxy.JSONRPCRequest{JSONRPC: "2.0", ID: 3, Method: "tools/call", Params: json.RawMessage(`{"name":"search_tools","arguments":{"query":"x"}}`)}
	call := &proxy.JSONRPCRequest{JSONRPC: "2.0", ID: 4, Method: "tools/call", Params: json.RawMessage(`{"name":"github__create_issue"}`)}

	for client, want := range map[string][3]bool{
		"other":       {false, false, false},
		"claude-code": {true, false, false},
		"hy":          {true, true, false},
	} {
		ctx := session(client)
		got := [3]bool{handlerAnswersListing(ctx, h, list), handlerAnswersListing(ctx, h, search), handlerAnswersListing(ctx, h, call)}
		if got != want {
			t.Errorf("%s: tools/list, search_tools, upstream call answered by the handler = %v, want %v", client, got, want)
		}
	}
}
