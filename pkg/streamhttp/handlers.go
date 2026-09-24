package streamhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/httpsec"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
)

// routes builds the endpoint behind the transport-level checks, in order:
// Host (DNS rebinding), Origin (cross-site browser requests), CORS
// preflight for allowlisted origins, then the bearer token. A rejected
// request never reaches the handler, let alone an upstream.
func (s *Server) routes(allowedHosts map[string]struct{}) http.Handler {
	endpoint := http.HandlerFunc(s.serveEndpoint)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := allowedHosts[strings.ToLower(r.Host)]; !ok {
			s.logger.Warn("streamable HTTP: rejected request with an unrecognized Host header", "remote", r.RemoteAddr)
			writeError(w, http.StatusForbidden, mcp.ErrCodeInvalidRequest, "Forbidden: unrecognized Host header")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if !httpsec.OriginAllowed(origin, r.Host, s.allowedOrigins) {
				s.logger.Warn("streamable HTTP: rejected request from a disallowed Origin", "remote", r.RemoteAddr)
				writeError(w, http.StatusForbidden, mcp.ErrCodeInvalidRequest, "Forbidden: Origin not allowed")
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Expose-Headers", HeaderSessionID+", "+HeaderProtocolVersion)
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, "+HeaderSessionID+", "+HeaderProtocolVersion+", "+headerLastEventID)
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		if s.opts.Token != "" && !httpsec.ValidBearer(r, s.opts.Token) {
			s.logger.Warn("streamable HTTP: rejected request with a missing or wrong bearer token", "remote", r.RemoteAddr)
			w.Header().Set("WWW-Authenticate", `Bearer realm="leanproxy"`)
			writeError(w, http.StatusUnauthorized, mcp.ErrCodeInvalidRequest, "Unauthorized")
			return
		}
		endpoint.ServeHTTP(w, r)
	})
}

func (s *Server) serveEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.opts.Endpoint {
		writeError(w, http.StatusNotFound, mcp.ErrCodeInvalidRequest, "Not Found: the MCP endpoint is "+s.opts.Endpoint)
		return
	}
	switch r.Method {
	case http.MethodPost:
		s.handlePost(w, r)
	case http.MethodGet:
		s.handleGet(w, r)
	case http.MethodDelete:
		s.handleDelete(w, r)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		writeError(w, http.StatusMethodNotAllowed, mcp.ErrCodeInvalidRequest, "Method Not Allowed")
	}
}

// writeError writes a transport-level error: an HTTP status with a
// JSON-RPC error body (id null), which every MCP client can decode.
func writeError(w http.ResponseWriter, status, code int, message string) {
	body, _ := json.Marshal(mcp.Response{JSONRPC: mcp.JSONRPCVersion, Error: mcp.NewError(code, message)})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// accepts parses an Accept header into what the response may be. A
// missing header, or */*, accepts both.
func accepts(header string) (sse, jsonOK bool) {
	if strings.TrimSpace(header) == "" {
		return true, true
	}
	for _, part := range strings.Split(header, ",") {
		mt, _, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		switch mt {
		case "text/event-stream":
			sse = true
		case "application/json":
			jsonOK = true
		case "*/*":
			sse, jsonOK = true, true
		}
	}
	return sse, jsonOK
}

// checkProtocolHeader validates MCP-Protocol-Version on a request of an
// initialized session: a supported revision, the one negotiated. A client
// that omits it (2025-03-26) is fine: the session knows its revision.
func checkProtocolHeader(r *http.Request, sess *session) string {
	v := r.Header.Get(HeaderProtocolVersion)
	if v == "" {
		return ""
	}
	if !mcp.IsSupportedProtocolVersion(v) {
		return "Bad Request: unsupported " + HeaderProtocolVersion + " " + strconv.Quote(v)
	}
	if negotiated := sess.cs.ProtocolVersion(); negotiated != "" && negotiated != v {
		return "Bad Request: " + HeaderProtocolVersion + " " + strconv.Quote(v) + " does not match the negotiated " + strconv.Quote(negotiated)
	}
	return ""
}

// sessionFor resolves the session of a non-initialize request, writing
// the error response itself when there is none: 400 without the header,
// 404 for an unknown, expired, ended or foreign session (the client must
// then initialize again).
func (s *Server) sessionFor(w http.ResponseWriter, r *http.Request) *session {
	id := r.Header.Get(HeaderSessionID)
	if id == "" {
		writeError(w, http.StatusBadRequest, mcp.ErrCodeInvalidRequest, "Bad Request: missing "+HeaderSessionID+" header")
		return nil
	}
	sess := s.lookup(id, s.principalOf(r))
	if sess == nil {
		writeError(w, http.StatusNotFound, mcp.ErrCodeInvalidRequest, "Not Found: unknown or expired session")
		return nil
	}
	if msg := checkProtocolHeader(r, sess); msg != "" {
		writeError(w, http.StatusBadRequest, mcp.ErrCodeInvalidRequest, msg)
		return nil
	}
	return sess
}

// envelope is one incoming JSON-RPC message.
type envelope struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcp.Error      `json:"error,omitempty"`
}

