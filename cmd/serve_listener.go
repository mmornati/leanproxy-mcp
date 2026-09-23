package cmd

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	lperrors "github.com/mmornati/leanproxy-mcp/pkg/errors"
	"github.com/mmornati/leanproxy-mcp/pkg/gateway"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
)

// The `serve` TCP listener speaks newline-delimited JSON-RPC. Because any
// local process, and any web page through a cross-protocol "simple" POST,
// can reach a loopback port, every connection must first authenticate
// (#298):
//
//	{"jsonrpc":"2.0","method":"auth","params":{"token":"<token>"}}
//
// A connection whose first line is anything else (including an HTTP
// request line) is closed without executing or answering anything.

const (
	// serveAuthMethod is the method of the handshake line.
	serveAuthMethod = "auth"
	// serveAuthTimeout bounds how long a new connection may take to send
	// its first line, so idle unauthenticated connections cannot hold
	// connection slots.
	serveAuthTimeout = 10 * time.Second
	// maxServeAuthLineBytes caps the first line when authentication is on:
	// an unauthenticated peer must not make the proxy buffer max_line_bytes.
	maxServeAuthLineBytes = 4096
	// serveReadBufferSize is the bufio.Reader size of one connection.
	serveReadBufferSize = 64 << 10
	// acceptBackoffMin and acceptBackoffMax bound the exponential backoff
	// after an accept error (e.g. EMFILE), so a persistent error does not
	// spin the accept loop.
	acceptBackoffMin = 5 * time.Millisecond
	acceptBackoffMax = time.Second
)

// errLineTooLong is returned by readBoundedLine when a line exceeds its cap.
var errLineTooLong = errors.New("line exceeds the maximum message size")

// serveConnOptions configures one `serve` connection. Zero numeric values
// mean the defaults.
type serveConnOptions struct {
	// AuthToken is the token the first line must carry. It is required
	// unless NoAuth is set: an empty token with NoAuth unset rejects every
	// connection (fail closed).
	AuthToken string
	// NoAuth disables the handshake (--no-auth, loopback only).
	NoAuth bool
	// MaxLineBytes caps one message (server.max_line_bytes).
	MaxLineBytes int
	// MaxConcurrent caps the requests one connection runs in parallel
	// (server.max_concurrent_requests). The reader waits at the cap.
	MaxConcurrent int
	// AuthTimeout bounds the wait for the first line.
	AuthTimeout time.Duration
	// onHandlerStart is a test hook called with the number of request
	// handlers running on the connection right after one starts.
	onHandlerStart func(active int64)
}

func (o serveConnOptions) withDefaults() serveConnOptions {
	if o.MaxLineBytes <= 0 {
		o.MaxLineBytes = migrate.DefaultMaxLineBytes
	}
	if o.MaxConcurrent <= 0 {
		o.MaxConcurrent = migrate.DefaultMaxConcurrentRequests
	}
	if o.AuthTimeout <= 0 {
		o.AuthTimeout = serveAuthTimeout
	}
	return o
}

// serveListener is the accept loop of `serve`.
type serveListener struct {
	router   Router
	gateway  gateway.GatewayTools
	pool     Pool
	conn     serveConnOptions
	maxConns int
}

// Serve accepts connections until ln is closed, then returns nil. Each
// connection runs in its own goroutine, at most maxConns at once; a
// connection over the cap is closed right away. Accept errors back off
// exponentially (5 ms up to 1 s).
func (s *serveListener) Serve(ln net.Listener) error {
	maxConns := s.maxConns
	if maxConns <= 0 {
		maxConns = migrate.DefaultMaxConnections
	}
	slots := make(chan struct{}, maxConns)
	var backoff time.Duration
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			backoff = nextAcceptBackoff(backoff)
			slog.Warn("accept error", "error", err, "retry_in", backoff)
			time.Sleep(backoff)
			continue
		}
		backoff = 0
		select {
		case slots <- struct{}{}:
		default:
			slog.Warn("too many serve connections, closing new connection", "remote", conn.RemoteAddr(), "max_connections", maxConns)
			_ = conn.Close()
			continue
		}
		slog.Debug("connection accepted", "remote", conn.RemoteAddr())
		go func() {
			defer func() { <-slots }()
			handleConnection(conn, s.router, s.gateway, s.pool, s.conn)
		}()
	}
}

