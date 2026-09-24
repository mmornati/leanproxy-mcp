package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp/governor"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
)

// Coverage for field projection (issue #320) through a real Handler, the
// governor, the response cache and the firewall, in the front ends' order.

// wideIssues is n GitHub-like issues with the usual API noise; issue 3's
// body carries govSecret (redacted before projection).
func wideIssues(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := range out {
		num := 100 + i
		body := fmt.Sprintf("Steps to reproduce issue %d.", num)
		if i == 3 {
			body = "token=" + govSecret
		}
		out[i] = map[string]any{
			"url":        fmt.Sprintf("https://api.example.com/repos/o/r/issues/%d", num),
			"html_url":   fmt.Sprintf("https://example.com/o/r/issues/%d", num),
			"id":         json.Number(fmt.Sprintf("90071992547409%02d", i)), // above 2^53
			"node_id":    fmt.Sprintf("I_kwDO%08d", i),
			"number":     num,
			"title":      fmt.Sprintf("Issue <%d> & more", num),
			"state":      "open",
			"user":       map[string]any{"login": "mona", "id": 1, "avatar_url": "https://avatars.example.com/u/1", "gravatar_id": "", "followers_url": "https://api.example.com/users/mona/followers"},
			"labels":     []map[string]any{{"name": "bug", "color": "d73a4a", "url": "https://api.example.com/labels/bug"}, {"name": "p1", "color": "b60205", "url": "https://api.example.com/labels/p1"}},
			"assignee":   nil,
			"reactions":  map[string]any{"url": "https://api.example.com/reactions", "total_count": 0, "+1": 0},
			"body":       body,
			"updated_at": "2026-09-20T12:34:56Z",
		}
	}
	return out
}

func wideJSON(n int) string {
	b, err := governor.MarshalNoEscape(wideIssues(n))
	if err != nil {
		panic(err)
	}
	return string(b)
}

type projUpstream struct {
	mu   sync.Mutex
	args []json.RawMessage // tools/call params received
}