func (e *envelope) isRequest() bool      { return e.Method != "" && len(e.ID) > 0 }
func (e *envelope) isNotification() bool { return e.Method != "" && len(e.ID) == 0 }
func (e *envelope) isResponse() bool {
	return e.Method == "" && len(e.ID) > 0 && (len(e.Result) > 0 || e.Error != nil)
}

// decodeID decodes a raw JSON-RPC id, keeping numbers exact.
func decodeID(raw json.RawMessage) (interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var id interface{}
	if err := dec.Decode(&id); err != nil {
		return nil, err
	}
	return id, nil
}

// requestIDKey normalizes a decoded id into a map key; strings and numbers
// never collide (1 and "1" are different ids).
func requestIDKey(id interface{}) (string, bool) {
	switch v := id.(type) {
	case string:
		return "s:" + v, true
	case json.Number:
		return "n:" + v.String(), true
	default:
		return "", false
	}
}

// parseBody splits a POST body into its messages.
func parseBody(body []byte) ([]envelope, bool, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var batch []envelope
		if err := json.Unmarshal(trimmed, &batch); err != nil {
			return nil, true, err
		}
		if len(batch) == 0 {
			return nil, true, errors.New("empty batch")
		}
		return batch, true, nil
	}
	var one envelope
	if err := json.Unmarshal(trimmed, &one); err != nil {
		return nil, false, err
	}
	return []envelope{one}, false, nil
}

func (s *Server) handlePost(w http.ResponseWriter, r *http.Request) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, mcp.ErrCodeInvalidRequest, "Unsupported Media Type: POST a JSON-RPC message as application/json")
		return
	}
	canSSE, jsonOK := accepts(r.Header.Get("Accept"))
	if !canSSE && !jsonOK {
		writeError(w, http.StatusNotAcceptable, mcp.ErrCodeInvalidRequest, "Not Acceptable: accept application/json and text/event-stream")
		return
	}

	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(s.opts.ReadTimeout))
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.opts.MaxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, mcp.ErrCodeInvalidRequest, "request body exceeds "+strconv.FormatInt(s.opts.MaxBodyBytes, 10)+" bytes")
			return
		}
		writeError(w, http.StatusBadRequest, mcp.ErrCodeParseError, "failed to read the request body")
		return
	}
	// The body is in: from here the request may legitimately take as long
	// as the call it carries.
	_ = rc.SetReadDeadline(time.Time{})

	msgs, batch, err := parseBody(body)
	if err != nil {
		// Never log the body: it can carry secrets.
		writeError(w, http.StatusBadRequest, mcp.ErrCodeParseError, "Parse error: invalid JSON-RPC message")
		return
	}
	for i := range msgs {
		if !msgs[i].isRequest() && !msgs[i].isNotification() && !msgs[i].isResponse() {
			writeError(w, http.StatusBadRequest, mcp.ErrCodeInvalidRequest, "Invalid Request: not a JSON-RPC request, notification or response")
			return
		}
		if msgs[i].Method == mcp.MethodInitialize && (batch || !msgs[i].isRequest()) {
			writeError(w, http.StatusBadRequest, mcp.ErrCodeInvalidRequest, "Invalid Request: initialize must be sent alone")
			return
		}
	}

	if msgs[0].Method == mcp.MethodInitialize {
		s.initialize(w, r, &msgs[0], canSSE, jsonOK)
		return
	}

	sess := s.sessionFor(w, r)
	if sess == nil {
		return
	}
	if batch && sess.cs.AtLeast(mcp.ProtocolVersion20250618) {
		// JSON-RPC batching was removed from MCP in 2025-06-18.
		writeError(w, http.StatusBadRequest, mcp.ErrCodeInvalidRequest, "Invalid Request: batching is not supported for protocol "+sess.cs.ProtocolVersion())
		return
	}
	if !sess.begin() {
		writeError(w, http.StatusNotFound, mcp.ErrCodeInvalidRequest, "Not Found: unknown or expired session")
		return
	}
	defer sess.end()

	ctx := mcp.WithClientSession(r.Context(), sess.cs)
	var requests []*envelope
	for i := range msgs {
		m := &msgs[i]
		switch {
		case m.isResponse():
			if !sess.deliverResponse(m.ID, m.Result, m.Error) {
				s.logger.Debug("streamable HTTP: dropping an answer to an unknown or canceled server-to-client request")
			}
		case m.isNotification():
			s.handleNotification(ctx, sess, m)
		default:
			requests = append(requests, m)
		}
	}
	if len(requests) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	st := newStream(sess, w, canSSE, jsonOK, s.opts.WriteTimeout)
	responses := make([][]byte, len(requests))
	if len(requests) == 1 {
		responses[0] = s.serveRequest(ctx, sess, requests[0], st)
	} else {
		var wg sync.WaitGroup
		for i, m := range requests {
			wg.Add(1)
			go func(i int, m *envelope) {
				defer wg.Done()
				responses[i] = s.serveRequest(ctx, sess, m, st)
			}(i, m)
		}
		wg.Wait()
	}
	st.finish(compact(responses), batch)
}

