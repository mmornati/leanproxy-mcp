package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/mmornati/leanproxy-mcp/pkg/policy"
	"github.com/mmornati/leanproxy-mcp/pkg/toolpin"
)

// Per-tool policy (issue #314).
//
// Policy connects a compiled policy.Engine to the pipeline and the
// handler:
//
//   - its Middleware decides every tool call, whatever form it takes
//     (tools/call with a namespaced name, invoke_tool — stdio's gateway
//     tool or serve's method — and serve's namespaced "server.tool"
//     methods), in both front ends, before the response cache and the
//     upstream: deny → JSON-RPC error naming what decided, confirm → ask
//     the user through MCP elicitation, allow → the call proceeds with the
//     deciding rule's injection override (if any) in its context;
//   - a tool the server does not list in its tools/list is refused when
//     unknown_tools is deny (the default), after one refresh of that list;
//   - list_tools and search_tools hide denied tools and mark the ones that
//     need confirmation "[confirm]".
//
// Each deny and confirm decision is logged (server, tool, rule, outcome and
// a SHA-256 of the redacted arguments, never the arguments) and counted in
// leanproxy.policy.decisions.
type Policy struct {
	engine  atomic.Pointer[policy.Engine]
	handler atomic.Pointer[Handler]
	servers atomic.Pointer[func() []string]
	redact  atomic.Pointer[func(string) string]

	// missAt throttles the refresh a call to an unlisted tool triggers,
	// per server.
	missMu sync.Mutex
	missAt map[string]time.Time
}

// policyMissRefresh is the minimum interval between two tools/list
// refreshes of one server triggered by calls to tools it does not list.
const policyMissRefresh = 10 * time.Second

// confirmSummaryChars caps the argument summary shown in a confirmation.
const confirmSummaryChars = 500

// Confirmation choices (the "decision" field of the elicitation form).
const (
	confirmApprove        = "approve"
	confirmApproveSession = "approve_session"
	confirmDeny           = "deny"
)

// NewPolicy wraps e (nil: no policy, every call passes).
func NewPolicy(e *policy.Engine) *Policy {
	p := &Policy{}
	p.Set(e)
	return p
}

// Set installs e (nil disables the policy).
func (p *Policy) Set(e *policy.Engine) {
	if p != nil {
		p.engine.Store(e)
	}
}

// Engine returns the installed engine (nil when disabled).
func (p *Policy) Engine() *policy.Engine {
	if p == nil {
		return nil
	}
	return p.engine.Load()
}

// SetServerNames tells the middleware which servers exist, to split
// namespaced tool names ("server_tool"). Without it the handler's servers
// are used.
func (p *Policy) SetServerNames(fn func() []string) {
	if p == nil || fn == nil {
		return
	}
	p.servers.Store(&fn)
}

// SetRedactor sets how the arguments shown in a confirmation, and hashed
// in the audit log, are redacted (the firewall's RedactText).
func (p *Policy) SetRedactor(fn func(string) string) {
	if p == nil || fn == nil {
		return
	}
	p.redact.Store(&fn)
}

// Summary is the one-line startup status.
func (p *Policy) Summary() string {
	e := p.Engine()
	if e == nil {
		return "policy disabled"
	}
	return e.Summary()
}

func (p *Policy) serverNames() []string {
	if fn := p.servers.Load(); fn != nil {
		return (*fn)()
	}
	if h := p.handler.Load(); h != nil {
		return h.pool.ListServers()
	}
	return nil
}

func (p *Policy) redactText(s string) string {
	if fn := p.redact.Load(); fn != nil {
		return (*fn)(s)
	}
	return s
}

// SetPolicy installs the per-tool policy the handler's discovery consults
// (nil: none) and gives the policy the handler's tool lists.
func (h *Handler) SetPolicy(p *Policy) {
	if p != nil {
		p.handler.Store(h)
	}
	h.policy.Store(p)
}

func (h *Handler) policyEngine() *policy.Engine {
	return h.policy.Load().Engine()
}

