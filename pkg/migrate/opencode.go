package migrate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

type opencodeConfig struct {
	MCPServers map[string]opencodeServer `json:"mcp_servers"`
}

type opencodeServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Env     []string `json:"env,omitempty"`
}

type OpenCodeScanner struct{}

func (s *OpenCodeScanner) Name() string {
	return "opencode"
}

func (s *OpenCodeScanner) Scan(ctx context.Context) ([]DiscoveredServer, error) {
	path := expandPath("~/.config/opencode/mcp.json")

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
	for name, srv := range cfg.MCPServers {
		servers = append(servers, DiscoveredServer{
			Name:      name,
			Source:    "opencode",
			Transport: "stdio",
			Stdio: &StdioConfig{
				Command: srv.Command,
				Args:    srv.Args,
				Env:     srv.Env,
				CWD:     filepath.Dir(srv.Command),
			},
		})
	}

	return servers, nil
}