// compact drops the nil entries (requests canceled by the client).
func compact(responses [][]byte) [][]byte {
	out := responses[:0]
	for _, r := range responses {
		if r != nil {
			out = append(out, r)
		}
	}
	return out
}

// initialize opens a session for an initialize request. The session id is
// only handed out when initialize succeeds.
func (s *Server) initialize(w http.ResponseWriter, r *http.Request, m *envelope, canSSE, jsonOK bool) {
	sess, err := s.newSession(s.principalOf(r))
	if err != nil {
		if errors.Is(err, errTooManySessions) {
			w.Header().Set("Retry-After", "5")
			writeError(w, http.StatusServiceUnavailable, mcp.ErrCodeServerError, "Service Unavailable: too many open sessions")
			return
		}
		writeError(w, http.StatusServiceUnavailable, mcp.ErrCodeServerError, "Service Unavailable: "+err.Error())
		return
	}
	if !sess.begin() {
		writeError(w, http.StatusServiceUnavailable, mcp.ErrCodeServerError, "Service Unavailable: server is shutting down")
		return
	}
	st := newStream(sess, w, canSSE, jsonOK, s.opts.WriteTimeout)
	resp := s.serveRequest(mcp.WithClientSession(r.Context(), sess.cs), sess, m, st)
	sess.end()
	if resp == nil || !sess.cs.Initialized() {
		s.remove(sess)
	} else {
		w.Header().Set(HeaderSessionID, sess.id)
		s.logger.Info("streamable HTTP session opened", "client", sess.cs.ClientInfo().Name, "protocol", sess.cs.ProtocolVersion())
	}
	var out [][]byte
	if resp != nil {
		out = [][]byte{resp}
	}
	st.finish(out, false)
}

// handleNotification handles a client notification: a cancellation here,
// anything else through the handler (its result, if any, is dropped).
func (s *Server) handleNotification(ctx context.Context, sess *session, m *envelope) {
	if m.Method == mcp.NotificationCancelled {
		if !sess.cancelRequest(m.Params) {
			s.logger.Debug("streamable HTTP: cancel notification for an unknown or finished request")
		}
		return
	}
	req := &mcp.Request{JSONRPC: m.JSONRPC, Method: m.Method, Params: m.Params}
	if _, err := s.h.HandleRequest(ctx, req); err != nil {
		s.logger.Debug("streamable HTTP: notification handler error", "method", m.Method, "error", err)
	}
}

