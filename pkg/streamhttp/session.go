package streamhttp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
)

// sessionIDBytes is the entropy of an Mcp-Session-Id: 256 bits from
// crypto/rand, so an id can be neither guessed nor enumerated.
const sessionIDBytes = 32

// newSessionID returns a fresh, unguessable session id: 43 URL-safe
// base64 characters, all visible ASCII as the transport spec requires.
func newSessionID() (string, error) {
	b := make([]byte, sessionIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// inflight is one client request being handled.
type inflight struct {
	cancel context.CancelFunc
	// canceledByClient is set when a cancel notification named the
	// request; its answer is then dropped, as the MCP spec says.
	canceledByClient atomic.Bool
}

// session is one Mcp-Session-Id: the pkg/mcp client session (negotiated
// version, capabilities, pending server-to-client requests) and the HTTP
// streams its messages can be written to.
type session struct {
	id string
	// principal is the fingerprint of the credential that created the
	// session; every later request must present the same one.
	principal [32]byte
	cs        *mcp.ClientSession
	closeCS   func()
	srv       *Server

	mu         sync.Mutex
	closed     bool
	done       chan struct{}
	lastActive time.Time
	// active counts the requests in flight and the open GET stream: a
	// session in use never expires.
	active int
	get    *stream
	// posts are the POST streams of the requests in flight, oldest first.
	posts []*stream
	// progress maps the progress token of a request in flight to its
	// stream.
	progress map[string]*stream
	// sentOn maps a server-to-client request id to the stream it was
	// written on, so its cancellation follows it.
	sentOn map[string]*stream
	// inflight maps a client request id (see requestIDKey) to its cancel.
	inflight map[string]*inflight
}

// touch records activity (under s.mu).
func (s *session) touchLocked() { s.lastActive = time.Now() }

// begin marks the start of a request or stream; it fails once the
// session is closed.
func (s *session) begin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.active++
	s.touchLocked()
	return true
}

// end is begin's counterpart.
func (s *session) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	s.touchLocked()
}

// expired reports whether the session sat idle longer than idle.
func (s *session) expired(now time.Time, idle time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active == 0 && now.Sub(s.lastActive) > idle
}

// shutdown closes the session: in-flight requests are canceled, the GET
// stream ends, and the pkg/mcp session fails its pending server-to-client
// requests and drops its subscriptions. It is idempotent.
func (s *session) shutdown() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.done)
	cancels := make([]context.CancelFunc, 0, len(s.inflight))
	for _, f := range s.inflight {
		cancels = append(cancels, f.cancel)
	}
	s.mu.Unlock()
	for _, c := range cancels {
		c()
	}
	s.closeCS()
}

// register adds a request in flight: its cancel, its stream and its
// progress token. It returns false when the id is already in flight.
func (s *session) register(key string, keyed bool, f *inflight, st *stream, progressKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if keyed {
		if _, dup := s.inflight[key]; dup {
			return false
		}
		s.inflight[key] = f
	}
	s.posts = append(s.posts, st)
	if progressKey != "" {
		s.progress[progressKey] = st
	}
	return true
}

// unregister is register's counterpart.
func (s *session) unregister(key string, keyed bool, f *inflight, st *stream, progressKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if keyed && s.inflight[key] == f {
		delete(s.inflight, key)
	}
	for i, p := range s.posts {
		if p == st {
			s.posts = append(s.posts[:i:i], s.posts[i+1:]...)
			break
		}
	}
	if progressKey != "" && s.progress[progressKey] == st {
		delete(s.progress, progressKey)
	}
	for id, sent := range s.sentOn {
		if sent == st {
			delete(s.sentOn, id)
		}
	}
}

// cancelRequest cancels the client request named by a cancel
// notification's params.requestId.
func (s *session) cancelRequest(params json.RawMessage) bool {
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if len(params) == 0 || json.Unmarshal(params, &p) != nil || len(p.RequestID) == 0 {
		return false
	}
	id, err := decodeID(p.RequestID)
	if err != nil {
		return false
	}
	key, ok := requestIDKey(id)
	if !ok {
		return false
	}
	s.mu.Lock()
	f := s.inflight[key]
	s.mu.Unlock()
	if f == nil {
		return false
	}
	f.canceledByClient.Store(true)
	f.cancel()
	return true
}

