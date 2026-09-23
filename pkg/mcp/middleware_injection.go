package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer/injection"
)

// InjectionRedacted replaces every string value of a tool call's arguments
// when the injection policy resolves to the `redact` action.
const InjectionRedacted = "[CONTENT_REDACTED]"

// InjectionGuard is the prompt-injection stage of the pipeline. It runs the
// classifier over the request params and applies the dispatcher's policy:
//
//   - block      → JSON-RPC error, the request is not forwarded;
//   - quarantine → a tool result with isError: true telling the model the
//     call was quarantined, the request is not forwarded;
//   - redact     → every string value of the tool arguments is replaced by
//     InjectionRedacted (params stay valid JSON), then forwarded;
//   - log        → forwarded unchanged.
//
// A nil *InjectionGuard, or one built without a classifier, is disabled.
type InjectionGuard struct {
	state atomic.Pointer[injectionState]
}

type injectionState struct {
	classifier *injection.Classifier
	dispatcher *injection.Dispatcher
}

// NewInjectionGuard builds a guard from the `injection:` config block. A nil
// cfg (no block) or `enabled: false` yields a disabled guard, matching the
// historical behavior of `serve`.
func NewInjectionGuard(cfg *injection.Config) *InjectionGuard {
	g := &InjectionGuard{}
	g.Configure(cfg)
	return g
}

// Configure (re)builds the classifier and dispatcher from cfg.
func (g *InjectionGuard) Configure(cfg *injection.Config) {
	if cfg == nil {
		g.Set(nil, nil)
		return
	}
	classifier, err := cfg.BuildClassifier()
	if err != nil {
		slog.Warn("injection: failed to build classifier", "error", err)
		classifier = nil
	}
	g.Set(classifier, cfg.BuildDispatcher())
}

// Set installs an already-built classifier and dispatcher. Either being nil
// disables the guard.
func (g *InjectionGuard) Set(c *injection.Classifier, d *injection.Dispatcher) {
	if c == nil || d == nil {
		g.state.Store(nil)
		return
	}
	g.state.Store(&injectionState{classifier: c, dispatcher: d})
}

// Enabled reports whether requests are classified.
func (g *InjectionGuard) Enabled() bool {
	return g != nil && g.state.Load() != nil
}

// PolicyCount returns the number of dispatcher rules in effect.
func (g *InjectionGuard) PolicyCount() int {
	if g == nil {
		return 0
	}
	st := g.state.Load()
	if st == nil {
		return 0
	}
	return len(st.dispatcher.Rules())
}

// Check classifies req and applies the policy. It returns a non-nil response
// when the request must not be forwarded (block, quarantine). For the
// redact action req.Params is rewritten in place and nil is returned.
func (g *InjectionGuard) Check(req *Request) *Response {
	if g == nil || req == nil || len(req.Params) == 0 {
		return nil
	}
	st := g.state.Load()
	if st == nil {
		return nil
	}

	result := st.classifier.Classify(string(req.Params))
	if result.RiskScore == 0 {
		return nil
	}

	action := st.dispatcher.Dispatch(result)
	switch action.Action {
	case injection.ActionBlock:
		return errorResponse(req, ErrCodeInvalidRequest, action.Message)
	case injection.ActionQuarantine:
		return quarantineResponse(req, action.Message)
	case injection.ActionRedact:
		neutralized, err := neutralizeArguments(req.Method, req.Params)
		if err != nil {
			// Fail closed: params we cannot rewrite are not forwarded.
			slog.Warn("injection: cannot redact params, blocking request", "error", err)
			return errorResponse(req, ErrCodeInvalidRequest,
				fmt.Sprintf("BLOCKED: payload risk score %d and params could not be redacted", action.RiskScore))
		}
		req.Params = neutralized
		return nil
	default:
		return nil
	}
}

// Middleware runs Check before next. A blocked or quarantined notification is
// dropped silently (JSON-RPC forbids replying to notifications).
func (g *InjectionGuard) Middleware() Middleware {
	return func(next Next) Next {
		return func(ctx context.Context, req *Request) (*Response, error) {
			if resp := g.Check(req); resp != nil {
				if req.IsNotification() {
					return nil, nil
				}
				return resp, nil
			}
			return next(ctx, req)
		}
	}
}

func quarantineResponse(req *Request, message string) *Response {
	result, err := json.Marshal(ToolsCallResult{
		Content: []ContentBlock{{
			Type: "text",
			Text: "This tool call was quarantined by leanproxy-mcp's prompt-injection guard and was not executed. " + message,
		}},
		IsError: true,
	})
	if err != nil {
		return errorResponse(req, ErrCodeInvalidRequest, message)
	}
	return &Response{JSONRPC: JSONRPCVersion, Result: result, ID: req.ID}
}

// routingKeys are the invoke_tool envelope fields that select the target
// server and tool. They are kept so a redacted call still routes; every
// other string is replaced.
var routingKeys = map[string]bool{
	"server": true, "tool": true, "server_name": true, "tool_name": true,
}

var errParamsNotObject = errors.New("params are not a JSON object")

// neutralizeArguments replaces the string values of a tool call's arguments
// with InjectionRedacted and returns valid JSON:
//
//   - tools/call invoke_tool → everything under `arguments` except the
//     routing keys (server/tool), nested `arguments` included;
//   - tools/call (any other tool) → everything under `arguments`;
//   - invoke_tool method (serve gateway envelope) → everything except the
//     routing keys (server_name/tool_name);
//   - any other method → everything under `arguments` when present,
//     otherwise every string in params.
func neutralizeArguments(method string, params json.RawMessage) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.UseNumber()
	var root map[string]interface{}
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("%w: %v", errParamsNotObject, err)
	}
	if root == nil {
		return nil, errParamsNotObject
	}

	args, hasArgs := root["arguments"]
	switch {
	case method == MethodToolsCall && root["name"] == "invoke_tool":
		if m, ok := args.(map[string]interface{}); ok {
			root["arguments"] = replaceStrings(m, routingKeys)
		} else if hasArgs {
			root["arguments"] = replaceStrings(args, nil)
		}
	case method == "invoke_tool":
		root = replaceStrings(root, routingKeys).(map[string]interface{})
	case hasArgs:
		root["arguments"] = replaceStrings(args, nil)
	default:
		root = replaceStrings(root, nil).(map[string]interface{})
	}
	return json.Marshal(root)
}

// replaceStrings returns v with every string value replaced by
// InjectionRedacted. Keys listed in keep are preserved at the top level only.
func replaceStrings(v interface{}, keep map[string]bool) interface{} {
	switch t := v.(type) {
	case string:
		return InjectionRedacted
	case map[string]interface{}:
		for k, val := range t {
			if keep[k] {
				continue
			}
			t[k] = replaceStrings(val, nil)
		}
		return t
	case []interface{}:
		for i, val := range t {
			t[i] = replaceStrings(val, nil)
		}
		return t
	default:
		return v
	}
}
