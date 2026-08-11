package pool

import (
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// isTransportError reports whether err looks like a transport/connection
// failure rather than a server-side JSON-RPC error. It is used to decide
// whether a request should be retried after reconnecting the underlying
// MCP client.
func isTransportError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"connection",
		"eof",
		"broken pipe",
		"reset",
		"refused",
		"timeout",
		"timed out",
		"connection lost",
		"stream",
		"session",
		"bad gateway",
		"service unavailable",
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
