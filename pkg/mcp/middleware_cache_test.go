package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp/responsecache"
)

func toolsCallRequest(t *testing.T, id int, name string, args interface{}) *Request {
	t.Helper()
	return &Request{
		JSONRPC: JSONRPCVersion,
		Method:  MethodToolsCall,
		ID:      id,
		Params:  mustJSON(t, ToolsCallParams{Name: name, Arguments: mustJSON(t, args)}),
	}
}

func resultResponse(t *testing.T, id int, v interface{}) *Response {
	t.Helper()
	return &Response{JSONRPC: JSONRPCVersion, ID: id, Result: mustJSON(t, v)}
}

// --- default-off behavior --------------------------------------------------

func TestResponseCache_DisabledByDefault(t *testing.T) {
	rc := NewResponseCache(nil)
	if rc.Enabled() {
		t.Fatal("expected a nil config to leave the cache disabled")
	}

	calls := 0
	next := func(ctx context.Context, req *Request) (*Response, error) {
		calls++
		return resultResponse(t, req.ID.(int), ToolsCallResult{Content: []ContentBlock{{Type: "text", Text: "ok"}}}), nil
	}
	mw := rc.Middleware()(next)

	req := toolsCallRequest(t, 1, "github.create_issue", map[string]string{"title": "x"})
	mw(context.Background(), req)
	mw(context.Background(), req)
	if calls != 2 {
		t.Fatalf("expected both calls to reach next() with caching disabled, got %d", calls)
	}
}

// --- allowlist --------------------------------------------------------------

func TestResponseCache_OnlyAllowlistedToolsAreCached(t *testing.T) {
	rc := NewResponseCache(&responsecache.Config{Enabled: true, Tools: []string{"github.get_file_contents"}})

	calls := 0
	next := func(ctx context.Context, req *Request) (*Response, error) {
		calls++
		return resultResponse(t, req.ID.(int), ToolsCallResult{Content: []ContentBlock{{Type: "text", Text: "ok"}}}), nil
	}
	mw := rc.Middleware()(next)

	// Not allowlisted: create_issue must always be forwarded (two identical
	// calls reach next() twice — acceptance criterion for the default and
	// for any non-allowlisted side-effecting tool).
	notAllowed := toolsCallRequest(t, 1, "github.create_issue", map[string]string{"title": "x"})
	mw(context.Background(), notAllowed)
	mw(context.Background(), notAllowed)
	if calls != 2 {
		t.Fatalf("expected a non-allowlisted tool to be forwarded every time, got %d calls", calls)
	}

	// Allowlisted: second identical call must be served from cache.
	calls = 0
	allowed := toolsCallRequest(t, 2, "github.get_file_contents", map[string]string{"path": "a"})
	mw(context.Background(), allowed)
	mw(context.Background(), allowed)
	if calls != 1 {
		t.Fatalf("expected the allowlisted tool's second call to be served from cache, got %d upstream calls", calls)
	}
}

func TestResponseCache_GlobAllowlist(t *testing.T) {
	rc := NewResponseCache(&responsecache.Config{Enabled: true, Tools: []string{"github.get_*"}})
	calls := 0
	next := func(ctx context.Context, req *Request) (*Response, error) {
		calls++
		return resultResponse(t, req.ID.(int), ToolsCallResult{Content: []ContentBlock{{Type: "text", Text: "ok"}}}), nil
	}
	mw := rc.Middleware()(next)

	req := toolsCallRequest(t, 1, "github.get_issue", map[string]string{"n": "1"})
	mw(context.Background(), req)
	mw(context.Background(), req)
	if calls != 1 {
		t.Fatalf("expected the glob-matched tool to be cached, got %d upstream calls", calls)
	}
}

// --- error exclusion ---------------------------------------------------------