func (u *projUpstream) result(name string) json.RawMessage {
	text := func(s string) json.RawMessage {
		b, _ := governor.MarshalNoEscape(map[string]any{"content": []map[string]string{{"type": "text", "text": s}}})
		return b
	}
	switch name {
	case "list_issues", "list_passthrough":
		return text(wideJSON(30))
	case "huge_list":
		return text(wideJSON(600))
	case "prose":
		return text(strings.Repeat("plain prose, not JSON. ", 400))
	case "list_fail":
		b, _ := governor.MarshalNoEscape(map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": wideJSON(30)}}})
		return b
	case "structured_free", "structured_strict":
		doc := `{"issues":` + wideJSON(20) + `}`
		b, _ := governor.MarshalNoEscape(map[string]any{
			"content":           []map[string]string{{"type": "text", "text": doc}},
			"structuredContent": json.RawMessage(doc),
		})
		return b
	case "echo":
		return text("ok")
	}
	return text("unknown tool")
}

const projToolsList = `{"tools":[
	{"name":"list_issues","inputSchema":{"type":"object"}},
	{"name":"huge_list","inputSchema":{"type":"object"}},
	{"name":"prose","inputSchema":{"type":"object"}},
	{"name":"list_fail","inputSchema":{"type":"object"}},
	{"name":"list_passthrough","inputSchema":{"type":"object"}},
	{"name":"structured_free","inputSchema":{"type":"object"}},
	{"name":"structured_strict","inputSchema":{"type":"object"},"outputSchema":{"type":"object","properties":{"issues":{"type":"array"}},"required":["issues"],"additionalProperties":false}},
	{"name":"echo","inputSchema":{"type":"object"}}
]}`

func newProjHarness(t *testing.T, cfg *governor.Config) (*govHarness, *projUpstream) {
	t.Helper()
	up := &projUpstream{}
	mp := newMockPool()
	mp.servers["gh"] = string(pool.StateIdle)
	mp.sendRequestFunc = func(ctx context.Context, name, method string, params json.RawMessage, timeout time.Duration) (*pool.Response, error) {
		switch method {
		case MethodToolsCall:
			var p ToolsCallParams
			_ = json.Unmarshal(params, &p)
			up.mu.Lock()
			up.args = append(up.args, append(json.RawMessage(nil), params...))
			up.mu.Unlock()
			return &pool.Response{Result: up.result(p.Name)}, nil
		case MethodToolsList:
			return &pool.Response{Result: json.RawMessage(projToolsList)}, nil
		default:
			return &pool.Response{Result: json.RawMessage(`{"capabilities":{"tools":{}}}`)}, nil
		}
	}
	h := NewHandler(mp, nil)
	gov := NewGovernor(cfg)
	t.Cleanup(gov.Close)
	h.SetGovernor(gov)
	fw := NewFirewall(nil, nil)
	rc := NewResponseCache(nil)
	mws := []Middleware{gov.Middleware(), rc.Middleware()}
	mws = append(mws, fw.Middlewares()...)
	h.Use(mws...)
	return &govHarness{h: h, gov: gov, fw: fw}, up
}

// githubDrop is the issue's github.* drop pack.
var githubDrop = []string{"**.node_id", "**.*_url", "**.url", "**.reactions", "**.avatar_url", "**.gravatar_id"}

func projCfg(budget int, rules ...governor.ProjectionRule) *governor.Config {
	return enabledCfg(func(c *governor.Config) {
		c.MaxTokens = intPtr(budget)
		c.Projections = rules
	})
}

func invokeGH(t *testing.T, g *govHarness, ctx context.Context, tool string, extra map[string]any) (*Response, govResult) {
	t.Helper()
	env := map[string]any{"server": "gh", "tool": tool, "arguments": map[string]any{"state": "open"}}
	for k, v := range extra {
		env[k] = v
	}
	return g.call(t, ctx, "invoke_tool", env)
}

// noteOf returns the projection note item, or "".
func noteOf(r govResult) string {
	for _, c := range r.Content {
		if c.Type == "text" && strings.HasPrefix(c.Text, "[LeanProxy: ") && strings.Contains(c.Text, "projected by") {
			return c.Text
		}
	}
	return ""
}

func linksOf(r govResult) []string {
	var out []string
	for _, c := range r.Content {
		if c.Type == "resource_link" {
			out = append(out, c.URI)
		}
	}
	return out
}

func readFull(t *testing.T, g *govHarness, ctx context.Context, id string) string {
	t.Helper()
	var b strings.Builder
	offset := 0
	for i := 0; i < 100; i++ {
		_, p := g.call(t, ctx, ReadResultToolName, map[string]any{"result_id": id, "offset": offset, "limit_tokens": 50000})
		require.False(t, p.IsError, "read_result: %+v", p)
		b.WriteString(p.Content[0].Text)
		nav := p.Content[1].Text
		if strings.Contains(nav, "end of result") {
			return b.String()
		}
		j := strings.Index(nav, "offset=")
		_, err := fmt.Sscanf(nav[j:], "offset=%d]", &offset)
		require.NoError(t, err)
	}
	t.Fatal("read_result never reached the end")
	return ""
}

func TestProjection_DropRuleOnJSONText(t *testing.T) {
	gh, _ := newProjHarness(t, projCfg(0, governor.ProjectionRule{Match: "gh.*", Drop: githubDrop}))
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()

	_, r := invokeGH(t, gh, ctx, "list_issues", nil)
	require.GreaterOrEqual(t, len(r.Content), 3, "projected text, note, resource_link")
	text := r.Content[0].Text
	var issues []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(text), &issues), "projected JSON must stay valid")
	require.Len(t, issues, 30)
	for _, is := range issues {
		for _, k := range []string{"url", "html_url", "node_id", "reactions"} {
			require.NotContains(t, is, k)
		}
		require.NotContains(t, string(is["user"]), "avatar_url")
	}
	require.Equal(t, "9007199254740903", string(issues[3]["id"]), "numbers keep their exact text")
	require.Contains(t, text, `"title":"Issue <103> & more"`, "no HTML escaping")
	require.Contains(t, text, "[SECRET_REDACTED]")
	require.NotContains(t, text, govSecret)

	note := noteOf(r)
	require.Contains(t, note, `response.projections rule "gh.*"`)
	require.Contains(t, note, "drop **.node_id")
	id := markerID(t, note)
	require.Equal(t, []string{ResultURI(id)}, linksOf(r))

	// The full, redacted result stays retrievable.
	full := readFull(t, gh, ctx, id)
	require.Equal(t, wideJSONRedacted(30), full)
	require.NotContains(t, full, govSecret)

	st := gh.gov.Stats()
	require.EqualValues(t, 1, st.Projected)
	require.Positive(t, st.ProjectionSavedTokens)
	require.EqualValues(t, 0, st.Truncated)
}

