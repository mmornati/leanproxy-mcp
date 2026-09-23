package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

// methodCancelled is the MCP notification a client sends to abandon one of
// its in-flight requests (params.requestId).
const methodCancelled = "notifications/cancelled" //nolint:misspell // MCP protocol method name

// defaultStdioShutdownGrace is how long shutdown/EOF waits for in-flight
// requests to finish before canceling them.
const defaultStdioShutdownGrace = 5 * time.Second

// stdioCancelWait bounds how long the front end waits, after canceling the
// requests still running at the end of the grace period, for them to return.
// A handler that ignores its context must not hang the shutdown forever.
const stdioCancelWait = 2 * time.Second

// requestHandler is the part of *mcp.Handler the stdio front end needs.
type requestHandler interface {
	HandleRequest(ctx context.Context, req *mcp.Request) (*mcp.Response, error)
}

// stdioFrontendOptions configures serveStdio. Zero values mean defaults.
type stdioFrontendOptions struct {
	// MaxConcurrent caps the requests handled in parallel
	// (server.max_concurrent_requests).
	MaxConcurrent int
	// ShutdownGrace is how long shutdown/EOF waits for in-flight requests.
	ShutdownGrace time.Duration
	Logger        *slog.Logger
}

// stdioEnvelope decodes one incoming JSON-RPC message. ID is kept raw so an
// absent id (a notification) can be told apart from an explicit "id": null
// (a request, which gets a response with id null).
type stdioEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      json.RawMessage `json:"id"`
}

// inflightRequest is one request being handled.
type inflightRequest struct {
	cancel context.CancelFunc
	// canceledByClient is set when a cancel notification named this
	// request; its response is then dropped, as the MCP spec says the
	// receiver should not answer a canceled request.
	canceledByClient atomic.Bool
}

// stdioFrontend is the concurrent JSON-RPC loop behind `server run --stdio`:
// one reader, one goroutine per request (bounded by a semaphore), a single
// mutex-protected writer that flushes after every message.
type stdioFrontend struct {
	handler requestHandler
	logger  *slog.Logger
	grace   time.Duration

	writeMu sync.Mutex
	w       *bufio.Writer

	sem chan struct{}
	wg  sync.WaitGroup

	mu       sync.Mutex
	inflight map[string]*inflightRequest
}

// serveStdio reads newline-delimited JSON-RPC messages from r and writes
// responses to w, handling requests concurrently. It returns after EOF or a
// `shutdown` request, once in-flight requests have drained (or been
// canceled after the grace period). It does not close any pool: the caller
// does that after serveStdio returns.
func serveStdio(ctx context.Context, r io.Reader, w io.Writer, handler requestHandler, opts stdioFrontendOptions) error {
	maxConc := opts.MaxConcurrent
	if maxConc <= 0 {
		maxConc = migrate.DefaultMaxConcurrentRequests
	}
	grace := opts.ShutdownGrace
	if grace <= 0 {
		grace = defaultStdioShutdownGrace
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	f := &stdioFrontend{
		handler:  handler,
		logger:   logger,
		grace:    grace,
		w:        bufio.NewWriter(w),
		sem:      make(chan struct{}, maxConc),
		inflight: make(map[string]*inflightRequest),
	}

	// Every request context derives from reqCtx so the end of the grace
	// period can cancel whatever is still running in one call.
	reqCtx, cancelAll := context.WithCancel(ctx)
	defer cancelAll()

	shutdownReq, readErr := f.readLoop(reqCtx, bufio.NewReader(r))

	f.drain(cancelAll)

	if shutdownReq != nil {
		// Handled only after the drain: the handler's shutdown closes the
		// pool, which must not happen under requests still in flight.
		f.dispatch(context.WithoutCancel(ctx), shutdownReq, nil)
	}
	return readErr
}

// readLoop reads and dispatches messages until EOF, a read error, a
// `shutdown` request (returned, not yet handled) or ctx ends.
func (f *stdioFrontend) readLoop(ctx context.Context, reader *bufio.Reader) (*mcp.Request, error) {
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if req := f.handleLine(ctx, line); req != nil {
				return req, nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				f.logger.Info("stdin closed, shutting down")
				return nil, nil
			}
			f.logger.Error("failed to read stdin", "error", err)
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, nil
		}
	}
}