// attachGet makes st the session's GET stream; false when one is open.
func (s *session) attachGet(st *stream) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.get != nil {
		return false
	}
	s.get = st
	s.active++
	s.touchLocked()
	return true
}

func (s *session) detachGet(st *stream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.get == st {
		s.get = nil
		s.active--
		s.touchLocked()
	}
}

// deliver tries the candidate streams in order and returns the one the
// message was written to.
func deliver(data []byte, candidates []*stream) (*stream, error) {
	err := errors.New("no open stream to the client")
	for _, st := range candidates {
		if st == nil {
			continue
		}
		if werr := st.send(data); werr != nil {
			err = werr
			continue
		}
		return st, nil
	}
	return nil, err
}

// fallbacksLocked lists the streams a message without a stream of its own
// may take: the GET stream, then the POST streams in flight, newest first
// (the relay routes an upstream request with no known call to the most
// recent one too).
func (s *session) fallbacksLocked() []*stream {
	out := make([]*stream, 0, len(s.posts)+1)
	if s.get != nil {
		out = append(out, s.get)
	}
	for i := len(s.posts) - 1; i >= 0; i-- {
		out = append(out, s.posts[i])
	}
	return out
}

// sendRequest writes a server-to-client request (mcp.ContextRequestFunc).
// It goes on the response stream of the client request that caused it
// when the relay knows it (ctx's route), otherwise on the GET stream or a
// stream in flight. With no open stream the upstream gets an error at
// once rather than waiting for an answer that cannot come.
func (s *session) sendRequest(ctx context.Context, id, method string, params json.RawMessage) error {
	data, err := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      string          `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params,omitempty"`
	}{mcp.JSONRPCVersion, id, method, params})
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errStreamClosed
	}
	var candidates []*stream
	if st, ok := mcp.ResponseRouteFrom(ctx).(*stream); ok && st.sess == s {
		candidates = append(candidates, st)
	}
	candidates = append(candidates, s.fallbacksLocked()...)
	s.mu.Unlock()

	st, err := deliver(data, candidates)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if !s.closed && !st.get {
		// Only a POST stream ends before the session; a request sent on
		// the GET stream needs no entry.
		s.sentOn[id] = st
	}
	s.mu.Unlock()
	return nil
}

// notify writes a server-to-client notification (mcp.NotifyFunc): a
// progress notification on the stream of the request that carried its
// token, a cancellation on the stream its request went out on, anything
// else (list changes, resource updates) on the GET stream. A notification
// with nowhere to go is dropped: notifications are informational.
func (s *session) notify(method string, params json.RawMessage) {
	data, err := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params,omitempty"`
	}{mcp.JSONRPCVersion, method, params})
	if err != nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	var candidates []*stream
	switch method {
	case mcp.NotificationProgress:
		var p struct {
			Token json.RawMessage `json:"progressToken"`
		}
		if json.Unmarshal(params, &p) == nil {
			if st := s.progress[compactKey(p.Token)]; st != nil {
				candidates = append(candidates, st)
			}
		}
		candidates = append(candidates, s.get)
	case mcp.NotificationCancelled:
		var p struct {
			RequestID string `json:"requestId"`
		}
		if json.Unmarshal(params, &p) == nil && p.RequestID != "" {
			if st := s.sentOn[p.RequestID]; st != nil {
				candidates = append(candidates, st)
				delete(s.sentOn, p.RequestID)
			}
		}
		candidates = append(candidates, s.fallbacksLocked()...)
	default:
		candidates = append(candidates, s.get)
	}
	s.mu.Unlock()
	if _, err := deliver(data, candidates); err != nil {
		s.srv.logger.Debug("streamable HTTP: notification dropped, no open stream", "method", method)
	}
}

// deliverResponse hands the client's answer to a server-to-client request
// to the pkg/mcp session.
func (s *session) deliverResponse(id, result json.RawMessage, rpcErr *mcp.Error) bool {
	var key string
	if json.Unmarshal(id, &key) == nil {
		s.mu.Lock()
		delete(s.sentOn, key)
		s.mu.Unlock()
	}
	return s.cs.DeliverResponse(id, result, rpcErr)
}

// compactKey normalizes a raw JSON value (a progress token) into a map
// key, so the client's spelling and the relay's re-encoding match.
func compactKey(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return ""
	}
	return b.String()
}
