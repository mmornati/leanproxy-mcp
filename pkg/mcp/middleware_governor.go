package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp/governor"
	"github.com/mmornati/leanproxy-mcp/pkg/sidecar"
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
// Field projection (#320) runs on the same parsed result, before the budget
// is applied (see shortenResult): it too only sees redacted, scanned
// content, and whatever it leaves out stays retrievable in full through
// read_result. In-session dedup (#321) can reuse the spill store and
// read_result the same way.
type Governor struct {
	cfg         *governor.Config
	store       *governor.Store
	projections *governor.Projections

	handler atomic.Pointer[Handler]
	servers atomic.Pointer[func() []string]

	// In-session dedup (#321): a content hash → stored result map, kept
	// per session owner (never across sessions: dedupIdx and dedupTurn are
	// both keyed by the same owner key the spill store uses). See
	// dedupUnits.
	dedupMu   sync.Mutex
	dedupIdx  map[any]map[[32]byte]dedupEntry
	dedupTurn map[any]int

	// Summarization (#321): the local-LLM client (nil: disabled or
	// unavailable) and the injection guard its output is scanned with
	// (nil: not wired, e.g. tests that do not need it — see scanSummary).
	summarizer     *sidecar.Client
	injectionGuard atomic.Pointer[InjectionGuard]

	stopJanitor chan struct{}
	closeOnce   sync.Once

	statsMu sync.Mutex
	stats   governorCounters
	byTool  map[string]*GovernorToolStats
}

// dedupEntry is one in-session dedup record: the stored id of the first
// occurrence, its estimated size, and the turn (governed call) it was
// first seen on.
type dedupEntry struct {
	ID     string
	Tokens int
	Turn   int
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
	g.projections = cfg.CompileProjections()
	if cfg.DedupEnabled() {
		g.dedupIdx = make(map[any]map[[32]byte]dedupEntry)
		g.dedupTurn = make(map[any]int)
	}
	if cfg.SummarizeEnabled() {
		sc := sidecar.Config{Provider: cfg.Summarize.ProviderValue(), Model: cfg.Summarize.Model, URL: cfg.Summarize.URLValue()}
		client, err := sidecar.NewOllamaClient(sc, slog.Default())
		if err != nil {
			slog.Warn("response governor: summarization disabled, could not create sidecar client", "error", err)
		} else {
			g.summarizer = client
		}
	}
	g.stopJanitor = make(chan struct{})
	go g.janitor()
	return g
}

// SetInjectionGuard wires the guard summarized output (#321) is scanned
// with, the same as any other tool output (checkResponse). Without it, a
// summary is used unscanned; cmd wires this after building the firewall.
func (g *Governor) SetInjectionGuard(ig *InjectionGuard) {
	if g != nil && ig != nil {
		g.injectionGuard.Store(ig)
	}
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
	return fmt.Sprintf("response governor enabled: max_tokens %d, %d tool rules, %d projection rules (default pack %s), dedup %s, summarize %s, spill to %s, ttl %s",
		g.cfg.GlobalMaxTokens(), len(g.cfg.Tools), g.projections.Len(), onOff(g.projections.Default()), onOff(g.cfg.DedupEnabled()), onOff(g.summarizer != nil), where, g.store.TTL())
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

// DropSession removes every result spilled for s, and its dedup index
// (#321): a session's dedup memory never outlives the session.
func (g *Governor) DropSession(s *ClientSession) {
	if g.Enabled() && s != nil {
		g.store.DropOwner(s)
		g.dedupDropOwner(s)
	}
}

// Close stops the janitor, removes every spilled result (and, with
// spill.disk, the per-process directory) and closes the summarizer client.
// Called on shutdown.
func (g *Governor) Close() {
	if !g.Enabled() {
		return
	}
	g.closeOnce.Do(func() {
		close(g.stopJanitor)
		g.store.Close()
		if g.summarizer != nil {
			_ = g.summarizer.Close()
		}
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

			// invoke_tool's fields argument is the proxy's: it is taken
			// out here and never reaches any later stage or the upstream.
			req, fields := takeFields(req)

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
					g.govern(ctx, server, tool, resp, fields)
				}
			}
			return resp, err
		}
	}
}

