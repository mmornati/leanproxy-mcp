package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// VSCodeScanner reads VS Code's MCP servers (VS Code, VS Code Insiders and
// VSCodium):
//   - "servers" of the user profile's mcp.json,
//   - "mcp": {"servers": ...} (or "mcp.servers") of the user settings.json,
//   - "servers" of the workspace .vscode/mcp.json in the current directory.
type VSCodeScanner struct {
	// goos and workspaceDir override runtime.GOOS and the current
	// directory (tests).
	goos         string
	workspaceDir string
}

func (s *VSCodeScanner) Name() string {
	return "vscode"
}

func (s *VSCodeScanner) Scan(ctx context.Context) ([]DiscoveredServer, error) {
	var servers []DiscoveredServer
	var errs []error
	add := func(found []DiscoveredServer, e []error) {
		servers = append(servers, found...)
		errs = append(errs, e...)
	}

	for _, dir := range vscodeUserDirs(s.goos) {
		add(scanServerMapFile(filepath.Join(dir, "mcp.json"), "vscode", "servers"))
		add(scanVSCodeSettings(filepath.Join(dir, "settings.json")))
	}

	workspace := s.workspaceDir
	if workspace == "" {
		workspace, _ = os.Getwd()
	}
	if workspace != "" {
		add(scanServerMapFile(filepath.Join(workspace, ".vscode", "mcp.json"), "vscode", "servers"))
	}

	return servers, errors.Join(errs...)
}

// scanVSCodeSettings reads the servers configured in a settings.json, either
// nested ("mcp": {"servers": {...}}) or as the flat "mcp.servers" key.
func scanVSCodeSettings(path string) ([]DiscoveredServer, []error) {
	doc, found, err := readConfigDoc(path)
	if err != nil || !found {
		return nil, errSlice(err)
	}
	servers, errs := convertServerMap(doc["mcp.servers"], "vscode", path, "mcp.servers")
	if raw, ok := doc["mcp"]; ok {
		var mcp struct {
			Servers json.RawMessage `json:"servers"`
		}
		if err := json.Unmarshal(raw, &mcp); err != nil {
			return servers, append(errs, fmt.Errorf("%s: mcp: %w", path, err))
		}
		found, e := convertServerMap(mcp.Servers, "vscode", path, "mcp.servers")
		servers = append(servers, found...)
		errs = append(errs, e...)
	}
	return servers, errs
}

// vscodeUserDirs returns the user profile directories of VS Code, VS Code
// Insiders and VSCodium.
func vscodeUserDirs(goos string) []string {
	if goos == "" {
		goos = runtime.GOOS
	}
	home := homeDir()
	var base string
	switch goos {
	case "darwin":
		base = filepath.Join(home, "Library", "Application Support")
	case "windows":
		base = appDataDir(home)
	default:
		base = filepath.Join(home, ".config")
	}
	products := []string{"Code", "Code - Insiders", "VSCodium"}
	dirs := make([]string, len(products))
	for i, p := range products {
		dirs[i] = filepath.Join(base, p, "User")
	}
	return dirs
}
