package migrate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

type opencodeConfig struct {
	MCP map[string]opencodeServer `json:"mcp"`
}

type opencodeServer struct {
	Type    string   `json:"type"`
	Command []string `json:"command"`
	Enabled bool     `json:"enabled"`
}

type OpenCodeScanner struct{}

func (s *OpenCodeScanner) Name() string {
	return "opencode"
}

func (s *OpenCodeScanner) Scan(ctx context.Context) ([]DiscoveredServer, error) {
	path := expandPath("~/.config/opencode/opencode.json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var cfg opencodeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	var servers []DiscoveredServer
	for name, srv := range cfg.MCP {
		if len(srv.Command) == 0 {
			continue
		}

		enabled := srv.Enabled
		command := srv.Command[0]
		args := srv.Command[1:]

		cwd := ""
		if len(srv.Command) > 0 {
			cwd = filepath.Dir(srv.Command[0])
		}

		servers = append(servers, DiscoveredServer{
			Name:      name,
			Source:    "opencode",
			Transport: "stdio",
			Enabled:   &enabled,
			Stdio: &StdioConfig{
				Command: command,
				Args:    args,
				CWD:     cwd,
			},
		})
	}

	return servers, nil
}
