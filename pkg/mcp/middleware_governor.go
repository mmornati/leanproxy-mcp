package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp/governor"
)

// Response token governor, part 1 (issue #319): smart truncation of large
// tool results and spill-to-resource with paged retrieval.
//
// # Pipeline placement
//
// The governor sits right inside the telemetry span, OUTSIDE tool pinning,
// the per-tool policy, the response cache and the Token Firewall:
//
//	telemetry → governor → tool_pinning → policy → cache → redact_response → redact_request → injection → dispatch
//
// so that:
//
//   - it only ever sees responses that were redacted (redact_response) and
//     scanned for prompt injection (injection) already: what it spills and
//     serves back through read_result is exactly what the client would
//     have received in full;
//   - the response cache stores the full (redacted) response and every
//     answer, hit or miss, is governed the same way for the session that
//     asked. A cached, governed answer can therefore never carry a
//     result_id of another session;
//   - read_result and resources/read of leanproxy://results/<id> are
//     answered from the spill store before any other stage: they only
//     return data that already went through all of them.
//
// Later stories hook in here: field projection (#320) runs on the same
// parsed result before the budget is applied (see shortenResult), and
// in-session dedup (#321) reuses the spill store and read_result.
type Governor struct {
	cfg   *governor.Config
	store *governor.Store

	handler atomic.Pointer[Handler]
	servers atomic.Pointer[func() []string]

	stopJanitor chan struct{}
	closeOnce   sync.Once

	statsMu sync.Mutex
	stats   governorCounters
	byTool  map[string]*GovernorToolStats
}

// ReadResultToolName is the gateway tool that serves spilled results.
const ReadResultToolName = "read_result"

// ResultURIPrefix starts the resource URI of a spilled result:
// leanproxy://results/<result_id>.
const ResultURIPrefix = resourceURIPrefix + "results/"

// ResultURI is the resource URI of a spilled result.
func ResultURI(id string) string { return ResultURIPrefix + id }

// maxReadTokens caps read_result's limit_tokens.
const maxReadTokens = 50000

// maxGovernorTools caps the per-tool accounting entries; further tools are
// counted under "other" (tool names come from clients).
const maxGovernorTools = 1000

// governorJanitorInterval is how often expired results are swept.
const governorJanitorInterval = time.Minute

// NewGovernor builds the governor from the `response:` block. A nil cfg or
// one with enabled: false gives a disabled governor whose Middleware only
// calls next. A disk spill store that cannot be created falls back to
// memory (with a warning).
func NewGovernor(cfg *governor.Config) *Governor {
	g := &Governor{cfg: cfg, byTool: make(map[string]*GovernorToolStats)}
	if cfg == nil || !cfg.Enabled {
		return g
	}
	if err := cfg.Validate(); err != nil {
		slog.Warn("response governor: invalid config, disabling", "error", err)
		g.cfg = nil
		return g
	}
	if cfg.Spill.Disk {
		home, _ := os.UserHomeDir()
		store, err := governor.NewDiskStore(cfg.DirValue(home), cfg.TTLValue(), cfg.MaxBytesValue())
		if err != nil {
			slog.Warn("response governor: spill directory unavailable, keeping results in memory", "error", err)
		} else {
			g.store = store
		}
	}
	if g.store == nil {
		g.store = governor.NewMemoryStore(cfg.TTLValue(), cfg.MaxBytesValue())
	}
	g.stopJanitor = make(chan struct{})
	go g.janitor()
	return g
}

// Enabled reports whether the governor is on.
func (g *Governor) Enabled() bool {
	return g != nil && g.cfg != nil && g.cfg.Enabled && g.store != nil
}

// Summary is the one-line startup status.
func (g *Governor) Summary() string {
	if !g.Enabled() {
		return "response governor disabled"
	}
	where := "memory"
	if d := g.store.Dir(); d != "" {
		where = "disk (" + d + ")"
	}
	return fmt.Sprintf("response governor enabled: max_tokens %d, %d tool rules, spill to %s, ttl %s",
		g.cfg.GlobalMaxTokens(), len(g.cfg.Tools), where, g.store.TTL())
}

