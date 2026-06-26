// Package migrate owns the on-disk YAML schema for LeanProxy's
// user-server configuration. It re-exports the canonical transport enum used
// throughout the codebase so callers can refer to it without depending on
// pkg/registry (which already depends on this package for ServerConfig).
package migrate

// TransportType enumerates the wire transports a server may speak. The
// string values are part of the public YAML schema and must not change
// without a migration step.
type TransportType string

const (
	TransportStdio TransportType = "stdio"
	TransportHTTP  TransportType = "http"
	TransportSSE   TransportType = "sse"
)
