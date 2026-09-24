//go:build codemode

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/mmornati/leanproxy-mcp/pkg/codemode"
)

// Code mode (issue #325, experimental, `-tags codemode` only): the
// execute_code gateway tool runs a short JavaScript program in a sandbox
// process (pkg/codemode); each tool the program calls is a new tools/call
// request sent through the handler's WHOLE pipeline (HandleRequest), from
// the outermost middleware in: exposure, telemetry, governor, tool pinning,
// policy (deny and confirm), response cache, redaction and the injection
// guard see a call made from code exactly as they see one the model makes
// with invoke_tool. Code mode adds a way to call tools, never a way around
// a check.

// ExecuteCodeToolName is the code mode gateway tool.
const ExecuteCodeToolName = "execute_code"

// codeModeCallKey marks the context of a tool call made from code:
// execute_code is refused there (no nested sandboxes).
type codeModeCallKey struct{}

// CodeMode serves execute_code.
type CodeMode struct {
	h      *Handler
	limits codemode.Limits
	opts   codemode.Options
	logger *slog.Logger

	runs, failures, calls atomic.Int64
}

// NewCodeMode builds the execute_code stage from the `code_mode:` block.
// opts sets the sandbox process (its zero value runs this binary's hidden
// worker command); its Limits and Servers are filled in per call.
func NewCodeMode(h *Handler, cfg *codemode.Config, opts codemode.Options) *CodeMode {
	return &CodeMode{h: h, limits: cfg.Limits(), opts: opts, logger: h.logger}
}

// Summary is the one-line startup log of code mode.
func (c *CodeMode) Summary() string {
	l := c.limits
	return fmt.Sprintf("code mode (EXPERIMENTAL): execute_code enabled; limits: timeout %s, cpu %s, memory %d MiB, %d calls (%d concurrent), output %d bytes; kernel limits: %t",
		l.Timeout, l.CPUTime, l.MaxMemoryMB, l.MaxCalls, l.MaxConcurrentCalls, l.MaxOutputBytes, codemode.HardLimits)
}

// Stats are the code mode counters.
func (c *CodeMode) Stats() (runs, failures, calls int64) {
	return c.runs.Load(), c.failures.Load(), c.calls.Load()
}

// Middleware lists execute_code in tools/list and serves it. Install it
// innermost (after every other stage): the execute_code request itself
// then passes policy, redaction and the injection guard like any tool
// call, and its answer goes back out through them.
func (c *CodeMode) Middleware() Middleware {
	return func(next Next) Next {
		return func(ctx context.Context, req *Request) (*Response, error) {
			if req == nil || req.IsNotification() {
				return next(ctx, req)
			}
			switch req.Method {
			case MethodToolsList:
				resp, err := next(ctx, req)
				if resp != nil && resp.Error == nil && len(resp.Result) > 0 {
					resp.Result = c.withExecuteCodeTool(ctx, resp.Result)
				}
				return resp, err
			case MethodToolsCall:
				var p ToolsCallParams
				if json.Unmarshal(req.Params, &p) != nil || p.Name != ExecuteCodeToolName {
					return next(ctx, req)
				}
				return c.execute(ctx, req, p.Arguments), nil
			}
			return next(ctx, req)
		}
	}
}

type executeCodeArgs struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

func (c *CodeMode) execute(ctx context.Context, req *Request, raw json.RawMessage) *Response {
	if ctx.Value(codeModeCallKey{}) != nil {
		return toolText(req.ID, true, "execute_code cannot be called from code")
	}
	var args executeCodeArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return &Response{JSONRPC: JSONRPCVersion, Error: NewError(ErrCodeInvalidParams, fmt.Sprintf("invalid execute_code arguments: %v", err)), ID: req.ID}
		}
	}
	switch strings.ToLower(args.Language) {
	case "", "js", "javascript":
	default:
		return &Response{JSONRPC: JSONRPCVersion, Error: NewError(ErrCodeInvalidParams, fmt.Sprintf("execute_code: unsupported language %q (only \"js\")", args.Language)), ID: req.ID}
	}

	c.runs.Add(1)
	opts := c.opts
	opts.Limits = c.limits
	opts.Servers = c.h.pool.ListServers()
	var seq atomic.Int64
	callCtx := context.WithValue(ctx, codeModeCallKey{}, true)
	call := func(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
		c.calls.Add(1)
		return c.callTool(ctx, fmt.Sprintf("codemode-%d", seq.Add(1)), server, tool, args)
	}
	res, err := codemode.Run(callCtx, args.Code, opts, call)
	if err != nil {
		c.failures.Add(1)
		var re *codemode.RunError
		if !errors.As(err, &re) {
			c.logger.Warn("code_mode: execute_code failed", "error", err)
			return toolText(req.ID, true, "execute_code failed: "+err.Error())
		}
		c.audit(re.Calls)
		c.logger.Info("code_mode: execute_code finished", "outcome", re.Kind, "calls", len(re.Calls), "error", re.Message)
		return toolText(req.ID, true, withLogs(fmt.Sprintf("execute_code failed (%s): %s", re.Kind, re.Message), re.Logs))
	}
	c.audit(res.Calls)
	c.logger.Info("code_mode: execute_code finished", "outcome", "ok", "calls", len(res.Calls), "duration", res.Duration, "output_bytes", len(res.Output))
	out := res.Output
	if out == "" {
		out = "(the code returned no value)"
	}
	return toolText(req.ID, false, withLogs(out, res.Logs))
}