// wideJSONRedacted is wideJSON(n) as the firewall redacts it.
func wideJSONRedacted(n int) string {
	return strings.ReplaceAll(wideJSON(n), govSecret, "[SECRET_REDACTED]")
}

func TestProjection_KeepRuleWithNestedArrays(t *testing.T) {
	gh, _ := newProjHarness(t, projCfg(4000, governor.ProjectionRule{Match: "gh.list_issues", Keep: []string{"[].number", "[].title", "[].labels[].name", "[].assignee.login"}}))
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()
	_, r := invokeGH(t, gh, ctx, "list_issues", nil)
	var issues []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(r.Content[0].Text), &issues))
	require.Len(t, issues, 30)
	for _, is := range issues {
		require.Len(t, is, 4, "number, title, labels, assignee (null kept)")
		require.JSONEq(t, `[{"name":"bug"},{"name":"p1"}]`, string(is["labels"]))
		require.Equal(t, "null", string(is["assignee"]))
	}
	require.Contains(t, noteOf(r), "keep [].number")
}

func TestProjection_FieldsArgumentAppliedAndNotForwarded(t *testing.T) {
	gh, up := newProjHarness(t, projCfg(4000))
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()
	for _, fields := range []any{[]string{"[].number", "[].title"}, "[].number, [].title"} {
		_, r := invokeGH(t, gh, ctx, "list_issues", map[string]any{"fields": fields})
		var issues []map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(r.Content[0].Text), &issues))
		require.Len(t, issues, 30)
		for _, is := range issues {
			require.Len(t, is, 2)
		}
		require.Contains(t, noteOf(r), "your fields argument")
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	require.Len(t, up.args, 2)
	for _, a := range up.args {
		require.NotContains(t, string(a), "fields", "fields must not reach the upstream")
		require.JSONEq(t, `{"name":"list_issues","arguments":{"state":"open"}}`, string(a))
	}
}

func TestProjection_FieldsOverridesRulesAndPassthrough(t *testing.T) {
	cfg := projCfg(4000, governor.ProjectionRule{Match: "gh.*", Drop: githubDrop})
	cfg.Tools = []governor.ToolRule{{Match: "gh.list_passthrough", Passthrough: true}}
	gh, _ := newProjHarness(t, cfg)
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()

	// passthrough: no configured projection…
	_, r := invokeGH(t, gh, ctx, "list_passthrough", nil)
	require.Equal(t, wideJSONRedacted(30), r.Content[0].Text)
	require.Empty(t, noteOf(r))
	// …but the model's fields still apply.
	_, r = invokeGH(t, gh, ctx, "list_passthrough", map[string]any{"fields": []string{"[].number"}})
	require.Contains(t, noteOf(r), "your fields argument")
	// fields wins over the matching rule.
	_, r = invokeGH(t, gh, ctx, "list_issues", map[string]any{"fields": []string{"[].state"}})
	var issues []map[string]string
	require.NoError(t, json.Unmarshal([]byte(r.Content[0].Text), &issues))
	require.Equal(t, map[string]string{"state": "open"}, issues[0])
}