func TestResponseCache_NeverCachesJSONRPCError(t *testing.T) {
	rc := NewResponseCache(&responsecache.Config{Enabled: true, Tools: []string{"t"}})
	calls := 0
	next := func(ctx context.Context, req *Request) (*Response, error) {
		calls++
		return &Response{JSONRPC: JSONRPCVersion, ID: req.ID, Error: NewError(ErrCodeInternalError, "boom")}, nil
	}
	mw := rc.Middleware()(next)

	req := toolsCallRequest(t, 1, "t", map[string]string{})
	mw(context.Background(), req)
	mw(context.Background(), req)
	if calls != 2 {
		t.Fatalf("expected an error response to never be cached, got %d upstream calls (want 2)", calls)
	}
}

func TestResponseCache_NeverCachesIsErrorResult(t *testing.T) {
	rc := NewResponseCache(&responsecache.Config{Enabled: true, Tools: []string{"t"}})
	calls := 0
	next := func(ctx context.Context, req *Request) (*Response, error) {
		calls++
		return resultResponse(t, req.ID.(int), ToolsCallResult{
			Content: []ContentBlock{{Type: "text", Text: "tool failed"}},
			IsError: true,
		}), nil
	}
	mw := rc.Middleware()(next)

	req := toolsCallRequest(t, 1, "t", map[string]string{})
	mw(context.Background(), req)
	mw(context.Background(), req)
	if calls != 2 {
		t.Fatalf("expected an isError:true result to never be cached, got %d upstream calls (want 2)", calls)
	}
}

// --- pre-redaction keying / secret leak prevention ---------------------------

// Two calls that differ only in a secret argument value must never share a
// cache entry — the acceptance criterion at the heart of issue #299. The
// cache middleware must be installed OUTERMOST (ahead of RequestMiddleware)
// for this to hold; this test wires the same order the front ends use.
func TestResponseCache_DifferentSecretsNeverShareEntry(t *testing.T) {
	rc := NewResponseCache(&responsecache.Config{Enabled: true, Tools: []string{"t"}})
	red := NewRedaction(nil) // built-in patterns

	var upstreamArgs []string
	dispatch := func(ctx context.Context, req *Request) (*Response, error) {
		var p ToolsCallParams
		_ = json.Unmarshal(req.Params, &p)
		upstreamArgs = append(upstreamArgs, string(p.Arguments))
		return resultResponse(t, req.ID.(int), ToolsCallResult{Content: []ContentBlock{{Type: "text", Text: "ok"}}}), nil
	}

	mws := []Middleware{rc.Middleware(), red.ResponseMiddleware(), red.RequestMiddleware()}
	pipeline := Chain(dispatch, mws...)

	reqA := toolsCallRequest(t, 1, "t", map[string]string{"key": fakeAWSKey})
	reqB := toolsCallRequest(t, 2, "t", map[string]string{"key": fakeGHToken})

	if _, err := pipeline(context.Background(), reqA); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline(context.Background(), reqB); err != nil {
		t.Fatal(err)
	}

	if len(upstreamArgs) != 2 {
		t.Fatalf("expected both distinct-secret calls to reach upstream (no false cache hit), got %d upstream calls: %v", len(upstreamArgs), upstreamArgs)
	}
}