// serveRequest runs one client request through the handler (under the
// concurrency cap) and returns its encoded response, or nil when the
// client canceled it. Server-to-client messages it causes go to st.
func (s *Server) serveRequest(ctx context.Context, sess *session, m *envelope, st *stream) []byte {
	id, err := decodeID(m.ID)
	if err != nil || id == nil {
		return encodeResponse(&mcp.Response{JSONRPC: mcp.JSONRPCVersion, Error: mcp.NewError(mcp.ErrCodeInvalidRequest, "Invalid Request: the id must be a string or a number")})
	}
	req := &mcp.Request{JSONRPC: m.JSONRPC, Method: m.Method, Params: m.Params, ID: id}

	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	rctx = mcp.WithResponseRoute(rctx, st)
	key, keyed := requestIDKey(id)
	f := &inflight{cancel: cancel}
	progressKey := compactKey(progressTokenOf(m.Params))
	if !sess.register(key, keyed, f, st, progressKey) {
		return encodeResponse(&mcp.Response{JSONRPC: mcp.JSONRPCVersion, Error: mcp.NewError(mcp.ErrCodeInvalidRequest, "Invalid Request: a request with this id is already in flight"), ID: id})
	}
	defer sess.unregister(key, keyed, f, st, progressKey)

	select {
	case s.sem <- struct{}{}:
	case <-rctx.Done():
		if f.canceledByClient.Load() {
			return nil
		}
		return encodeResponse(&mcp.Response{JSONRPC: mcp.JSONRPCVersion, Error: mcp.NewError(mcp.ErrCodeInternalError, "request canceled"), ID: id})
	}
	resp, err := s.h.HandleRequest(rctx, req)
	<-s.sem
	if err != nil {
		s.logger.Error("streamable HTTP: handler error", "method", req.Method, "error", err)
	}
	if f.canceledByClient.Load() {
		return nil
	}
	if resp == nil {
		resp = &mcp.Response{JSONRPC: mcp.JSONRPCVersion, Error: mcp.NewError(mcp.ErrCodeInternalError, "internal error: no response"), ID: id}
	}
	return encodeResponse(resp)
}

// progressTokenOf returns params._meta.progressToken, or nil.
func progressTokenOf(params json.RawMessage) json.RawMessage {
	if len(params) == 0 {
		return nil
	}
	var p struct {
		Meta struct {
			ProgressToken json.RawMessage `json:"progressToken"`
		} `json:"_meta"`
	}
	if json.Unmarshal(params, &p) != nil {
		return nil
	}
	return p.Meta.ProgressToken
}

func encodeResponse(resp *mcp.Response) []byte {
	if resp.Result == nil && resp.Error == nil {
		resp = &mcp.Response{JSONRPC: mcp.JSONRPCVersion, Error: mcp.NewError(mcp.ErrCodeInternalError, "internal error: empty response"), ID: resp.ID}
	}
	if resp.JSONRPC == "" {
		resp.JSONRPC = mcp.JSONRPCVersion
	}
	data, err := json.Marshal(resp)
	if err != nil {
		data, _ = json.Marshal(mcp.Response{JSONRPC: mcp.JSONRPCVersion, Error: mcp.NewError(mcp.ErrCodeInternalError, "internal error: response could not be encoded"), ID: resp.ID})
	}
	return data
}

// handleGet serves the session's server-initiated SSE stream until the
// client disconnects, the session ends or the server stops.
func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	if canSSE, _ := accepts(r.Header.Get("Accept")); !canSSE || r.Header.Get("Accept") == "" {
		writeError(w, http.StatusNotAcceptable, mcp.ErrCodeInvalidRequest, "Not Acceptable: a GET opens a text/event-stream")
		return
	}
	sess := s.sessionFor(w, r)
	if sess == nil {
		return
	}
	if r.Header.Get(headerLastEventID) != "" {
		// Resumability is not supported: the stream starts afresh.
		s.logger.Debug("streamable HTTP: Last-Event-ID ignored (resumability not supported)")
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	st := newStream(sess, w, true, false, s.opts.WriteTimeout)
	st.get = true
	if !sess.attachGet(st) {
		writeError(w, http.StatusConflict, mcp.ErrCodeInvalidRequest, "Conflict: this session already has an open GET stream")
		return
	}
	defer sess.detachGet(st)
	defer st.close()
	if err := st.openSSE(); err != nil {
		return
	}

	heartbeat := time.NewTicker(s.opts.Heartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sess.done:
			return
		case <-s.stop:
			return
		case <-heartbeat.C:
			if st.ping() != nil {
				return
			}
		}
	}
}

// handleDelete ends a session at the client's request.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionFor(w, r)
	if sess == nil {
		return
	}
	s.remove(sess)
	s.logger.Info("streamable HTTP session ended by the client")
	w.WriteHeader(http.StatusNoContent)
}
