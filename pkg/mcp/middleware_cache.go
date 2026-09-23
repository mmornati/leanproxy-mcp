package mcp

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp/responsecache"
)

// ResponseCache is the opt-in, exact-match `tools/call` response cache
// (issue #299): off by default, allowlisted read tools only, keyed on the
// pre-redaction request, bounded LRU by bytes. Unlike the semantic cache it
// replaces for tools/call, it never does embedding-similarity lookups.
//
// # Pipeline ordering (read this before moving the middleware)
//
// ResponseCache.Middleware() MUST be installed OUTERMOST — before (i.e.
// first in the slice passed to Chain/Handler.Use, ahead of) the firewall's
// Redaction.ResponseMiddleware() and Redaction.RequestMiddleware():
//
//	handler.Use(append([]mcp.Middleware{cache.Middleware()}, firewall.Middlewares()...)...)
//
// Chain makes the first entry outermost, so the request-side effect is that
// this middleware's func runs and reads req.Params BEFORE
// Redaction.RequestMiddleware() has a chance to redact them: the cache key
// is therefore derived from the original, unredacted arguments (required so
// two calls that differ only in a secret value never share a key — see
// responsecache.Key).
//
// On the way back, because this middleware is outermost, the *Response it
// gets from next() has already passed through Redaction.ResponseMiddleware()
// (which sits just inside it), i.e. it is the REDACTED response. That is
// exactly what gets stored: the cached value is safe to replay to any future
// caller.
//
// A cache hit short-circuits before calling next() at all, so on a hit no
// firewall stage and no upstream call runs for that request — the cached
// bytes already went through redaction once, when they were stored.
type ResponseCache struct {
	cfg   *responsecache.Config
	store *responsecache.Cache
}

// NewResponseCache builds a ResponseCache from cfg (the `response_cache:`
// config block). A nil cfg, or one with Enabled: false (the default),
// produces a disabled cache: Middleware() then just calls next unconditionally
// and no memory is allocated for a store.
func NewResponseCache(cfg *responsecache.Config) *ResponseCache {
	normalized := responsecache.Config{}
	if cfg != nil {
		normalized = *cfg
	}
	if err := normalized.Normalize(); err != nil {
		slog.Warn("response_cache: invalid config, disabling", "error", err)
		return &ResponseCache{cfg: &responsecache.Config{}}
	}

	rc := &ResponseCache{cfg: &normalized}
	if normalized.Enabled {
		rc.store = responsecache.New(normalized.TTLValue, normalized.MaxBytes, normalized.MaxEntryBytes)
	}
	return rc
}

// Enabled reports whether the cache is on and ready to serve lookups.
func (rc *ResponseCache) Enabled() bool {
	return rc != nil && rc.cfg != nil && rc.cfg.Enabled && rc.store != nil
}

// Allows reports whether the cache is on and its policy (the allowlist of
// idempotent read tools) permits caching the tool identified by identity
// ("server.tool"). Other caches, e.g. `serve`'s semantic cache, use it so
// that every cache honors the same policy.
func (rc *ResponseCache) Allows(identity string) bool {
	return rc.Enabled() && identity != "" && rc.cfg.Allowed(identity)
}

// Stats returns the underlying store's counters, or a zero Stats when the
// cache is disabled.
func (rc *ResponseCache) Stats() responsecache.Stats {
	if rc == nil || rc.store == nil {
		return responsecache.Stats{}
	}
	return rc.store.Stats()
}

// Middleware returns the cache stage. See the ResponseCache doc comment for
// the ordering requirement.
func (rc *ResponseCache) Middleware() Middleware {
	return func(next Next) Next {
		return func(ctx context.Context, req *Request) (*Response, error) {
			if !rc.Enabled() || req == nil || req.IsNotification() {
				return next(ctx, req)
			}

			server, tool, args, ok := extractToolCall(req)
			if !ok {
				return next(ctx, req)
			}
			identity := cacheIdentity(server, tool)
			if !rc.cfg.Allowed(identity) {
				return next(ctx, req)
			}

			key := responsecache.Key(server, tool, args)
			if cached, hit := rc.store.Get(key); hit {
				var resp Response
				if err := json.Unmarshal(cached, &resp); err == nil {
					resp.ID = req.ID
					return &resp, nil
				}
				slog.Warn("response_cache: cached entry could not be decoded, treating as a miss",
					"tool", identity)
			}

			resp, err := next(ctx, req)
			if err == nil && responseCacheable(resp) {
				if data, merr := json.Marshal(resp); merr == nil {
					rc.store.Set(key, data)
				}
			}
			return resp, err
		}
	}
}

// cacheIdentity is the "server.tool" string matched against the allowlist
// (Config.Tools) and used in the key derivation. When server is unknown
// (plain `tools/call` outside the gateway envelope, where the tool name may
// already be namespaced, e.g. "github.get_file_contents") it is just tool.
func cacheIdentity(server, tool string) string {
	if server == "" {
		return tool
	}
	return server + "." + tool
}

// invokeToolGatewayName is the virtual tool name the pkg/mcp Handler
// (`server run --stdio`) exposes for its invoke_tool gateway envelope; see
// handleInvokeTool / invokeToolEnvelope.
const invokeToolGatewayName = "invoke_tool"

// extractToolCall extracts the (server, tool, arguments) identifying a tool
// call from req, covering both front ends:
//
//   - `tools/call` with name != "invoke_tool": the direct call form used by
//     `server run --stdio` for a (possibly already namespaced) tool.
//   - `tools/call` with name == "invoke_tool": the stdio gateway's own
//     virtual tool, whose arguments carry {server, tool, arguments}.
//   - top-level `invoke_tool` method: the `serve` gateway envelope, whose
//     params carry {server_name, tool_name, arguments}.
//
// ok is false when req does not address a single tool (so it is never
// cacheable), including `list_servers`/`list_tools`, which have no stable
// per-tool identity to key on.
func extractToolCall(req *Request) (server, tool string, args json.RawMessage, ok bool) {
	if req == nil {
		return "", "", nil, false
	}
	switch req.Method {
	case MethodToolsCall:
		var p ToolsCallParams
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			return "", "", nil, false
		}
		switch p.Name {
		case invokeToolGatewayName:
			var env invokeToolEnvelope
			if err := json.Unmarshal(p.Arguments, &env); err != nil || env.Server == "" || env.Tool == "" {
				return "", "", nil, false
			}
			return env.Server, env.Tool, env.Arguments, true
		case "list_servers", "list_tools":
			return "", "", nil, false
		default:
			return "", p.Name, p.Arguments, true
		}
	case invokeToolGatewayName:
		var env struct {
			ServerName string          `json:"server_name"`
			ToolName   string          `json:"tool_name"`
			Arguments  json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &env); err != nil || env.ServerName == "" || env.ToolName == "" {
			return "", "", nil, false
		}
		return env.ServerName, env.ToolName, env.Arguments, true
	default:
		return "", "", nil, false
	}
}

// responseCacheable reports whether resp may be stored: never a transport
// error, a JSON-RPC error, or a ToolsCallResult with isError: true.
func responseCacheable(resp *Response) bool {
	if resp == nil || resp.Error != nil {
		return false
	}
	if len(resp.Result) == 0 {
		return true
	}
	var probe struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp.Result, &probe); err == nil && probe.IsError {
		return false
	}
	return true
}
