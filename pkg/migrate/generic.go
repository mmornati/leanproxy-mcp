package migrate

import (
	"context"
	"errors"
)

// GenericScanner reads ~/.config/mcp.json, under "mcp_servers" or
// "mcpServers".
type GenericScanner struct{}

func (s *GenericScanner) Name() string {
	return "generic"
}

func (s *GenericScanner) Scan(ctx context.Context) ([]DiscoveredServer, error) {
	servers, errs := scanServerMapFile(expandPath("~/.config/mcp.json"), "generic", "mcp_servers", "mcpServers")
	return servers, errors.Join(errs...)
}
