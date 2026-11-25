// Package bridge connects an MCP client that only speaks stdio to a
// Streamable HTTP MCP endpoint, the shared LeanProxy gateway in practice.
//
// Some clients (Claude Desktop's config file, older IDE plugins) can only
// start a local command. Pointing them at `server run --stdio` gives each
// of them its own proxy and its own copy of every upstream server. The
// bridge is that local command instead: a thin process that owns no
// upstream server and relays JSON-RPC between the client's stdin/stdout
// and the gateway.
//
// It relays messages verbatim. It never interprets tool calls or results:
// every check (redaction, policy, pinning, ...) happens in the gateway.
package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	headerSession  = "Mcp-Session-Id"
	headerProtocol = "MCP-Protocol-Version"

	methodInitialize  = "initialize"
	methodInitialized = "notifications/initialized"

	// maxLineBytes bounds one message read from the client or from an SSE
	// event of the gateway.
	maxLineBytes = 64 << 20

	// listenRetry is the pause between two attempts to open the stream of
	// server-initiated messages.
	listenRetry = 2 * time.Second
)

// Options configure a bridge.
type Options struct {
	// URL is the gateway's MCP endpoint, e.g. http://127.0.0.1:8765/mcp.
	URL string
	// Token is the gateway's bearer token; empty for a --no-auth gateway.
	Token string
	// Client is the HTTP client to use; nil means a client with no overall
	// timeout (responses may be long-lived streams).
	Client *http.Client
	Logger *slog.Logger
}

// Bridge relays one stdio client to the gateway.
type Bridge struct {
	opts   Options
	client *http.Client
	log    *slog.Logger

	outMu sync.Mutex
	out   io.Writer

	mu sync.Mutex
	// session and protocol are the gateway session of this client.
	session  string
	protocol string
	// initLine and initializedLine are the client's own handshake, kept to
	// replay it when the gateway forgot the session (it restarted).
	initLine        []byte
	initializedLine []byte
	// listening is true once the stream of server-initiated messages was
	// started for the current session.
	listening bool
	// reinit serializes re-initializations.
	reinit sync.Mutex
}

// New returns a bridge that writes the gateway's messages to out.
func New(opts Options, out io.Writer) *Bridge {
	client := opts.Client
	if client == nil {
		client = &http.Client{}
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Bridge{opts: opts, client: client, log: logger, out: out}
}

// envelope is what the bridge needs to know about a message.
type envelope struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

func (e envelope) isRequest() bool { return e.Method != "" && len(e.ID) > 0 && string(e.ID) != "null" }

// Run relays messages until in ends or ctx is canceled, then ends the
// gateway session. Each line of in is one JSON-RPC message.
func (b *Bridge) Run(ctx context.Context, in io.Reader) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	reader := bufio.NewReaderSize(in, 64<<10)
	for {
		line, err := readLine(reader)
		if len(bytes.TrimSpace(line)) > 0 {
			msg := append([]byte(nil), bytes.TrimSpace(line)...)
			var env envelope
			if json.Unmarshal(msg, &env) != nil {
				b.log.Warn("bridge: dropping a line that is not JSON")
			} else if env.Method == methodInitialize || env.Method == methodInitialized {
				// The handshake goes first and in order: everything after
				// it needs the session it creates.
				b.forward(ctx, msg, env)
			} else {
				wg.Add(1)
				go func() {
					defer wg.Done()
					b.forward(ctx, msg, env)
				}()
			}
		}
		if err != nil {
			break
		}
	}
	// The client closed its side: let the calls in flight answer, briefly,
	// then end the session so the gateway frees it at once.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	case <-ctx.Done():
	}
	b.endSession()
	return nil
}

// readLine reads one line of any length up to maxLineBytes.
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxLineBytes {
			return nil, fmt.Errorf("bridge: message larger than %d bytes", maxLineBytes)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, err
	}
}