// govern applies the tool's projection and budget to a tools/call response
// in place. fields is invoke_tool's fields argument (nil when absent).
func (g *Governor) govern(ctx context.Context, server, tool string, resp *Response, fields json.RawMessage) {
	identity := cacheIdentity(server, tool)
	budget := g.cfg.BudgetFor(identity)
	proj := g.projectionFor(identity, budget, fields)
	before := len(resp.Result)
	limit := 0
	if budget.Limited() {
		limit = budget.MaxTokens * governor.BytesPerToken
	}
	overBudget := limit > 0 && before > limit
	// Dedup (#321) and summarization need to parse the result even when it
	// already fits the budget: dedup tracks every result over its own
	// (smaller) threshold, and summarization is keyed off its own
	// threshold_tokens, not the truncation budget.
	needsDedup := g.cfg.DedupEnabled() && before >= dedupMinBytes
	needsSummarize := g.summarizer != nil && overBudget && g.cfg.MatchesSummarizeTool(identity) && before >= g.cfg.ThresholdTokensValue()*governor.BytesPerToken
	if proj == nil && !overBudget && !needsDedup && !needsSummarize {
		// Fast path: nothing to project, dedup or summarize, and the
		// whole result fits (or the tool is not governed); nothing is
		// parsed.
		g.account(ctx, server, identity, governedResult{before: before, after: before})
		return
	}
	res, err := g.shortenResult(ctx, server, tool, resp.Result, limit, proj)
	if err != nil {
		slog.Warn("response governor: result passed through unchanged", "tool", identity, "error", err)
	}
	if res.out == nil {
		g.account(ctx, server, identity, governedResult{before: before, after: before})
		return
	}
	resp.Result = res.out
	res.before, res.after = before, len(res.out)
	g.account(ctx, server, identity, res)
}

// projectionFor is the projection of one call: the model's fields (a
// one-off keep), else the first matching response.projections rule or the
// default pack, unless the tool is passthrough. nil means none.
func (g *Governor) projectionFor(identity string, budget governor.Budget, fields json.RawMessage) *governor.RuleProjection {
	if fields != nil {
		paths, err := parseFields(fields)
		if err == nil {
			var p *governor.Projection
			if p, err = governor.CompileProjection(paths, nil); err == nil {
				return &governor.RuleProjection{Projection: p, Rule: "fields"}
			}
		}
		// Safety (#320): a bad fields argument leaves the result as is.
		slog.Debug("response governor: fields argument ignored", "tool", identity, "error", err)
		return nil
	}
	if budget.Passthrough {
		return nil
	}
	if rp, ok := g.projections.For(identity); ok {
		return &rp
	}
	return nil
}

// parseFields reads invoke_tool's fields argument: a list of paths, or
// one string of comma-separated paths.
func parseFields(raw json.RawMessage) ([]string, error) {
	var paths []string
	if err := json.Unmarshal(raw, &paths); err != nil {
		var one string
		if json.Unmarshal(raw, &one) != nil {
			return nil, fmt.Errorf("fields must be a list of paths")
		}
		paths = strings.Split(one, ",")
	}
	out := paths[:0]
	for _, p := range paths {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("fields is empty")
	}
	return out, nil
}

// fieldsArg is invoke_tool's proxy-side argument.
const fieldsArg = `"fields"`

// takeFields returns req without invoke_tool's fields argument, and that
// argument (nil when absent). It looks at the two invoke_tool forms: the
// tools/call envelope ({"name":"invoke_tool","arguments":{...,"fields":…}})
// and serve's method form (method invoke_tool, fields in params). req is
// not modified: a copy carries the new params, and every other byte of
// the params is kept (the tool's own arguments travel byte for byte).
func takeFields(req *Request) (*Request, json.RawMessage) {
	if len(req.Params) == 0 || !bytes.Contains(req.Params, []byte(fieldsArg)) {
		return req, nil
	}
	params, err := governor.SplitObject(req.Params)
	if err != nil {
		return req, nil
	}
	var fields json.RawMessage
	switch req.Method {
	case MethodToolsCall:
		nameAt, argsAt := -1, -1
		for i, m := range params {
			switch string(m.Key) {
			case `"name"`:
				nameAt = i
			case `"arguments"`:
				argsAt = i
			}
		}
		if nameAt < 0 || argsAt < 0 || string(params[nameAt].Value) != `"`+invokeToolGatewayName+`"` {
			return req, nil
		}
		args, err := governor.SplitObject(params[argsAt].Value)
		if err != nil {
			return req, nil
		}
		if args, fields = withoutMember(args, fieldsArg); fields == nil {
			return req, nil
		}
		params[argsAt].Value = joinObject(args)
	case invokeToolGatewayName:
		if params, fields = withoutMember(params, fieldsArg); fields == nil {
			return req, nil
		}
	default:
		return req, nil
	}
	out := *req
	out.Params = joinObject(params)
	if string(fields) == "null" {
		return &out, nil // "fields": null is no fields
	}
	return &out, fields
}

// withoutMember removes the member named key (a raw JSON string) and
// returns its value (nil when absent).
func withoutMember(members []governor.Member, key string) ([]governor.Member, json.RawMessage) {
	for i, m := range members {
		if string(m.Key) == key {
			return append(members[:i:i], members[i+1:]...), json.RawMessage(m.Value)
		}
	}
	return members, nil
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

	// Field projection (#320): the result id of the full, unprojected
	// data (empty: not projected) and its size.
	projectedID   string
	projectedFrom int

	// In-session dedup (#321): true once this unit has been replaced by a
	// dedup marker (data/out already final; never truncated or
	// summarized).
	deduped bool
}