// callTarget resolves the upstream server and tool a request would call,
// and the tool's arguments: tools/call with a namespaced name ("server_tool"
// or "server.tool"), invoke_tool (stdio's gateway tool, serve's method),
// and serve's namespaced "server.tool" methods. invoke is true for the
// invoke_tool forms, whose tool name may repeat the server prefix. ok is
// false for anything that does not call one upstream tool (gateway tools,
// protocol methods, names of no configured server).
func callTarget(req *Request, servers func() []string) (server, tool string, args json.RawMessage, invoke, ok bool) {
	if req == nil {
		return "", "", nil, false, false
	}
	server, tool, args, isCall := extractToolCall(req)
	switch {
	case isCall && server != "":
		return server, tool, args, true, true
	case isCall:
		if GetToolDefinition(tool) != nil {
			return "", "", nil, false, false // a gateway tool
		}
		s, t, err := SplitToolName(tool, servers())
		return s, t, args, false, err == nil
	}
	// serve's namespaced tool methods ("server.tool").
	switch req.Method {
	case MethodInitialize, MethodPing, MethodShutdown, "list_tools", "list_servers", "search_tools":
		return "", "", nil, false, false
	}
	if req.Method == "" || strings.Contains(req.Method, "/") {
		return "", "", nil, false, false
	}
	s, t, err := SplitToolName(req.Method, servers())
	return s, t, req.Params, false, err == nil
}

// toolHints converts a tool's annotations for the policy engine.
func toolHints(t Tool) policy.Hints {
	a := t.Annotations
	if a == nil {
		return policy.Hints{}
	}
	return policy.Hints{ReadOnly: a.ReadOnlyHint, Destructive: a.DestructiveHint, Idempotent: a.IdempotentHint, OpenWorld: a.OpenWorldHint}
}

// toolListing is what the handler knows of a tool for the policy.
type toolListing int

const (
	toolListed          toolListing = iota // in the server's tools/list
	toolNotListed                          // the list is known and lacks it
	toolListUnavailable                    // the list could not be fetched
)

// policyTool looks a called tool up in server's cached tool list. A list
// never fetched is fetched first (joining the refresh in progress, bounded
// by the server's timeout); a tool missing from a known list triggers one
// more refresh (at most every policyMissRefresh per server) in case the
// server added it without notifying. With stripPrefix, "server_tool" also
// finds "tool" (invoke_tool tolerates a repeated server prefix, see
// handleInvokeTool); the returned name is the one found.
func (h *Handler) policyTool(ctx context.Context, p *Policy, server, tool string, stripPrefix bool) (Tool, string, toolListing) {
	find := func() (Tool, string, bool) {
		if t, ok := h.cachedTool(server, tool); ok {
			return t, tool, true
		}
		if stripPrefix {
			if rest, cut := strings.CutPrefix(tool, server+"_"); cut && rest != "" {
				if t, ok := h.cachedTool(server, rest); ok {
					return t, rest, true
				}
			}
		}
		return Tool{}, tool, false
	}
	if !h.toolsKnown(server) {
		_ = h.RefreshServerTools(ctx, server)
	} else if t, name, ok := find(); ok {
		return t, name, toolListed
	} else if p.missRefreshDue(server) {
		_ = h.RefreshServerTools(ctx, server)
	}
	if t, name, ok := find(); ok {
		return t, name, toolListed
	}
	if !h.toolsKnown(server) {
		return Tool{}, tool, toolListUnavailable
	}
	return Tool{}, tool, toolNotListed
}

func (p *Policy) missRefreshDue(server string) bool {
	p.missMu.Lock()
	defer p.missMu.Unlock()
	now := time.Now()
	if last, ok := p.missAt[server]; ok && now.Sub(last) < policyMissRefresh {
		return false
	}
	if p.missAt == nil {
		p.missAt = make(map[string]time.Time)
	}
	p.missAt[server] = now
	return true
}

