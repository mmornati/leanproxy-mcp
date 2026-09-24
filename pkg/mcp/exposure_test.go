package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp/exposure"
	"github.com/mmornati/leanproxy-mcp/pkg/policy"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/toolpin"
)

// exposureEnv is a handler over github, slack and fs (see newPolicyEnv)
// plus a server "rich" whose tools carry the full MCP 2025 metadata and
// one name too long for 64 characters, with the exposure stage in front
// of the per-tool policy.
type exposureEnv struct {
	*policyEnv
	notesMu sync.Mutex
	notes   map[*ClientSession][]string
}

const longToolName = "fetch_the_complete_quarterly_revenue_report_for_every_region_and_currency"

func newExposureEnv(t *testing.T, cfg *exposure.Config, forced exposure.Mode, pol *policy.Config) *exposureEnv {
	t.Helper()
	if pol == nil {
		pol = &policy.Config{UnknownTools: policy.ActionAllow}
	}
	e := &exposureEnv{policyEnv: newPolicyEnv(t, pol), notes: map[*ClientSession][]string{}}
	e.mp.SetServerState("rich", pool.StateIdle)
	e.mp.SetTools("rich", []Tool{
		{
			Name: "report", Title: "Quarterly report", Description: "Build a report.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"total":{"type":"number"}}}`),
			Annotations:  &ToolAnnotations{ReadOnlyHint: bp(true)},
			Icons:        []Icon{{Src: "https://example.test/r.png", MimeType: "image/png"}},
			Meta:         json.RawMessage(`{"ui/resourceUri":"ui://rich/report","anthropic/alwaysLoad":true}`),
		},
		{Name: longToolName, Description: "Long name.", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	r, err := exposure.NewResolver(cfg, forced)
	require.NoError(t, err)
	e.h.SetExposure(r)
	e.h.Use(e.h.ExposureMiddleware(), e.pol.Middleware())
	for _, s := range []string{"github", "slack", "fs", "rich"} {
		require.NoError(t, e.h.RefreshServerTools(context.Background(), s))
	}
	return e
}

// do runs req through the handler's pipeline (exposure, then policy).
func (e *exposureEnv) do(ctx context.Context, req *Request) *Response {
	e.t.Helper()
	if req.JSONRPC == "" {
		req.JSONRPC, req.ID = "2.0", 9
	}
	resp, err := e.h.HandleRequest(ctx, req)
	require.NoError(e.t, err)
	return resp
}

// open opens a session named client that negotiates version and records
// its notifications.
func (e *exposureEnv) open(t *testing.T, client, version string) (context.Context, *ClientSession) {
	t.Helper()
	var s *ClientSession
	s, closeS := e.h.OpenSession(func(method string, _ json.RawMessage) {
		e.notesMu.Lock()
		e.notes[s] = append(e.notes[s], method)
		e.notesMu.Unlock()
	})
	t.Cleanup(closeS)
	ctx := WithClientSession(context.Background(), s)
	params, _ := json.Marshal(map[string]interface{}{"protocolVersion": version, "capabilities": map[string]interface{}{}, "clientInfo": map[string]string{"name": client, "version": "1"}})
	resp := e.do(ctx, &Request{JSONRPC: "2.0", ID: 1, Method: MethodInitialize, Params: params})
	require.Nil(t, resp.Error)
	var res InitializeResult
	require.NoError(t, json.Unmarshal(resp.Result, &res))
	require.NotNil(t, res.Capabilities.Tools)
	assert.Equal(t, s.ExposureMode().ListsUpstreamTools(), res.Capabilities.Tools.ListChanged, "listChanged is advertised exactly when the upstream tools are listed")
	return ctx, s
}

func (e *exposureEnv) notesOf(s *ClientSession) []string {
	e.notesMu.Lock()
	defer e.notesMu.Unlock()
	return append([]string(nil), e.notes[s]...)
}

func (e *exposureEnv) list(t *testing.T, ctx context.Context) ([]Tool, string) {
	t.Helper()
	resp := e.do(ctx, &Request{JSONRPC: "2.0", ID: 2, Method: MethodToolsList})
	require.Nil(t, resp.Error)
	var res ToolsListResult
	require.NoError(t, json.Unmarshal(resp.Result, &res))
	return res.Tools, string(resp.Result)
}

func toolNames(tools []Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

func findTool(tools []Tool, name string) (Tool, bool) {
	for _, t := range tools {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

var clientToolName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func TestExposure_ClaudeCodeGetsEveryToolNamespacedWithMetadata(t *testing.T) {
	e := newExposureEnv(t, nil, "", nil)
	ctx, s := e.open(t, "claude-code", ProtocolVersion20251125)
	require.Equal(t, exposure.ModePassthrough, s.ExposureMode())

	tools, raw := e.list(t, ctx)
	names := toolNames(tools)
	longName := exposure.ToolName("rich", longToolName, 64)
	assert.Equal(t, []string{"fs__remove", "fs__read", "github__create_issue", "github__list_pull_requests", "github__get_job_logs",
		"rich__report", longName, "slack__slack_post_message"}, names)
	for _, n := range names {
		assert.Regexp(t, clientToolName, n)
	}
	for _, gw := range []string{"search_tools", "list_servers", "list_tools", "invoke_tool"} {
		assert.NotContains(t, names, gw, "passthrough lists no gateway tool")
	}
	report, _ := findTool(tools, "rich__report")
	assert.Equal(t, "Quarterly report", report.Title)
	assert.JSONEq(t, `{"type":"object","properties":{"total":{"type":"number"}}}`, string(report.OutputSchema))
	require.NotNil(t, report.Annotations)
	assert.True(t, *report.Annotations.ReadOnlyHint)
	assert.Len(t, report.Icons, 1)
	// The upstream's own anthropic/alwaysLoad is dropped; the rest of
	// _meta is kept.
	assert.JSONEq(t, `{"ui/resourceUri":"ui://rich/report"}`, string(report.Meta))
	assert.NotContains(t, raw, "alwaysLoad")

	// Calls route to the upstream under the original names, plain and
	// shortened, and so does the router form.
	for name, want := range map[string]string{"github__create_issue": "github.create_issue", longName: "rich." + longToolName, "rich__report": "rich.report"} {
		resp := e.do(ctx, toolsCall(name, `{"q":"x"}`))
		require.Nil(t, resp.Error, "%s: %+v", name, resp.Error)
		assert.Equal(t, want, e.sentCalls()[len(e.sentCalls())-1])
	}
}

func TestExposure_UnknownClientKeepsRouter(t *testing.T) {
	e := newExposureEnv(t, nil, "", nil)
	ctx, s := e.open(t, "some-agent", ProtocolVersion20250618)
	assert.Equal(t, exposure.ModeRouter, s.ExposureMode())
	_, raw := e.list(t, ctx)

	// Byte-identical to a handler without any exposure configuration.
	plain, _ := searchTestHandler(t)
	ps, closePS := plain.OpenSession(nil)
	defer closePS()
	pctx := WithClientSession(context.Background(), ps)
	params, _ := json.Marshal(map[string]interface{}{"protocolVersion": ProtocolVersion20250618, "capabilities": map[string]interface{}{}, "clientInfo": map[string]string{"name": "claude-code", "version": "1"}})
	_, err := plain.HandleRequest(pctx, &Request{JSONRPC: "2.0", ID: 1, Method: MethodInitialize, Params: params})
	require.NoError(t, err)
	want, err := plain.HandleRequest(pctx, &Request{JSONRPC: "2.0", ID: 2, Method: MethodToolsList})
	require.NoError(t, err)
	assert.Equal(t, string(want.Result), raw)
	assert.Contains(t, raw, `"name":"invoke_tool"`)
}

func TestExposure_ProtocolRevisionStripsUnknownFields(t *testing.T) {
	e := newExposureEnv(t, &exposure.Config{AlwaysLoad: []string{"rich.report"}}, exposure.ModePassthrough, nil)
	for version, want := range map[string][]string{
		ProtocolVersion20241105: {"description", "inputSchema", "name"},
		ProtocolVersion20250326: {"annotations", "description", "inputSchema", "name"},
		ProtocolVersion20250618: {"_meta", "annotations", "description", "inputSchema", "name", "outputSchema", "title"},
		ProtocolVersion20251125: {"_meta", "annotations", "description", "icons", "inputSchema", "name", "outputSchema", "title"},
	} {
		ctx, _ := e.open(t, "legacy", version)
		tools, raw := e.list(t, ctx)
		var body struct {
			Tools []map[string]json.RawMessage `json:"tools"`
		}
		require.NoError(t, json.Unmarshal([]byte(raw), &body))
		var keys []string
		for _, tool := range body.Tools {
			if string(tool["name"]) == `"rich__report"` {
				for k := range tool {
					keys = append(keys, k)
				}
			}
		}
		sort.Strings(keys)
		assert.Equal(t, want, keys, "fields of rich__report for %s", version)
		if ProtocolAtLeast(version, ProtocolVersion20250618) {
			report, _ := findTool(tools, "rich__report")
			// exposure.always_load decides anthropic/alwaysLoad.
			assert.JSONEq(t, `{"ui/resourceUri":"ui://rich/report","anthropic/alwaysLoad":true}`, string(report.Meta), version)
			other, _ := findTool(tools, "github__create_issue")
			assert.Empty(t, other.Meta)
		}
	}
}

func TestExposure_PolicyHidesDeniedAndMarksConfirmInEveryMode(t *testing.T) {
	pol := &policy.Config{UnknownTools: policy.ActionDeny, Rules: []policy.Rule{
		{Match: "github.get_job_logs", Action: policy.ActionDeny},
		{Match: "*", Annotations: map[string]bool{policy.HintDestructive: true}, Action: policy.ActionConfirm},
	}}
	for _, mode := range []exposure.Mode{exposure.ModePassthrough, exposure.ModeHybrid} {
		e := newExposureEnv(t, nil, mode, pol)
		ctx, _ := e.open(t, "any", ProtocolVersion20250618)
		tools, _ := e.list(t, ctx)
		names := toolNames(tools)
		assert.NotContains(t, names, "github__get_job_logs", mode)
		remove, ok := findTool(tools, "fs__remove")
		require.True(t, ok)
		assert.True(t, strings.HasPrefix(remove.Description, confirmMarker), "%s: %q", mode, remove.Description)

		// A call on the namespaced name of a denied tool is refused and
		// never reaches the upstream, as are unknown tools.
		requireRefused(t, e.do(ctx, toolsCall("github__get_job_logs", `{}`)), string(mode), "deny", "denied by policy")
		requireRefused(t, e.do(ctx, toolsCall("github__drop_tables", `{}`)), string(mode), "deny_unknown_tool", "does not advertise")
		assert.Empty(t, e.sentCalls())
	}
	// The router's discovery hides it too.
	e := newExposureEnv(t, nil, exposure.ModeRouter, pol)
	ctx, _ := e.open(t, "any", ProtocolVersion20250618)
	text := resultText(t, e.do(ctx, toolsCall("list_tools", `{"server_name":"github"}`)))
	assert.NotContains(t, text, "get_job_logs")
}

func TestExposure_HybridAddsSearchWithCallableNames(t *testing.T) {
	e := newExposureEnv(t, nil, exposure.ModeHybrid, nil)
	ctx, s := e.open(t, "any", ProtocolVersion20250618)
	require.Equal(t, exposure.ModeHybrid, s.ExposureMode())
	tools, _ := e.list(t, ctx)
	require.NotEmpty(t, tools)
	assert.Equal(t, "search_tools", tools[0].Name)
	assert.Equal(t, hybridSearchDescription, tools[0].Description)
	assert.Contains(t, toolNames(tools), "github__create_issue")
	assert.NotContains(t, toolNames(tools), "invoke_tool")

	text := resultText(t, e.do(ctx, toolsCall("search_tools", `{"query":"create an issue"}`)))
	assert.Contains(t, text, "github__create_issue", "search results name the tools as the client calls them")
	assert.NotContains(t, text, "github_create_issue:")
}

func TestExposure_ToolPinningHidesPendingToolsInEveryMode(t *testing.T) {
	for _, mode := range []exposure.Mode{exposure.ModeRouter, exposure.ModePassthrough, exposure.ModeHybrid} {
		t.Run(string(mode), func(t *testing.T) {
			e := newExposureEnv(t, nil, mode, nil)
			store, err := toolpin.OpenStore(filepath.Join(t.TempDir(), "pins.json"))
			require.NoError(t, err)
			store.SetRecheckInterval(0)
			p := toolpin.NewWithOptions(toolpin.Options{Mode: toolpin.ModeBlock, Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			pins := NewToolPins(p)
			e.h.SetToolPins(pins)
			e.h.Use(pins.Middleware())
			for _, s := range []string{"github", "slack", "fs", "rich"} {
				require.NoError(t, e.h.RefreshServerTools(context.Background(), s))
			}
			ctx, _ := e.open(t, "claude-code", ProtocolVersion20250618)

			// Rug pull: create_issue's description changes upstream.
			tools := append([]Tool(nil), e.mp.tools["github"]...)
			tools[0].Description = poisoned
			e.mu.Lock()
			e.mp.tools["github"] = tools
			e.mu.Unlock()
			require.NoError(t, e.h.RefreshServerTools(context.Background(), "github"))

			if mode == exposure.ModeRouter {
				text := resultText(t, e.do(ctx, toolsCall("list_tools", `{"server_name":"github"}`)))
				assert.NotContains(t, text, "github_create_issue:")
			} else {
				listed, raw := e.list(t, ctx)
				assert.NotContains(t, toolNames(listed), "github__create_issue")
				assert.NotContains(t, raw, "ssh", "the poisoned description never reaches the client")
			}
			resp := e.do(ctx, toolsCall("github__create_issue", `{}`))
			require.NotNil(t, resp.Error)
			assert.Contains(t, resp.Error.Message, "tool pinning")
			assert.Empty(t, e.sentCalls())
		})
	}
}

func TestExposure_ToolPinningWarnModeMarksChangedTools(t *testing.T) {
	e := newExposureEnv(t, nil, exposure.ModePassthrough, nil)
	store, err := toolpin.OpenStore(filepath.Join(t.TempDir(), "pins.json"))
	require.NoError(t, err)
	store.SetRecheckInterval(0)
	e.h.SetToolPins(NewToolPins(toolpin.NewWithOptions(toolpin.Options{Mode: toolpin.ModeWarn, Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})))
	require.NoError(t, e.h.RefreshServerTools(context.Background(), "github"))
	tools := append([]Tool(nil), e.mp.tools["github"]...)
	tools[1].Description = "List pull requests, now differently."
	e.mu.Lock()
	e.mp.tools["github"] = tools
	e.mu.Unlock()
	require.NoError(t, e.h.RefreshServerTools(context.Background(), "github"))

	ctx, _ := e.open(t, "claude-code", ProtocolVersion20250618)
	listed, _ := e.list(t, ctx)
	changed, ok := findTool(listed, "github__list_pull_requests")
	require.True(t, ok, "warn mode keeps the tool listed")
	assert.True(t, strings.HasPrefix(changed.Description, pinWarnMarker), changed.Description)
	same, _ := findTool(listed, "github__create_issue")
	assert.False(t, strings.HasPrefix(same.Description, pinWarnMarker))
}

func TestExposure_InvisibleCharactersStripped(t *testing.T) {
	e := newExposureEnv(t, nil, exposure.ModePassthrough, nil)
	e.mu.Lock()
	e.mp.tools["slack"] = []Tool{{Name: "post", Description: "Post\u200b a\u202e message.", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	e.mu.Unlock()
	require.NoError(t, e.h.RefreshServerTools(context.Background(), "slack"))
	ctx, _ := e.open(t, "claude-code", ProtocolVersion20250618)
	listed, _ := e.list(t, ctx)
	post, ok := findTool(listed, "slack__post")
	require.True(t, ok)
	assert.Equal(t, "Post a message.", post.Description)
}

func TestExposure_ListChangedNotifiesListingSessionsOnly(t *testing.T) {
	oldDebounce, oldWatch := exposureDebounce, exposureWatchInterval
	exposureDebounce, exposureWatchInterval = 10*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { exposureDebounce, exposureWatchInterval = oldDebounce, oldWatch })

	e := newExposureEnv(t, nil, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go e.h.watchExposure(ctx)
	e.h.refreshMu.Lock()
	e.h.bgCtx = ctx
	e.h.refreshMu.Unlock()

	time.Sleep(100 * time.Millisecond) // the setup's own refreshes are flushed
	_, cc := e.open(t, "claude-code", ProtocolVersion20250618)
	_, other := e.open(t, "other", ProtocolVersion20250618)

	// An upstream adds a tool.
	e.mu.Lock()
	e.mp.tools["slack"] = append(append([]Tool(nil), e.mp.tools["slack"]...), Tool{Name: "slack_list_channels", InputSchema: json.RawMessage(`{"type":"object"}`)})
	e.mu.Unlock()
	require.NoError(t, e.h.RefreshServerTools(context.Background(), "slack"))
	require.Eventually(t, func() bool { return len(e.notesOf(cc)) == 1 }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, []string{NotificationToolsListChanged}, e.notesOf(cc))

	// The same list again: no notification.
	require.NoError(t, e.h.RefreshServerTools(context.Background(), "slack"))
	time.Sleep(100 * time.Millisecond)
	assert.Len(t, e.notesOf(cc), 1)

	// A visibility change no refresh reports (the policy view changes
	// under the watcher): caught by the periodic comparison.
	e.pol.Set(policy.MustCompile(&policy.Config{Rules: []policy.Rule{{Match: "slack.*", Action: policy.ActionDeny}}}))
	require.Eventually(t, func() bool { return len(e.notesOf(cc)) == 2 }, 5*time.Second, 5*time.Millisecond)
	assert.Empty(t, e.notesOf(other), "a router session never gets tools/list_changed")
}

// TestExposure_ConcurrentListingCallsAndRefreshes runs tools/list, calls
// on namespaced names and upstream refreshes concurrently (go test -race).
func TestExposure_ConcurrentListingCallsAndRefreshes(t *testing.T) {
	oldDebounce := exposureDebounce
	exposureDebounce = time.Millisecond
	t.Cleanup(func() { exposureDebounce = oldDebounce })
	e := newExposureEnv(t, nil, exposure.ModeHybrid, nil)
	ctx, _ := e.open(t, "claude-code", ProtocolVersion20251125)
	longName := exposure.ToolName("rich", longToolName, 64)

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				switch (w + i) % 4 {
				case 0:
					resp, err := e.h.HandleRequest(ctx, &Request{JSONRPC: "2.0", ID: i, Method: MethodToolsList})
					if err != nil || resp.Error != nil {
						errs <- fmt.Errorf("tools/list: %v %+v", err, resp)
					}
				case 1:
					resp, err := e.h.HandleRequest(ctx, toolsCall(longName, `{}`))
					if err != nil || resp.Error != nil {
						errs <- fmt.Errorf("call %s: %v %+v", longName, err, resp)
					}
				case 2:
					resp, err := e.h.HandleRequest(ctx, toolsCall("github__create_issue", `{}`))
					if err != nil || resp.Error != nil {
						errs <- fmt.Errorf("call: %v %+v", err, resp)
					}
				default:
					if err := e.h.RefreshServerTools(context.Background(), "github"); err != nil {
						errs <- err
					}
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestExposure_MiddlewareLeavesOtherRequestsAlone(t *testing.T) {
	e := newExposureEnv(t, nil, exposure.ModePassthrough, nil)
	var seen []string
	mw := e.h.ExposureMiddleware()(func(_ context.Context, req *Request) (*Response, error) {
		var p ToolsCallParams
		_ = json.Unmarshal(req.Params, &p)
		seen = append(seen, req.Method+" "+p.Name)
		return &Response{JSONRPC: "2.0", Result: json.RawMessage(`{}`)}, nil
	})
	for _, req := range []*Request{
		toolsCall("search_tools", `{}`),
		toolsCall("read_result", `{}`),
		toolsCall("github_create_issue", `{}`),
		toolsCall("nosuch__tool", `{}`),
		toolsCall("github__create_issue", `{}`),
		{JSONRPC: "2.0", ID: 1, Method: MethodToolsList},
		{JSONRPC: "2.0", ID: 1, Method: MethodToolsCall, Params: json.RawMessage(`not json`)},
	} {
		_, err := mw(context.Background(), req)
		require.NoError(t, err)
	}
	assert.Equal(t, []string{"tools/call search_tools", "tools/call read_result", "tools/call github_create_issue", "tools/call nosuch__tool",
		"tools/call github.create_issue", "tools/list ", "tools/call "}, seen)
}

func TestExposedMeta(t *testing.T) {
	for _, c := range []struct {
		in         string
		alwaysLoad bool
		want       string
	}{
		{"", false, ""},
		{"", true, `{"anthropic/alwaysLoad":true}`},
		{`{"a":1}`, false, `{"a":1}`},
		{`{"anthropic/alwaysLoad":true}`, false, ""},
		{`{"anthropic/alwaysLoad":false,"a":1}`, true, `{"a":1,"anthropic/alwaysLoad":true}`},
		{`[1]`, true, ""},
		{`null`, false, ""},
	} {
		got := exposedMeta(json.RawMessage(c.in), c.alwaysLoad)
		if c.want == "" {
			assert.Empty(t, got, c.in)
			continue
		}
		assert.JSONEq(t, c.want, string(got), c.in)
	}
}