func nextAcceptBackoff(prev time.Duration) time.Duration {
	if prev <= 0 {
		return acceptBackoffMin
	}
	return min(prev*2, acceptBackoffMax)
}

// readBoundedLine reads one line (including its '\n') of at most maxBytes
// bytes. A longer line returns errLineTooLong after reading at most
// maxBytes plus one buffer of input, so memory stays bounded by the cap.
// At EOF it returns the partial line read so far together with the error.
func readBoundedLine(r *bufio.Reader, maxBytes int) ([]byte, error) {
	var line []byte
	for {
		frag, err := r.ReadSlice('\n')
		if len(line)+len(frag) > maxBytes {
			return nil, errLineTooLong
		}
		line = append(line, frag...)
		if err == nil {
			return line, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, err
		}
	}
}

// httpRequestPrefixes are the request-line starts of the HTTP methods a
// browser or HTTP client can send.
var httpRequestPrefixes = [][]byte{
	[]byte("GET "), []byte("POST "), []byte("PUT "), []byte("OPTIONS "),
	[]byte("HEAD "), []byte("DELETE "), []byte("PATCH "), []byte("CONNECT "),
}

// looksLikeHTTP reports whether a first line is an HTTP request line rather
// than JSON-RPC.
func looksLikeHTTP(line []byte) bool {
	for _, p := range httpRequestPrefixes {
		if bytes.HasPrefix(line, p) {
			return true
		}
	}
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		// JSON: an "HTTP/1." inside a string value is not a request line.
		return false
	}
	return bytes.Contains(line, []byte("HTTP/1."))
}

// authLine is the decoded handshake line.
type authLine struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  struct {
		Token string `json:"token"`
	} `json:"params"`
	ID json.RawMessage `json:"id"`
}

// parseAuthLine decodes line as the auth handshake. ok is false when it is
// not an auth message at all.
func parseAuthLine(line []byte) (authLine, bool) {
	var a authLine
	if err := json.Unmarshal(line, &a); err != nil {
		return authLine{}, false
	}
	if a.JSONRPC != "2.0" || a.Method != serveAuthMethod {
		return authLine{}, false
	}
	return a, true
}

// deadlineSetter is the part of net.Conn used for the handshake timeout.
type deadlineSetter interface {
	SetReadDeadline(t time.Time) error
}

// serveHandshake reads and checks the first line of a connection. It
// returns ok=false when the connection must be closed without further
// processing, and pending when the first line is a request to run (only
// with NoAuth).
func serveHandshake(conn io.ReadWriter, reader *bufio.Reader, writer *bufio.Writer, writerMu *sync.Mutex, opts serveConnOptions, remote string) (pending []byte, ok bool) {
	if d, isConn := conn.(deadlineSetter); isConn {
		_ = d.SetReadDeadline(time.Now().Add(opts.AuthTimeout))
		defer func() { _ = d.SetReadDeadline(time.Time{}) }()
	}
	maxFirst := opts.MaxLineBytes
	if !opts.NoAuth {
		maxFirst = min(maxFirst, maxServeAuthLineBytes)
	}
	line, err := readBoundedLine(reader, maxFirst)
	if err != nil {
		// No complete first line: EOF, timeout, or too long. Nothing was
		// executed; close.
		if !errors.Is(err, io.EOF) {
			slog.Warn("serve connection closed before a valid first line", "remote", remote, "error", err)
		}
		return nil, false
	}
	line = trimNewline(line)
	if looksLikeHTTP(line) {
		slog.Warn("rejected HTTP request on the serve JSON-RPC port", "remote", remote)
		return nil, false
	}

	auth, isAuth := parseAuthLine(line)
	if opts.NoAuth {
		if isAuth {
			// Clients may always authenticate; the token is ignored.
			writeAuthResult(writer, writerMu, auth)
			return nil, true
		}
		return line, true
	}
	if !isAuth || opts.AuthToken == "" ||
		subtle.ConstantTimeCompare([]byte(auth.Params.Token), []byte(opts.AuthToken)) != 1 {
		slog.Warn("serve connection failed authentication", "remote", remote)
		return nil, false
	}
	writeAuthResult(writer, writerMu, auth)
	return nil, true
}

