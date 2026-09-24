package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp/governor"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp/responsecache"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
)

// Unit/integration coverage for the response governor (issue #319): a real
// Handler behind the same stage order the front ends install (governor,
// cache, firewall), with a mock upstream returning large results.

// govSecret is a credential-shaped string the built-in redaction patterns
// catch; built at runtime so no secret-looking literal is committed.
var govSecret = "ghp_" + strings.Repeat("a1B2", 9)

// govBigText is ~200 KB of numbered lines with govSecret on line 1700.
func govBigText() string {
	var b strings.Builder
	for i := 1; i <= 3500; i++ {
		if i == 1700 {
			fmt.Fprintf(&b, "line %05d: token=%s\n", i, govSecret)
			continue
		}
		fmt.Fprintf(&b, "line %05d: the quick brown fox jumps over the lazy dog\n", i)
	}
	return b.String()
}

func govJSONArray(n int) string {
	items := make([]map[string]any, n)
	for i := range items {
		items[i] = map[string]any{"number": i, "title": fmt.Sprintf("Issue %d", i), "state": "open"}
	}
	b, _ := json.Marshal(items)
	return string(b)
}

type govUpstream struct {
	calls atomic.Int64
}

// result answers a tools/call for the fake "fs" server by tool name.
func (u *govUpstream) result(tool string) json.RawMessage {
	u.calls.Add(1)
	text := func(s string) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"content": []map[string]string{{"type": "text", "text": s}}})
		return b
	}
	switch tool {
	case "read_file", "raw_read":
		return text(govBigText())
	case "list_issues":
		return text(govJSONArray(1000))
	case "small":
		return text("just a few words")
	case "fail_big":
		b, _ := json.Marshal(map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": "stack trace:\n" + govBigText()}}})
		return b
	case "screenshot":
		b, _ := json.Marshal(map[string]any{"content": []map[string]string{
			{"type": "image", "mimeType": "image/png", "data": strings.Repeat("iVBORw0KGgo", 30000)},
			{"type": "text", "text": "caption"},
		}})
		return b
	case "structured":
		arr := govJSONArray(1000)
		b, _ := json.Marshal(map[string]any{
			"content":           []map[string]string{{"type": "text", "text": `{"items":` + arr + `}`}},
			"structuredContent": json.RawMessage(`{"items":` + arr + `}`),
		})
		return b
	}
	return text("unknown tool")
}

type govHarness struct {
	h        *Handler
	gov      *Governor
	fw       *Firewall
	upstream *govUpstream
}

func newGovHarness(t *testing.T, cfg *governor.Config, cache *responsecache.Config) *govHarness {
	t.Helper()
	up := &govUpstream{}
	mp := newMockPool()
	mp.servers["fs"] = string(pool.StateIdle)
	mp.sendRequestFunc = func(ctx context.Context, name, method string, params json.RawMessage, timeout time.Duration) (*pool.Response, error) {
		switch method {
		case MethodToolsCall:
			var p ToolsCallParams
			_ = json.Unmarshal(params, &p)
			return &pool.Response{Result: up.result(p.Name)}, nil
		case MethodToolsList:
			return &pool.Response{Result: json.RawMessage(`{"tools":[]}`)}, nil
		default:
			return &pool.Response{Result: json.RawMessage(`{"capabilities":{"tools":{}}}`)}, nil
		}
	}
	h := NewHandler(mp, nil)
	gov := NewGovernor(cfg)
	t.Cleanup(gov.Close)
	h.SetGovernor(gov)
	fw := NewFirewall(nil, nil)
	rc := NewResponseCache(cache)
	mws := []Middleware{gov.Middleware(), rc.Middleware()}
	mws = append(mws, fw.Middlewares()...)
	h.Use(mws...)
	return &govHarness{h: h, gov: gov, fw: fw, upstream: up}
}

func enabledCfg(mut ...func(*governor.Config)) *governor.Config {
	c := &governor.Config{Enabled: true}
	for _, m := range mut {
		m(c)
	}
	return c
}

