package streamhttp

import (
	"bytes"
	"errors"
	"net/http"
	"sync"
	"time"
)

// errStreamClosed is returned when a message is written to a stream that
// already ended (its response was sent, or the client went away).
var errStreamClosed = errors.New("stream closed")

// errNoSSE is returned when a message would need an SSE stream the client
// did not accept (its POST Accept header lacked text/event-stream).
var errNoSSE = errors.New("client did not accept an SSE stream")

// stream is one HTTP response a session's messages can be written to: the
// response of a POST carrying a client request, or the session's GET
// stream.
//
// A POST response starts undecided. When the handler answers before any
// server-to-client message needs to go out, the answer is written as a
// plain application/json body. The first server-to-client message (a
// progress notification, an elicitation request, ...) upgrades the
// response to text/event-stream instead; the answer is then the last
// event of the stream. Plain JSON keeps the common case cheap and
// debuggable with curl; SSE is used exactly when the transport needs it.
//
// Every write happens under mu, with a write deadline, so concurrent
// writers never interleave and a client that stops reading cannot hold a
// writer longer than writeTimeout.
type stream struct {
	sess *session
	// get marks the session's GET stream.
	get bool

	mu     sync.Mutex
	w      http.ResponseWriter
	rc     *http.ResponseController
	sse    bool // headers sent, as text/event-stream
	canSSE bool // the client accepts text/event-stream
	jsonOK bool // the client accepts application/json
	closed bool

	writeTimeout time.Duration
}

func newStream(sess *session, w http.ResponseWriter, canSSE, jsonOK bool, writeTimeout time.Duration) *stream {
	return &stream{
		sess:         sess,
		w:            w,
		rc:           http.NewResponseController(w),
		canSSE:       canSSE,
		jsonOK:       jsonOK,
		writeTimeout: writeTimeout,
	}
}

// startSSELocked sends the SSE response headers.
func (st *stream) startSSELocked() error {
	h := st.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no")
	h.Del("Content-Length")
	st.w.WriteHeader(http.StatusOK)
	st.sse = true
	return st.flushLocked()
}

func (st *stream) deadlineLocked() {
	// Unsupported only with test recorders; the write then just has no
	// deadline.
	_ = st.rc.SetWriteDeadline(time.Now().Add(st.writeTimeout))
}

func (st *stream) flushLocked() error {
	st.deadlineLocked()
	return st.rc.Flush()
}

// writeEventLocked writes one SSE "message" event carrying data (one
// JSON-RPC message). JSON never needs a raw newline, but a data line per
// input line keeps the framing valid whatever data holds.
func (st *stream) writeEventLocked(data []byte) error {
	var b bytes.Buffer
	b.Grow(len(data) + 32)
	b.WriteString("event: message\n")
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		b.WriteString("data: ")
		b.Write(bytes.TrimSuffix(line, []byte{'\r'}))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	st.deadlineLocked()
	if _, err := st.w.Write(b.Bytes()); err != nil {
		return err
	}
	return st.rc.Flush()
}

// send writes one server-to-client message (request or notification) to
// the stream, upgrading an undecided POST response to SSE.
func (st *stream) send(data []byte) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return errStreamClosed
	}
	if !st.canSSE {
		return errNoSSE
	}
	if !st.sse {
		if err := st.startSSELocked(); err != nil {
			st.closed = true
			return err
		}
	}
	if err := st.writeEventLocked(data); err != nil {
		// A failed write leaves the framing unknown: never write again.
		st.closed = true
		return err
	}
	return nil
}

// ping writes an SSE comment, which keeps idle proxies from dropping the
// connection and detects a client that went away.
func (st *stream) ping() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed || !st.sse {
		return errStreamClosed
	}
	st.deadlineLocked()
	if _, err := st.w.Write([]byte(": keep-alive\n\n")); err != nil {
		st.closed = true
		return err
	}
	if err := st.rc.Flush(); err != nil {
		st.closed = true
		return err
	}
	return nil
}

// openSSE sends the headers of a GET stream right away, so the client
// sees the stream open before any message.
func (st *stream) openSSE() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return errStreamClosed
	}
	return st.startSSELocked()
}

// finish writes the JSON-RPC answer(s) to the client request(s) of a POST
// and closes the stream. responses holds one encoded response, or those of
// a batch (batch set). On an SSE stream every response is one event; on
// an undecided one they form a JSON body (an array for a batch). With no
// response at all (every request was canceled by the client) the POST
// gets 202 Accepted, or an SSE stream that ends without an answer.
func (st *stream) finish(responses [][]byte, batch bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return
	}
	st.closed = true
	switch {
	case st.sse || (!st.jsonOK && len(responses) > 0):
		if !st.sse {
			if err := st.startSSELocked(); err != nil {
				return
			}
		}
		for _, r := range responses {
			if err := st.writeEventLocked(r); err != nil {
				return
			}
		}
	case len(responses) == 0:
		st.w.WriteHeader(http.StatusAccepted)
	default:
		body := responses[0]
		if batch {
			body = append([]byte{'['}, bytes.Join(responses, []byte{','})...)
			body = append(body, ']')
		}
		st.w.Header().Set("Content-Type", "application/json")
		st.deadlineLocked()
		st.w.WriteHeader(http.StatusOK)
		_, _ = st.w.Write(body)
	}
}

// close ends the stream without writing anything more.
func (st *stream) close() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.closed = true
}