// writeAuthResult answers an auth line that carries an id. The handshake is
// normally a notification (no id) and gets no answer.
func writeAuthResult(writer *bufio.Writer, writerMu *sync.Mutex, auth authLine) {
	if len(auth.ID) == 0 {
		return
	}
	var id interface{}
	if err := json.Unmarshal(auth.ID, &id); err != nil {
		return
	}
	writeResponseAsync(writer, writerMu, &proxy.JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  json.RawMessage(`{"authenticated":true}`),
		ID:      id,
	})
}

// handleConnection serves one `serve` connection: handshake, then one
// handler goroutine per request line, at most opts.MaxConcurrent at once.
// When the reader hits EOF or an error, the connection context is canceled
// (so in-flight upstream calls stop) before waiting for the handlers.
func handleConnection(conn io.ReadWriter, r Router, gt gateway.GatewayTools, p Pool, opts serveConnOptions) {
	opts = opts.withDefaults()
	if c, isConn := conn.(net.Conn); isConn {
		defer c.Close()
	}
	remote := ""
	if c, isConn := conn.(net.Conn); isConn {
		remote = c.RemoteAddr().String()
	}

	connCtx, connCancel := context.WithCancel(context.Background())
	defer connCancel()

	reader := bufio.NewReaderSize(conn, serveReadBufferSize)
	writer := bufio.NewWriter(conn)
	writerMu := &sync.Mutex{}

	pending, ok := serveHandshake(conn, reader, writer, writerMu, opts, remote)
	if !ok {
		return
	}

	// One MCP client session per connection (#307): it carries the
	// protocol version this client negotiated, and receives the
	// list_changed notifications through the connection's writer.
	var session *mcp.ClientSession
	if h := serveMCPHandler.Load(); h != nil {
		var closeSession func()
		session, closeSession = h.OpenSession(func(method string, params json.RawMessage) {
			writeNotificationAsync(writer, writerMu, method, params)
		})
		defer closeSession()
		// The connection is bidirectional: the relay may send this client
		// server-to-client requests (sampling, elicitation, roots; #308).
		session.EnableRequests(func(id, method string, params json.RawMessage) error {
			return writeServerRequestAsync(writer, writerMu, id, method, params)
		})
		connCtx = mcp.WithClientSession(connCtx, session)
	}

	sem := make(chan struct{}, opts.MaxConcurrent)
	var active atomic.Int64
	var wg sync.WaitGroup
	inflight := &serveInflight{entries: make(map[string]*inflightRequest)}

	dispatch := func(line []byte) bool {
		select {
		case sem <- struct{}{}:
		case <-connCtx.Done():
			return false
		}
		n := active.Add(1)
		if opts.onHandlerStart != nil {
			opts.onHandlerStart(n)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				active.Add(-1)
				<-sem
			}()
			if isBatchRequest(line) {
				handleBatchRequestAsync(connCtx, line, writer, writerMu, r, gt, p)
			} else {
				handleCancelableRequest(connCtx, line, inflight, writer, writerMu, r, gt, p)
			}
		}()
		return true
	}

	if len(bytes.TrimSpace(pending)) > 0 {
		dispatch(pending)
	}

	for {
		line, err := readBoundedLine(reader, opts.MaxLineBytes)
		if errors.Is(err, errLineTooLong) {
			slog.Warn("serve message exceeds server.max_line_bytes, closing connection", "remote", remote, "max_line_bytes", opts.MaxLineBytes)
			writeErrorAsync(writer, writerMu, nil, lperrors.ErrCodeInvalidRequest, "message exceeds the maximum size")
			break
		}
		if err != nil {
			// A partial line at EOF is dropped: the client is gone.
			if !errors.Is(err, io.EOF) {
				slog.Debug("serve read error", "remote", remote, "error", err)
			}
			break
		}
		line = trimNewline(line)
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		// The client's answers to server-to-client requests and its
		// cancel notifications are handled inline, never behind the
		// concurrency cap: the requests holding every slot may be the
		// ones waiting for them (#308).
		if handleControlLine(line, session, inflight) {
			continue
		}
		if !dispatch(line) {
			break
		}
	}

	// The client is gone (or misbehaved): cancel what is still running,
	// then wait for the handlers to return.
	connCancel()
	wg.Wait()
}

