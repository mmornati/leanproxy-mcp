package mcp

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer"
)

// RedactionFailedMessage is returned to the client when a request's params
// could not be safely redacted. The request is never forwarded in that case.
const RedactionFailedMessage = "Secret redaction failed; request not forwarded"

// ResponseRedactionFailedMessage replaces a response that could not be
// safely redacted. The original response is never written to the client.
const ResponseRedactionFailedMessage = "Secret redaction failed; response withheld"

// Redaction is the secret-redaction stage of the pipeline. It owns the
// regex redactor built from the `bouncer:` config block and exposes it both
// as middlewares (RequestMiddleware, ResponseMiddleware) and as primitives
// for front ends that need to redact at an extra point (for example before
// writing to a cache).
//
// The redactor is swapped atomically, so Configure can be called at any time
// (e.g. on SIGHUP) while requests are in flight. A nil *Redaction is valid
// and behaves as "redaction disabled".
type Redaction struct {
	redactor atomic.Pointer[bouncer.Redactor]
}

// NewRedaction builds a Redaction from cfg. A nil cfg (no `bouncer:` block)
// enables the built-in patterns; only an explicit `enabled: false` disables
// redaction.
func NewRedaction(cfg *bouncer.Config) *Redaction {
	r := &Redaction{}
	r.Configure(cfg)
	return r
}

// Configure (re)builds the redactor from cfg with the same defaults as
// NewRedaction.
func (r *Redaction) Configure(cfg *bouncer.Config) {
	r.redactor.Store(cfg.NewRedactor(bouncer.NewAlertManager(false)))
}

// SetRedactor installs an already-built redactor; nil disables redaction.
func (r *Redaction) SetRedactor(red *bouncer.Redactor) {
	r.redactor.Store(red)
}

// Redactor returns the active redactor, or nil when redaction is disabled.
func (r *Redaction) Redactor() *bouncer.Redactor {
	if r == nil {
		return nil
	}
	return r.redactor.Load()
}

// Enabled reports whether secrets are being redacted.
func (r *Redaction) Enabled() bool { return r.Redactor() != nil }

// PatternCount returns the number of active redaction patterns.
func (r *Redaction) PatternCount() int {
	red := r.Redactor()
	if red == nil {
		return 0
	}
	return len(red.Patterns())
}

// RedactRequest redacts req.Params in place, including every nested value
// (so the `arguments` of tools/call and of an invoke_tool envelope are
// covered). Params that are not valid JSON are scanned byte by byte instead
// of being passed through. An error means the params could not be safely
// redacted and the request must not be forwarded.
func (r *Redaction) RedactRequest(req *Request) error {
	return r.RedactRequestContext(context.Background(), req)
}

// RedactRequestContext is RedactRequest with a context so redaction counts
// can be recorded against the request's telemetry span (issue #317).
func (r *Redaction) RedactRequestContext(ctx context.Context, req *Request) error {
	red := r.Redactor()
	if red == nil || req == nil || len(req.Params) == 0 {
		return nil
	}
	redacted, count, err := red.RedactJSON(req.Params)
	if err != nil {
		return err
	}
	req.Params = redacted
	RecordRedaction(ctx, int64(count))
	return nil
}

// RedactResponse redacts resp.Result, resp.Error.Message and resp.Error.Data
// in place.
func (r *Redaction) RedactResponse(resp *Response) error {
	return r.RedactResponseContext(context.Background(), resp)
}

// RedactResponseContext is RedactResponse with a context so redaction
// counts can be recorded against the request's telemetry span (issue #317).
func (r *Redaction) RedactResponseContext(ctx context.Context, resp *Response) error {
	red := r.Redactor()
	if red == nil || resp == nil {
		return nil
	}
	if len(resp.Result) > 0 {
		redacted, count, err := red.RedactJSON(resp.Result)
		if err != nil {
			return err
		}
		resp.Result = redacted
		RecordRedaction(ctx, int64(count))
	}
	if resp.Error != nil {
		resp.Error.Message = red.RedactText(resp.Error.Message)
		if len(resp.Error.Data) > 0 {
			redacted, count, err := red.RedactJSON(resp.Error.Data)
			if err != nil {
				return err
			}
			resp.Error.Data = redacted
			RecordRedaction(ctx, int64(count))
		}
	}
	return nil
}

// RedactText redacts a free-form string (log lines, error strings).
func (r *Redaction) RedactText(s string) string {
	red := r.Redactor()
	if red == nil || s == "" {
		return s
	}
	return red.RedactText(s)
}

// RequestMiddleware redacts request params before anything downstream
// inspects, forwards, caches or persists them. It fails closed: if the
// params cannot be redacted the client gets a JSON-RPC error carrying
// RedactionFailedMessage and next is not called.
func (r *Redaction) RequestMiddleware() Middleware {
	return func(next Next) Next {
		return func(ctx context.Context, req *Request) (*Response, error) {
			if err := r.RedactRequestContext(ctx, req); err != nil {
				slog.Warn("redaction: request params could not be redacted, not forwarding",
					"method", req.Method, "error", err)
				return errorResponse(req, ErrCodeInternalError, RedactionFailedMessage), nil
			}
			return next(ctx, req)
		}
	}
}

// ResponseMiddleware redacts every response produced downstream — results,
// error messages and error data alike, whether they come from an upstream
// server, a gateway tool (list_tools text, error hints) or another
// middleware. It fails closed: a response that cannot be redacted is
// replaced by a JSON-RPC error carrying ResponseRedactionFailedMessage.
func (r *Redaction) ResponseMiddleware() Middleware {
	return func(next Next) Next {
		return func(ctx context.Context, req *Request) (*Response, error) {
			resp, err := next(ctx, req)
			if resp == nil {
				return resp, err
			}
			if rerr := r.RedactResponseContext(ctx, resp); rerr != nil {
				slog.Warn("redaction: response could not be redacted, withholding it",
					"method", req.Method, "error", rerr)
				return &Response{
					JSONRPC: JSONRPCVersion,
					Error:   NewError(ErrCodeInternalError, ResponseRedactionFailedMessage),
					ID:      resp.ID,
				}, err
			}
			return resp, err
		}
	}
}
