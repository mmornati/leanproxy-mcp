package migrate

import (
	"context"
)

// CursorScanner reads the "mcpServers" of Cursor's global ~/.cursor/mcp.json.
type CursorScanner struct{}

func (s *CursorScanner) Name() string {
	return "cursor"
}

func (s *CursorScanner) Scan(ctx context.Context) ([]DiscoveredServer, error) {
	return scanFiles([]string{expandPath("~/.cursor/mcp.json")}, "mcpServers", "cursor")
}