// SetServerNames tells the governor which servers exist, to split
// namespaced tool names. Without it the handler's servers are used.
func (g *Governor) SetServerNames(fn func() []string) {
	if g != nil && fn != nil {
		g.servers.Store(&fn)
	}
}

// SetGovernor gives the governor the handler whose sessions own the
// spilled results (their results are dropped when they end) and whose
// servers it splits tool names against.
func (h *Handler) SetGovernor(g *Governor) {
	if g == nil || !g.Enabled() {
		return
	}
	g.handler.Store(h)
	h.OnSessionClose(g.DropSession)
}

// DropSession removes every result spilled for s.
func (g *Governor) DropSession(s *ClientSession) {
	if g.Enabled() && s != nil {
		g.store.DropOwner(s)
	}
}

// Close stops the janitor and removes every spilled result (and, with
// spill.disk, the per-process directory). Called on shutdown.
func (g *Governor) Close() {
	if !g.Enabled() {
		return
	}
	g.closeOnce.Do(func() {
		close(g.stopJanitor)
		g.store.Close()
	})
}

func (g *Governor) janitor() {
	t := time.NewTicker(governorJanitorInterval)
	defer t.Stop()
	for {
		select {
		case <-g.stopJanitor:
			return
		case <-t.C:
			g.store.Sweep()
		}
	}
}

func (g *Governor) serverNames() []string {
	if fn := g.servers.Load(); fn != nil {
		return (*fn)()
	}
	if h := g.handler.Load(); h != nil {
		return h.pool.ListServers()
	}
	return nil
}

// session returns the client session of the request (the handler's
// default session when the context carries none). It owns what the
// request spills.
func (g *Governor) session(ctx context.Context) *ClientSession {
	if s := ClientSessionFrom(ctx); s != nil {
		return s
	}
	if h := g.handler.Load(); h != nil {
		return h.defaultSession
	}
	return nil
}

// owner is the spill-store owner key of a session. Requests without any
// session share one anonymous owner.
func owner(s *ClientSession) any {
	if s == nil {
		return anonymousOwner
	}
	return s
}

var anonymousOwner = new(struct{ _ byte })

// Middleware returns the governor stage. See the Governor doc comment for
// where it must sit in the pipeline.
func (g *Governor) Middleware() Middleware {
	return func(next Next) Next {
		return func(ctx context.Context, req *Request) (*Response, error) {
			if !g.Enabled() || req == nil || req.IsNotification() {
				return next(ctx, req)
			}
			if args, ok := readResultArgs(req); ok {
				return g.readResult(ctx, req, args), nil
			}
			if req.Method == MethodResourcesRead {
				if resp, ok := g.readResultResource(ctx, req); ok {
					return resp, nil
				}
			}

			resp, err := next(ctx, req)
			if resp == nil || resp.Error != nil || len(resp.Result) == 0 {
				return resp, err
			}
			switch req.Method {
			case MethodInitialize:
				resp.Result = withResourcesCapability(resp.Result)
			case MethodToolsList:
				resp.Result = g.withReadResultTool(ctx, resp.Result)
			default:
				if server, tool, _, _, ok := callTarget(req, g.serverNames); ok {
					g.govern(ctx, server, tool, resp)
				}
			}
			return resp, err
		}
	}
}

// govern applies the tool's budget to a tools/call response in place.
func (g *Governor) govern(ctx context.Context, server, tool string, resp *Response) {
	identity := cacheIdentity(server, tool)
	budget := g.cfg.BudgetFor(identity)
	before := len(resp.Result)
	if !budget.Limited() || before <= budget.MaxTokens*governor.BytesPerToken {
		// Fast path: the whole result fits (or the tool is not governed);
		// nothing is parsed.
		g.account(ctx, server, identity, before, before, 0, 0)
		return
	}
	out, spilled, err := g.shortenResult(ctx, server, tool, resp.Result, budget.MaxTokens*governor.BytesPerToken)
	if err != nil {
		slog.Warn("response governor: result passed through unchanged", "tool", identity, "error", err)
	}
	if out == nil {
		g.account(ctx, server, identity, before, before, 0, 0)
		return
	}
	resp.Result = out
	g.account(ctx, server, identity, before, len(out), 1, spilled)
}

