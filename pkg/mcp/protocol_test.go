package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mmornati/leanproxy-mcp/pkg/errors"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
)

// fakeUpstream is one scripted upstream MCP server of fakeUpstreamPool.
type fakeUpstream struct {
	caps      string // raw initialize capabilities
	hang      bool   // never answer initialize
	pageSize  int    // list page size (0: everything in one page)
	endless   bool   // every list page has a nextCursor
	fail      map[string]bool
	tools     []json.RawMessage
	resources []json.RawMessage
	templates []json.RawMessage
	prompts   []json.RawMessage
	// results answers other methods (tools/call, resources/read, ...).
	results map[string]json.RawMessage
}

// fakeUpstreamPool is a pool.ServerSource (plus SessionInfoProvider and
// ServerEventSource) over scripted upstreams. It records every request.
type fakeUpstreamPool struct {
	mu        sync.Mutex
	servers   map[string]*fakeUpstream
	sessions  map[string]*pool.InitializeResult
	requests  []fakeRequest
	onEvent   pool.ServerEventHandler
	stateOver map[string]pool.ServerState
}

type fakeRequest struct {
	server, method string
	params         json.RawMessage
}

func newFakeUpstreamPool(servers map[string]*fakeUpstream) *fakeUpstreamPool {
	return &fakeUpstreamPool{servers: servers, sessions: map[string]*pool.InitializeResult{}, stateOver: map[string]pool.ServerState{}}
}

func (p *fakeUpstreamPool) record(server, method string, params json.RawMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, fakeRequest{server, method, append(json.RawMessage(nil), params...)})
}

// requestsFor returns the recorded requests of one server and method.
func (p *fakeUpstreamPool) requestsFor(server, method string) []fakeRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []fakeRequest
	for _, r := range p.requests {
		if r.server == server && r.method == method {
			out = append(out, r)
		}
	}
	return out
}

