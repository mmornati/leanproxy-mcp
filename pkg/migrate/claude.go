package migrate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

type claudeConfig struct {
	MCPServers map[string]claudeServer `json:"mcpServers"`
}

type claudeServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Env     []string `json:"env,omitempty"`
}

type ClaudeScanner struct{}

func (s *ClaudeScanner) Name() string {
	return "claude"
}

func (s *ClaudeScanner) Scan(ctx context.Context) ([]DiscoveredServer, error) {
	var servers []DiscoveredServer

	paths := []string{
		expandPath("~/.claude.json"),
		expandPath("~/.config/claude/mcp_config.json"),
	}

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		var cfg claudeConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			continue
		}

		for name, srv := range cfg.MCPServers {
			servers = append(servers, DiscoveredServer{
				Name:      name,
				Source:    "claude",
				Transport: "stdio",
				Stdio: &StdioConfig{
					Command: srv.Command,
					Args:    srv.Args,
					Env:     srv.Env,
					CWD:     filepath.Dir(srv.Command),
				},
			})
		}
	}

	return servers, nil
}