package mcp

import "context"

// Next is one step of the MCP request pipeline. It receives a (possibly
// already transformed) request and returns the response that travels back
// towards the client. A nil response means "nothing to write" (for example a
// notification).
type Next func(ctx context.Context, req *Request) (*Response, error)

// Middleware wraps a Next with cross-cutting behavior (redaction, injection
// checks, caching, metering, ...). A middleware may:
//
//   - transform the request before calling next,
//   - short-circuit by returning a response without calling next,
//   - transform the response returned by next.
//
// Middlewares must be safe for concurrent use: front ends may run several
// requests through the same chain at once.
type Middleware func(next Next) Next

// Chain composes mws around inner. mws[0] is the outermost middleware: it
// sees the request first and the response last. Nil entries are skipped.
func Chain(inner Next, mws ...Middleware) Next {
	for i := len(mws) - 1; i >= 0; i-- {
		if mws[i] != nil {
			inner = mws[i](inner)
		}
	}
	return inner
}

// Use appends middlewares to the handler's pipeline. The first middleware
// ever registered is the outermost one; the handler's own method dispatch is
// always the innermost step. Use is safe to call concurrently with
// HandleRequest, but a request already in flight keeps the chain it started
// with, so register middlewares during startup.
func (h *Handler) Use(mws ...Middleware) {
	h.pipelineMu.Lock()
	defer h.pipelineMu.Unlock()
	h.middlewares = append(h.middlewares, mws...)
	chain := Chain(h.dispatch, h.middlewares...)
	h.pipeline.Store(&chain)
}

// errorResponse builds a JSON-RPC error response for req.
func errorResponse(req *Request, code int, message string) *Response {
	var id interface{}
	if req != nil {
		id = req.ID
	}
	return &Response{
		JSONRPC: JSONRPCVersion,
		Error:   NewError(code, message),
		ID:      id,
	}
}