// The cached VALUE must be the redacted response, and a cache hit must not
// call next() at all (so redaction/injection never re-run on a hit — the
// cached bytes are already safe to replay).
func TestResponseCache_StoresRedactedResponseAndHitSkipsNext(t *testing.T) {
	rc := NewResponseCache(&responsecache.Config{Enabled: true, Tools: []string{"t"}})
	red := NewRedaction(nil)

	calls := 0
	dispatch := func(ctx context.Context, req *Request) (*Response, error) {
		calls++
		return resultResponse(t, req.ID.(int), ToolsCallResult{
			Content: []ContentBlock{{Type: "text", Text: "token=" + fakeGHToken}},
		}), nil
	}

	mws := []Middleware{rc.Middleware(), red.ResponseMiddleware(), red.RequestMiddleware()}
	pipeline := Chain(dispatch, mws...)

	req := toolsCallRequest(t, 1, "t", map[string]string{"q": "x"})

	first, err := pipeline(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecrets(t, "first response", first.Result)

	second, err := pipeline(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected the second identical call to be served from cache (dispatch called once), got %d calls", calls)
	}
	assertNoSecrets(t, "cached response", second.Result)
}

// --- notifications / non tool calls ------------------------------------------

func TestResponseCache_IgnoresNonToolsCallMethods(t *testing.T) {
	rc := NewResponseCache(&responsecache.Config{Enabled: true, Tools: []string{"*"}})
	calls := 0
	next := func(ctx context.Context, req *Request) (*Response, error) {
		calls++
		return &Response{JSONRPC: JSONRPCVersion, ID: req.ID, Result: json.RawMessage(`{}`)}, nil
	}
	mw := rc.Middleware()(next)

	req := &Request{JSONRPC: JSONRPCVersion, Method: MethodPing, ID: 1}
	mw(context.Background(), req)
	mw(context.Background(), req)
	if calls != 2 {
		t.Fatalf("expected a non tools/call method to never be cached, got %d calls", calls)
	}
}

func TestResponseCache_NotificationsPassThrough(t *testing.T) {
	rc := NewResponseCache(&responsecache.Config{Enabled: true, Tools: []string{"*"}})
	calls := 0
	next := func(ctx context.Context, req *Request) (*Response, error) {
		calls++
		return nil, nil
	}
	mw := rc.Middleware()(next)

	req := &Request{JSONRPC: JSONRPCVersion, Method: MethodToolsCall,
		Params: mustJSON(t, ToolsCallParams{Name: "t"})} // ID nil => notification
	if _, err := mw(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected a notification to always pass through, got %d calls", calls)
	}
}

// --- extractToolCall ----------------------------------------------------------

func TestExtractToolCall_PlainToolsCall(t *testing.T) {
	req := toolsCallRequest(t, 1, "github.get_file_contents", map[string]string{"path": "a"})
	server, tool, args, ok := extractToolCall(req)
	if !ok || server != "" || tool != "github.get_file_contents" {
		t.Fatalf("got server=%q tool=%q ok=%v", server, tool, ok)
	}
	var m map[string]string
	_ = json.Unmarshal(args, &m)
	if m["path"] != "a" {
		t.Fatalf("args not preserved: %s", args)
	}
}

func TestExtractToolCall_StdioGatewayEnvelope(t *testing.T) {
	req := toolsCallRequest(t, 1, "invoke_tool", invokeToolEnvelope{
		Server: "github", Tool: "get_file_contents", Arguments: mustJSON(t, map[string]string{"path": "a"}),
	})
	server, tool, _, ok := extractToolCall(req)
	if !ok || server != "github" || tool != "get_file_contents" {
		t.Fatalf("got server=%q tool=%q ok=%v", server, tool, ok)
	}
}

func TestExtractToolCall_ServeGatewayTopLevelMethod(t *testing.T) {
	req := &Request{
		JSONRPC: JSONRPCVersion,
		Method:  "invoke_tool",
		ID:      1,
		Params: mustJSON(t, map[string]interface{}{
			"server_name": "github",
			"tool_name":   "get_file_contents",
			"arguments":   map[string]string{"path": "a"},
		}),
	}
	server, tool, _, ok := extractToolCall(req)
	if !ok || server != "github" || tool != "get_file_contents" {
		t.Fatalf("got server=%q tool=%q ok=%v", server, tool, ok)
	}
}

func TestExtractToolCall_ListServersNeverCacheable(t *testing.T) {
	req := toolsCallRequest(t, 1, "list_servers", map[string]string{})
	if _, _, _, ok := extractToolCall(req); ok {
		t.Error("expected list_servers to never be cacheable")
	}
}

func TestResponseCacheable(t *testing.T) {
	cases := []struct {
		name string
		resp *Response
		want bool
	}{
		{"nil", nil, false},
		{"jsonrpc error", &Response{Error: NewError(ErrCodeInternalError, "x")}, false},
		{"empty result", &Response{Result: nil}, true},
		{"ok result", &Response{Result: mustJSON(t, ToolsCallResult{})}, true},
		{"isError true", &Response{Result: mustJSON(t, ToolsCallResult{IsError: true})}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := responseCacheable(c.resp); got != c.want {
				t.Errorf("responseCacheable() = %v, want %v", got, c.want)
			}
		})
	}
}
