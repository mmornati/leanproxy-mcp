package migrate

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
)

type vscodeSettings struct {
	MCPExtensions map[string]vscodeMCPExtension `json:"mcpExtensions"`
}

type vscodeMCPExtension struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Env     []string `json:"env,omitempty"`
}

type VSCodeScanner struct{}

func (s *VSCodeScanner) Name() string {
	return "vscode"
}

func (s *VSCodeScanner) Scan(ctx context.Context) ([]DiscoveredServer, error) {
	var servers []DiscoveredServer

	paths := getVSCodeSettingsPaths()

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		var settings map[string]interface{}
		if err := json.Unmarshal(data, &settings); err != nil {
			continue
		}

		if mcpExts, ok := settings["mcpExtensions"].(map[string]interface{}); ok {
			for name, ext := range mcpExts {
				if extMap, ok := ext.(map[string]interface{}); ok {
					cmd, _ := extMap["command"].(string)
					args, _ := extMap["args"].([]interface{})
					env, _ := extMap["env"].([]interface{})

					var argsList []string
					for _, a := range args {
						if s, ok := a.(string); ok {
							argsList = append(argsList, s)
						}
					}

					var envList []string
					for _, e := range env {
						if s, ok := e.(string); ok {
							envList = append(envList, s)
						}
					}

					servers = append(servers, DiscoveredServer{
						Name:      name,
						Source:    "vscode",
						Transport: "stdio",
						Stdio: &StdioConfig{
							Command: cmd,
							Args:    argsList,
							Env:     envList,
							CWD:     filepath.Dir(cmd),
						},
					})
				}
			}
		}
	}

	return servers, nil
}

func getVSCodeSettingsPaths() []string {
	home := homeDir()
	var paths []string

	switch {
	case isMacOS():
		paths = []string{
			filepath.Join(home, "Library/Application Support/Code/User/settings.json"),
			filepath.Join(home, "Library/Application Support/VSCodium/User/settings.json"),
		}
	case isWindows():
		paths = []string{
			filepath.Join(os.Getenv("APPDATA"), "Code/User/settings.json"),
			filepath.Join(os.Getenv("APPDATA"), "VSCodium/User/settings.json"),
		}
	default:
		paths = []string{
			filepath.Join(home, ".config/Code/User/settings.json"),
			filepath.Join(home, ".config/VSCodium/User/settings.json"),
		}
	}

	return paths
}

func isMacOS() bool {
	if _, err := exec.LookPath("darwin"); err == nil {
		return true
	}
	exec.LookPath("uname")
	cmd := exec.Command("uname")
	out, _ := cmd.Output()
	return string(out) == "Darwin\n"
}

func isWindows() bool {
	return os.Getenv("OS") == "Windows_NT" || filepath.Separator == '\\'
}