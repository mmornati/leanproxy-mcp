package migrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The `response:` block (issue #319) is parsed and validated at load time.

func loadYAML(t *testing.T, content string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "leanproxy_servers.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadConfig(context.Background(), path)
}

func TestLoadConfigResponseGovernor(t *testing.T) {
	cfg, err := loadYAML(t, `
servers: []
response:
  enabled: true
  max_tokens: 6000
  tools:
    - match: "db.*"
      passthrough: true
    - match: "fs.read_file"
      max_tokens: 12000
  spill:
    ttl: 10m
    max_bytes: 1048576
    disk: true
    dir: ~/results
`)
	if err != nil {
		t.Fatal(err)
	}
	r := cfg.Response
	if r == nil || !r.Enabled || r.GlobalMaxTokens() != 6000 || len(r.Tools) != 2 || !r.Spill.Disk {
		t.Fatalf("response block = %+v", r)
	}
	if b := r.BudgetFor("fs.read_file"); b.MaxTokens != 12000 {
		t.Errorf("fs.read_file budget = %+v", b)
	}
	if b := r.BudgetFor("db.query"); !b.Passthrough {
		t.Errorf("db.query budget = %+v", b)
	}
}

func TestLoadConfigResponseGovernorAbsentIsOff(t *testing.T) {
	cfg, err := loadYAML(t, "servers: []\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Response != nil {
		t.Fatalf("response = %+v, want nil (governor off)", cfg.Response)
	}
}

func TestLoadConfigResponseGovernorRejected(t *testing.T) {
	for name, block := range map[string]string{
		"negative budget": "response:\n  max_tokens: -1\n",
		"tiny budget":     "response:\n  max_tokens: 20\n",
		"bad glob":        "response:\n  tools:\n    - match: \"a[\"\n",
		"bad ttl":         "response:\n  spill:\n    ttl: forever\n",
		"relative dir":    "response:\n  spill:\n    dir: results\n",
	} {
		_, err := loadYAML(t, "servers: []\n"+block)
		if err == nil || !strings.Contains(err.Error(), "response.") {
			t.Errorf("%s: LoadConfig error = %v, want a response.* validation error", name, err)
		}
	}
}
