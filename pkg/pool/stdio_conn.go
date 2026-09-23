package pool

import (
	"bytes"
	"encoding/json"
	errstd "errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"

	errs "github.com/mmornati/leanproxy-mcp/pkg/errors"
)

// methodCancelledNotification is the MCP cancellation notification method.
const methodCancelledNotification = "notifications/cancelled" //nolint:misspell // MCP protocol method name

// errServerExited is wrapped by the error every pending waiter receives when
// the child process (or its stdout reader) goes away underneath it.
var errServerExited = errstd.New("exited")

// rpcReply is what a waiter receives for its request: either the upstream
// result, an upstream *errs.JSONRPCError, or a transport failure.
type rpcReply struct {
	result json.RawMessage
	err    error
}

// stdioConn is one process generation's JSON-RPC connection. It multiplexes
// any number of concurrent requests over a single stdin/stdout pair:
//
//   - writes to stdin are serialized by writeMu, one full line per Write;
//   - every in-flight request is registered in pending under its unique wire
//     ID with a buffered (capacity 1) reply channel;
//   - the stdout reader (StdioServerV2.readResponses) hands each line to
//     handleLine, which decodes it once and delivers it to exactly one waiter
//     without ever blocking or dropping it;
//   - when the process or the reader dies, fail() delivers an error to every
//     waiter at once so nobody waits for its timeout.
//
// A new stdioConn is created for every spawn, so a late answer from a
// previous generation can never reach a waiter of the current one.
type stdioConn struct {
	name   string
	stdin  io.WriteCloser
	logger *slog.Logger

	writeMu sync.Mutex

	mu      sync.Mutex
	pending map[int64]chan rpcReply
	// failErr is non-nil once the connection is dead; register refuses new
	// requests with it.
	failErr error
}

func newStdioConn(name string, stdin io.WriteCloser, logger *slog.Logger) *stdioConn {
	if logger == nil {
		logger = slog.Default()
	}
	return &stdioConn{
		name:    name,
		stdin:   stdin,
		logger:  logger,
		pending: make(map[int64]chan rpcReply),
	}
}

// register adds a waiter for wireID. It fails when the connection is already
// dead, so a request is never written to a process known to be gone.
func (c *stdioConn) register(wireID int64) (chan rpcReply, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failErr != nil {
		return nil, c.failErr
	}
	ch := make(chan rpcReply, 1)
	c.pending[wireID] = ch
	return ch, nil
}

// unregister removes the waiter for wireID. It reports whether the entry was
// still pending; false means a reply (or a failure) has already been
// delivered to its channel.
func (c *stdioConn) unregister(wireID int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.pending[wireID]; !ok {
		return false
	}
	delete(c.pending, wireID)
	return true
}

// deliver hands a reply to the waiter for wireID and removes the entry. The
// channel has capacity 1 and receives at most one value, so this never
// blocks. It reports whether a waiter existed.
func (c *stdioConn) deliver(wireID int64, reply rpcReply) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.pending[wireID]
	if !ok {
		return false
	}
	delete(c.pending, wireID)
	ch <- reply
	return true
}

// fail marks the connection dead and fails every pending waiter with err.
// Only the first call has an effect. This is the single "reader died /
// process exited / stopping" hook: every path that ends a generation calls
// it.
func (c *stdioConn) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failErr != nil {
		return
	}
	c.failErr = err
	for id, ch := range c.pending {
		ch <- rpcReply{err: err}
		delete(c.pending, id)
	}
}

// inFlight returns the number of requests currently awaiting a response.
func (c *stdioConn) inFlight() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

// writeLine writes one JSON-RPC message followed by a newline as a single
// Write, serialized against every other writer of this connection.
func (c *stdioConn) writeLine(msg []byte) error {
	buf := make([]byte, 0, len(msg)+1)
	buf = append(buf, msg...)
	buf = append(buf, '\n')

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(buf); err != nil {
		return err
	}
	return nil
}

// notifyCancelled tells the server that the proxy gave up on wireID, per the
// MCP cancellation spec. Best effort: the server may already be gone.
func (c *stdioConn) notifyCancelled(wireID int64, reason string) {
	msg, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  methodCancelledNotification,
		"params": map[string]interface{}{
			"requestId": wireID,
			"reason":    reason,
		},
	})
	if err != nil {
		return
	}
	if err := c.writeLine(msg); err != nil {
		c.logger.Debug("failed to send cancellation", "name", c.name, "id", wireID, "error", err)
	}
}

// wireMessage is the union of every JSON-RPC message shape a server can
// write on stdout. Each line is decoded into it exactly once.
type wireMessage struct {
	ID     json.RawMessage    `json:"id"`
	Method string             `json:"method"`
	Result json.RawMessage    `json:"result"`
	Error  *errs.JSONRPCError `json:"error"`
}

// handleLine processes one line read from the server's stdout.
func (c *stdioConn) handleLine(line []byte) {
	var msg wireMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		c.logger.Warn("failed to parse server message", "name", c.name, "error", err)
		return
	}

	hasID := len(msg.ID) > 0 && !bytes.Equal(msg.ID, []byte("null"))

	if msg.Method != "" {
		if !hasID {
			// Server notification (progress, logging, list_changed, ...).
			c.logger.Debug("received notification, ignoring", "name", c.name, "method", msg.Method)
			return
		}
		// A request from the server to the client (roots/list,
		// sampling/createMessage, elicitation/create, ping). The proxy does
		// not relay these yet, so answer "method not found" rather than let
		// the server wait forever. The reply is written asynchronously so
		// the reader can never block on a full stdin pipe while the server
		// is itself blocked writing to stdout.
		c.logger.Debug("answering server-to-client request with method not found", "name", c.name, "method", msg.Method)
		go c.replyMethodNotFound(msg.ID, msg.Method)
		return
	}

	if len(msg.Result) == 0 && msg.Error == nil {
		c.logger.Debug("discarding message without result or error", "name", c.name)
		return
	}

	wireID, err := strconv.ParseInt(string(msg.ID), 10, 64)
	if err != nil {
		c.logger.Debug("discarding response with unknown id", "name", c.name, "id", string(msg.ID))
		return
	}

	reply := rpcReply{result: msg.Result}
	if msg.Error != nil {
		reply = rpcReply{err: msg.Error}
	}
	if !c.deliver(wireID, reply) {
		// Late answer to a request whose caller already gave up, or an ID
		// the proxy never issued.
		c.logger.Debug("discarding response with unknown id", "name", c.name, "id", wireID)
	}
}

func (c *stdioConn) replyMethodNotFound(id json.RawMessage, method string) {
	msg, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]interface{}{
			"code":    errs.ErrCodeMethodNotFound,
			"message": fmt.Sprintf("Method not found: %s (not supported by leanproxy)", method),
		},
	})
	if err != nil {
		return
	}
	if err := c.writeLine(msg); err != nil {
		c.logger.Debug("failed to answer server-to-client request", "name", c.name, "method", method, "error", err)
	}
}
