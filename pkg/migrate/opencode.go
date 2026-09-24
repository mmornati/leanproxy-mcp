package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

type opencodeConfig struct {
	MCP map[string]json.RawMessage `json:"mcp"`
}

type opencodeServer struct {
	Type        string            `json:"type"`
	Command     []string          `json:"command"`
	Environment envList           `json:"environment"`
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers"`
	Enabled     *bool             `json:"enabled"`
}

// OpenCodeScanner reads the "mcp" block of ~/.config/opencode/opencode.json:
// "local" entries (a command array) and "remote" entries (a url).
type OpenCodeScanner struct{}

func (s *OpenCodeScanner) Name() string {
	return "opencode"
}

func (s *OpenCodeScanner) Scan(ctx context.Context) ([]DiscoveredServer, error) {
	path := expandPath("~/.config/opencode/opencode.json")

	data, found, err := readConfigFile(path)
	if err != nil || !found {
		return nil, err
	}

	var cfg opencodeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	names := make([]string, 0, len(cfg.MCP))
	for name := range cfg.MCP {
		names = append(names, name)
	}
	sort.Strings(names)

	var servers []DiscoveredServer
	var errs []error
	for _, name := range names {
		var srv opencodeServer
		if err := json.Unmarshal(cfg.MCP[name], &srv); err != nil {
			errs = append(errs, fmt.Errorf("%s: server %q: %w", path, name, err))
			continue
		}

		// OpenCode enables an entry unless it says "enabled": false.
		enabled := srv.Enabled == nil || *srv.Enabled

		entry := mcpServerEntry{
			Type:    srv.Type,
			Env:     srv.Environment,
			URL:     srv.URL,
			Headers: srv.Headers,
		}
		if len(srv.Command) > 0 {
			entry.Command = srv.Command[0]
			entry.Args = srv.Command[1:]
		}
		discovered, err := entry.toDiscovered(name, "opencode")
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		discovered.Enabled = &enabled
		servers = append(servers, discovered)
	}

	return servers, errors.Join(errs...)
}