// forward sends one client message to the gateway and writes what comes
// back. A request that cannot be delivered is answered with a JSON-RPC
// error, so the client never waits forever.
func (b *Bridge) forward(ctx context.Context, msg []byte, env envelope) {
	err := b.post(ctx, msg, env, true)
	if err == nil {
		return
	}
	if ctx.Err() != nil {
		return
	}
	b.log.Warn("bridge: gateway request failed", "method", env.Method, "error", err)
	if env.isRequest() {
		b.writeError(env.ID, err)
	}
}

// post sends msg. retry allows one transparent re-initialization when the
// gateway no longer knows the session.
func (b *Bridge) post(ctx context.Context, msg []byte, env envelope, retry bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.opts.URL, bytes.NewReader(msg))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	isInit := env.Method == methodInitialize
	b.setHeaders(req, !isInit)

	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach the LeanProxy gateway at %s: %w (is it running? try `leanproxy-mcp daemon status`)", b.opts.URL, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound && !isInit && retry:
		// The gateway restarted and forgot the session: redo the client's
		// handshake, then send the message again.
		_, _ = io.Copy(io.Discard, resp.Body)
		if err := b.reinitialize(ctx, req.Header.Get(headerSession)); err != nil {
			return err
		}
		return b.post(ctx, msg, env, false)
	case resp.StatusCode == http.StatusUnauthorized:
		return errors.New("the LeanProxy gateway refused the token: check ~/.config/leanproxy/serve.token or $LEANPROXY_SERVE_TOKEN")
	case resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent:
		_, _ = io.Copy(io.Discard, resp.Body)
	case resp.StatusCode >= 300:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("the LeanProxy gateway answered %s: %s", resp.Status, strings.TrimSpace(string(body)))
	default:
		if isInit {
			b.rememberSession(resp.Header.Get(headerSession), msg)
		}
		if err := b.relayBody(resp, isInit); err != nil {
			return err
		}
	}

	if env.Method == methodInitialized {
		b.mu.Lock()
		b.initializedLine = msg
		b.mu.Unlock()
		b.startListening(ctx)
	}
	return nil
}

func (b *Bridge) setHeaders(req *http.Request, withSession bool) {
	if b.opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+b.opts.Token)
	}
	if !withSession {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.session != "" {
		req.Header.Set(headerSession, b.session)
	}
	if b.protocol != "" {
		req.Header.Set(headerProtocol, b.protocol)
	}
}

func (b *Bridge) rememberSession(id string, initLine []byte) {
	b.mu.Lock()
	b.session = id
	b.protocol = ""
	b.initLine = initLine
	b.listening = false
	b.mu.Unlock()
}

// relayBody writes the response to the client: one JSON document, or
// every data event of an SSE stream. For the handshake it also notes the
// negotiated protocol revision.
func (b *Bridge) relayBody(resp *http.Response, isInit bool) error {
	deliver := func(data []byte) {
		if isInit {
			b.noteProtocol(data)
		}
		b.write(data)
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return readSSE(resp.Body, deliver)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLineBytes))
	if err != nil {
		return err
	}
	if body = bytes.TrimSpace(body); len(body) > 0 {
		deliver(body)
	}
	return nil
}

func (b *Bridge) noteProtocol(data []byte) {
	var res struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &res) == nil && res.Result.ProtocolVersion != "" {
		b.mu.Lock()
		b.protocol = res.Result.ProtocolVersion
		b.mu.Unlock()
	}
}

