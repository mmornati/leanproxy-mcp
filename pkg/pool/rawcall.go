package pool

import (
	"context"
	"encoding/json"
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
	if err != nil && isTransportError(err) {
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
