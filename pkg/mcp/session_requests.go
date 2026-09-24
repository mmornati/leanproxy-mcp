package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Server-to-client requests on a client session (issue #308). The relay
// (relay.go) forwards an upstream's sampling, elicitation and roots
// requests to the client with ClientSession.Request, under an id of the
// session's own ("lp-<n>"): the upstream's id never reaches the client, so
// ids of different upstreams can never collide on one client, and the
// client's answer is matched back by DeliverResponse.

// RequestFunc writes one server-to-client JSON-RPC request with the given
// id through the front end's serialized writer.
type RequestFunc func(id, method string, params json.RawMessage) error

// ContextRequestFunc is a RequestFunc that also gets the context of the
// relayed request. A front end with several response streams per session
// (Streamable HTTP, #309) reads from it, with ResponseRouteFrom, the
// stream of the client request the message belongs to.
type ContextRequestFunc func(ctx context.Context, id, method string, params json.RawMessage) error

const (
	// clientRequestIDPrefix starts every id the proxy gives a
	// server-to-client request.
	clientRequestIDPrefix = "lp-"
	// maxPendingClientRequests caps the requests one session may have
	// outstanding at the client.
	maxPendingClientRequests = 64
	// ClientRequestTimeout bounds how long a relayed request waits for the
	// client's answer when the upstream never cancels it. Elicitation waits
	// for a human, hence the generous bound.
	ClientRequestTimeout = 10 * time.Minute
	// notificationQueueSize bounds the relayed notifications waiting to be
	// written to one client; beyond it they are dropped.
	notificationQueueSize = 256
)

// queuedNotification is one relayed notification waiting to be written,
// or, when flushed is set, a marker closed once everything queued before
// it was written.
type queuedNotification struct {
	method  string
	params  json.RawMessage
	flushed chan struct{}
}

// flushQueueWait bounds how long flushQueued waits for the queue to drain
// (a client that stopped reading must not hold up a response forever).
const flushQueueWait = time.Second

// sendQueued writes a relayed upstream notification (progress, resource
// update, ...) to the client in order, from a per-session goroutine: an
// upstream's reader hands it over and returns at once, even when this
// client is slow to read. When the queue is full the notification is
// dropped (it is informational); it reports whether it was queued.
func (s *ClientSession) sendQueued(method string, params json.RawMessage) bool {
	if s == nil || s.notify == nil || !s.Initialized() {
		return false
	}
	s.queueOnce.Do(func() {
		s.reqMu.Lock()
		s.queue = make(chan queuedNotification, notificationQueueSize)
		if s.queueDone == nil {
			s.queueDone = make(chan struct{})
		}
		s.reqMu.Unlock()
		go s.drainQueue()
	})
	select {
	case <-s.queueDone:
		return false
	default:
	}
	select {
	case s.queue <- queuedNotification{method: method, params: params}:
		return true
	default:
		return false
	}
}

func (s *ClientSession) drainQueue() {
	for {
		select {
		case n := <-s.queue:
			if n.flushed != nil {
				close(n.flushed)
				continue
			}
			s.send(n.method, n.params)
		case <-s.queueDone:
			return
		}
	}
}

// flushQueued waits (at most flushQueueWait) until every notification
// queued so far was written, so a call's last progress notification
// reaches the client before the call's response.
func (s *ClientSession) flushQueued() {
	if s == nil {
		return
	}
	s.reqMu.Lock()
	started := s.queue != nil
	done := s.queueDone
	s.reqMu.Unlock()
	if !started {
		return
	}
	marker := make(chan struct{})
	timer := time.NewTimer(flushQueueWait)
	defer timer.Stop()
	select {
	case s.queue <- queuedNotification{flushed: marker}:
	case <-done:
		return
	case <-timer.C:
		return
	}
	select {
	case <-marker:
	case <-done:
	case <-timer.C:
	}
}

// ErrCodeRequestTimeout is the error an upstream gets when the client did
// not answer a relayed request within ClientRequestTimeout.
const ErrCodeRequestTimeout = -32001

// errSessionClosed is delivered to pending requests when the client
// disconnects.
var errSessionClosed = errors.New("client disconnected")

// clientReply is the client's answer to one relayed request.
type clientReply struct {
	result json.RawMessage
	err    *Error
}

// EnableRequests lets the relay send server-to-client requests to this
// client through fn. A front end that cannot carry them never calls it, and
// the session then never gets any (the upstream is answered -32601).
func (s *ClientSession) EnableRequests(fn RequestFunc) {
	if fn == nil {
		s.EnableContextRequests(nil)
		return
	}
	s.EnableContextRequests(func(_ context.Context, id, method string, params json.RawMessage) error {
		return fn(id, method, params)
	})
}

// EnableContextRequests is EnableRequests for a front end that routes each
// request by its context (see ContextRequestFunc).
func (s *ClientSession) EnableContextRequests(fn ContextRequestFunc) {
	s.reqMu.Lock()
	defer s.reqMu.Unlock()
	s.sendRequest = fn
}

// AcceptsRequests reports whether the front end can deliver
// server-to-client requests to this client.
func (s *ClientSession) AcceptsRequests() bool {
	if s == nil {
		return false
	}
	s.reqMu.Lock()
	defer s.reqMu.Unlock()
	return s.sendRequest != nil && !s.reqClosed
}

// HasCapability reports whether the client declared the named capability
// (e.g. "sampling", "elicitation", "roots") in its initialize request.
func (s *ClientSession) HasCapability(name string) bool {
	_, ok := s.capability(name)
	return ok
}

// HasSubCapability reports whether the client declared capability name
// with the member sub (e.g. elicitation.url).
func (s *ClientSession) HasSubCapability(name, sub string) bool {
	raw, ok := s.capability(name)
	if !ok {
		return false
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil {
		return false
	}
	v, ok := members[sub]
	return ok && len(v) > 0 && string(v) != "null"
}

func (s *ClientSession) capability(name string) (json.RawMessage, bool) {
	caps := s.Capabilities()
	if len(caps) == 0 {
		return nil, false
	}
	var all map[string]json.RawMessage
	if json.Unmarshal(caps, &all) != nil {
		return nil, false
	}
	v, ok := all[name]
	if !ok || len(v) == 0 || string(v) == "null" {
		return nil, false
	}
	return v, true
}

// Request sends a server-to-client request to the client and waits for its
// answer. When ctx ends first (the upstream canceled, or its connection
// died) the pending entry is freed and the client gets the MCP cancel
// notification for the request. The returned error is what the upstream
// should be answered.
func (s *ClientSession) Request(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, *Error) {
	id := clientRequestIDPrefix + strconv.FormatUint(s.nextReqID.Add(1), 10)
	ch := make(chan clientReply, 1)

	s.reqMu.Lock()
	send := s.sendRequest
	switch {
	case send == nil:
		s.reqMu.Unlock()
		return nil, NewError(ErrCodeMethodNotFound, "client connection cannot receive "+method+" requests")
	case s.reqClosed:
		s.reqMu.Unlock()
		return nil, NewError(ErrCodeInternalError, errSessionClosed.Error())
	case len(s.pending) >= maxPendingClientRequests:
		s.reqMu.Unlock()
		return nil, NewError(ErrCodeInternalError, fmt.Sprintf("too many requests pending at the client (max %d)", maxPendingClientRequests))
	}
	if s.pending == nil {
		s.pending = make(map[string]chan clientReply)
	}
	s.pending[id] = ch
	s.reqMu.Unlock()

	if err := send(ctx, id, method, params); err != nil {
		s.forget(id)
		return nil, NewError(ErrCodeInternalError, "failed to send "+method+" to the client: "+err.Error())
	}

	timer := time.NewTimer(ClientRequestTimeout)
	defer timer.Stop()
	select {
	case reply := <-ch:
		return reply.result, reply.err
	case <-ctx.Done():
		if s.forget(id) {
			s.cancelAtClient(id, "canceled by the server")
		}
		return nil, NewError(ErrCodeInternalError, method+" canceled")
	case <-timer.C:
		if s.forget(id) {
			s.cancelAtClient(id, "timeout")
		}
		return nil, NewError(ErrCodeRequestTimeout, fmt.Sprintf("client did not answer %s within %s", method, ClientRequestTimeout))
	}
}

// forget removes a pending request; false means it was already answered
// or failed.
func (s *ClientSession) forget(id string) bool {
	s.reqMu.Lock()
	defer s.reqMu.Unlock()
	if _, ok := s.pending[id]; !ok {
		return false
	}
	delete(s.pending, id)
	return true
}

// cancelAtClient tells the client the proxy no longer waits for request
// id.
func (s *ClientSession) cancelAtClient(id, reason string) {
	params, err := json.Marshal(map[string]string{"requestId": id, "reason": reason})
	if err != nil {
		return
	}
	s.send(NotificationCancelled, params)
}

// DeliverResponse hands the client's answer to a server-to-client request
// to its waiter. It reports whether id named a pending request; an answer
// to an unknown (already canceled or never sent) request is dropped.
func (s *ClientSession) DeliverResponse(id json.RawMessage, result json.RawMessage, rpcErr *Error) bool {
	if s == nil {
		return false
	}
	var key string
	if json.Unmarshal(id, &key) != nil || !strings.HasPrefix(key, clientRequestIDPrefix) {
		return false
	}
	s.reqMu.Lock()
	ch, ok := s.pending[key]
	delete(s.pending, key)
	s.reqMu.Unlock()
	if !ok {
		return false
	}
	if rpcErr == nil && len(result) == 0 {
		result = json.RawMessage(`{}`)
	}
	ch <- clientReply{result: result, err: rpcErr}
	return true
}

// PendingRequests returns the number of server-to-client requests waiting
// for the client.
func (s *ClientSession) PendingRequests() int {
	s.reqMu.Lock()
	defer s.reqMu.Unlock()
	return len(s.pending)
}

// closeRequests fails every pending request and refuses new ones (the
// client disconnected).
func (s *ClientSession) closeRequests() {
	s.reqMu.Lock()
	s.reqClosed = true
	pending := s.pending
	s.pending = nil
	if s.queueDone == nil {
		s.queueDone = make(chan struct{})
	}
	done := s.queueDone
	s.reqMu.Unlock()
	s.closeOnce.Do(func() { close(done) })
	for _, ch := range pending {
		ch <- clientReply{err: NewError(ErrCodeInternalError, errSessionClosed.Error())}
	}
}