// readSSE calls deliver with the data of each event of an SSE stream.
func readSSE(body io.Reader, deliver func([]byte)) error {
	r := bufio.NewReaderSize(body, 64<<10)
	var data []byte
	flush := func() {
		if len(bytes.TrimSpace(data)) > 0 {
			deliver(bytes.TrimSpace(data))
		}
		data = nil
	}
	for {
		line, err := readLine(r)
		trimmed := bytes.TrimRight(line, "\r\n")
		switch {
		case len(trimmed) == 0:
			flush()
		case bytes.HasPrefix(trimmed, []byte("data:")):
			chunk := bytes.TrimPrefix(bytes.TrimPrefix(trimmed, []byte("data:")), []byte(" "))
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, chunk...)
		}
		if err != nil {
			flush()
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// reinitialize replays the client's handshake after the gateway lost the
// session. stale is the session id the failed request carried: when
// another goroutine already replaced it, there is nothing left to do.
func (b *Bridge) reinitialize(ctx context.Context, stale string) error {
	b.reinit.Lock()
	defer b.reinit.Unlock()

	b.mu.Lock()
	current, initLine, initialized := b.session, b.initLine, b.initializedLine
	b.mu.Unlock()
	if current != stale {
		return nil
	}
	if initLine == nil {
		return errors.New("the LeanProxy gateway does not know this session and the client never initialized")
	}
	b.log.Info("bridge: the gateway forgot the session, initializing again")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.opts.URL, bytes.NewReader(initLine))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	b.setHeaders(req, false)
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach the LeanProxy gateway at %s: %w", b.opts.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("the LeanProxy gateway refused to initialize again: %s", resp.Status)
	}
	b.rememberSession(resp.Header.Get(headerSession), initLine)
	// The client already has its initialize result: read this one for the
	// protocol revision only.
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		_ = readSSE(resp.Body, b.noteProtocol)
	} else if body, err := io.ReadAll(io.LimitReader(resp.Body, maxLineBytes)); err == nil {
		b.noteProtocol(body)
	}
	if initialized != nil {
		if err := b.post(ctx, initialized, envelope{Method: methodInitialized}, false); err != nil {
			return err
		}
	}
	return nil
}

// startListening opens, once per session, the stream on which the gateway
// sends what it initiates (requests to the client, notifications).
func (b *Bridge) startListening(ctx context.Context) {
	b.mu.Lock()
	if b.listening || b.session == "" {
		b.mu.Unlock()
		return
	}
	b.listening = true
	session := b.session
	b.mu.Unlock()
	go b.listen(ctx, session)
}

func (b *Bridge) listen(ctx context.Context, session string) {
	for ctx.Err() == nil {
		b.mu.Lock()
		current := b.session
		b.mu.Unlock()
		if current != session {
			return // a new session has its own listener
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.opts.URL, nil)
		if err != nil {
			return
		}
		req.Header.Set("Accept", "text/event-stream")
		b.setHeaders(req, true)
		resp, err := b.client.Do(req)
		if err == nil {
			switch {
			case resp.StatusCode == http.StatusOK:
				_ = readSSE(resp.Body, b.write)
			case resp.StatusCode == http.StatusMethodNotAllowed, resp.StatusCode == http.StatusNotFound:
				// No such stream, or the session is gone (the next request
				// re-initializes and starts a new listener).
				resp.Body.Close()
				return
			}
			resp.Body.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(listenRetry):
		}
	}
}

// endSession tells the gateway the client is gone.
func (b *Bridge) endSession() {
	b.mu.Lock()
	session := b.session
	b.mu.Unlock()
	if session == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, b.opts.URL, nil)
	if err != nil {
		return
	}
	b.setHeaders(req, true)
	if resp, err := b.client.Do(req); err == nil {
		resp.Body.Close()
	}
}

// write sends one message to the client, as one line.
func (b *Bridge) write(data []byte) {
	// A JSON document may be pretty-printed; the stdio transport is one
	// message per line.
	var compact bytes.Buffer
	if json.Compact(&compact, data) == nil {
		data = compact.Bytes()
	}
	b.outMu.Lock()
	defer b.outMu.Unlock()
	_, _ = b.out.Write(append(data, '\n'))
}

func (b *Bridge) writeError(id json.RawMessage, cause error) {
	resp := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{JSONRPC: "2.0", ID: id}
	resp.Error.Code = -32000
	resp.Error.Message = cause.Error()
	if data, err := json.Marshal(resp); err == nil {
		b.write(data)
	}
}
