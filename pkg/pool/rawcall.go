package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	lperrors "github.com/mmornati/leanproxy-mcp/pkg/errors"
)

// Raw relay for HTTP/SSE upstreams (issue #307). Every MCP method (a method
// name containing '/': tools/list, tools/call, resources/*, prompts/*, ...)
// is sent on the mcp-go transport as is, and the upstream's result comes
// back byte for byte: no round-trip through mcp-go's typed results, which
// would drop fields they do not model (structuredContent details, tool
// titles, icons, _meta), re-encode numbers through float64, or turn an
// upstream JSON-RPC error into an opaque Go error.

// rawRequestIDs numbers the relayed requests. The string form ("lp-N")
// never collides with the numeric ids mcp-go's own client methods use on
// the same transport.
var rawRequestIDs atomic.Int64

// forwardsRaw reports whether method is relayed verbatim (every MCP method)
// rather than through the legacy "method name is a tool name" call.
func forwardsRaw(method string) bool {
	return strings.Contains(method, "/")
}

// rawRequest sends one request on c's transport and returns the upstream's
// raw result, or its JSON-RPC error. err is a transport failure.
func rawRequest(ctx context.Context, c *client.Client, method string, params json.RawMessage) (json.RawMessage, *lperrors.JSONRPCError, error) {
	req := transport.JSONRPCRequest{
		JSONRPC: mcp.JSONRPC_VERSION,
		ID:      mcp.NewRequestId(fmt.Sprintf("lp-%d", rawRequestIDs.Add(1))),
		Method:  method,
	}
	if len(params) > 0 {
		req.Params = params
	}
	resp, err := c.GetTransport().SendRequest(ctx, req)
	if err != nil {
		if ctx.Err() != nil {
			// The caller gave up (a client cancel or a timeout): tell the
			// upstream, per the MCP cancellation spec, as the stdio pool
			// does (issue #308).
			go notifyRemoteCancelled(context.WithoutCancel(ctx), c, req.ID, ctx.Err())
		}
		return nil, nil, err
	}
	if resp == nil {
		return nil, nil, fmt.Errorf("no response to %s", method)
	}
	if resp.Error != nil {
		rpcErr := &lperrors.JSONRPCError{Code: resp.Error.Code, Message: resp.Error.Message}
		if resp.Error.Data != nil {
			if data, merr := json.Marshal(resp.Error.Data); merr == nil {
				rpcErr.Data = data
			}
		}
		return nil, rpcErr, nil
	}
	result := resp.Result
	if len(result) == 0 {
		result = json.RawMessage(`{}`)
	}
	return result, nil, nil
}

// notifyRemoteCancelled sends the MCP cancel notification for a relayed request
// the caller abandoned. Best effort, bounded by backgroundWriteTimeout.
func notifyRemoteCancelled(ctx context.Context, c *client.Client, id mcp.RequestId, cause error) {
	reason := "timeout"
	if errors.Is(cause, context.Canceled) {
		reason = "canceled"
	}
	ctx, cancel := context.WithTimeout(ctx, backgroundWriteTimeout)
	defer cancel()
	_ = c.GetTransport().SendNotification(ctx, mcp.JSONRPCNotification{
		JSONRPC: mcp.JSONRPC_VERSION,
		Notification: mcp.Notification{
			Method: methodCancelledNotification,
			Params: mcp.NotificationParams{AdditionalFields: map[string]any{"requestId": id, "reason": reason}},
		},
	})
}

// sendRemoteNotification sends a notification to a remote upstream.
func sendRemoteNotification(ctx context.Context, c *client.Client, method string, params map[string]interface{}) error {
	return c.GetTransport().SendNotification(ctx, mcp.JSONRPCNotification{
		JSONRPC:      mcp.JSONRPC_VERSION,
		Notification: mcp.Notification{Method: method, Params: mcp.NotificationParams{AdditionalFields: params}},
	})
}