// handleLine parses one line and starts handling it. It returns the request
// when it is a `shutdown` request, which the caller handles after draining.
func (f *stdioFrontend) handleLine(ctx context.Context, line []byte) *mcp.Request {
	line = trimStdioNewline(line)
	if len(bytes.TrimSpace(line)) == 0 {
		return nil
	}

	var env stdioEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		// Never log the raw line: it can carry secrets (tool arguments,
		// tokens). The length is enough to correlate.
		f.logger.Warn("failed to parse JSON-RPC request", "error", err, "bytes", len(line))
		f.write(&mcp.Response{
			JSONRPC: mcp.JSONRPCVersion,
			Error:   mcp.NewError(mcp.ErrCodeParseError, "invalid JSON-RPC request"),
			ID:      nil,
		})
		return nil
	}

	isNotification := len(env.ID) == 0
	req := &mcp.Request{JSONRPC: env.JSONRPC, Method: env.Method, Params: env.Params}
	if !isNotification {
		var id interface{}
		if err := json.Unmarshal(env.ID, &id); err == nil {
			req.ID = id
		}
	}

	if isNotification {
		f.handleNotification(ctx, req)
		return nil
	}
	if req.Method == mcp.MethodShutdown {
		f.logger.Info("shutdown request received")
		return req
	}

	// Concurrency cap: when every slot is taken the reader waits here
	// instead of rejecting the request.
	select {
	case f.sem <- struct{}{}:
	case <-ctx.Done():
		f.write(&mcp.Response{
			JSONRPC: mcp.JSONRPCVersion,
			Error:   mcp.NewError(mcp.ErrCodeInternalError, "server is shutting down"),
			ID:      req.ID,
		})
		return nil
	}

	rctx, cancel := context.WithCancel(ctx)
	entry := &inflightRequest{cancel: cancel}
	key, keyed := requestIDKey(req.ID)
	if keyed {
		f.mu.Lock()
		f.inflight[key] = entry
		f.mu.Unlock()
	}

	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		defer func() { <-f.sem }()
		defer cancel()
		defer func() {
			if keyed {
				f.mu.Lock()
				if f.inflight[key] == entry {
					delete(f.inflight, key)
				}
				f.mu.Unlock()
			}
		}()
		f.dispatch(rctx, req, entry)
	}()
	return nil
}

// handleNotification processes a message without an id. It never writes a
// response. A cancel notification is handled here; other notifications go
// through the handler (e.g. notifications/initialized) and their result, if
// any, is discarded.
func (f *stdioFrontend) handleNotification(ctx context.Context, req *mcp.Request) {
	if req.Method == methodCancelled {
		f.cancelRequest(req.Params)
		return
	}
	if _, err := f.handler.HandleRequest(ctx, req); err != nil {
		f.logger.Debug("notification handler error", "method", req.Method, "error", err)
	}
}

// cancelRequest cancels the in-flight request named by params.requestId.
func (f *stdioFrontend) cancelRequest(params json.RawMessage) {
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if len(params) == 0 || json.Unmarshal(params, &p) != nil || len(p.RequestID) == 0 {
		f.logger.Debug("ignoring cancel notification without a requestId")
		return
	}
	var id interface{}
	if json.Unmarshal(p.RequestID, &id) != nil {
		return
	}
	key, ok := requestIDKey(id)
	if !ok {
		return
	}
	f.mu.Lock()
	entry := f.inflight[key]
	f.mu.Unlock()
	if entry == nil {
		// Already finished (or never existed): nothing to do, per spec.
		f.logger.Debug("cancel notification for an unknown or finished request", "id", id)
		return
	}
	f.logger.Info("canceling in-flight request", "id", id)
	entry.canceledByClient.Store(true)
	entry.cancel()
}

// dispatch runs one request through the handler and writes its response.
// entry is nil for requests that cannot be canceled by the client.
func (f *stdioFrontend) dispatch(ctx context.Context, req *mcp.Request, entry *inflightRequest) {
	resp, err := f.handler.HandleRequest(ctx, req)
	if err != nil {
		f.logger.Error("handler error", "error", err, "method", req.Method)
	}
	if entry != nil && entry.canceledByClient.Load() {
		f.logger.Debug("dropping response to a request the client canceled", "id", req.ID)
		return
	}
	if resp == nil {
		// Every request must be answered.
		resp = &mcp.Response{
			JSONRPC: mcp.JSONRPCVersion,
			Error:   mcp.NewError(mcp.ErrCodeInternalError, "internal error: no response"),
			ID:      req.ID,
		}
	}
	f.write(resp)
}

// drain waits up to the grace period for in-flight requests, then cancels
// the rest and waits (bounded) for them to return.
func (f *stdioFrontend) drain(cancelAll context.CancelFunc) {
	done := make(chan struct{})
	go func() {
		f.wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(f.grace)
	defer timer.Stop()
	select {
	case <-done:
		return
	case <-timer.C:
	}

	f.mu.Lock()
	remaining := len(f.inflight)
	f.mu.Unlock()
	f.logger.Warn("shutdown grace period elapsed, canceling in-flight requests", "grace", f.grace, "remaining", remaining)
	cancelAll()

	wait := time.NewTimer(stdioCancelWait)
	defer wait.Stop()
	select {
	case <-done:
	case <-wait.C:
		f.logger.Warn("in-flight requests did not return after cancellation")
	}
}

// write marshals resp and writes it as one line, flushed, under the writer
// mutex so concurrent responses never interleave.
func (f *stdioFrontend) write(resp *mcp.Response) {
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	writeStdioResponse(f.w, resp)
}

// requestIDKey normalizes a decoded JSON-RPC id (a JSON number decodes as
// float64, a string as string) into a map key. Numbers and strings get
// distinct prefixes: 1 and "1" are different ids.
func requestIDKey(id interface{}) (string, bool) {
	switch v := id.(type) {
	case string:
		return "s:" + v, true
	case float64:
		return "n:" + strconv.FormatFloat(v, 'g', -1, 64), true
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return "n:" + v.String(), true
		}
		return "n:" + strconv.FormatFloat(f, 'g', -1, 64), true
	default:
		return "", false
	}
}