// decide evaluates a call. It returns the decision, the tool name as the
// server lists it, and how the tool was found.
func (p *Policy) decide(ctx context.Context, e *policy.Engine, server, tool string, invoke bool) (policy.Decision, string, toolListing) {
	h := p.handler.Load()
	if h == nil || !h.knownServer(server) {
		// No tool list to consult: a server that is not configured fails
		// in routing anyway.
		return e.Evaluate(server, tool, policy.Hints{}), tool, toolListed
	}
	t, name, listing := h.policyTool(ctx, p, server, tool, invoke)
	if listing != toolListed {
		if e.UnknownTools() == policy.ActionDeny {
			return e.UnknownDecision(), name, listing
		}
		return e.Evaluate(server, name, policy.Hints{}), name, listing
	}
	return e.Evaluate(server, name, toolHints(t)), name, listing
}

// Middleware enforces the policy on every tool call. A refused call never
// reaches the next stage (response cache, firewall, upstream).
func (p *Policy) Middleware() Middleware {
	return func(next Next) Next {
		return func(ctx context.Context, req *Request) (*Response, error) {
			e := p.Engine()
			if e == nil || req == nil {
				return next(ctx, req)
			}
			server, tool, args, invoke, ok := callTarget(req, p.serverNames)
			if !ok {
				return next(ctx, req)
			}
			d, tool, listing := p.decide(ctx, e, server, tool, invoke)
			switch d.Action {
			case policy.ActionAllow:
				p.record(ctx, "allow", server, d)
				return next(withPolicyDecision(ctx, d), req)
			case policy.ActionConfirm:
				if req.IsNotification() {
					p.audit(ctx, "confirm_unavailable", server, tool, d, args)
					return nil, nil
				}
				outcome, why := p.confirm(ctx, e, server, tool, args, d)
				p.audit(ctx, outcome, server, tool, d, args)
				if why == "" {
					return next(withPolicyDecision(ctx, d), req)
				}
				return policyRefusal(req, server, tool, d, outcome, why), nil
			default:
				outcome := "deny"
				if d.Rule == policy.RuleUnknownTools {
					outcome = "deny_unknown_tool"
				}
				p.audit(ctx, outcome, server, tool, d, args)
				if req.IsNotification() {
					return nil, nil
				}
				return policyRefusal(req, server, tool, d, outcome, denyReason(server, tool, d, listing)), nil
			}
		}
	}
}

// record counts a decision and tags the request's span with it.
func (p *Policy) record(ctx context.Context, outcome, server string, d policy.Decision) {
	RecordPolicyDecision(ctx, outcome, server)
	if telemetryActive.Load() {
		trace.SpanFromContext(ctx).SetAttributes(
			attribute.String("leanproxy.policy.decision", outcome),
			attribute.String("leanproxy.policy.rule", d.Label))
	}
}

// audit records and logs a deny or confirm decision: server, tool, rule,
// outcome and a hash of the redacted arguments, never the arguments.
func (p *Policy) audit(ctx context.Context, outcome, server, tool string, d policy.Decision, args json.RawMessage) {
	p.record(ctx, outcome, server, d)
	logger := slog.Default()
	if h := p.handler.Load(); h != nil {
		logger = h.logger
	}
	attrs := []any{"server", server, "tool", tool, "rule", d.Label, "action", string(d.Action), "outcome", outcome, "args_sha256", p.argsDigest(args)}
	switch outcome {
	case "confirm_approved", "confirm_approved_session", "confirm_cached":
		logger.Info("policy: tool call approved", attrs...)
	default:
		logger.Warn("policy: tool call refused", attrs...)
	}
}

// argsDigest is the first 16 hex digits of the SHA-256 of the redacted
// arguments ("" when there are none).
func (p *Policy) argsDigest(args json.RawMessage) string {
	if len(args) == 0 || string(args) == "null" {
		return ""
	}
	sum := sha256.Sum256([]byte(p.redactText(string(args))))
	return hex.EncodeToString(sum[:8])
}

// argsSummary is the redacted, single-line argument summary a confirmation
// shows (at most confirmSummaryChars characters).
func (p *Policy) argsSummary(args json.RawMessage) string {
	if len(args) == 0 || string(args) == "null" {
		return "{}"
	}
	// Marshaling a RawMessage compacts it.
	if b, err := json.Marshal(args); err == nil {
		args = b
	}
	s, _ := toolpin.StripInvisible(p.redactText(string(args)))
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > confirmSummaryChars {
		s = string(r[:confirmSummaryChars-1]) + "…"
	}
	return s
}

