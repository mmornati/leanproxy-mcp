package migrate

import (
	"context"
	"errors"
)

// ClaudeScanner reads Claude Code's user-scoped servers: the top-level
// "mcpServers" of ~/.claude.json (and the older
// ~/.config/claude/mcp_config.json).
type ClaudeScanner struct{}

func (s *ClaudeScanner) Name() string {
	return "claude"
}

func (s *ClaudeScanner) Scan(ctx context.Context) ([]DiscoveredServer, error) {
	return scanFiles([]string{
		expandPath("~/.claude.json"),
		expandPath("~/.config/claude/mcp_config.json"),
	}, "mcpServers", "claude")
}

// scanFiles reads the server map under key from each of paths. Unreadable or
// malformed files do not stop the others: their errors are joined and
// returned with the servers that were found.
func scanFiles(paths []string, key, source string) ([]DiscoveredServer, error) {
	var servers []DiscoveredServer
	var errs []error
	for _, path := range paths {
		found, fileErrs := scanServerMapFile(path, source, key)
		servers = append(servers, found...)
		errs = append(errs, fileErrs...)
	}
	return servers, errors.Join(errs...)
}