// serveInflight is the per-connection registry of requests a client may
// cancel (with the MCP cancel notification), keyed by requestIDKey.
type serveInflight struct {
	mu      sync.Mutex
	entries map[string]*inflightRequest
}

// handleControlLine handles a line that must not wait for a concurrency
// slot: the client's answer to a server-to-client request, or a cancel
// notification. It reports whether line was one.
func handleControlLine(line []byte, session *mcp.ClientSession, inflight *serveInflight) bool {
	if isBatchRequest(line) {
		return false
	}
	// Cheap pre-filter so an ordinary request (possibly megabytes of
	// params) is not decoded twice: a control line is either a response
	// (no "method" member) or a cancel notification.
	if bytes.Contains(line, []byte(`"method"`)) && !bytes.Contains(line, []byte(methodCancelled)) {
		return false
	}
	var env stdioEnvelope
	if json.Unmarshal(line, &env) != nil {
		return false
	}
	if isClientResponse(env.Method, env.ID, env.Result, env.Error) {
		if !session.DeliverResponse(env.ID, env.Result, env.Error) {
			slog.Debug("dropping a response to an unknown or canceled server-to-client request")
		}
		return true
	}
	if env.Method != methodCancelled || len(env.ID) != 0 {
		return false
	}
	var p struct {
		RequestID interface{} `json:"requestId"`
	}
	if len(env.Params) == 0 || json.Unmarshal(env.Params, &p) != nil {
		return true
	}
	key, ok := requestIDKey(p.RequestID)
	if !ok {
		return true
	}
	inflight.mu.Lock()
	entry := inflight.entries[key]
	inflight.mu.Unlock()
	if entry != nil {
		slog.Info("canceling in-flight serve request", "id", p.RequestID)
		entry.canceledByClient.Store(true)
		entry.cancel()
	}
	return true
}

// handleCancelableRequest is handleSingleRequestAsync for a request the
// client may cancel: it runs under its own context, registered by id, and
// its response is dropped when the client canceled it (per the MCP spec,
// a canceled request gets no answer).
func handleCancelableRequest(ctx context.Context, line []byte, inflight *serveInflight, writer *bufio.Writer, writerMu *sync.Mutex, r Router, gt gateway.GatewayTools, p Pool) {
	req, err := proxy.ParseJSONRPCRequest(line)
	if err != nil {
		writeErrorAsync(writer, writerMu, nil, lperrors.ErrCodeParseError, "Parse error")
		return
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	entry := &inflightRequest{cancel: cancel}
	key, keyed := requestIDKey(req.ID)
	if keyed {
		inflight.mu.Lock()
		inflight.entries[key] = entry
		inflight.mu.Unlock()
		defer func() {
			inflight.mu.Lock()
			if inflight.entries[key] == entry {
				delete(inflight.entries, key)
			}
			inflight.mu.Unlock()
		}()
	}
	resp := serveRequest(rctx, req, r, gt, p)
	if entry.canceledByClient.Load() {
		slog.Debug("dropping response to a serve request the client canceled", "id", req.ID)
		return
	}
	if resp != nil {
		writeResponseAsync(writer, writerMu, resp)
	}
}

// writeServerRequestAsync writes one server-to-client request on a serve
// connection, serialized with the responses.
func writeServerRequestAsync(writer *bufio.Writer, mu *sync.Mutex, id, method string, params json.RawMessage) error {
	data, err := marshalServerRequest(id, method, params)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	writeJSONLine(writer, data)
	return writer.Flush()
}

// writeNotificationAsync writes one server-to-client notification on a
// serve connection, serialized with the responses.
func writeNotificationAsync(writer *bufio.Writer, mu *sync.Mutex, method string, params json.RawMessage) {
	data, err := json.Marshal(jsonRPCNotification{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		slog.Warn("failed to marshal notification", "method", method, "error", err)
		return
	}
	mu.Lock()
	defer mu.Unlock()
	writeJSONLine(writer, data)
	if err := writer.Flush(); err != nil {
		slog.Debug("failed to flush notification", "method", method, "error", err)
	}
}