// resultUnit is one piece of a tool result the budget applies to: a text
// content item or the structuredContent value.
type resultUnit struct {
	content int // index in content, or -1 for structuredContent
	data    []byte
	wire    int // bytes on the wire (a text item's string is JSON-escaped)
	kind    governor.Kind
	out     []byte // shortened data (nil: kept whole)
	id      string
}

// resourceLinkReserve is the budget kept, per shortened unit, for its
// resource_link content item.
const resourceLinkReserve = 256

// shortenResult shortens a tools/call result that is over budget bytes.
// It returns nil (keep the result as is) for an error result, a result
// with nothing to shorten, or when a spilled copy cannot be stored: a
// result is never cut without its full copy being retrievable.
//
// The result is valid JSON (the front end decoded it); it is split with
// the governor's scanner, never fully decoded, and every member the
// governor does not shorten is written back byte for byte, in order.
func (g *Governor) shortenResult(ctx context.Context, server, tool string, result json.RawMessage, budget int) (json.RawMessage, int, error) {
	body, err := governor.SplitObject(result)
	if err != nil {
		return nil, 0, nil
	}
	member := func(key string) int {
		for i, m := range body {
			if string(m.Key) == `"`+key+`"` {
				return i
			}
		}
		return -1
	}
	if i := member("isError"); i >= 0 && string(body[i].Value) == "true" {
		return nil, 0, nil // never shorten an error result
	}
	contentAt := member("content")
	var content [][]byte
	if contentAt >= 0 && string(body[contentAt].Value) != "null" {
		if content, err = governor.SplitArray(body[contentAt].Value); err != nil {
			return nil, 0, nil
		}
	}

	// Text items and structuredContent are budgeted; images, audio and
	// embedded resources pass through untouched.
	units := make([]*resultUnit, 0, len(content)+1)
	textAt := make(map[int]int) // content index → member index of "text"
	for i, item := range content {
		members, err := governor.SplitObject(item)
		if err != nil {
			continue
		}
		isText, textMember := false, -1
		for j, m := range members {
			switch string(m.Key) {
			case `"type"`:
				isText = string(m.Value) == `"text"`
			case `"text"`:
				textMember = j
			}
		}
		if !isText || textMember < 0 {
			continue
		}
		raw := members[textMember].Value
		var text string
		if json.Unmarshal(raw, &text) != nil {
			continue
		}
		kind := governor.KindText
		if governor.IsJSON(text) {
			kind = governor.KindJSON
		}
		textAt[i] = textMember
		units = append(units, &resultUnit{content: i, data: []byte(text), wire: len(raw) - 2, kind: kind})
	}
	structAt := member("structuredContent")
	if structAt >= 0 && string(body[structAt].Value) != "null" {
		v := body[structAt].Value
		units = append(units, &resultUnit{content: -1, data: v, wire: len(v), kind: governor.KindJSON})
	}
	sizes := make([]int, len(units))
	total := 0
	for i, u := range units {
		sizes[i] = u.wire
		total += u.wire
	}
	if total <= budget {
		return nil, 0, nil
	}

	sess := g.session(ctx)
	links := sess.AtLeast(ProtocolVersion20250618)
	// 5% of the budget covers the envelope; resource links get their own
	// reserve.
	avail := budget * 95 / 100
	if links {
		over := 0
		for _, s := range sizes {
			if s > avail/len(units) {
				over++
			}
		}
		avail -= over * resourceLinkReserve
	}
	alloc := governor.WaterFill(sizes, avail)

	// Field projection (#320) will run here, on units, before the budget.

	spilled := make([]*resultUnit, 0, len(units))
	for i, u := range units {
		if sizes[i] <= alloc[i] {
			continue
		}
		meta, err := g.store.Put(owner(sess), governor.Meta{Server: server, Tool: tool, Kind: u.kind}, u.data)
		if err != nil {
			// Units spilled before this one just expire with the TTL.
			return nil, 0, fmt.Errorf("spill: %w", err)
		}
		u.id = meta.ID
		// From wire bytes back to the unit's own bytes (escaping).
		u.out = shortenUnit(u, int(int64(alloc[i])*int64(len(u.data))/int64(max(u.wire, 1))))
		spilled = append(spilled, u)
	}
	if len(spilled) == 0 {
		return nil, 0, nil
	}

	for _, u := range spilled {
		if u.content < 0 {
			body[structAt].Value = u.out
			continue
		}
		item, err := replaceText(content[u.content], textAt[u.content], string(u.out))
		if err != nil {
			return nil, 0, err
		}
		content[u.content] = item
	}
	if links {
		for _, u := range spilled {
			link, err := resultLink(u, server, tool)
			if err != nil {
				return nil, 0, err
			}
			content = append(content, link)
		}
	}
	if contentAt >= 0 {
		body[contentAt].Value = joinArray(content)
	}
	out := joinObject(body)
	if !json.Valid(out) {
		return nil, 0, fmt.Errorf("shortened result is not valid JSON")
	}
	return out, len(spilled), nil
}