// remoteNotificationHandler returns the OnNotification hook of a remote
// (HTTP/SSE) upstream: list changes become server events, the cancellation
// of a server-to-client request cancels it, and everything else goes to
// the message handler (issue #308).
func remoteNotificationHandler(name string, events *eventHub, generation func() uint64, messages *messageHub, inbound func() *inboundRequests) func(mcp.JSONRPCNotification) {
	return func(n mcp.JSONRPCNotification) {
		if kind, ok := remoteNotificationEvent(n.Method); ok {
			events.emit(ServerEvent{Server: name, Kind: kind, Generation: generation()})
			return
		}
		params, err := json.Marshal(n.Params)
		if err != nil {
			return
		}
		if n.Method == methodCancelledNotification {
			if key, ok := cancelledRequestKey(params); ok && inbound().cancel(key) {
				return
			}
		}
		messages.notify(context.Background(), name, n.Method, params)
	}
}

// installRequestHandler makes a remote upstream's server-to-client requests
// (sampling, elicitation, roots, ping) go through the message handler, with
// their raw params and the upstream's own id. It must run after Start,
// which installs mcp-go's typed handlers. A transport that cannot receive
// requests (legacy SSE) is left alone. mcp-go runs the handler with the
// context of the stream that carried the request (the calling request's
// for a response stream, a bare one for the GET stream), bounded by 30 s.
func installRequestHandler(c *client.Client, name string, messages *messageHub, inbound *inboundRequests) {
	bi, ok := c.GetTransport().(transport.BidirectionalInterface)
	if !ok {
		return
	}
	bi.SetRequestHandler(func(ctx context.Context, req transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
		idRaw, err := json.Marshal(req.ID)
		if err != nil {
			return nil, err
		}
		key, ok := RequestIDKey(idRaw)
		if !ok {
			return nil, fmt.Errorf("invalid request id")
		}
		var params json.RawMessage
		if req.Params != nil {
			if params, err = json.Marshal(req.Params); err != nil {
				return nil, err
			}
		}
		rctx, done, rpcErr := inbound.start(ctx, key)
		if rpcErr != nil {
			return transport.NewJSONRPCErrorResponse(req.ID, rpcErr.Code, rpcErr.Message, nil), nil
		}
		defer done()
		result, rpcErr := messages.request(rctx, name, req.Method, params)
		if rctx.Err() != nil {
			// Canceled by the upstream: no answer, per the MCP spec.
			return nil, nil
		}
		if rpcErr != nil {
			var data any
			if len(rpcErr.Data) > 0 {
				data = rpcErr.Data
			}
			return transport.NewJSONRPCErrorResponse(req.ID, rpcErr.Code, rpcErr.Message, data), nil
		}
		if len(result) == 0 {
			result = json.RawMessage(`{}`)
		}
		return transport.NewJSONRPCResultResponse(req.ID, result), nil
	})
}

// remoteConn is the connection management shared by the HTTP and SSE
// servers.
type remoteConn interface {
	ensureConnected(ctx context.Context) (*client.Client, error)
	setState(state ServerState)
}

// relayRaw sends method verbatim to a remote server, reconnecting and
// retrying once on a transport failure (like CallTool/ListTools do).
func relayRaw(ctx context.Context, s remoteConn, name, method string, params json.RawMessage) (json.RawMessage, *lperrors.JSONRPCError, error) {
	c, err := s.ensureConnected(ctx)
	if err != nil {
		return nil, nil, err
	}
	result, rpcErr, err := rawRequest(ctx, c, method, params)
	// A caller that gave up (a client cancel, a timeout) is not a broken
	// connection: reconnecting would tear the session down under every
	// other in-flight call, and retrying would outlive the caller.
	if err != nil && ctx.Err() == nil && isTransportError(err) {
		s.setState(StateDisconnected)
		c, rerr := s.ensureConnected(ctx)
		if rerr != nil {
			return nil, nil, fmt.Errorf("%s: reconnect: %w", name, rerr)
		}
		result, rpcErr, err = rawRequest(ctx, c, method, params)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %s: %w", name, method, err)
	}
	return result, rpcErr, nil
}

// remoteNotificationEvent maps an upstream notification to the server
// event it raises, if any.
func remoteNotificationEvent(method string) (ServerEventKind, bool) {
	switch method {
	case MethodToolsListChanged:
		return EventToolsListChanged, true
	case MethodResourcesListChanged:
		return EventResourcesListChanged, true
	case MethodPromptsListChanged:
		return EventPromptsListChanged, true
	default:
		return 0, false
	}
}