// confirm asks the user to approve a call. It returns the outcome and,
// when the call must be refused, why ("" when approved).
func (p *Policy) confirm(ctx context.Context, e *policy.Engine, server, tool string, args json.RawMessage, d policy.Decision) (outcome, why string) {
	s := ClientSessionFrom(ctx)
	key := server + "." + tool
	if s.policyApproved(key) {
		return "confirm_cached", ""
	}
	if s == nil || !s.Initialized() || !s.AcceptsRequests() || !formElicitation(s) {
		return "confirm_unavailable", "the client does not support MCP elicitation (form mode), so it cannot be asked"
	}
	name, _ := toolpin.StripInvisible(tool)
	params, err := json.Marshal(map[string]interface{}{
		"message": fmt.Sprintf("leanproxy-mcp policy (%s): allow %s.%s with arguments %s?", d.Label, server, name, p.argsSummary(args)),
		"requestedSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"decision": map[string]interface{}{
					"type":        "string",
					"title":       "Decision",
					"description": "Approve this call once, approve " + server + "." + name + " for the rest of this session, or deny it.",
					"enum":        []string{confirmApprove, confirmApproveSession, confirmDeny},
					"enumNames":   []string{"Approve", "Approve for this session", "Deny"},
				},
			},
			"required": []string{"decision"},
		},
	})
	if err != nil {
		return "confirm_unavailable", "the confirmation could not be built"
	}
	cctx, cancel := context.WithTimeout(ctx, e.ConfirmTimeout())
	defer cancel()
	result, rpcErr := s.Request(cctx, methodElicitationCreate, params)
	if rpcErr != nil {
		if errors.Is(cctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return "confirm_timeout", fmt.Sprintf("the user did not answer the confirmation within %s", e.ConfirmTimeout())
		}
		return "confirm_unavailable", "the confirmation request failed: " + rpcErr.Message
	}
	var answer struct {
		Action  string `json:"action"`
		Content struct {
			Decision string `json:"decision"`
		} `json:"content"`
	}
	_ = json.Unmarshal(result, &answer)
	if answer.Action != "accept" {
		return "confirm_denied", "the user declined the confirmation"
	}
	switch answer.Content.Decision {
	case confirmApprove:
		return "confirm_approved", ""
	case confirmApproveSession:
		s.approvePolicy(key)
		return "confirm_approved_session", ""
	default:
		return "confirm_denied", "the user denied it"
	}
}