// joinArray writes raw elements as a JSON array.
func joinArray(elems [][]byte) []byte {
	n := 2
	for _, e := range elems {
		n += len(e) + 1
	}
	out := make([]byte, 0, n)
	out = append(out, '[')
	for i, e := range elems {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, e...)
	}
	return append(out, ']')
}

// joinObject writes members as a JSON object, in order.
func joinObject(members []governor.Member) []byte {
	n := 2
	for _, m := range members {
		n += len(m.Key) + len(m.Value) + 2
	}
	out := make([]byte, 0, n)
	out = append(out, '{')
	for i, m := range members {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, m.Key...)
		out = append(out, ':')
		out = append(out, m.Value...)
	}
	return append(out, '}')
}

// shortenUnit truncates one unit to at most budget bytes: structurally
// for JSON (the result always stays valid JSON), head + marker + tail for
// text, and a bare marker when the budget is too small for either.
func shortenUnit(u *resultUnit, budget int) []byte {
	if u.kind == governor.KindJSON {
		if out, ok := governor.TruncateValidJSON(u.data, budget, u.id); ok {
			return out
		}
		out, _ := governor.MarshalNoEscape(map[string]map[string]any{governor.OmittedKey: {"bytes": len(u.data), "result_id": u.id}})
		return out
	}
	if cut, ok := governor.TruncateText(string(u.data), budget, u.id); ok {
		return []byte(cut.Text)
	}
	return []byte(governor.TextMarker(governor.Tokens(len(u.data)), u.id, 0))
}

// replaceText returns a content item with the text of member textMember
// replaced; every other member is kept byte for byte.
func replaceText(item []byte, textMember int, text string) ([]byte, error) {
	members, err := governor.SplitObject(item)
	if err != nil {
		return nil, err
	}
	encoded, err := governor.MarshalNoEscape(text)
	if err != nil {
		return nil, err
	}
	members[textMember].Value = encoded
	return joinObject(members), nil
}

// resultLink is the resource_link content item (MCP 2025-06-18) pointing
// at a spilled result.
func resultLink(u *resultUnit, server, tool string) (json.RawMessage, error) {
	mime := "text/plain"
	if u.kind == governor.KindJSON {
		mime = "application/json"
	}
	what := "text content"
	if u.content < 0 {
		what = "structuredContent"
	}
	return governor.MarshalNoEscape(map[string]string{
		"type":        "resource_link",
		"uri":         ResultURI(u.id),
		"name":        u.id,
		"description": fmt.Sprintf("Full %s of %s (%d tokens), kept by LeanProxy for this session", what, cacheIdentity(server, tool), governor.Tokens(len(u.data))),
		"mimeType":    mime,
	})
}

// withResourcesCapability makes an initialize result advertise resources
// (resources/read serves spilled results), keeping the upstream-derived
// capability when there is one.
func withResourcesCapability(result json.RawMessage) json.RawMessage {
	var body map[string]json.RawMessage
	if json.Unmarshal(result, &body) != nil {
		return result
	}
	var caps map[string]json.RawMessage
	if json.Unmarshal(body["capabilities"], &caps) != nil || caps == nil {
		caps = map[string]json.RawMessage{}
	}
	if raw, ok := caps["resources"]; ok && string(raw) != "null" {
		return result
	}
	caps["resources"] = json.RawMessage(`{}`)
	encoded, err := governor.MarshalNoEscape(caps)
	if err != nil {
		return result
	}
	body["capabilities"] = encoded
	out, err := governor.MarshalNoEscape(body)
	if err != nil {
		return result
	}
	return out
}

