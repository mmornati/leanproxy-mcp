package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/mmornati/leanproxy-mcp/pkg/toolpin"
)

// Tool pinning (issue #310).
//
// ToolPins connects a toolpin.Pinner to the pipeline and the handler:
//
//   - every tools/list answer the background refresh fetches is handed to
//     the pinner (Handler.observePins) before it reaches the tool cache, so
//     drift is detected on startup, tools/list_changed and restarts;
//   - list_tools and search_tools hide the tools the policy blocks and
//     carry a one-line warning about the ones awaiting approval;
//   - its Middleware refuses calls to blocked tools (block mode) in both
//     front ends, whatever form the call takes: tools/call with a
//     namespaced name, invoke_tool (stdio's gateway tool or serve's
//     method) and serve's namespaced "server.tool" methods.
//
// Invisible and bidi characters are stripped from tool metadata before it
// reaches a client in every mode (sanitizeTools), pinning on or off.
type ToolPins struct {
	pinner  atomic.Pointer[toolpin.Pinner]
	servers atomic.Pointer[func() []string]
	// refresh, set by Handler.SetToolPins, refreshes one server's tools
	// and waits for the result.
	refresh atomic.Pointer[func(ctx context.Context, server string)]
}

// NewToolPins wraps p (nil: pinning disabled) and counts its events in the
// telemetry.
func NewToolPins(p *toolpin.Pinner) *ToolPins {
	g := &ToolPins{}
	g.Set(p)
	return g
}

// Set installs p (nil disables pinning).
func (g *ToolPins) Set(p *toolpin.Pinner) {
	if g == nil {
		return
	}
	p.OnEvent(func(ev toolpin.Event) {
		RecordToolPinEvent(context.Background(), string(ev.Kind), ev.Server)
	})
	g.pinner.Store(p)
}

// Pinner returns the installed pinner (nil when disabled).
func (g *ToolPins) Pinner() *toolpin.Pinner {
	if g == nil {
		return nil
	}
	return g.pinner.Load()
}

// Enabled reports whether a pinner is installed.
func (g *ToolPins) Enabled() bool { return g.Pinner() != nil }

// SetServerNames tells the middleware which servers exist, to split
// namespaced tool names ("server_tool"). Without it the pinned servers are
// used.
func (g *ToolPins) SetServerNames(fn func() []string) {
	if g == nil || fn == nil {
		return
	}
	g.servers.Store(&fn)
}

func (g *ToolPins) serverNames(p *toolpin.Pinner) []string {
	names := p.Servers()
	if fn := g.servers.Load(); fn != nil {
		names = append(names, (*fn)()...)
	}
	return names
}

// Summary is the one-line startup status.
func (g *ToolPins) Summary() string {
	p := g.Pinner()
	if p == nil {
		return "tool pinning disabled"
	}
	return fmt.Sprintf("tool pinning: mode %s, pins in %s", p.Mode(), p.Store().Path())
}

// Middleware refuses, in block mode, calls to tools that are new, changed
// or whose server's identity changed, with a JSON-RPC error naming the
// approval command. The upstream is not called.
func (g *ToolPins) Middleware() Middleware {
	return func(next Next) Next {
		return func(ctx context.Context, req *Request) (*Response, error) {
			p := g.Pinner()
			if p == nil || p.Mode() != toolpin.ModeBlock || req == nil {
				return next(ctx, req)
			}
			server, tool, ok := g.target(p, req)
			if !ok {
				return next(ctx, req)
			}
			if !p.Observed(server) {
				// Right after startup the tool cache (and the pins) may
				// predate what the upstream serves now: compare first.
				if fn := g.refresh.Load(); fn != nil {
					(*fn)(ctx, server)
				}
			}
			status, blocked := p.Check(server, tool)
			if !blocked {
				return next(ctx, req)
			}
			identity := p.IdentityPending(server)
			RecordToolPinEvent(ctx, "call_blocked", server)
			if telemetryActive.Load() {
				trace.SpanFromContext(ctx).SetAttributes(
					attribute.String("leanproxy.tool_pin.status", string(status)),
					attribute.Bool("leanproxy.tool_pin.blocked", true))
			}
			approveTool := tool
			if identity {
				approveTool = "" // approve the server (--all)
			}
			resp := errorResponse(req, ErrCodeInvalidRequest, toolpin.BlockedMessage(server, tool, status, identity))
			resp.Error.Data, _ = json.Marshal(map[string]string{
				"reason":  "tool_pinning",
				"server":  server,
				"tool":    tool,
				"status":  string(status),
				"approve": toolpin.ApproveCommand(server, approveTool),
			})
			return resp, nil
		}
	}
}

// target resolves the upstream server and tool a request would call.
func (g *ToolPins) target(p *toolpin.Pinner, req *Request) (server, tool string, ok bool) {
	server, tool, _, invoke, ok := callTarget(req, func() []string { return g.serverNames(p) })
	if ok && invoke {
		// invoke_tool; it tolerates a repeated server prefix
		// ("github_create_issue" on server "github").
		if st, _ := p.Check(server, tool); st == toolpin.StatusUnknown {
			if rest, cut := strings.CutPrefix(tool, server+"_"); cut && rest != "" {
				tool = rest
			}
		}
	}
	return server, tool, ok
}

// SetToolPins installs the tool pinning state the handler consults (nil:
// disabled). The handler's configured servers are used to split
// namespaced tool names.
func (h *Handler) SetToolPins(g *ToolPins) {
	if g != nil {
		if g.servers.Load() == nil {
			g.SetServerNames(h.pool.ListServers)
		}
		refresh := func(ctx context.Context, server string) { h.awaitPinned(ctx, []string{server}) }
		g.refresh.Store(&refresh)
	}
	h.pins.Store(g)
}

