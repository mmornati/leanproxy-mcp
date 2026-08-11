package pool

import (
	"errors"
	"io"
	"net"
	"strings"
	"syscall"

	"github.com/mark3labs/mcp-go/mcp"
)

// isTransportError reports whether err is a transport/connection failure
// rather than a server-side JSON-RPC error. It is used to decide whether a
// request should be retried after reconnecting the underlying MCP client.
//
// The check is type-driven first: mcp-go surfaces server-side JSON-RPC errors
// as plain fmt.Errorf values carrying the server's message, so a loose
// substring match ("session", "stream", "timeout", ...) would misclassify
// application errors as transport failures and re-execute non-idempotent
// tool calls after a pointless reconnect.
func isTransportError(err error) bool {
	if err == nil {
		return false
	}

	// Network-layer errors (dial failures, timeouts, connection refused...).
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	// Server closed the connection mid-stream.
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.ECONNRESET, syscall.ECONNREFUSED, syscall.EPIPE,
			syscall.ETIMEDOUT, syscall.ECONNABORTED:
			return true
		}
	}

	// Fallback for transport wrappers that flatten the error chain to text.
	// Markers are specific enough that a server-side JSON-RPC message would
	// not normally contain them.
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"connection refused",
		"connection reset",
		"broken pipe",
		"unexpected eof",
		"no such host",
		"network is unreachable",
		"connection lost",
		"transport is closed",
		"client not initialized",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// mcpInitializeRequest returns the canonical initialize request used when a
// remote (SSE/HTTP) MCP client is (re)established.
func mcpInitializeRequest() mcp.InitializeRequest {
	return mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: "2024-11-05",
			Capabilities:    mcp.ClientCapabilities{},
			ClientInfo: mcp.Implementation{
				Name:    "leanproxy-mcp",
				Version: "1.0.0",
			},
		},
	}
}