// readResultTool is read_result's definition in tools/list (only listed
// when the governor is on, so the default tools/list budget is unchanged).
var readResultTool = ToolDefinition{
	Name:        ReadResultToolName,
	ReadOnly:    true,
	Description: "Read a result LeanProxy shortened: page it from offset, or search it with grep or jsonpath.",
	InputSchema: json.RawMessage(`{
		"type": "object",
		"properties": {
			"result_id": {"type": "string"},
			"offset": {"type": "integer", "description": "Byte offset (from the marker); with grep, the line to start at."},
			"limit_tokens": {"type": "integer"},
			"grep": {"type": "string", "description": "Regex: matching lines with line numbers."},
			"jsonpath": {"type": "string", "description": "e.g. $.items[10:20], $..name"}
		},
		"required": ["result_id"]
	}`),
}

// withReadResultTool appends read_result to a tools/list result.
func (g *Governor) withReadResultTool(ctx context.Context, result json.RawMessage) json.RawMessage {
	var body map[string]json.RawMessage
	if json.Unmarshal(result, &body) != nil {
		return result
	}
	var tools []json.RawMessage
	if json.Unmarshal(body["tools"], &tools) != nil {
		return result
	}
	for _, t := range tools {
		var probe struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(t, &probe) == nil && probe.Name == ReadResultToolName {
			return result
		}
	}
	tool := Tool{Name: readResultTool.Name, Description: readResultTool.Description, InputSchema: readResultTool.InputSchema}
	if g.session(ctx).AtLeast(ProtocolVersion20250326) {
		readOnly := true
		tool.Annotations = &ToolAnnotations{ReadOnlyHint: &readOnly}
	}
	encoded, err := json.Marshal(tool)
	if err != nil {
		return result
	}
	tools = append(tools, encoded)
	if body["tools"], err = json.Marshal(tools); err != nil {
		return result
	}
	out, err := json.Marshal(body)
	if err != nil {
		return result
	}
	return out
}

// readResultParams are read_result's arguments.
type readResultParams struct {
	ResultID    string `json:"result_id"`
	Offset      *int   `json:"offset"`
	LimitTokens *int   `json:"limit_tokens"`
	Grep        string `json:"grep"`
	JSONPath    string `json:"jsonpath"`
}

// readResultArgs recognizes a read_result call: tools/call with name
// read_result (every front end), or serve's gateway-method form (method
// read_result, the arguments as params).
func readResultArgs(req *Request) (json.RawMessage, bool) {
	switch req.Method {
	case MethodToolsCall:
		var p ToolsCallParams
		if json.Unmarshal(req.Params, &p) != nil || p.Name != ReadResultToolName {
			return nil, false
		}
		return p.Arguments, true
	case ReadResultToolName:
		return req.Params, true
	}
	return nil, false
}

// notFoundText is read_result's answer for an id it cannot serve. It does
// not tell an expired id from another session's.
func (g *Governor) notFoundText(id string) string {
	return fmt.Sprintf("LeanProxy: result_id %q not found. It is unknown, expired (results are kept %s), or belongs to another session; call the tool again to get a fresh result.", id, g.store.TTL())
}