// awaitPinned makes sure, in block mode, that servers' current tool lists
// were compared with the pins by this process before a decision is made
// from them: it refreshes the servers not compared yet, in parallel, waiting
// at most searchColdWait. A server that cannot be reached keeps the pin
// file's verdict.
func (h *Handler) awaitPinned(ctx context.Context, servers []string) {
	p := h.pinner()
	if p == nil || p.Mode() != toolpin.ModeBlock {
		return
	}
	var pending []string
	for _, s := range servers {
		if !p.Observed(s) && h.knownServer(s) && !h.refreshFailed(s) {
			pending = append(pending, s)
		}
	}
	if len(pending) == 0 {
		return
	}
	wctx, cancel := context.WithTimeout(ctx, searchColdWait)
	defer cancel()
	var wg sync.WaitGroup
	for _, s := range pending {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			_ = h.RefreshServerTools(wctx, s)
		}(s)
	}
	wg.Wait()
}

func (h *Handler) pinner() *toolpin.Pinner {
	return h.pins.Load().Pinner()
}

// observePins hands a server's freshly listed tools to the pinner.
func (h *Handler) observePins(server string, tools []Tool) {
	p := h.pinner()
	if p == nil {
		return
	}
	var info *toolpin.ServerInfo
	if res := h.serverInitializeResult(server); res != nil && res.ServerInfo.Name != "" {
		info = &toolpin.ServerInfo{Name: res.ServerInfo.Name, Version: res.ServerInfo.Version}
	}
	defs := make([]toolpin.Definition, len(tools))
	for i, t := range tools {
		defs[i] = pinDefinition(t)
	}
	p.Observe(server, info, defs)
}

// pinDefinition is the pinned part of a tool (see toolpin.Hash).
func pinDefinition(t Tool) toolpin.Definition {
	d := toolpin.Definition{
		Name:         t.Name,
		Title:        t.Title,
		Description:  t.Description,
		InputSchema:  t.InputSchema,
		OutputSchema: t.OutputSchema,
	}
	if t.Annotations != nil {
		d.Annotations, _ = json.Marshal(t.Annotations)
	}
	return d
}

// sanitizeTools strips invisible and bidi characters (toolpin.IsInvisible)
// from the metadata a client sees: title, description, every string value
// of inputSchema and outputSchema, and the annotations' title. Tools
// without any are returned as they are.
func sanitizeTools(tools []Tool) []Tool {
	var out []Tool
	for i, t := range tools {
		changed := false
		var c bool
		t.Title, c = toolpin.StripInvisible(t.Title)
		changed = changed || c
		t.Description, c = toolpin.StripInvisible(t.Description)
		changed = changed || c
		t.InputSchema, c = toolpin.StripInvisibleJSON(t.InputSchema)
		changed = changed || c
		t.OutputSchema, c = toolpin.StripInvisibleJSON(t.OutputSchema)
		changed = changed || c
		if t.Annotations != nil && t.Annotations.Title != "" {
			if title, c := toolpin.StripInvisible(t.Annotations.Title); c {
				a := *t.Annotations
				a.Title = title
				t.Annotations = &a
				changed = true
			}
		}
		if !changed {
			continue
		}
		if out == nil {
			out = make([]Tool, len(tools))
			copy(out, tools)
		}
		out[i] = t
	}
	if out == nil {
		return tools
	}
	return out
}

// pinView applies the pinning policy to server's tools for discovery:
// in block mode the blocked tools are removed; the returned notice is the
// one-line warning (or hidden-tools note) to show, "" when none.
func (h *Handler) pinView(server string, tools []Tool) ([]Tool, string) {
	p := h.pinner()
	if p == nil {
		return tools, ""
	}
	pending, identity := p.Unapproved(server)
	if len(pending) == 0 && !identity {
		return tools, ""
	}
	if p.Mode() == toolpin.ModeBlock {
		visible := make([]Tool, 0, len(tools))
		var hidden []string
		for _, t := range tools {
			if _, blocked := p.Check(server, t.Name); blocked {
				hidden = append(hidden, t.Name)
				continue
			}
			visible = append(visible, t)
		}
		if len(hidden) == 0 {
			return tools, ""
		}
		reason := "new or changed since approval"
		if identity {
			reason = "the server's identity changed since approval"
		}
		return visible, fmt.Sprintf("Tool pinning: %d tool(s) of %s hidden until approved (%s): %s. Review with `leanproxy-mcp tools pins diff %s`.",
			len(hidden), server, reason, strings.Join(hidden, ", "), server)
	}
	return tools, pinWarning(server, pending, identity)
}

// pinWarning is warn mode's one-line warning for a server.
func pinWarning(server string, pending []toolpin.Unapproved, identity bool) string {
	parts := make([]string, 0, len(pending)+1)
	if identity {
		parts = append(parts, "server identity changed")
	}
	for _, u := range pending {
		part := u.Tool + " (" + string(u.Status)
		if u.Severity.AtLeast(toolpin.SeverityMedium) {
			part += ", " + string(u.Severity) + "-severity scanner finding"
		}
		parts = append(parts, part+")")
	}
	return fmt.Sprintf("WARNING (tool pinning): %s changed since approval: %s. Review with `leanproxy-mcp tools pins diff %s`, approve with `leanproxy-mcp tools pins approve %s <tool>|--all`.",
		server, strings.Join(parts, ", "), server, server)
}

// pinExclude returns the search filter that drops blocked tools (nil when
// nothing is blocked).
func (h *Handler) pinExclude() func(server, name string) bool {
	p := h.pinner()
	if p == nil || p.Mode() != toolpin.ModeBlock || !p.HasPending() {
		return nil
	}
	return func(server, name string) bool {
		_, blocked := p.Check(server, name)
		return blocked
	}
}