// session opens a client session that negotiated version.
func (g *govHarness) session(t *testing.T, version string) (context.Context, func()) {
	t.Helper()
	s, closeFn := g.h.OpenSession(nil)
	ctx := WithClientSession(context.Background(), s)
	resp, err := g.h.HandleRequest(ctx, &Request{JSONRPC: JSONRPCVersion, Method: MethodInitialize, ID: 0,
		Params: json.RawMessage(`{"protocolVersion":"` + version + `","capabilities":{},"clientInfo":{"name":"t","version":"1"}}`)})
	require.NoError(t, err)
	require.Nil(t, resp.Error)
	return ctx, closeFn
}

type govResult struct {
	Content []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		URI      string `json:"uri"`
		MimeType string `json:"mimeType"`
		Data     string `json:"data"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

func (g *govHarness) call(t *testing.T, ctx context.Context, name string, args any) (*Response, govResult) {
	t.Helper()
	resp, err := g.h.HandleRequest(ctx, toolsCallRequest(t, 1, name, args))
	require.NoError(t, err)
	require.NotNil(t, resp)
	var r govResult
	if resp.Result != nil {
		require.NoError(t, json.Unmarshal(resp.Result, &r), "result: %.300s", resp.Result)
	}
	return resp, r
}

func (g *govHarness) invoke(t *testing.T, ctx context.Context, tool string) (*Response, govResult) {
	t.Helper()
	return g.call(t, ctx, "invoke_tool", map[string]any{"server": "fs", "tool": tool, "arguments": map[string]any{}})
}

func markerID(t *testing.T, text string) string {
	t.Helper()
	i := strings.Index(text, "id=r_")
	require.GreaterOrEqual(t, i, 0, "no result id in %.300q", text)
	return text[i+3 : i+3+28]
}

func contentTokens(r govResult) int {
	n := 0
	for _, c := range r.Content {
		n += len(c.Text) + len(c.URI)
	}
	return governor.Tokens(n)
}

func TestGovernor_DisabledByDefault(t *testing.T) {
	for _, cfg := range []*governor.Config{nil, {}, {MaxTokens: intPtr(4000)}} {
		g := NewGovernor(cfg)
		require.False(t, g.Enabled())
	}
	gh := newGovHarness(t, nil, nil)
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()
	_, r := gh.invoke(t, ctx, "read_file")
	require.Len(t, r.Content, 1)
	require.Greater(t, len(r.Content[0].Text), 190_000, "a disabled governor must not shorten anything")
	resp, err := gh.h.HandleRequest(ctx, &Request{JSONRPC: JSONRPCVersion, Method: MethodToolsList, ID: 2})
	require.NoError(t, err)
	require.NotContains(t, string(resp.Result), ReadResultToolName)
}

func intPtr(v int) *int { return &v }

func TestGovernor_TextResultTruncatedAndPagedBack(t *testing.T) {
	gh := newGovHarness(t, enabledCfg(), nil)
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()

	_, r := gh.invoke(t, ctx, "read_file")
	require.LessOrEqual(t, contentTokens(r), 4000)
	require.Len(t, r.Content, 2, "shortened text + resource_link")
	text := r.Content[0].Text
	require.True(t, strings.HasPrefix(text, "line 00001:"))
	require.True(t, strings.HasSuffix(text, "line 03500: the quick brown fox jumps over the lazy dog\n"))
	require.Contains(t, text, "tokens omitted — call read_result with id=")
	id := markerID(t, text)
	require.Equal(t, "resource_link", r.Content[1].Type)
	require.Equal(t, ResultURI(id), r.Content[1].URI)

	// Paging from 0 reassembles the whole (redacted) original.
	want := gh.fw.Redaction.RedactText(govBigText())
	require.NotContains(t, want, govSecret)
	var b strings.Builder
	offset := 0
	for pages := 0; pages < 100; pages++ {
		_, p := gh.call(t, ctx, ReadResultToolName, map[string]any{"result_id": id, "offset": offset})
		require.False(t, p.IsError, "%+v", p)
		require.Len(t, p.Content, 2)
		b.WriteString(p.Content[0].Text)
		nav := p.Content[1].Text
		if strings.Contains(nav, "end of result") {
			break
		}
		i := strings.Index(nav, "offset=")
		require.GreaterOrEqual(t, i, 0, nav)
		_, err := fmt.Sscanf(nav[i:], "offset=%d]", &offset)
		require.NoError(t, err, nav)
	}
	require.Equal(t, want, b.String(), "concatenated pages must equal the redacted original")

	// resources/read serves the same full text natively.
	resp, err := gh.h.HandleRequest(ctx, &Request{JSONRPC: JSONRPCVersion, Method: MethodResourcesRead, ID: 3,
		Params: mustJSON(t, map[string]string{"uri": ResultURI(id)})})
	require.NoError(t, err)
	require.Nil(t, resp.Error)
	var rr struct {
		Contents []struct{ URI, MimeType, Text string } `json:"contents"`
	}
	require.NoError(t, json.Unmarshal(resp.Result, &rr))
	require.Len(t, rr.Contents, 1)
	require.Equal(t, want, rr.Contents[0].Text)
	require.Equal(t, "text/plain", rr.Contents[0].MimeType)

	st := gh.gov.Stats()
	require.EqualValues(t, 1, st.Truncated)
	require.Greater(t, st.SavedTokens, int64(40000))
	require.Equal(t, 1, st.Store.Entries)
}

func TestGovernor_OffsetMarkerPointsAtTheOmittedPart(t *testing.T) {
	gh := newGovHarness(t, enabledCfg(), nil)
	ctx, done := gh.session(t, ProtocolVersion20241105)
	defer done()
	_, r := gh.invoke(t, ctx, "read_file")
	text := r.Content[0].Text
	id := markerID(t, text)
	var offset int
	i := strings.Index(text, ", offset=")
	_, err := fmt.Sscanf(text[i:], ", offset=%d", &offset)
	require.NoError(t, err)
	head := text[:strings.Index(text, "… [LeanProxy")]
	require.Equal(t, len(head), offset)
	_, p := gh.call(t, ctx, ReadResultToolName, map[string]any{"result_id": id, "offset": offset, "limit_tokens": 100})
	require.True(t, strings.HasPrefix(gh.fw.Redaction.RedactText(govBigText())[offset:], p.Content[0].Text))
}

func TestGovernor_JSONArrayStructuralAndJSONPath(t *testing.T) {
	gh := newGovHarness(t, enabledCfg(), nil)
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()
	_, r := gh.invoke(t, ctx, "list_issues")
	require.LessOrEqual(t, contentTokens(r), 4000)
	var items []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(r.Content[0].Text), &items), "must stay valid JSON")
	last := items[len(items)-1]
	var om struct {
		Items    int    `json:"items"`
		ResultID string `json:"result_id"`
	}
	require.NoError(t, json.Unmarshal(last[governor.OmittedKey], &om))
	require.Equal(t, 1000-(len(items)-1), om.Items)
	require.Equal(t, "application/json", r.Content[1].MimeType)

	_, p := gh.call(t, ctx, ReadResultToolName, map[string]any{"result_id": om.ResultID, "jsonpath": "$[500:510]"})
	require.False(t, p.IsError, "%+v", p)
	var page []struct {
		Number int `json:"number"`
	}
	require.NoError(t, json.Unmarshal([]byte(p.Content[0].Text), &page))
	require.Len(t, page, 10)
	for i, it := range page {
		require.Equal(t, 500+i, it.Number)
	}
	require.Contains(t, p.Content[1].Text, "10 values matched")

	// jsonpath on a text result is refused clearly.
	_, tr := gh.invoke(t, ctx, "read_file")
	_, p = gh.call(t, ctx, ReadResultToolName, map[string]any{"result_id": markerID(t, tr.Content[0].Text), "jsonpath": "$"})
	require.True(t, p.IsError)
	require.Contains(t, p.Content[0].Text, "needs a JSON result")
}

func TestGovernor_GrepReturnsNumberedLines(t *testing.T) {
	gh := newGovHarness(t, enabledCfg(), nil)
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()
	_, r := gh.invoke(t, ctx, "read_file")
	id := markerID(t, r.Content[0].Text)
	_, p := gh.call(t, ctx, ReadResultToolName, map[string]any{"result_id": id, "grep": "^line 0170[0-1]"})
	require.False(t, p.IsError)
	require.Contains(t, p.Content[0].Text, "1700:line 01700: token=[SECRET_REDACTED]")
	require.Contains(t, p.Content[0].Text, "1701:line 01701:")
	require.Contains(t, p.Content[0].Text, "1698-line 01698:")
	require.Contains(t, p.Content[0].Text, "1703-line 01703:")
	require.NotContains(t, p.Content[0].Text, govSecret)
	require.Contains(t, p.Content[1].Text, "2 matching lines")

	_, p = gh.call(t, ctx, ReadResultToolName, map[string]any{"result_id": id, "grep": "("})
	require.True(t, p.IsError)
	require.Contains(t, p.Content[0].Text, "invalid grep pattern")
}

func TestGovernor_ErrorsImagesAndSmallResultsUntouched(t *testing.T) {
	gh := newGovHarness(t, enabledCfg(), nil)
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()
	for _, tool := range []string{"fail_big", "screenshot", "small"} {
		resp, _ := gh.invoke(t, ctx, tool)
		want := gh.upstream.result(tool)
		redacted := &Response{Result: want}
		require.NoError(t, gh.fw.Redaction.RedactResponse(redacted))
		require.JSONEq(t, string(redacted.Result), string(resp.Result), tool)
	}
	require.EqualValues(t, 0, gh.gov.Stats().Truncated)
}

func TestGovernor_PassthroughAndZeroBudgetRules(t *testing.T) {
	gh := newGovHarness(t, enabledCfg(func(c *governor.Config) {
		c.Tools = []governor.ToolRule{
			{Match: "fs.raw_read", Passthrough: true},
			{Match: "fs.list_*", MaxTokens: intPtr(0)},
			{Match: "fs.read_file", MaxTokens: intPtr(1000)},
		}
	}), nil)
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()
	_, r := gh.invoke(t, ctx, "raw_read")
	require.Greater(t, len(r.Content[0].Text), 190_000)
	_, r = gh.invoke(t, ctx, "list_issues")
	require.Greater(t, len(r.Content[0].Text), 40_000)
	_, r = gh.invoke(t, ctx, "read_file")
	require.LessOrEqual(t, contentTokens(r), 1000)
}

func TestGovernor_UnknownExpiredAndForeignIDsRefused(t *testing.T) {
	gh := newGovHarness(t, enabledCfg(func(c *governor.Config) { c.Spill.TTL = "300ms" }), nil)
	alice, doneA := gh.session(t, ProtocolVersion20250618)
	defer doneA()
	bob, doneB := gh.session(t, ProtocolVersion20250618)
	defer doneB()

	_, r := gh.invoke(t, alice, "read_file")
	id := markerID(t, r.Content[0].Text)

	_, p := gh.call(t, bob, ReadResultToolName, map[string]any{"result_id": id})
	require.True(t, p.IsError, "another session's id must be refused")
	require.Contains(t, p.Content[0].Text, "not found")
	resp, err := gh.h.HandleRequest(bob, &Request{JSONRPC: JSONRPCVersion, Method: MethodResourcesRead, ID: 3,
		Params: mustJSON(t, map[string]string{"uri": ResultURI(id)})})
	require.NoError(t, err)
	require.NotNil(t, resp.Error)
	require.Equal(t, ErrCodeResourceNotFound, resp.Error.Code)

	for _, bad := range []any{"r_aaaaaaaaaaaaaaaaaaaaaaaaaa", "../../etc/passwd", ""} {
		_, p = gh.call(t, alice, ReadResultToolName, map[string]any{"result_id": bad})
		require.True(t, p.IsError, "%v", bad)
	}
	_, p = gh.call(t, alice, ReadResultToolName, map[string]any{"result_id": id, "offset": 10_000_000})
	require.True(t, p.IsError)

	time.Sleep(400 * time.Millisecond)
	_, p = gh.call(t, alice, ReadResultToolName, map[string]any{"result_id": id})
	require.True(t, p.IsError, "an expired id must be refused")
	require.Contains(t, p.Content[0].Text, "expired")
}

func TestGovernor_SessionEndDropsResults(t *testing.T) {
	gh := newGovHarness(t, enabledCfg(), nil)
	ctx, done := gh.session(t, ProtocolVersion20250618)
	gh.invoke(t, ctx, "read_file")
	gh.invoke(t, ctx, "list_issues")
	require.Equal(t, 2, gh.gov.Stats().Store.Entries)
	done()
	require.Equal(t, 0, gh.gov.Stats().Store.Entries)
}

func TestGovernor_OlderProtocolGetsNoResourceLink(t *testing.T) {
	gh := newGovHarness(t, enabledCfg(), nil)
	s, done := gh.h.OpenSession(nil)
	defer done()
	ctx := WithClientSession(context.Background(), s)
	resp, err := gh.h.HandleRequest(ctx, &Request{JSONRPC: JSONRPCVersion, Method: MethodInitialize, ID: 0,
		Params: json.RawMessage(`{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}`)})
	require.NoError(t, err)
	require.Contains(t, string(resp.Result), `"resources":{}`, "resources/read serves spilled results")

	_, r := gh.invoke(t, ctx, "read_file")
	require.Len(t, r.Content, 1, "resource_link needs 2025-06-18")
	require.Contains(t, r.Content[0].Text, "call read_result with id=")

	resp, err = gh.h.HandleRequest(ctx, &Request{JSONRPC: JSONRPCVersion, Method: MethodToolsList, ID: 2})
	require.NoError(t, err)
	var tl ToolsListResult
	require.NoError(t, json.Unmarshal(resp.Result, &tl))
	last := tl.Tools[len(tl.Tools)-1]
	require.Equal(t, ReadResultToolName, last.Name)
	require.NotNil(t, last.Annotations)
	require.True(t, *last.Annotations.ReadOnlyHint)
}

func TestGovernor_StructuredContentStaysValidJSON(t *testing.T) {
	gh := newGovHarness(t, enabledCfg(), nil)
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()
	_, r := gh.invoke(t, ctx, "structured")
	require.True(t, json.Valid(r.StructuredContent))
	require.True(t, json.Valid([]byte(r.Content[0].Text)))
	require.LessOrEqual(t, governor.Tokens(len(r.StructuredContent)+len(r.Content[0].Text)), 4000)
	require.Contains(t, string(r.StructuredContent), governor.OmittedKey)
	links := 0
	for _, c := range r.Content {
		if c.Type == "resource_link" {
			links++
		}
	}
	require.Equal(t, 2, links, "one spilled copy per shortened unit")
}

func TestGovernor_StoreTooSmallPassesThrough(t *testing.T) {
	gh := newGovHarness(t, enabledCfg(func(c *governor.Config) { c.Spill.MaxBytes = 1024 }), nil)
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()
	_, r := gh.invoke(t, ctx, "read_file")
	require.Greater(t, len(r.Content[0].Text), 190_000, "never cut without a retrievable copy")
}

func TestGovernor_CachedResponsesAreGovernedPerSession(t *testing.T) {
	gh := newGovHarness(t, enabledCfg(), &responsecache.Config{Enabled: true, Tools: []string{"fs.read_file"}})
	alice, doneA := gh.session(t, ProtocolVersion20250618)
	defer doneA()
	bob, doneB := gh.session(t, ProtocolVersion20250618)
	defer doneB()
	_, ra := gh.invoke(t, alice, "read_file")
	_, rb := gh.invoke(t, bob, "read_file")
	require.EqualValues(t, 1, gh.upstream.calls.Load(), "the second call is a cache hit")
	idA, idB := markerID(t, ra.Content[0].Text), markerID(t, rb.Content[0].Text)
	require.NotEqual(t, idA, idB)
	require.Equal(t, strings.ReplaceAll(ra.Content[0].Text, idA, "ID"), strings.ReplaceAll(rb.Content[0].Text, idB, "ID"),
		"a hit is governed exactly like the miss")
	_, p := gh.call(t, bob, ReadResultToolName, map[string]any{"result_id": idB})
	require.False(t, p.IsError)
	_, p = gh.call(t, bob, ReadResultToolName, map[string]any{"result_id": idA})
	require.True(t, p.IsError)
}

func TestGovernor_ServeMethodForms(t *testing.T) {
	gh := newGovHarness(t, enabledCfg(), nil)
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()
	_, r := gh.invoke(t, ctx, "read_file")
	id := markerID(t, r.Content[0].Text)
	// serve's gateway-method form: method read_result, arguments as params.
	resp, err := gh.h.HandleRequest(ctx, &Request{JSONRPC: JSONRPCVersion, Method: ReadResultToolName, ID: 9,
		Params: mustJSON(t, map[string]any{"result_id": id, "limit_tokens": 200})})
	require.NoError(t, err)
	var p govResult
	require.NoError(t, json.Unmarshal(resp.Result, &p))
	require.False(t, p.IsError)
	require.LessOrEqual(t, len(p.Content[0].Text), 800)
}

func TestGovernor_Concurrent(t *testing.T) {
	gh := newGovHarness(t, enabledCfg(), &responsecache.Config{Enabled: true, Tools: []string{"fs.list_issues"}})
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, done := gh.h.OpenSession(nil)
			defer done()
			ctx := WithClientSession(context.Background(), s)
			for i := 0; i < 5; i++ {
				tool := "read_file"
				if i%2 == 1 {
					tool = "list_issues"
				}
				resp, err := gh.h.HandleRequest(ctx, toolsCallRequest(t, i, "invoke_tool", map[string]any{"server": "fs", "tool": tool}))
				if err != nil || resp.Error != nil {
					t.Errorf("call: %v %+v", err, resp)
					return
				}
				text := string(resp.Result)
				i := strings.Index(text, "r_")
				if i < 0 {
					t.Errorf("no id in %.200s", text)
					return
				}
				id := text[i : i+28]
				rr, err := gh.h.HandleRequest(ctx, toolsCallRequest(t, 99, ReadResultToolName, map[string]any{"result_id": id, "grep": "fox|Issue 9"}))
				if err != nil || strings.Contains(string(rr.Result), `"isError":true`) {
					t.Errorf("read_result: %v %.200s", err, rr.Result)
				}
				_ = gh.gov.Stats()
			}
		}()
	}
	wg.Wait()
	require.Equal(t, 0, gh.gov.Stats().Store.Entries, "every session ended")
}

func TestGovernor_AccountingAndTelemetry(t *testing.T) {
	before := TelemetrySnapshot()
	gh := newGovHarness(t, enabledCfg(), nil)
	ctx, done := gh.session(t, ProtocolVersion20250618)
	defer done()
	gh.invoke(t, ctx, "read_file")
	gh.invoke(t, ctx, "small")
	st := gh.gov.Stats()
	require.EqualValues(t, 2, st.Results)
	require.EqualValues(t, 1, st.Truncated)
	require.Len(t, st.ByTool, 2)
	for _, bt := range st.ByTool {
		switch bt.Tool {
		case "fs.read_file":
			require.Less(t, bt.ReturnedTokens, bt.OriginalTokens)
		case "fs.small":
			require.Equal(t, bt.ReturnedTokens, bt.OriginalTokens)
		default:
			t.Fatalf("unexpected tool %q", bt.Tool)
		}
	}
	after := TelemetrySnapshot()
	require.GreaterOrEqual(t, after.GovernedResults-before.GovernedResults, int64(2))
	require.GreaterOrEqual(t, after.GovernorTruncations-before.GovernorTruncations, int64(1))
	require.Greater(t, after.GovernorTokensSaved, before.GovernorTokensSaved)
	require.Contains(t, gh.gov.Summary(), "max_tokens 4000")
}