// resourceLinkReserve is the budget kept, per shortened unit, for its
// resource_link content item.
const resourceLinkReserve = 256

// governedResult is what the governor did to one result: shortenResult
// fills it, govern adds the sizes, account records it.
type governedResult struct {
	before, after int // bytes of the result JSON before and after

	out       json.RawMessage // nil: keep the result as is
	truncated int             // units shortened and spilled
	projected int             // units projected
	removed   int             // members left out by the projection
	// Wire bytes of the projected units before and after projection.
	projBefore, projAfter int

	// In-session dedup (#321): units replaced by a dedup marker, and the
	// estimated tokens saved.
	dedupHits        int
	dedupSavedTokens int
	// Summarization (#321): units replaced by a local-LLM summary, the
	// estimated tokens saved, and how many summarization attempts fell
	// back to truncation (timeout, error, empty output, or blocked by the
	// injection guard).
	summarized           int
	summarizeSavedTokens int
	summarizeFallbacks   int
}

// shortenResult projects (proj, may be nil) and then shortens (budget
// bytes, 0 for no budget) a tools/call result. out is nil (keep the result
// as is) for an error result, a result with nothing to project or shorten,
// or when a full copy cannot be stored: a result is never cut, nor
// projected, without its full copy being retrievable.
//
// The result is valid JSON (the front end decoded it); it is split with
// the governor's scanner, never fully decoded, and every member the
// governor does not change is written back byte for byte, in order.
func (g *Governor) shortenResult(ctx context.Context, server, tool string, result json.RawMessage, budget int, proj *governor.RuleProjection) (governedResult, error) {
	var none governedResult
	body, err := governor.SplitObject(result)
	if err != nil {
		return none, nil
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
		return none, nil // never project or shorten an error result
	}
	contentAt := member("content")
	var content [][]byte
	if contentAt >= 0 && string(body[contentAt].Value) != "null" {
		if content, err = governor.SplitArray(body[contentAt].Value); err != nil {
			return none, nil
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

	sess := g.session(ctx)
	var res governedResult
	// Field projection (#320), before the budget.
	if proj != nil {
		if err := g.projectUnits(ctx, sess, server, tool, units, proj, &res); err != nil {
			return none, err
		}
	}
	// In-session dedup (#321), before the budget and before summarization:
	// a unit identical to one already seen in this session is replaced by
	// a short marker, regardless of whether the result would otherwise
	// have needed truncation.
	if g.cfg.DedupEnabled() {
		g.dedupUnits(sess, server, tool, units, &res)
	}

	sizes := make([]int, len(units))
	total := 0
	for i, u := range units {
		sizes[i] = u.wire
		total += u.wire
	}
	var spilled []*resultUnit
	links := sess.AtLeast(ProtocolVersion20250618)
	if budget > 0 && total > budget {
		// 5% of the budget covers the envelope (and the projection
		// note); resource links get their own reserve.
		avail := budget * 95 / 100
		if res.projected > 0 {
			avail -= projectionNoteReserve
		}
		if links {
			over := 0
			for _, s := range sizes {
				if s > avail/len(units) {
					over++
				}
			}
			avail -= (over + res.projected) * resourceLinkReserve
		}
		alloc := governor.WaterFill(sizes, max(avail, 0))
		spilled = make([]*resultUnit, 0, len(units))
		for i, u := range units {
			if u.deduped || sizes[i] <= alloc[i] {
				continue
			}
			// Dedup (#321) may already have spilled this unit's full data
			// to learn its hash; reuse that id instead of storing it a
			// second time.
			if u.id == "" {
				meta, err := g.store.Put(owner(sess), governor.Meta{Server: server, Tool: tool, Kind: u.kind}, u.data)
				if err != nil {
					// Units spilled before this one just expire with the TTL.
					if res.projected > 0 {
						// The projection stands on its own: keep it,
						// unshortened.
						slog.Debug("response governor: projected result not shortened", "tool", cacheIdentity(server, tool), "error", err)
						for _, v := range spilled {
							v.out, v.id = nil, ""
						}
						spilled = nil
						break
					}
					return none, fmt.Errorf("spill: %w", err)
				}
				u.id = meta.ID
			}
			// From wire bytes back to the unit's own bytes (escaping).
			target := int(int64(alloc[i]) * int64(len(u.data)) / int64(max(u.wire, 1)))
			if out, ok := g.trySummarize(ctx, server, tool, u, &res); ok {
				u.out = out
			} else {
				u.out = shortenUnit(u, target)
			}
			spilled = append(spilled, u)
		}
	}
	if len(spilled) == 0 && res.projected == 0 && res.dedupHits == 0 {
		return none, nil
	}
	for _, u := range spilled {
		u.data = u.out
	}

	for _, u := range units {
		if u.out == nil && u.projectedID == "" {
			continue
		}
		if u.content < 0 {
			body[structAt].Value = u.data
			continue
		}
		item, err := replaceText(content[u.content], textAt[u.content], string(u.data))
		if err != nil {
			return none, err
		}
		content[u.content] = item
	}
	if res.projected > 0 {
		note, err := governor.MarshalNoEscape(map[string]string{"type": "text", "text": projectionNote(units, server, tool, proj, res)})
		if err != nil {
			return none, err
		}
		content = append(content, note)
	}
	if links {
		linked := make(map[string]bool)
		for _, u := range units {
			if u.projectedID == "" || linked[u.projectedID] {
				continue
			}
			linked[u.projectedID] = true
			link, err := projectionLink(u, server, tool)
			if err != nil {
				return none, err
			}
			content = append(content, link)
		}
		for _, u := range spilled {
			link, err := resultLink(u, server, tool)
			if err != nil {
				return none, err
			}
			content = append(content, link)
		}
	}
	if contentAt >= 0 {
		body[contentAt].Value = joinArray(content)
	} else if len(content) > 0 {
		body = append(body, governor.Member{Key: []byte(`"content"`), Value: joinArray(content)})
	}
	out := joinObject(body)
	if !json.Valid(out) {
		return none, fmt.Errorf("governed result is not valid JSON")
	}
	res.out = out
	res.truncated = len(spilled)
	return res, nil
}

// projectionNoteReserve is the budget kept for the projection note.
const projectionNoteReserve = 600

// projectUnits applies proj to the JSON units, in place. A unit whose full
// copy cannot be stored is left as is. structuredContent is only projected
// when the tool declares no outputSchema: a projected structuredContent
// could fail a strict schema's validation (required members dropped), so
// with a schema only the text rendering is projected.
func (g *Governor) projectUnits(ctx context.Context, sess *ClientSession, server, tool string, units []*resultUnit, proj *governor.RuleProjection, res *governedResult) error {
	schemaChecked, schema := false, false
	byData := make(map[string]string) // full data → its result id (text and structuredContent are often the same JSON)
	for _, u := range units {
		if u.kind != governor.KindJSON {
			continue // non-JSON text is left to truncation
		}
		if u.content < 0 {
			if !schemaChecked {
				schemaChecked, schema = true, g.declaresOutputSchema(ctx, server, tool)
			}
			if schema {
				continue
			}
		}
		out, stats, changed, err := proj.ApplyValid(u.data)
		if err != nil {
			slog.Debug("response governor: projection skipped", "tool", cacheIdentity(server, tool), "rule", proj.Rule, "error", err)
			continue
		}
		if !changed {
			continue
		}
		key := string(bytes.TrimSpace(u.data))
		id, ok := byData[key]
		if !ok {
			meta, err := g.store.Put(owner(sess), governor.Meta{Server: server, Tool: tool, Kind: governor.KindJSON}, u.data)
			if err != nil {
				// Never project without a retrievable full copy.
				slog.Debug("response governor: projection skipped, full result not stored", "tool", cacheIdentity(server, tool), "error", err)
				continue
			}
			id = meta.ID
			byData[key] = id
		}
		wire := len(out)
		if u.content >= 0 {
			encoded, err := governor.MarshalNoEscape(string(out))
			if err != nil {
				return err
			}
			wire = len(encoded) - 2
		}
		res.projected++
		res.removed += stats.Removed
		res.projBefore += u.wire
		res.projAfter += wire
		u.projectedID, u.projectedFrom = id, len(u.data)
		u.data, u.wire = out, wire
	}
	return nil
}

// declaresOutputSchema reports whether the tool declares an outputSchema
// (#307). A tool the handler does not know (its list cannot be fetched)
// counts as declaring one: structuredContent is then left unprojected.
func (g *Governor) declaresOutputSchema(ctx context.Context, server, tool string) bool {
	h := g.handler.Load()
	if h == nil {
		return true
	}
	find := func() (Tool, bool) {
		if t, ok := h.cachedTool(server, tool); ok {
			return t, true
		}
		if rest, cut := strings.CutPrefix(tool, server+"_"); cut && rest != "" {
			return h.cachedTool(server, rest)
		}
		return Tool{}, false
	}
	t, ok := find()
	if !ok && !h.toolsKnown(server) {
		_ = h.RefreshServerTools(ctx, server)
		t, ok = find()
	}
	if !ok {
		return true
	}
	return len(t.OutputSchema) > 0 && string(t.OutputSchema) != "null"
}

// ---------------------------------------------------------- dedup (#321)

// dedupMinBytes is the smallest unit (wire bytes) in-session dedup tracks.
const dedupMinBytes = governor.DefaultDedupMinTokens * governor.BytesPerToken

// dedupMarker is the stub returned instead of a byte-identical result
// (issue #321's exact wording).
func dedupMarker(server, tool, id string) string {
	return fmt.Sprintf("[LeanProxy: identical to the result of %s returned earlier (result_id=%s). Call read_result to get it again if it is no longer in context.]",
		cacheIdentity(server, tool), id)
}

// dedupUnits replaces any unit byte-identical (after projection) to one
// already seen in this session with a short marker, and remembers every
// unit over dedupMinBytes for future calls. The hash is per session owner
// only (see owner): two sessions never learn what the other saw, even when
// the content is identical.
func (g *Governor) dedupUnits(sess *ClientSession, server, tool string, units []*resultUnit, res *governedResult) {
	own := owner(sess)
	turn := g.dedupNextTurn(own)
	for _, u := range units {
		if len(u.data) < dedupMinBytes {
			continue
		}
		hash := sha256.Sum256(u.data)
		if entry, ok := g.dedupLookup(own, hash); ok {
			if _, _, err := g.store.Get(own, entry.ID); err == nil {
				marker := dedupMarker(server, tool, entry.ID)
				res.dedupHits++
				res.dedupSavedTokens += governor.Tokens(u.wire) - governor.Tokens(len(marker))
				u.data = []byte(marker)
				u.wire = len(marker)
				u.kind = governor.KindText
				u.out = u.data
				u.deduped = true
				continue
			}
			// The first occurrence expired or was evicted: fall through
			// and re-store this one so later calls can dedup against it.
		}
		meta, err := g.store.Put(own, governor.Meta{Server: server, Tool: tool, Kind: u.kind}, u.data)
		if err != nil {
			continue // not fatal: this unit is just never deduped
		}
		g.dedupRemember(own, hash, dedupEntry{ID: meta.ID, Tokens: governor.Tokens(len(u.data)), Turn: turn})
		u.id = meta.ID // reused below instead of a second Put, if it also needs truncating
	}
}

func (g *Governor) dedupLookup(own any, hash [32]byte) (dedupEntry, bool) {
	g.dedupMu.Lock()
	defer g.dedupMu.Unlock()
	e, ok := g.dedupIdx[own][hash]
	return e, ok
}

func (g *Governor) dedupRemember(own any, hash [32]byte, e dedupEntry) {
	g.dedupMu.Lock()
	defer g.dedupMu.Unlock()
	if g.dedupIdx == nil {
		g.dedupIdx = make(map[any]map[[32]byte]dedupEntry)
	}
	m := g.dedupIdx[own]
	if m == nil {
		m = make(map[[32]byte]dedupEntry)
		g.dedupIdx[own] = m
	}
	m[hash] = e
}

func (g *Governor) dedupNextTurn(own any) int {
	g.dedupMu.Lock()
	defer g.dedupMu.Unlock()
	if g.dedupTurn == nil {
		g.dedupTurn = make(map[any]int)
	}
	g.dedupTurn[own]++
	return g.dedupTurn[own]
}

// dedupDropOwner forgets everything remembered for own (its session
// ended): dedup memory never outlives a session.
func (g *Governor) dedupDropOwner(own any) {
	if g.dedupIdx == nil {
		return
	}
	g.dedupMu.Lock()
	defer g.dedupMu.Unlock()
	delete(g.dedupIdx, own)
	delete(g.dedupTurn, own)
}

// ------------------------------------------------------ summarize (#321)

// summarizePromptTemplate is the fixed prompt sent to the local model.
const summarizePromptTemplate = `Summarize the following tool result for an AI coding agent. Keep identifiers, numbers, paths and errors verbatim. List what was omitted at the end.

%s`

// trySummarize attempts to replace u with a local-LLM summary instead of
// truncating it: only for a tool listed in response.summarize.tools, only
// for a unit at least threshold_tokens large, with a strict timeout and
// size caps on input and output. Its output is treated as untrusted and run
// back through the injection guard's response scan (#321), exactly like
// any other tool output. Any failure — not configured, not allowlisted,
// too small, timeout, error, empty output, or blocked by the injection
// guard — returns ok=false so the caller falls back to truncation (#319).
func (g *Governor) trySummarize(ctx context.Context, server, tool string, u *resultUnit, res *governedResult) ([]byte, bool) {
	if g.summarizer == nil || !g.cfg.SummarizeEnabled() {
		return nil, false
	}
	identity := cacheIdentity(server, tool)
	if !g.cfg.MatchesSummarizeTool(identity) {
		return nil, false
	}
	thresholdBytes := g.cfg.ThresholdTokensValue() * governor.BytesPerToken
	if u.wire < thresholdBytes {
		return nil, false
	}
	sctx, cancel := context.WithTimeout(ctx, g.cfg.SummarizeTimeoutValue())
	defer cancel()
	input := capBytes(string(u.data), governor.MaxSummarizeInputBytes())
	summary, err := g.summarizer.Generate(sctx, fmt.Sprintf(summarizePromptTemplate, input))
	if err != nil || strings.TrimSpace(summary) == "" {
		slog.Warn("response governor: summarization failed, falling back to truncation", "tool", identity, "error", err)
		res.summarizeFallbacks++
		return nil, false
	}
	summary = capBytes(summary, g.cfg.MaxSummaryTokensValue()*governor.BytesPerToken)
	// The summarizer's own output is untrusted (it echoes the redacted
	// tool result back through a local model): scan it exactly like any
	// other tool output before it is ever returned to the model.
	scanned, blocked := g.scanSummary(ctx, summary)
	if blocked {
		slog.Warn("response governor: summary blocked by the injection guard, falling back to truncation", "tool", identity)
		res.summarizeFallbacks++
		return nil, false
	}
	text := fmt.Sprintf("[LeanProxy: summary of %s (%s → %s estimated tokens); full result kept as result_id=%s, call read_result to read it in full.]\n\n%s",
		identity, groupDigits(governor.Tokens(u.wire)), groupDigits(governor.Tokens(len(scanned))), u.id, scanned)
	res.summarized++
	res.summarizeSavedTokens += governor.Tokens(u.wire) - governor.Tokens(len(text))
	return []byte(text), true
}

// scanSummary runs text through the wired injection guard's response scan
// (#315/#321), the same policy a tool result would get. ok is true when the
// guard blocked it (never return summarized content the guard would have
// refused); the (possibly annotated or redacted) text is returned
// otherwise. Without a wired guard, text is returned unchanged: cmd wires
// SetInjectionGuard after building the firewall, so this only applies when
// the summarizer runs before that wiring (e.g. a bespoke embedding).
func (g *Governor) scanSummary(ctx context.Context, text string) (string, bool) {
	ig := g.injectionGuard.Load()
	if ig == nil || !ig.ScansResponses() {
		return text, false
	}
	result, err := governor.MarshalNoEscape(map[string]any{
		"content": []map[string]string{{"type": "text", "text": text}},
	})
	if err != nil {
		return text, false
	}
	req := &Request{JSONRPC: JSONRPCVersion, Method: MethodToolsCall,
		Params: json.RawMessage(`{"name":"invoke_tool","arguments":{}}`)}
	resp := &Response{JSONRPC: JSONRPCVersion, Result: result}
	out := ig.CheckResponse(ctx, req, resp)
	if out == nil || len(out.Result) == 0 {
		return text, false
	}
	var body struct {
		Content []ContentBlock `json:"content"`
		IsError bool           `json:"isError"`
	}
	if json.Unmarshal(out.Result, &body) != nil {
		return text, false
	}
	if body.IsError {
		return "", true
	}
	if len(body.Content) == 0 {
		return text, false
	}
	var b strings.Builder
	for i, c := range body.Content {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(c.Text)
	}
	return b.String(), false
}

// capBytes cuts s to at most n bytes, never inside a UTF-8 rune, appending
// a marker when it cut.
func capBytes(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n[…truncated for summarization…]"
}

// maxNotePaths caps the paths quoted in the projection note (bytes).
const maxNotePaths = 160

// projectionNote is the text item that tells the model a result was
// projected and how to get what was left out.
func projectionNote(units []*resultUnit, server, tool string, proj *governor.RuleProjection, res governedResult) string {
	var ids []string
	seen := make(map[string]bool)
	for _, u := range units {
		if u.projectedID != "" && !seen[u.projectedID] {
			seen[u.projectedID] = true
			ids = append(ids, u.projectedID)
		}
	}
	source := fmt.Sprintf("response.projections rule %q", proj.Rule)
	switch proj.Rule {
	case "fields":
		source = "your fields argument"
	case "default_projections":
		source = "the default projections"
	}
	paths := strings.Join(proj.Paths, ", ")
	if len(paths) > maxNotePaths {
		cut := maxNotePaths
		for cut > 0 && !utf8.RuneStart(paths[cut]) {
			cut--
		}
		paths = paths[:cut] + "…"
	}
	return fmt.Sprintf("[LeanProxy: %s JSON fields projected by %s (%s %s): %d values left out, %s → %s tokens. Full result: result_id=%s; call read_result with it (jsonpath, e.g. $[0], or grep) to get omitted fields.]",
		cacheIdentity(server, tool), source, proj.Mode, paths, res.removed,
		groupDigits(governor.Tokens(res.projBefore)), groupDigits(governor.Tokens(res.projAfter)), strings.Join(ids, ", result_id="))
}

// groupDigits writes n with thousands separators.
func groupDigits(n int) string {
	s := strconv.Itoa(n)
	if n < 1000 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	b.WriteString(s[:pre])
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// projectionLink is the resource_link content item (MCP 2025-06-18) to the
// full, unprojected copy of a projected unit.
func projectionLink(u *resultUnit, server, tool string) (json.RawMessage, error) {
	what := "text content"
	if u.content < 0 {
		what = "structuredContent"
	}
	return governor.MarshalNoEscape(map[string]string{
		"type":        "resource_link",
		"uri":         ResultURI(u.projectedID),
		"name":        u.projectedID,
		"description": fmt.Sprintf("Full, unprojected %s of %s (%d tokens), kept by LeanProxy for this session", what, cacheIdentity(server, tool), governor.Tokens(u.projectedFrom)),
		"mimeType":    "application/json",
	})
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

// invokeToolFieldsDescription is invoke_tool's description while the
// governor is on: it documents the fields argument (#320) in one sentence.
// The default tools/list (governor off) is unchanged, so its token budget
// is too.
const invokeToolFieldsDescription = "Invoke a tool found by search_tools. Optional fields: JSON paths of the result to keep (e.g. [\"[].title\"]); the rest stays readable with read_result."

// invokeToolFieldsProperty is the fields property added to invoke_tool's
// inputSchema while the governor is on.
var invokeToolFieldsProperty = json.RawMessage(`{"type":"array","items":{"type":"string"}}`)

// withReadResultTool appends read_result to a tools/list result, and adds
// the fields argument to invoke_tool when it is listed.
func (g *Governor) withReadResultTool(ctx context.Context, result json.RawMessage) json.RawMessage {
	var body map[string]json.RawMessage
	if json.Unmarshal(result, &body) != nil {
		return result
	}
	var tools []json.RawMessage
	if json.Unmarshal(body["tools"], &tools) != nil {
		return result
	}
	listed := false
	for i, t := range tools {
		var probe struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(t, &probe) != nil {
			continue
		}
		switch probe.Name {
		case ReadResultToolName:
			listed = true
		case invokeToolGatewayName:
			tools[i] = withFieldsArgument(t)
		}
	}
	if !listed {
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
	}
	var err error
	if body["tools"], err = json.Marshal(tools); err != nil {
		return result
	}
	out, err := json.Marshal(body)
	if err != nil {
		return result
	}
	return out
}

// withFieldsArgument documents and declares invoke_tool's fields argument
// in its tools/list entry. Every other member is kept byte for byte; an
// entry it cannot parse is returned as is.
func withFieldsArgument(entry json.RawMessage) json.RawMessage {
	members, err := governor.SplitObject(entry)
	if err != nil {
		return entry
	}
	for i, m := range members {
		switch string(m.Key) {
		case `"description"`:
			desc, err := governor.MarshalNoEscape(invokeToolFieldsDescription)
			if err != nil {
				return entry
			}
			members[i].Value = desc
		case `"inputSchema"`:
			schema, err := governor.SplitObject(m.Value)
			if err != nil {
				return entry
			}
			for j, sm := range schema {
				if string(sm.Key) != `"properties"` {
					continue
				}
				props, err := governor.SplitObject(sm.Value)
				if err != nil {
					return entry
				}
				if _, present := withoutMember(props, fieldsArg); present != nil {
					return entry
				}
				props = append(props, governor.Member{Key: []byte(fieldsArg), Value: invokeToolFieldsProperty})
				schema[j].Value = joinObject(props)
			}
			members[i].Value = joinObject(schema)
		}
	}
	return joinObject(members)
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
	projected, projectionSaved         int64
	// In-session dedup (#321).
	dedupHits, dedupSaved int64
	// Summarization (#321).
	summarized, summarizeSaved, summarizeFallbacks int64
}

// GovernorToolStats is the per-tool accounting (for the savings report,
// #21.6): estimated tokens of the results before and after the governor.
type GovernorToolStats struct {
	Tool           string `json:"tool"`
	Results        int64  `json:"results"`
	Truncated      int64  `json:"truncated"`
	OriginalTokens int64  `json:"original_tokens"`
	ReturnedTokens int64  `json:"returned_tokens"`
	// Field projection (#320): results projected, and the estimated
	// tokens the projection removed (before truncation).
	Projected             int64 `json:"projected,omitempty"`
	ProjectionSavedTokens int64 `json:"projection_saved_tokens,omitempty"`
	// In-session dedup (#321): results replaced by a dedup marker, and the
	// estimated tokens saved.
	DedupHits        int64 `json:"dedup_hits,omitempty"`
	DedupSavedTokens int64 `json:"dedup_saved_tokens,omitempty"`
	// Summarization (#321): results replaced by a local-LLM summary, the
	// estimated tokens saved, and summarization attempts that fell back to
	// truncation.
	Summarized           int64 `json:"summarized,omitempty"`
	SummarizeSavedTokens int64 `json:"summarize_saved_tokens,omitempty"`
	SummarizeFallbacks   int64 `json:"summarize_fallbacks,omitempty"`
}

// GovernorStats is the governor's accounting, as exposed on /metrics.
// Only numbers: never a payload.
type GovernorStats struct {
	Enabled        bool  `json:"enabled"`
	MaxTokens      int   `json:"max_tokens"`
	Results        int64 `json:"results"`
	Truncated      int64 `json:"truncated"`
	Spilled        int64 `json:"spilled"`
	ReadResult     int64 `json:"read_result_calls"`
	OriginalTokens int64 `json:"original_tokens"`
	ReturnedTokens int64 `json:"returned_tokens"`
	SavedTokens    int64 `json:"saved_tokens"`
	// Field projection (#320): results projected and the estimated
	// tokens it removed (part of SavedTokens).
	Projected             int64 `json:"projected"`
	ProjectionSavedTokens int64 `json:"projection_saved_tokens"`
	ProjectionRules       int   `json:"projection_rules"`
	DefaultProjections    bool  `json:"default_projections"`
	// In-session dedup (#321): results replaced by a dedup marker
	// (never across sessions) and the estimated tokens it saved (part of
	// SavedTokens).
	DedupEnabled     bool  `json:"dedup_enabled"`
	DedupHits        int64 `json:"dedup_hits"`
	DedupSavedTokens int64 `json:"dedup_saved_tokens"`
	// Summarization (#321): results replaced by a local-LLM summary, the
	// estimated tokens it saved (part of SavedTokens), and how many
	// summarization attempts fell back to truncation.
	SummarizeEnabled     bool                `json:"summarize_enabled"`
	Summarized           int64               `json:"summarized"`
	SummarizeSavedTokens int64               `json:"summarize_saved_tokens"`
	SummarizeFallbacks   int64               `json:"summarize_fallbacks"`
	Store                governor.StoreStats `json:"store"`
	ByTool               []GovernorToolStats `json:"by_tool,omitempty"`
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

		Projected:             c.projected,
		ProjectionSavedTokens: c.projectionSaved,
		ProjectionRules:       g.projections.Len(),
		DefaultProjections:    g.projections.Default(),

		DedupEnabled:     g.cfg.DedupEnabled(),
		DedupHits:        c.dedupHits,
		DedupSavedTokens: c.dedupSaved,

		SummarizeEnabled:     g.summarizer != nil,
		Summarized:           c.summarized,
		SummarizeSavedTokens: c.summarizeSaved,
		SummarizeFallbacks:   c.summarizeFallbacks,

		Store:  g.store.Stats(),
		ByTool: byTool,
	}
}

// account records one governed tool result (sizes in bytes of the result
// JSON before and after, and what was done) in the stats and the telemetry
// counters.
func (g *Governor) account(ctx context.Context, server, identity string, r governedResult) {
	orig, ret := int64(governor.Tokens(r.before)), int64(governor.Tokens(r.after))
	projSaved := int64(governor.Tokens(r.projBefore) - governor.Tokens(r.projAfter))
	g.statsMu.Lock()
	g.stats.results++
	g.stats.truncated += int64(min(r.truncated, 1))
	g.stats.spilled += int64(r.truncated)
	g.stats.originalTokens += orig
	g.stats.returnedTokens += ret
	if r.projected > 0 {
		g.stats.projected++
		g.stats.projectionSaved += projSaved
	}
	g.stats.dedupHits += int64(r.dedupHits)
	g.stats.dedupSaved += int64(r.dedupSavedTokens)
	g.stats.summarized += int64(r.summarized)
	g.stats.summarizeSaved += int64(r.summarizeSavedTokens)
	g.stats.summarizeFallbacks += int64(r.summarizeFallbacks)
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
	t.Truncated += int64(min(r.truncated, 1))
	t.OriginalTokens += orig
	t.ReturnedTokens += ret
	if r.projected > 0 {
		t.Projected++
		t.ProjectionSavedTokens += projSaved
	}
	t.DedupHits += int64(r.dedupHits)
	t.DedupSavedTokens += int64(r.dedupSavedTokens)
	t.Summarized += int64(r.summarized)
	t.SummarizeSavedTokens += int64(r.summarizeSavedTokens)
	t.SummarizeFallbacks += int64(r.summarizeFallbacks)
	g.statsMu.Unlock()
	RecordGovernedResult(ctx, server, orig, ret, r.truncated > 0)
	if r.projected > 0 {
		RecordProjection(ctx, server, int64(governor.Tokens(r.projBefore)), int64(governor.Tokens(r.projAfter)))
	}
	if r.dedupHits > 0 {
		RecordDedup(ctx, server, int64(r.dedupHits), int64(r.dedupSavedTokens))
	}
	if r.summarized > 0 || r.summarizeFallbacks > 0 {
		RecordSummarization(ctx, server, int64(r.summarized), int64(r.summarizeSavedTokens), int64(r.summarizeFallbacks))
	}
}

func (g *Governor) bumpReads() {
	g.statsMu.Lock()
	g.stats.reads++
	g.statsMu.Unlock()
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