func (p *fakeUpstreamPool) SendRequestToServer(ctx context.Context, name, method string, params json.RawMessage, _ time.Duration) (*pool.Response, error) {
	p.record(name, method, params)
	u, ok := p.servers[name]
	if !ok {
		return nil, fmt.Errorf("server %s not found", name)
	}
	if u.fail[method] {
		return &pool.Response{Error: &errors.JSONRPCError{Code: -32000, Message: method + " exploded"}}, nil
	}
	switch method {
	case MethodInitialize:
		if u.hang {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		raw := json.RawMessage(`{"protocolVersion":"2025-06-18","capabilities":` + u.caps + `,"serverInfo":{"name":"` + name + `","version":"1"}}`)
		p.mu.Lock()
		p.sessions[name] = &pool.InitializeResult{ProtocolVersion: "2025-06-18", Capabilities: json.RawMessage(u.caps), Generation: 1, Raw: raw}
		p.mu.Unlock()
		return &pool.Response{Result: raw}, nil
	case MethodToolsList:
		return u.page(params, "tools", u.tools), nil
	case MethodResourcesList:
		return u.page(params, "resources", u.resources), nil
	case MethodResourcesTemplatesList:
		return u.page(params, "resourceTemplates", u.templates), nil
	case MethodPromptsList:
		return u.page(params, "prompts", u.prompts), nil
	}
	if r, ok := u.results[method]; ok {
		return &pool.Response{Result: r}, nil
	}
	return &pool.Response{Error: &errors.JSONRPCError{Code: ErrCodeMethodNotFound, Message: "method not found: " + method}}, nil
}

// page answers one page of a list, with cursors being item offsets.
func (u *fakeUpstream) page(params json.RawMessage, key string, items []json.RawMessage) *pool.Response {
	var p struct {
		Cursor string `json:"cursor"`
	}
	_ = json.Unmarshal(params, &p)
	start, _ := strconv.Atoi(p.Cursor)
	requested := start
	if items == nil {
		items = []json.RawMessage{}
	}
	end := len(items)
	if u.pageSize > 0 && start+u.pageSize < end {
		end = start + u.pageSize
	}
	if start > len(items) {
		start = len(items)
	}
	body := map[string]interface{}{key: items[start:end]}
	if end < len(items) {
		body["nextCursor"] = strconv.Itoa(end)
	} else if u.endless {
		body["nextCursor"] = strconv.Itoa(requested + 1)
	}
	raw, _ := json.Marshal(body)
	return &pool.Response{Result: raw}
}

func (p *fakeUpstreamPool) SendRequestToServerWithID(ctx context.Context, name, method string, params json.RawMessage, timeout time.Duration, _ int) (*pool.Response, error) {
	return p.SendRequestToServer(ctx, name, method, params, timeout)
}

func (p *fakeUpstreamPool) SendServerNotification(context.Context, string, string, map[string]interface{}) error {
	return nil
}

func (p *fakeUpstreamPool) ListServers() []string {
	names := make([]string, 0, len(p.servers))
	for n := range p.servers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (p *fakeUpstreamPool) GetServerState(name string) (pool.ServerState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.stateOver[name]; ok {
		return s, nil
	}
	return pool.StateRunning, nil
}

func (p *fakeUpstreamPool) GetServerTransport(string) (string, error)   { return "stdio", nil }
func (p *fakeUpstreamPool) RestartServer(context.Context, string) error { return nil }
func (p *fakeUpstreamPool) Close() error                                { return nil }

func (p *fakeUpstreamPool) ServerInitializeResult(name string) (*pool.InitializeResult, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	res, ok := p.sessions[name]
	return res, ok
}

func (p *fakeUpstreamPool) SetServerEventHandler(fn pool.ServerEventHandler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onEvent = fn
}

var (
	_ pool.ServerSource        = (*fakeUpstreamPool)(nil)
	_ pool.SessionInfoProvider = (*fakeUpstreamPool)(nil)
	_ pool.ServerEventSource   = (*fakeUpstreamPool)(nil)
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// capturedNotifications records the notifications written to one session.
type capturedNotifications struct {
	mu      sync.Mutex
	methods []string
}

func (c *capturedNotifications) notify(method string, _ json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.methods = append(c.methods, method)
}

func (c *capturedNotifications) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.methods...)
}

// call runs one request through h with session s in the context.
func call(t *testing.T, h *Handler, s *ClientSession, method string, params interface{}) *Response {
	t.Helper()
	var raw json.RawMessage
	if params != nil {
		var err error
		raw, err = json.Marshal(params)
		require.NoError(t, err)
	}
	ctx := context.Background()
	if s != nil {
		ctx = WithClientSession(ctx, s)
	}
	resp, err := h.HandleRequest(ctx, &Request{JSONRPC: JSONRPCVersion, ID: 1, Method: method, Params: raw})
	require.NoError(t, err)
	require.NotNil(t, resp)
	return resp
}

func initialize(t *testing.T, h *Handler, s *ClientSession, version string) InitializeResult {
	t.Helper()
	resp := call(t, h, s, MethodInitialize, map[string]interface{}{
		"protocolVersion": version,
		"capabilities":    map[string]interface{}{"roots": map[string]interface{}{}},
		"clientInfo":      map[string]string{"name": "test", "version": "1"},
	})
	require.Nil(t, resp.Error)
	var res InitializeResult
	require.NoError(t, json.Unmarshal(resp.Result, &res))
	return res
}

func TestNegotiateProtocolVersion(t *testing.T) {
	for _, v := range SupportedProtocolVersions {
		assert.Equal(t, v, NegotiateProtocolVersion(v), "a supported version is echoed")
	}
	assert.Equal(t, LatestProtocolVersion, NegotiateProtocolVersion(""))
	assert.Equal(t, LatestProtocolVersion, NegotiateProtocolVersion("2099-01-01"))
	assert.Equal(t, LatestProtocolVersion, NegotiateProtocolVersion("1.0"))

	assert.True(t, ProtocolAtLeast("2025-06-18", ProtocolVersion20250326))
	assert.True(t, ProtocolAtLeast("2025-06-18", ProtocolVersion20250618))
	assert.False(t, ProtocolAtLeast("2025-03-26", ProtocolVersion20250618))
	assert.False(t, ProtocolAtLeast("", ProtocolVersion20250326), "no initialize counts as the oldest revision")
}

// TestProtocolVersionsAreConsistent pins the single list of supported
// versions to the pool's handshake and to mcp-go's list.
func TestProtocolVersionsAreConsistent(t *testing.T) {
	assert.Equal(t, LatestProtocolVersion, SupportedProtocolVersions[0], "newest first")
	assert.Equal(t, LatestProtocolVersion, pool.RequestedProtocolVersion, "the pool must request the latest version")
	for _, v := range SupportedProtocolVersions {
		assert.Contains(t, mcpgo.ValidProtocolVersions, v, "mcp-go (HTTP/SSE upstreams) must accept %s", v)
	}
	for i := 1; i < len(SupportedProtocolVersions); i++ {
		assert.Greater(t, SupportedProtocolVersions[i-1], SupportedProtocolVersions[i])
	}
}

func TestInitialize_NegotiatesPerSession(t *testing.T) {
	h := NewHandler(newFakeUpstreamPool(map[string]*fakeUpstream{}), quietLogger())
	modern, closeModern := h.OpenSession(nil)
	defer closeModern()
	legacy, closeLegacy := h.OpenSession(nil)
	defer closeLegacy()
	odd, closeOdd := h.OpenSession(nil)
	defer closeOdd()

	assert.Equal(t, "2025-06-18", initialize(t, h, modern, "2025-06-18").ProtocolVersion)
	assert.Equal(t, "2024-11-05", initialize(t, h, legacy, "2024-11-05").ProtocolVersion)
	assert.Equal(t, LatestProtocolVersion, initialize(t, h, odd, "2099-12-31").ProtocolVersion)

	assert.Equal(t, "2025-06-18", modern.ProtocolVersion())
	assert.Equal(t, "2024-11-05", legacy.ProtocolVersion())
	assert.Equal(t, LatestProtocolVersion, odd.ProtocolVersion())
	assert.Equal(t, "test", modern.ClientInfo().Name)
	assert.JSONEq(t, `{"roots":{}}`, string(modern.Capabilities()))

	// A 2024-11-05 client gets no field newer than its revision.
	legacyInit := call(t, h, legacy, MethodInitialize, map[string]string{"protocolVersion": "2024-11-05"})
	assert.NotContains(t, string(legacyInit.Result), `"title"`)
	legacyTools := call(t, h, legacy, MethodToolsList, nil)
	assert.NotContains(t, string(legacyTools.Result), "annotations")

	modernTools := call(t, h, modern, MethodToolsList, nil)
	assert.Contains(t, string(modernTools.Result), `"readOnlyHint":true`)
	var list ToolsListResult
	require.NoError(t, json.Unmarshal(modernTools.Result, &list))
	for _, tool := range list.Tools {
		if tool.Name == "invoke_tool" {
			assert.Nil(t, tool.Annotations, "invoke_tool is not read-only")
		} else {
			assert.True(t, tool.ReadOnly(), "%s is read-only", tool.Name)
		}
	}
	modernInit := call(t, h, modern, MethodInitialize, map[string]string{"protocolVersion": "2025-06-18"})
	assert.Contains(t, string(modernInit.Result), `"title":"LeanProxy"`)
}

func TestInitialize_AdvertisesUpstreamCapabilities(t *testing.T) {
	t.Run("only tools", func(t *testing.T) {
		h := NewHandler(newFakeUpstreamPool(map[string]*fakeUpstream{"a": {caps: `{"tools":{}}`}}), quietLogger())
		res := initialize(t, h, nil, "2025-06-18")
		assert.NotNil(t, res.Capabilities.Tools)
		assert.Nil(t, res.Capabilities.Resources)
		assert.Nil(t, res.Capabilities.Prompts)
	})
	t.Run("resources and prompts on different servers", func(t *testing.T) {
		h := NewHandler(newFakeUpstreamPool(map[string]*fakeUpstream{
			"a": {caps: `{"tools":{},"resources":{}}`},
			"b": {caps: `{"prompts":{"listChanged":false}}`},
		}), quietLogger())
		res := initialize(t, h, nil, "2025-06-18")
		require.NotNil(t, res.Capabilities.Resources)
		require.NotNil(t, res.Capabilities.Prompts)
		assert.True(t, res.Capabilities.Resources.ListChanged)
		assert.False(t, res.Capabilities.Resources.Subscribe, "updates are not relayed yet (#308)")
		assert.True(t, res.Capabilities.Prompts.ListChanged)
	})
	t.Run("a hung upstream does not hold initialize", func(t *testing.T) {
		h := NewHandler(newFakeUpstreamPool(map[string]*fakeUpstream{
			"hung": {hang: true},
			"ok":   {caps: `{"resources":{}}`},
		}), quietLogger())
		start := time.Now()
		res := initialize(t, h, nil, "2025-06-18")
		assert.Less(t, time.Since(start), initializeCapabilityWait+time.Second)
		assert.NotNil(t, res.Capabilities.Resources)
		assert.Nil(t, res.Capabilities.Prompts)
	})
	t.Run("a crashed stdio upstream is not restarted to probe it", func(t *testing.T) {
		fp := newFakeUpstreamPool(map[string]*fakeUpstream{"down": {caps: `{"resources":{}}`}})
		fp.stateOver["down"] = pool.StateError
		h := NewHandler(fp, quietLogger())
		res := initialize(t, h, nil, "2025-06-18")
		assert.Nil(t, res.Capabilities.Resources)
		assert.Empty(t, fp.requestsFor("down", MethodInitialize))
	})
}

func boolPtr(b bool) *bool { return &b }

func TestListAndSearchTools_AnnotationsAndStructuredContent(t *testing.T) {
	deleteRepo := json.RawMessage(`{"name":"delete_repo","title":"Delete repository","description":"Delete a repository forever","inputSchema":{"type":"object","properties":{"repo":{"type":"string"}},"required":["repo"]},"outputSchema":{"type":"object","properties":{"deleted":{"type":"boolean"}}},"annotations":{"destructiveHint":true,"idempotentHint":true},"icons":[{"src":"https://example.test/i.png","mimeType":"image/png","sizes":["48x48"],"theme":"dark"}],"_meta":{"ui/resourceUri":"ui://repo/delete"}}`)
	getRepo := json.RawMessage(`{"name":"get_repo","description":"Read repository metadata","inputSchema":{"type":"object"},"annotations":{"readOnlyHint":true}}`)
	plain := json.RawMessage(`{"name":"plain","description":"No hints","inputSchema":{"type":"object"}}`)
	fp := newFakeUpstreamPool(map[string]*fakeUpstream{"gh": {caps: `{"tools":{}}`, tools: []json.RawMessage{deleteRepo, getRepo, plain}}})
	h := NewHandler(fp, quietLogger())
	h.PopulateToolCache(context.Background())

	modern, closeModern := h.OpenSession(nil)
	defer closeModern()
	legacy, closeLegacy := h.OpenSession(nil)
	defer closeLegacy()
	initialize(t, h, modern, "2025-06-18")
	initialize(t, h, legacy, "2024-11-05")

	listTools := func(s *ClientSession) CallToolResult {
		resp := call(t, h, s, MethodToolsCall, map[string]interface{}{"name": "list_tools", "arguments": map[string]string{"server_name": "gh"}})
		require.Nil(t, resp.Error)
		var res CallToolResult
		require.NoError(t, json.Unmarshal(resp.Result, &res))
		return res
	}
	text := func(res CallToolResult) string {
		var block ContentBlock
		require.NoError(t, json.Unmarshal(res.Content[0], &block))
		return block.Text
	}

	legacyRes := listTools(legacy)
	assert.Contains(t, text(legacyRes), "gh_delete_repo [destructive]: Delete a repository forever [repo: string]")
	assert.Contains(t, text(legacyRes), "gh_get_repo [read-only]: Read repository metadata")
	assert.Contains(t, text(legacyRes), "gh_plain: No hints")
	assert.Empty(t, legacyRes.StructuredContent, "no structuredContent before 2025-06-18")

	modernRes := listTools(modern)
	assert.Equal(t, text(legacyRes), text(modernRes), "the text form is the same for every revision")
	require.NotEmpty(t, modernRes.StructuredContent)
	var structured struct {
		Tools []struct {
			Server string          `json:"server"`
			Tool   json.RawMessage `json:"tool"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(modernRes.StructuredContent, &structured))
	require.Len(t, structured.Tools, 3)
	assert.Equal(t, "gh", structured.Tools[0].Server)
	assert.JSONEq(t, string(deleteRepo), string(structured.Tools[0].Tool), "the full upstream tool object is relayed")

	search := func(s *ClientSession) CallToolResult {
		resp := call(t, h, s, MethodToolsCall, map[string]interface{}{"name": "search_tools", "arguments": map[string]string{"query": "delete repository"}})
		require.Nil(t, resp.Error)
		var res CallToolResult
		require.NoError(t, json.Unmarshal(resp.Result, &res))
		return res
	}
	legacySearch := search(legacy)
	assert.Contains(t, text(legacySearch), "gh_delete_repo [destructive]:")
	assert.Empty(t, legacySearch.StructuredContent)
	modernSearch := search(modern)
	require.NoError(t, json.Unmarshal(modernSearch.StructuredContent, &structured))
	require.NotEmpty(t, structured.Tools)
	assert.JSONEq(t, string(deleteRepo), string(structured.Tools[0].Tool))
}

func TestTool_Hints(t *testing.T) {
	assert.False(t, Tool{}.ReadOnly())
	assert.False(t, Tool{}.Destructive(), "the implicit default is not flagged")
	assert.True(t, Tool{Annotations: &ToolAnnotations{ReadOnlyHint: boolPtr(true)}}.ReadOnly())
	assert.True(t, Tool{Annotations: &ToolAnnotations{DestructiveHint: boolPtr(true)}}.Destructive())
	assert.False(t, Tool{Annotations: &ToolAnnotations{ReadOnlyHint: boolPtr(true), DestructiveHint: boolPtr(true)}}.Destructive(),
		"destructiveHint is meaningless on a read-only tool")
	assert.Equal(t, "", annotationTags(Tool{Annotations: &ToolAnnotations{DestructiveHint: boolPtr(false)}}))
}

func TestInvokeTool_RelaysRichResultUnchanged(t *testing.T) {
	result := json.RawMessage(`{"content":[{"type":"text","text":"{\"n\":12345678901234567}"},{"type":"resource_link","uri":"file:///repo/README.md","name":"README","mimeType":"text/markdown"},{"type":"image","data":"aGk=","mimeType":"image/png"}],"structuredContent":{"n":12345678901234567,"ok":true},"isError":false,"_meta":{"trace":"x"}}`)
	fp := newFakeUpstreamPool(map[string]*fakeUpstream{"gh": {caps: `{"tools":{}}`, results: map[string]json.RawMessage{MethodToolsCall: result}}})
	h := NewHandler(fp, quietLogger())

	for _, v := range []string{ProtocolVersion20241105, ProtocolVersion20250618} {
		s, closeS := h.OpenSession(nil)
		initialize(t, h, s, v)
		resp := call(t, h, s, MethodToolsCall, map[string]interface{}{"name": "invoke_tool", "arguments": map[string]interface{}{"server": "gh", "tool": "t", "arguments": map[string]int{"a": 1}}})
		closeS()
		require.Nil(t, resp.Error)
		assert.Equal(t, string(result), string(resp.Result), "relayed byte for byte (%s)", v)

		var decoded CallToolResult
		require.NoError(t, json.Unmarshal(resp.Result, &decoded))
		require.Len(t, decoded.Content, 3)
		assert.Equal(t, ContentTypeText, ContentItemType(decoded.Content[0]))
		assert.Equal(t, ContentTypeResourceLink, ContentItemType(decoded.Content[1]))
		assert.Equal(t, ContentTypeImage, ContentItemType(decoded.Content[2]))
		assert.JSONEq(t, `{"n":12345678901234567,"ok":true}`, string(decoded.StructuredContent))
	}
}

func TestToolCachePersistence_KeepsMetadata(t *testing.T) {
	tools := []Tool{
		{
			Name: "a", Title: "A", Description: "d", InputSchema: json.RawMessage(`{"type":"object"}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`),
			Annotations:  &ToolAnnotations{ReadOnlyHint: boolPtr(true), Title: "A tool"},
			Icons:        []Icon{{Src: "data:image/png;base64,AA==", Sizes: []string{"any"}, Theme: "light"}},
			Meta:         json.RawMessage(`{"k":1}`),
		},
		{Name: "b", Description: "plain", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
	back := cachedToolsToTools(toolsToCachedTools(tools))
	assert.Equal(t, tools, back)
}

func TestFetchServerTools_FollowsPagination(t *testing.T) {
	tools := make([]json.RawMessage, 0, 7)
	for i := 0; i < 7; i++ {
		tools = append(tools, json.RawMessage(fmt.Sprintf(`{"name":"t%d","description":"","inputSchema":{"type":"object"}}`, i)))
	}
	fp := newFakeUpstreamPool(map[string]*fakeUpstream{"s": {caps: `{"tools":{}}`, tools: tools, pageSize: 3}})
	h := NewHandler(fp, quietLogger())
	require.NoError(t, h.RefreshServerTools(context.Background(), "s"))
	assert.Len(t, h.CachedTools()["s"], 7)
	assert.Len(t, fp.requestsFor("s", MethodToolsList), 3)
}

func TestListChangedNotifications(t *testing.T) {
	fp := newFakeUpstreamPool(map[string]*fakeUpstream{
		"r": {caps: `{"resources":{"listChanged":true}}`},
		"p": {caps: `{"prompts":{}}`},
	})
	h := NewHandler(fp, quietLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.StartBackgroundRefresh(ctx)

	ready := &capturedNotifications{}
	s, closeS := h.OpenSession(ready.notify)
	notReady := &capturedNotifications{}
	_, closePending := h.OpenSession(notReady.notify)
	defer closePending()
	initialize(t, h, s, ProtocolVersion20250618)

	h.handleServerEvent(pool.ServerEvent{Server: "r", Kind: pool.EventResourcesListChanged, Generation: 1})
	h.handleServerEvent(pool.ServerEvent{Server: "p", Kind: pool.EventPromptsListChanged, Generation: 1})
	assert.Equal(t, []string{NotificationResourcesListChanged, NotificationPromptsListChanged}, ready.snapshot())

	// A restarted upstream (second session) may serve different lists.
	h.handleServerEvent(pool.ServerEvent{Server: "r", Kind: pool.EventSessionStarted, Generation: 2})
	h.handleServerEvent(pool.ServerEvent{Server: "p", Kind: pool.EventSessionStarted, Generation: 1})
	assert.Equal(t, []string{NotificationResourcesListChanged, NotificationPromptsListChanged, NotificationResourcesListChanged}, ready.snapshot())

	assert.Empty(t, notReady.snapshot(), "no notification before initialize")

	closeS()
	h.handleServerEvent(pool.ServerEvent{Server: "r", Kind: pool.EventResourcesListChanged, Generation: 2})
	assert.Len(t, ready.snapshot(), 3, "a closed session gets nothing")
	assert.True(t, strings.HasPrefix(NotificationResourcesListChanged, "notifications/"))
}
