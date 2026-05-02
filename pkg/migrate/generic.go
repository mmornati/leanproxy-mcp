package migrate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

type genericConfig struct {
	MCPServers map[string]genericServer `json:"mcp_servers"`
}

type genericServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Env     []string `json:"env,omitempty"`
}

type GenericScanner struct{}

func (s *GenericScanner) Name() string {
	return "generic"
}

func (s *GenericScanner) Scan(ctx context.Context) ([]DiscoveredServer, error) {
	path := expandPath("~/.config/mcp.json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var cfg genericConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	var servers []DiscoveredServer
	for name, srv := range cfg.MCPServers {
		servers = append(servers, DiscoveredServer{
			Name:      name,
			Source:    "generic",
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