func TestProjection_BadFieldsLeaveTheResultUnchanged(t *testing.T) {
	gh, up := newProjHarness(t, projCfg(0))
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()
	for _, fields := range []any{[]string{"items[0].x"}, 42, []string{}, "", []string{"a.**"}} {
		_, r := invokeGH(t, gh, ctx, "list_issues", map[string]any{"fields": fields})
		require.Len(t, r.Content, 1, "fields %v", fields)
		require.Equal(t, wideJSONRedacted(30), r.Content[0].Text)
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	for _, a := range up.args {
		require.NotContains(t, string(a), "fields")
	}
}

func TestProjection_NonJSONTextAndErrorsUntouched(t *testing.T) {
	gh, _ := newProjHarness(t, projCfg(4000, governor.ProjectionRule{Match: "gh.*", Drop: []string{"**.title"}}))
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()
	_, r := invokeGH(t, gh, ctx, "prose", map[string]any{"fields": []string{"x"}})
	require.Len(t, r.Content, 1)
	require.Equal(t, strings.Repeat("plain prose, not JSON. ", 400), r.Content[0].Text)

	_, r = invokeGH(t, gh, ctx, "list_fail", nil)
	require.True(t, r.IsError)
	require.Len(t, r.Content, 1)
	require.Equal(t, wideJSONRedacted(30), r.Content[0].Text, "an error result is never projected")
	require.EqualValues(t, 0, gh.gov.Stats().Projected)
}

func TestProjection_StructuredContentAndOutputSchema(t *testing.T) {
	gh, _ := newProjHarness(t, projCfg(0, governor.ProjectionRule{Match: "gh.*", Drop: githubDrop}))
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()

	// No outputSchema: text and structuredContent are both projected; the
	// identical full copies share one result id.
	_, r := invokeGH(t, gh, ctx, "structured_free", nil)
	require.NotContains(t, string(r.StructuredContent), "node_id")
	require.NotContains(t, r.Content[0].Text, "node_id")
	require.JSONEq(t, r.Content[0].Text, string(r.StructuredContent))
	require.Len(t, linksOf(r), 1, "one full copy for identical units")

	// A declared outputSchema: only the text rendering is projected, the
	// structuredContent is passed through byte for byte.
	_, r = invokeGH(t, gh, ctx, "structured_strict", nil)
	require.NotContains(t, r.Content[0].Text, "node_id")
	doc := strings.ReplaceAll(`{"issues":`+wideJSON(20)+`}`, govSecret, "[SECRET_REDACTED]")
	require.Equal(t, doc, string(r.StructuredContent))
	require.Len(t, linksOf(r), 1)
}

func TestProjection_ThenTruncationWithinBudget(t *testing.T) {
	gh, _ := newProjHarness(t, projCfg(4000, governor.ProjectionRule{Match: "gh.*", Drop: githubDrop}))
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()
	resp, r := invokeGH(t, gh, ctx, "huge_list", nil)
	require.LessOrEqual(t, governor.Tokens(len(resp.Result)), 4000, "the whole result stays within budget")
	var items []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(r.Content[0].Text), &items))
	var om struct {
		Items    int    `json:"items"`
		ResultID string `json:"result_id"`
	}
	require.NoError(t, json.Unmarshal(items[len(items)-1][governor.OmittedKey], &om))
	require.Equal(t, 600, len(items)-1+om.Items)
	for _, it := range items[:len(items)-1] {
		require.NotContains(t, it, "node_id")
	}
	note := noteOf(r)
	projID := markerID(t, note)
	require.NotEqual(t, projID, om.ResultID)
	require.ElementsMatch(t, []string{ResultURI(projID), ResultURI(om.ResultID)}, linksOf(r))

	// The projection id serves the full original; the truncation id the
	// projected document.
	require.Equal(t, wideJSONRedacted(600), readFull(t, gh, ctx, projID))
	projected := readFull(t, gh, ctx, om.ResultID)
	require.NotContains(t, projected, "node_id")
	require.True(t, json.Valid([]byte(projected)))

	// More items fit in the budget than without projection.
	plain, _ := newProjHarness(t, projCfg(4000))
	pctx, pdone := plain.session(t, ProtocolVersion20250618)
	defer pdone()
	_, pr := invokeGH(t, plain, pctx, "huge_list", nil)
	var plainItems []json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(pr.Content[0].Text), &plainItems))
	require.Greater(t, len(items), 2*len(plainItems), "projection lets more items fit in the budget")
}

func TestProjection_OlderProtocolGetsTheNoteOnly(t *testing.T) {
	gh, _ := newProjHarness(t, projCfg(0, governor.ProjectionRule{Match: "gh.*", Drop: githubDrop}))
	ctx, done := gh.session(t, ProtocolVersion20241105)
	defer done()
	_, r := invokeGH(t, gh, ctx, "list_issues", nil)
	require.Len(t, r.Content, 2)
	require.Empty(t, linksOf(r), "resource_link does not exist before 2025-06-18")
	id := markerID(t, noteOf(r))
	require.Equal(t, wideJSONRedacted(30), readFull(t, gh, ctx, id))
}

