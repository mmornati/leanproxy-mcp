package migrate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
)

// ClaudeDesktopScanner reads the "mcpServers" of Claude Desktop's
// claude_desktop_config.json.
type ClaudeDesktopScanner struct {
	// goos overrides runtime.GOOS (tests).
	goos string
}

func (s *ClaudeDesktopScanner) Name() string {
	return "claude-desktop"
}

func (s *ClaudeDesktopScanner) Scan(ctx context.Context) ([]DiscoveredServer, error) {
	return scanFiles([]string{claudeDesktopConfigPath(s.goos)}, "mcpServers", "claude-desktop")
}

func claudeDesktopConfigPath(goos string) string {
	if goos == "" {
		goos = runtime.GOOS
	}
	home := homeDir()
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		return filepath.Join(appDataDir(home), "Claude", "claude_desktop_config.json")
	default:
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	}
}

// appDataDir returns %APPDATA%, or its usual location under home.
func appDataDir(home string) string {
	if d := os.Getenv("APPDATA"); d != "" {
		return d
	}
	return filepath.Join(home, "AppData", "Roaming")
}
