package migrate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

type cursorConfig struct {
	MCPServers map[string]cursorServer `json:"mcp_servers"`
}

type cursorServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Env     []string `json:"env,omitempty"`
}

type CursorScanner struct{}

func (s *CursorScanner) Name() string {
	return "cursor"
}

func (s *CursorScanner) Scan(ctx context.Context) ([]DiscoveredServer, error) {
	path := expandPath("~/.cursor/mcp.json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var cfg cursorConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	var servers []DiscoveredServer
	for name, srv := range cfg.MCPServers {
		servers = append(servers, DiscoveredServer{
			Name:      name,
			Source:    "cursor",
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