func TestProjection_DefaultPackAndExemptions(t *testing.T) {
	cfg := projCfg(0, governor.ProjectionRule{Match: "gh.list_passthrough"})
	cfg.DefaultProjections = true
	gh, _ := newProjHarness(t, cfg)
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()
	_, r := invokeGH(t, gh, ctx, "list_issues", nil)
	require.Contains(t, noteOf(r), "the default projections")
	require.NotContains(t, r.Content[0].Text, "node_id")
	require.NotContains(t, r.Content[0].Text, "html_url")
	require.Contains(t, r.Content[0].Text, `"url":`, "the default pack is conservative")
	_, r = invokeGH(t, gh, ctx, "list_passthrough", nil)
	require.Empty(t, noteOf(r), "an empty rule exempts the tool")
	require.Contains(t, gh.gov.Summary(), "1 projection rules (default pack on)")
}

func TestProjection_DisabledGovernorIgnoresFields(t *testing.T) {
	gh, up := newProjHarness(t, nil)
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()
	_, r := invokeGH(t, gh, ctx, "list_issues", map[string]any{"fields": []string{"[].number"}})
	require.Len(t, r.Content, 1)
	require.Equal(t, wideJSONRedacted(30), r.Content[0].Text)
	up.mu.Lock()
	defer up.mu.Unlock()
	require.NotContains(t, string(up.args[0]), "fields", "the handler only forwards arguments")
}

func TestProjection_ToolsListDeclaresFields(t *testing.T) {
	list := func(g *govHarness, ctx context.Context) string {
		resp, err := g.h.HandleRequest(ctx, &Request{JSONRPC: JSONRPCVersion, Method: MethodToolsList, ID: 2})
		require.NoError(t, err)
		return string(resp.Result)
	}
	on, _ := newProjHarness(t, projCfg(4000))
	ctx, done := on.session(t, ProtocolVersion20250618)
	defer done()
	var body struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal([]byte(list(on, ctx)), &body))
	found := false
	for _, tl := range body.Tools {
		if tl.Name == "invoke_tool" {
			found = true
			require.Contains(t, tl.Description, "fields")
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			require.NoError(t, json.Unmarshal(tl.InputSchema, &schema))
			require.JSONEq(t, `{"type":"array","items":{"type":"string"}}`, string(schema.Properties["fields"]))
		}
	}
	require.True(t, found)

	off, _ := newProjHarness(t, nil)
	ctx2, done2 := off.session(t, ProtocolVersion20250618)
	defer done2()
	require.NotContains(t, list(off, ctx2), `"fields"`, "the default tools/list is unchanged")
}

func TestTakeFields(t *testing.T) {
	args := `{"b":9007199254740993,"a":"x<y"}`
	tests := []struct {
		name       string
		req        *Request
		wantFields string
		wantParams string
	}{
		{
			name:       "tools/call invoke_tool",
			req:        &Request{Method: MethodToolsCall, Params: json.RawMessage(`{"name":"invoke_tool","arguments":{"server":"s","tool":"t","arguments":` + args + `,"fields":["[].a"]}}`)},
			wantFields: `["[].a"]`,
			wantParams: `{"name":"invoke_tool","arguments":{"server":"s","tool":"t","arguments":` + args + `}}`,
		},
		{
			name:       "serve method form",
			req:        &Request{Method: "invoke_tool", Params: json.RawMessage(`{"server_name":"s","fields":"a,b","tool_name":"t","arguments":` + args + `}`)},
			wantFields: `"a,b"`,
			wantParams: `{"server_name":"s","tool_name":"t","arguments":` + args + `}`,
		},
		{
			name: "a tool's own fields argument is the tool's",
			req:  &Request{Method: MethodToolsCall, Params: json.RawMessage(`{"name":"gh_search","arguments":{"fields":["a"]}}`)},
		},
		{
			name: "fields inside invoke_tool's tool arguments is the tool's",
			req:  &Request{Method: MethodToolsCall, Params: json.RawMessage(`{"name":"invoke_tool","arguments":{"server":"s","tool":"t","arguments":{"fields":["a"]}}}`)},
		},
		{
			name:       "null fields is stripped and ignored",
			req:        &Request{Method: MethodToolsCall, Params: json.RawMessage(`{"name":"invoke_tool","arguments":{"server":"s","tool":"t","fields":null}}`)},
			wantParams: `{"name":"invoke_tool","arguments":{"server":"s","tool":"t"}}`,
		},
		{
			name: "no params",
			req:  &Request{Method: MethodToolsCall},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := string(tt.req.Params)
			got, fields := takeFields(tt.req)
			require.Equal(t, orig, string(tt.req.Params), "the request is not modified")
			if tt.wantFields == "" {
				require.Nil(t, fields)
				if tt.wantParams == "" {
					require.Same(t, tt.req, got)
				} else {
					require.Equal(t, tt.wantParams, string(got.Params))
				}
				return
			}
			require.Equal(t, tt.wantFields, string(fields))
			require.Equal(t, tt.wantParams, string(got.Params))
		})
	}
}