// audit logs every tool call a program made, with its outcome as the
// program saw it (a tool error included).
func (c *CodeMode) audit(calls []codemode.CallRecord) {
	for _, rec := range calls {
		outcome := "ok"
		if rec.Err != "" {
			outcome = rec.Err
		}
		c.logger.Info("code_mode: tool call", "server", rec.Server, "tool", rec.Tool, "duration", rec.Duration, "result_bytes", rec.ResultBytes, "outcome", outcome)
	}
}

// callTool is the program's one capability: a tools/call on invoke_tool,
// through the full pipeline. A JSON-RPC error (a policy refusal, a pinning
// block, an injection block, an upstream error) is the program's error.
func (c *CodeMode) callTool(ctx context.Context, id, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	envelope := map[string]interface{}{"server": server, "tool": tool}
	if len(args) > 0 {
		envelope["arguments"] = args
	}
	params, err := json.Marshal(map[string]interface{}{"name": invokeToolGatewayName, "arguments": envelope})
	if err != nil {
		return nil, err
	}
	resp, err := c.h.HandleRequest(ctx, &Request{JSONRPC: JSONRPCVersion, ID: id, Method: MethodToolsCall, Params: params})
	switch {
	case err != nil:
		return nil, err
	case resp == nil:
		return nil, errors.New("no response")
	case resp.Error != nil:
		return nil, errors.New(resp.Error.Message)
	}
	return resp.Result, nil
}

func withLogs(text string, logs []string) string {
	if len(logs) == 0 {
		return text
	}
	return text + "\n\n[console]\n" + strings.Join(logs, "\n")
}

// withExecuteCodeTool appends execute_code to a tools/list result.
func (c *CodeMode) withExecuteCodeTool(ctx context.Context, result json.RawMessage) json.RawMessage {
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
		if json.Unmarshal(t, &probe) == nil && probe.Name == ExecuteCodeToolName {
			return result
		}
	}
	tool := c.executeCodeTool()
	if c.h.sessionFor(ctx).AtLeast(ProtocolVersion20250326) {
		// A program can call any tool the policy allows, destructive ones
		// included (each still gated by the policy's confirm).
		destructive, openWorld := true, true
		tool.Annotations = &ToolAnnotations{DestructiveHint: &destructive, OpenWorldHint: &openWorld}
	}
	encoded, err := json.Marshal(tool)
	if err != nil {
		return result
	}
	body["tools"], err = json.Marshal(append(tools, encoded))
	if err != nil {
		return result
	}
	out, err := json.Marshal(body)
	if err != nil {
		return result
	}
	return out
}

func (c *CodeMode) executeCodeTool() Tool {
	servers := c.h.pool.ListServers()
	sort.Strings(servers)
	l := c.limits
	desc := fmt.Sprintf("Run a short JavaScript program (the body of an async function) that calls tools and filters their results; only the value it returns reaches you. "+
		"Call a tool with `await tools.<server>.<tool>(args)` (tools[\"my-server\"] for names with dashes); servers: %s. "+
		"A call returns the tool's structuredContent, else its text parsed as JSON when it is JSON, else the text; a tool error or a policy refusal throws. "+
		"Every call goes through LeanProxy's policy and redaction. No filesystem, network, require or timers. "+
		"Limits: %s, %d tool calls, %d bytes of output.",
		strings.Join(servers, ", "), l.Timeout, l.MaxCalls, l.MaxOutputBytes)
	schema, _ := json.Marshal(map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"code":     map[string]string{"type": "string", "description": "JavaScript: await tool calls, return the answer."},
			"language": map[string]interface{}{"type": "string", "enum": []string{"js"}},
		},
		"required": []string{"code"},
	})
	return Tool{Name: ExecuteCodeToolName, Description: desc, InputSchema: schema}
}