// readResult serves read_result from the spill store.
func (g *Governor) readResult(ctx context.Context, req *Request, raw json.RawMessage) *Response {
	g.bumpReads()
	var p readResultParams
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &p); err != nil {
			return toolText(req.ID, true, fmt.Sprintf("LeanProxy: invalid read_result arguments: %v", err))
		}
	}
	if p.ResultID == "" {
		return toolText(req.ID, true, "LeanProxy: result_id is required (it is in the marker of the shortened result).")
	}
	if p.Grep != "" && p.JSONPath != "" {
		return toolText(req.ID, true, "LeanProxy: use grep or jsonpath, not both.")
	}
	if !governor.ValidID(p.ResultID) {
		return toolText(req.ID, true, g.notFoundText(p.ResultID))
	}
	data, meta, err := g.store.Get(owner(g.session(ctx)), p.ResultID)
	if err != nil {
		return toolText(req.ID, true, g.notFoundText(p.ResultID))
	}
	limitTokens := g.cfg.GlobalMaxTokens()
	if limitTokens <= 0 {
		limitTokens = governor.DefaultMaxTokens
	}
	if p.LimitTokens != nil && *p.LimitTokens > 0 {
		limitTokens = min(max(*p.LimitTokens, governor.MinMaxTokens), maxReadTokens)
	}
	limit := limitTokens * governor.BytesPerToken
	offset := 0
	if p.Offset != nil {
		offset = *p.Offset
	}

	switch {
	case p.Grep != "":
		res, err := governor.Grep(string(data), p.Grep, offset, limit)
		if err != nil {
			return toolText(req.ID, true, "LeanProxy: "+err.Error())
		}
		nav := fmt.Sprintf("[LeanProxy: result_id=%s, %d matching lines", meta.ID, res.Total)
		if res.NextLine > 0 {
			nav += fmt.Sprintf(", %d shown; continue with offset=%d (line number) to see more", res.Matches, res.NextLine)
		}
		nav += "]"
		if res.Total == 0 {
			return toolTexts(req.ID, false, nav)
		}
		return toolTexts(req.ID, false, res.Text, nav)
	case p.JSONPath != "":
		if meta.Kind != governor.KindJSON {
			return toolText(req.ID, true, "LeanProxy: jsonpath needs a JSON result; this one is text (use offset or grep).")
		}
		out, n, err := governor.JSONPath(data, p.JSONPath)
		if err != nil {
			return toolText(req.ID, true, "LeanProxy: "+err.Error())
		}
		nav := fmt.Sprintf("[LeanProxy: result_id=%s, %d values matched]", meta.ID, n)
		if short, ok := governor.TruncateJSON(out, limit, meta.ID); ok {
			out = short
			nav = fmt.Sprintf("[LeanProxy: result_id=%s, %d values matched, shortened to limit_tokens=%d; narrow the path (e.g. a smaller slice)]", meta.ID, n, limitTokens)
		}
		return toolTexts(req.ID, false, string(out), nav)
	default:
		page, err := governor.ReadPage(string(data), offset, limit)
		if err != nil {
			return toolText(req.ID, true, "LeanProxy: "+err.Error())
		}
		nav := fmt.Sprintf("[LeanProxy: result_id=%s, bytes %d-%d of %d", meta.ID, page.Offset, page.End, page.Total)
		if page.Done() {
			nav += ", end of result]"
		} else {
			nav += fmt.Sprintf("; next: read_result with id=%s, offset=%d]", meta.ID, page.End)
		}
		return toolTexts(req.ID, false, page.Text, nav)
	}
}

// readResultResource answers resources/read of leanproxy://results/<id>.
// ok is false for any other URI (the request then goes on to the
// aggregated resources).
func (g *Governor) readResultResource(ctx context.Context, req *Request) (*Response, bool) {
	var p struct {
		URI string `json:"uri"`
	}
	if json.Unmarshal(req.Params, &p) != nil {
		return nil, false
	}
	id, found := strings.CutPrefix(p.URI, ResultURIPrefix)
	if !found || !governor.ValidID(id) {
		return nil, false
	}
	g.bumpReads()
	data, meta, err := g.store.Get(owner(g.session(ctx)), id)
	if err != nil {
		return resourceNotFound(req, p.URI), true
	}
	mime := "text/plain"
	if meta.Kind == governor.KindJSON {
		mime = "application/json"
	}
	result, err := governor.MarshalNoEscape(map[string]any{
		"contents": []map[string]string{{"uri": p.URI, "mimeType": mime, "text": string(data)}},
	})
	if err != nil {
		return errorResponse(req, ErrCodeInternalError, "encode result"), true
	}
	return &Response{JSONRPC: JSONRPCVersion, Result: result, ID: req.ID}, true
}

// toolText is a tools/call result with one text item.
func toolText(id any, isError bool, text string) *Response {
	return toolTexts(id, isError, text)
}