func TestProjection_Concurrent(t *testing.T) {
	gh, _ := newProjHarness(t, projCfg(4000, governor.ProjectionRule{Match: "gh.*", Drop: githubDrop}))
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			s, done := gh.h.OpenSession(nil)
			defer done()
			ctx := WithClientSession(context.Background(), s)
			for i := 0; i < 4; i++ {
				tool := []string{"list_issues", "huge_list", "structured_free"}[(w+i)%3]
				env := map[string]any{"server": "gh", "tool": tool}
				if i%2 == 1 {
					env["fields"] = []string{"[].number", "issues[].number"}
				}
				resp, err := gh.h.HandleRequest(ctx, toolsCallRequest(t, i, "invoke_tool", env))
				if err != nil || resp.Error != nil {
					t.Errorf("call: %v %+v", err, resp)
					return
				}
				var r govResult
				if err := json.Unmarshal(resp.Result, &r); err != nil {
					t.Errorf("result: %v", err)
					return
				}
				note := noteOf(r)
				j := strings.Index(note, "result_id=r_")
				if j < 0 {
					t.Errorf("no projection note: %.300s", resp.Result)
					return
				}
				id := note[j+10 : j+10+28]
				rr, err := gh.h.HandleRequest(ctx, toolsCallRequest(t, 99, ReadResultToolName, map[string]any{"result_id": id, "jsonpath": "$[0].node_id"}))
				if err != nil || strings.Contains(string(rr.Result), `"isError":true`) {
					t.Errorf("read_result: %v %.200s", err, rr.Result)
				}
				_ = gh.gov.Stats()
			}
		}(w)
	}
	wg.Wait()
	st := gh.gov.Stats()
	require.EqualValues(t, 24, st.Projected)
	require.Equal(t, 0, st.Store.Entries, "every session ended")
}

func TestProjection_AccountingAndTelemetry(t *testing.T) {
	before := TelemetrySnapshot()
	gh, _ := newProjHarness(t, projCfg(4000, governor.ProjectionRule{Match: "gh.list_issues", Drop: githubDrop}))
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()
	invokeGH(t, gh, ctx, "list_issues", nil)
	invokeGH(t, gh, ctx, "echo", nil)
	st := gh.gov.Stats()
	require.EqualValues(t, 2, st.Results)
	require.EqualValues(t, 1, st.Projected)
	require.Equal(t, 1, st.ProjectionRules)
	for _, bt := range st.ByTool {
		switch bt.Tool {
		case "gh.list_issues":
			require.EqualValues(t, 1, bt.Projected)
			require.Positive(t, bt.ProjectionSavedTokens)
			require.Less(t, bt.ReturnedTokens, bt.OriginalTokens)
		case "gh.echo":
			require.Zero(t, bt.Projected)
		}
	}
	after := TelemetrySnapshot()
	require.GreaterOrEqual(t, after.GovernorProjections-before.GovernorProjections, int64(1))
	require.Greater(t, after.GovernorProjectionTokensSaved, before.GovernorProjectionTokensSaved)
}
