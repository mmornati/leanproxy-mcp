package migrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type ScanResult struct {
	Scanners []string
	Servers  []DiscoveredServer
	// Skipped are entries that run LeanProxy itself (see
	// IsLeanProxyServer); they are not imported.
	Skipped []DiscoveredServer
	// Warnings are files or entries that could not be read. They do not
	// stop the scan of the other files.
	Warnings []error
}

type MigrationSummary struct {
	OpenCodeCount int
	ClaudeCount   int
	// ClaudeDesktopCount counts Claude Desktop's servers.
	ClaudeDesktopCount int
	VSCodeCount        int
	CursorCount        int
	GenericCount       int
	TotalServers       int
}

func (s *MigrationSummary) Total() int {
	return s.OpenCodeCount + s.ClaudeCount + s.ClaudeDesktopCount + s.VSCodeCount + s.CursorCount + s.GenericCount
}

type ImportResult struct {
	Imported   int
	Duplicates int
	Errors     []error
	Validation *ValidationResult
}

type Migrator struct {
	scanners []Scanner
}

func NewMigrator() *Migrator {
	return &Migrator{
		scanners: []Scanner{
			&OpenCodeScanner{},
			&ClaudeScanner{},
			&ClaudeDesktopScanner{},
			&VSCodeScanner{},
			&CursorScanner{},
			&GenericScanner{},
		},
	}
}

func (m *Migrator) Scan(ctx context.Context) (*ScanResult, error) {
	result := &ScanResult{
		Scanners: make([]string, 0),
		Servers:  make([]DiscoveredServer, 0),
	}

	for _, scanner := range m.scanners {
		servers, err := scanner.Scan(ctx)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Errorf("%s: %w", scanner.Name(), err))
		}
		kept := 0
		for _, srv := range servers {
			if IsLeanProxyServer(srv) {
				result.Skipped = append(result.Skipped, srv)
				continue
			}
			result.Servers = append(result.Servers, srv)
			kept++
		}
		if kept > 0 {
			result.Scanners = append(result.Scanners, scanner.Name())
		}
	}

	return result, nil
}

func (m *Migrator) Summarize(servers []DiscoveredServer) *MigrationSummary {
	summary := &MigrationSummary{}

	for _, srv := range servers {
		switch srv.Source {
		case "opencode":
			summary.OpenCodeCount++
		case "claude":
			summary.ClaudeCount++
		case "claude-desktop":
			summary.ClaudeDesktopCount++
		case "vscode":
			summary.VSCodeCount++
		case "cursor":
			summary.CursorCount++
		case "generic":
			summary.GenericCount++
		}
		summary.TotalServers++
	}

	return summary
}

func (m *Migrator) Validate(servers []DiscoveredServer) *ValidationResult {
	validator := NewValidator()
	return validator.ValidateServers(servers)
}

func (m *Migrator) Import(ctx context.Context, servers []DiscoveredServer, targetPath string, yes bool) (*ImportResult, error) {
	if len(servers) == 0 {
		return nil, fmt.Errorf("no servers to import")
	}

	result := &ImportResult{}

	validator := NewValidatorWithoutExecutableCheck()
	validationResult := validator.ValidateServers(servers)
	result.Validation = validationResult

	if validationResult.HasErrors() {
		for _, err := range validationResult.Errors {
			result.Errors = append(result.Errors, &err)
		}
	}

	configPath := expandPath(targetPath)
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create config directory: %w", err)
	}

	var existingServers []*ServerConfig
	if fileExists(configPath) {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("read existing config: %w", err)
		}

		var cfg Config
		if err := yaml.Unmarshal(data, &cfg); err == nil {
			existingServers = cfg.Servers
		}
	}

	existingNames := make(map[string]bool)
	for _, srv := range existingServers {
		existingNames[srv.Name] = true
	}

	newServers := make([]*ServerConfig, 0, len(servers))
	for _, srv := range servers {
		name := srv.Name
		if existingNames[name] {
			name = fmt.Sprintf("%s_%s", name, srv.Source)
		}
		existingNames[name] = true

		sc := &ServerConfig{
			Name:      name,
			Transport: srv.Transport,
			Stdio:     srv.Stdio,
			HTTP:      srv.HTTP,
		}
		if sc.Timeout == "" {
			sc.Timeout = "30s"
		}
		if sc.ConnectTimeout == "" {
			sc.ConnectTimeout = "10s"
		}
		if srv.Enabled != nil {
			sc.Enabled = srv.Enabled
		} else {
			enabled := true
			sc.Enabled = &enabled
		}

		newServers = append(newServers, sc)
		result.Imported++
	}

	allServers := append(existingServers, newServers...)

	cfg := &Config{
		Version: "1.0",
		Servers: allServers,
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	return result, nil
}

func userConfigPath() string {
	if path := os.Getenv("LEANPROXY_CONFIG"); path != "" {
		return path
	}
	return expandPath("~/.config/leanproxy_servers.yaml")
}

func (m *Migrator) ImportAll(ctx context.Context, servers []DiscoveredServer, yes bool) (*ImportResult, error) {
	return m.Import(ctx, servers, userConfigPath(), yes)
}