// toolTexts is a tools/call result with one text item per text.
func toolTexts(id any, isError bool, texts ...string) *Response {
	items := make([]map[string]string, 0, len(texts))
	for _, t := range texts {
		items = append(items, map[string]string{"type": "text", "text": t})
	}
	body := map[string]any{"content": items}
	if isError {
		body["isError"] = true
	}
	result, err := governor.MarshalNoEscape(body)
	if err != nil {
		return &Response{JSONRPC: JSONRPCVersion, Error: NewError(ErrCodeInternalError, "encode result"), ID: id}
	}
	return &Response{JSONRPC: JSONRPCVersion, Result: result, ID: id}
}

// ---------------------------------------------------------------- stats

type governorCounters struct {
	results, truncated, spilled, reads int64
	originalTokens, returnedTokens     int64
}

// GovernorToolStats is the per-tool accounting (for the savings report,
// #21.6): estimated tokens of the results before and after the governor.
type GovernorToolStats struct {
	Tool           string `json:"tool"`
	Results        int64  `json:"results"`
	Truncated      int64  `json:"truncated"`
	OriginalTokens int64  `json:"original_tokens"`
	ReturnedTokens int64  `json:"returned_tokens"`
}

// GovernorStats is the governor's accounting, as exposed on /metrics.
// Only numbers: never a payload.
type GovernorStats struct {
	Enabled        bool                `json:"enabled"`
	MaxTokens      int                 `json:"max_tokens"`
	Results        int64               `json:"results"`
	Truncated      int64               `json:"truncated"`
	Spilled        int64               `json:"spilled"`
	ReadResult     int64               `json:"read_result_calls"`
	OriginalTokens int64               `json:"original_tokens"`
	ReturnedTokens int64               `json:"returned_tokens"`
	SavedTokens    int64               `json:"saved_tokens"`
	Store          governor.StoreStats `json:"store"`
	ByTool         []GovernorToolStats `json:"by_tool,omitempty"`
}

// Stats returns a snapshot of the accounting.
func (g *Governor) Stats() GovernorStats {
	if !g.Enabled() {
		return GovernorStats{}
	}
	g.statsMu.Lock()
	c := g.stats
	byTool := make([]GovernorToolStats, 0, len(g.byTool))
	for _, t := range g.byTool {
		byTool = append(byTool, *t)
	}
	g.statsMu.Unlock()
	sort.Slice(byTool, func(i, j int) bool { return byTool[i].Tool < byTool[j].Tool })
	return GovernorStats{
		Enabled:        true,
		MaxTokens:      g.cfg.GlobalMaxTokens(),
		Results:        c.results,
		Truncated:      c.truncated,
		Spilled:        c.spilled,
		ReadResult:     c.reads,
		OriginalTokens: c.originalTokens,
		ReturnedTokens: c.returnedTokens,
		SavedTokens:    c.originalTokens - c.returnedTokens,
		Store:          g.store.Stats(),
		ByTool:         byTool,
	}
}

// account records one governed tool result (sizes in bytes of the result
// JSON before and after) in the stats and the telemetry counters.
func (g *Governor) account(ctx context.Context, server, identity string, before, after, truncated, spilled int) {
	orig, ret := int64(governor.Tokens(before)), int64(governor.Tokens(after))
	g.statsMu.Lock()
	g.stats.results++
	g.stats.truncated += int64(truncated)
	g.stats.spilled += int64(spilled)
	g.stats.originalTokens += orig
	g.stats.returnedTokens += ret
	t, ok := g.byTool[identity]
	if !ok {
		key := identity
		if len(g.byTool) >= maxGovernorTools {
			key = "other"
		}
		if t, ok = g.byTool[key]; !ok {
			t = &GovernorToolStats{Tool: key}
			g.byTool[key] = t
		}
	}
	t.Results++
	t.Truncated += int64(truncated)
	t.OriginalTokens += orig
	t.ReturnedTokens += ret
	g.statsMu.Unlock()
	RecordGovernedResult(ctx, server, orig, ret, truncated > 0)
}

func (g *Governor) bumpReads() {
	g.statsMu.Lock()
	g.stats.reads++
	g.statsMu.Unlock()
}