// formElicitation reports whether the client can answer a form-mode
// elicitation: it declared the elicitation capability either empty (the
// pre-2025-11-25 form, meaning form mode) or with a form member.
func formElicitation(s *ClientSession) bool {
	raw, ok := s.capability(capabilityElicitation)
	if !ok {
		return false
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil {
		return false
	}
	return len(members) == 0 || s.HasSubCapability(capabilityElicitation, "form")
}

// denyReason explains a deny decision and its remedy.
func denyReason(server, tool string, d policy.Decision, listing toolListing) string {
	switch {
	case d.Rule == policy.RuleUnknownTools && listing == toolListUnavailable:
		return fmt.Sprintf("the tool list of %s is not available (the server is unreachable or failed tools/list), so %q cannot be checked against it and calls to tools a server does not advertise are refused (policy.unknown_tools: deny). Retry once the server is up; list_servers shows its state", server, tool)
	case d.Rule == policy.RuleUnknownTools:
		return fmt.Sprintf("%s does not advertise a tool named %q in its tools/list, and calls to tools a server does not advertise are refused (policy.unknown_tools: deny). Use search_tools (or list_tools) to find the tool's exact name", server, tool)
	case d.Rule == policy.RuleDefault:
		return fmt.Sprintf("no policy rule allows %s.%s and policy.default is deny. To allow it, add a rule {match: %q, action: allow} under policy.rules in the leanproxy config", server, tool, server+"."+tool)
	default:
		return fmt.Sprintf("%s.%s is denied by policy %s. To allow it, change that rule under policy.rules in the leanproxy config (`leanproxy-mcp policy check %s.%s` explains the decision)", server, tool, d.Label, server, tool)
	}
}

// policyRefusal is the JSON-RPC error of a refused call: code -32600 (as a
// call refused by tool pinning), a message saying what decided and how to
// change it, and machine-readable data. The upstream was not called.
func policyRefusal(req *Request, server, tool string, d policy.Decision, outcome, why string) *Response {
	var msg string
	switch outcome {
	case "deny", "deny_unknown_tool":
		msg = fmt.Sprintf("Call refused by leanproxy-mcp policy: %s. The tool was not called.", why)
	default:
		msg = fmt.Sprintf("Call refused by leanproxy-mcp policy: %s.%s requires confirmation (%s), but %s. The tool was not called. To allow it without confirmation, change that rule to `action: allow` under policy.rules in the leanproxy config.", server, tool, d.Label, why)
	}
	resp := errorResponse(req, ErrCodeInvalidRequest, msg)
	resp.Error.Data, _ = json.Marshal(map[string]string{
		"reason":   "policy",
		"server":   server,
		"tool":     tool,
		"decision": string(d.Action),
		"outcome":  outcome,
		"rule":     d.Label,
		"check":    "leanproxy-mcp policy check " + server + "." + tool,
	})
	return resp
}

type policyDecisionKey struct{}

// withPolicyDecision carries the decision of an allowed call to the later
// stages (the injection guard applies the rule's injection override).
func withPolicyDecision(ctx context.Context, d policy.Decision) context.Context {
	if d.Injection == nil {
		return ctx
	}
	return context.WithValue(ctx, policyDecisionKey{}, d.Injection)
}

// policyInjectionFrom returns the injection override of the call's policy
// rule, or nil.
func policyInjectionFrom(ctx context.Context) *policy.Injection {
	inj, _ := ctx.Value(policyDecisionKey{}).(*policy.Injection)
	return inj
}

// policyView applies the policy to a server's tools for discovery: denied
// tools are removed (their number is returned) and the names of the tools
// that need confirmation are returned. Unlike a call it never refreshes a
// tool list and records nothing.
func (h *Handler) policyView(server string, tools []Tool) (visible []Tool, confirm map[string]bool, hidden int) {
	e := h.policyEngine()
	if e == nil || e.Trivial() || len(tools) == 0 {
		return tools, nil, 0
	}
	visible = tools
	for i, t := range tools {
		switch e.Evaluate(server, t.Name, toolHints(t)).Action {
		case policy.ActionDeny:
			if hidden == 0 {
				visible = append(make([]Tool, 0, len(tools)-1), tools[:i]...)
			}
			hidden++
			continue
		case policy.ActionConfirm:
			if confirm == nil {
				confirm = make(map[string]bool)
			}
			confirm[t.Name] = true
		}
		if hidden > 0 {
			visible = append(visible, t)
		}
	}
	return visible, confirm, hidden
}

// policyExclude returns the search filter that drops denied tools (nil
// when the policy hides nothing).
func (h *Handler) policyExclude() func(server, name string) bool {
	e := h.policyEngine()
	if e == nil || !e.MayDeny() {
		return nil
	}
	return func(server, name string) bool {
		t, ok := h.cachedTool(server, name)
		if !ok {
			t = Tool{Name: name}
		}
		return e.Evaluate(server, name, toolHints(t)).Action == policy.ActionDeny
	}
}

// policyConfirm reports whether the policy asks for confirmation before
// calling tool t of server.
func (h *Handler) policyConfirm(server string, t Tool) bool {
	e := h.policyEngine()
	return e != nil && !e.Trivial() && e.Evaluate(server, t.Name, toolHints(t)).Action == policy.ActionConfirm
}

// policyHiddenNote is the discovery note about tools the policy hides.
func policyHiddenNote(n int) string {
	return fmt.Sprintf("(%d tool(s) hidden by the leanproxy-mcp policy; see `leanproxy-mcp doctor security`)", n)
}
