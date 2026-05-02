package migrate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mmornati/leanproxy-mcp/pkg/registry"
)

type DiscoveredServer struct {
	Name      string
	Source    string
	Transport registry.TransportType
	Stdio     *StdioConfig
	HTTP      *HTTPConfig
}

type Scanner interface {
	Name() string
	Scan(ctx context.Context) ([]DiscoveredServer, error)
}

func ExecutableExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE")
}

func expandPath(path string) string {
	if path == "" {
		return ""
	}
	if path == "~" {
		return homeDir()
	}
	if len(path) > 1 && path[:2] == "~/" {
		return filepath.Join(homeDir(), path[2:])
	}
	return path
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func validateTransport(transport string) (registry.TransportType, error) {
	switch transport {
	case "stdio":
		return registry.TransportStdio, nil
	case "http":
		return registry.TransportHTTP, nil
	case "sse":
		return registry.TransportSSE, nil
	default:
		return "", fmt.Errorf("invalid transport type: %s", transport)
	}
}