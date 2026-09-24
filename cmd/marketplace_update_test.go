package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/spf13/cobra"
)

func TestMarketplaceOutdatedCmd_Registered(t *testing.T) {
	var found bool
	for _, c := range marketplaceCmd.Commands() {
		if c.Use == "outdated" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("'outdated' subcommand not registered on marketplaceCmd")
	}
}

func TestMarketplaceUpdateCmd_Registered(t *testing.T) {
	var found bool
	for _, c := range marketplaceCmd.Commands() {
		if c.Use == "update <name>" {
			found = true
			if c.Args == nil {
				t.Error("update command should declare an Args validator")
			}
			break
		}
	}
	if !found {
		t.Fatal("'update' subcommand not registered on marketplaceCmd")
	}
}

func writeConfigYAML(t *testing.T, path, yaml string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunMarketplaceOutdated_NoServers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("LEANPROXY_CONFIG", filepath.Join(dir, "cfg", "leanproxy_servers.yaml"))

	stdout := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})

	if err := runMarketplaceOutdated(cmd, nil); err != nil {
		t.Fatalf("runMarketplaceOutdated: %v", err)
	}
	if !strings.Contains(stdout.String(), "No servers") {
		t.Errorf("expected 'No servers' message, got %q", stdout.String())
	}
}

func TestRunMarketplaceOutdated_ListsVersionDrift(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	cfgPath := filepath.Join(dir, "cfg", "leanproxy_servers.yaml")
	t.Setenv("LEANPROXY_CONFIG", cfgPath)

	writeConfigYAML(t, cfgPath, `version: "1.0"
servers:
  - name: github
    transport: stdio
    enabled: true
    stdio:
      command: npx
      args: ["-y", "server-github@1.0.0"]
    installed_from:
      registry: official
      name: github
      version: "1.0.0"
      installed_at: "2026-01-01T00:00:00Z"
`)
	writeIndex(t, filepath.Join(dir, ".leanproxy"), registry.FeedIndex{
		SyncedAt: testNow(t),
		Entries: []registry.RegistryFeedEntry{
			{Name: "github", Transport: "stdio", Command: "npx", Version: "1.2.0", Source: "official"},
		},
	})

	stdout := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})

	if err := runMarketplaceOutdated(cmd, nil); err != nil {
		t.Fatalf("runMarketplaceOutdated: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "github") || !strings.Contains(out, "1.0.0") || !strings.Contains(out, "1.2.0") {
		t.Errorf("expected outdated row for github 1.0.0 -> 1.2.0, got %q", out)
	}
}

func TestRunMarketplaceUpdate_YesFlagUpdatesConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	cfgPath := filepath.Join(dir, "cfg", "leanproxy_servers.yaml")
	t.Setenv("LEANPROXY_CONFIG", cfgPath)

	writeConfigYAML(t, cfgPath, `version: "1.0"
servers:
  - name: github
    transport: stdio
    enabled: true
    stdio:
      command: npx
      args: ["-y", "@modelcontextprotocol/server-github@1.0.0"]
    installed_from:
      registry: official
      name: github
      version: "1.0.0"
      installed_at: "2026-01-01T00:00:00Z"
`)
	writeIndex(t, filepath.Join(dir, ".leanproxy"), registry.FeedIndex{
		SyncedAt: testNow(t),
		Entries: []registry.RegistryFeedEntry{
			{
				Name: "github", Transport: "stdio",
				Version: "1.2.0", Source: "official",
				PackageRegistry: "npm", PackageIdentifier: "@modelcontextprotocol/server-github",
			},
		},
	})

	stdout := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})

	prevYes := marketplaceUpdateYes
	marketplaceUpdateYes = true
	defer func() { marketplaceUpdateYes = prevYes }()

	if err := runMarketplaceUpdate(cmd, []string{"github"}); err != nil {
		t.Fatalf("runMarketplaceUpdate: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "@modelcontextprotocol/server-github@1.2.0") {
		t.Errorf("expected config to pin the new version: %s", data)
	}
	if !strings.Contains(string(data), "enabled: true") {
		t.Errorf("update should preserve the previous enabled state: %s", data)
	}
}

func TestRunMarketplaceUpdate_DryRunNoWrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	cfgPath := filepath.Join(dir, "cfg", "leanproxy_servers.yaml")
	t.Setenv("LEANPROXY_CONFIG", cfgPath)

	writeConfigYAML(t, cfgPath, `version: "1.0"
servers:
  - name: github
    transport: stdio
    enabled: false
    stdio:
      command: npx
      args: ["-y", "server-github@1.0.0"]
`)
	writeIndex(t, filepath.Join(dir, ".leanproxy"), registry.FeedIndex{
		SyncedAt: testNow(t),
		Entries: []registry.RegistryFeedEntry{
			{Name: "github", Transport: "stdio", Command: "npx", Version: "1.2.0"},
		},
	})

	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	stdout := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})

	prevDry := marketplaceUpdateDryRun
	marketplaceUpdateDryRun = true
	defer func() { marketplaceUpdateDryRun = prevDry }()

	if err := runMarketplaceUpdate(cmd, []string{"github"}); err != nil {
		t.Fatalf("runMarketplaceUpdate: %v", err)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("dry-run should not modify the config: before=%s after=%s", before, after)
	}
	if !strings.Contains(stdout.String(), "Dry-run") {
		t.Errorf("expected dry-run message, got %q", stdout.String())
	}
}

func TestRunMarketplaceUpdate_UnknownServer(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("LEANPROXY_CONFIG", filepath.Join(dir, "cfg", "leanproxy_servers.yaml"))

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := runMarketplaceUpdate(cmd, []string{"missing"}); err == nil {
		t.Fatal("expected error for a server that is not configured")
	}
}
