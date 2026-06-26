package cmd

import (
	"strings"
	"testing"
)

func TestMarketplaceCmd_Subcommands(t *testing.T) {
	if marketplaceCmd == nil {
		t.Fatal("marketplaceCmd is nil")
	}

	subcommands := marketplaceCmd.Commands()
	if len(subcommands) != 2 {
		t.Fatalf("expected 2 subcommands, got %d", len(subcommands))
	}

	seen := map[string]bool{}
	for _, c := range subcommands {
		seen[c.Use] = true
	}
	if !seen["sync"] {
		t.Errorf("expected subcommand 'sync'")
	}
	if !seen["search <query>"] {
		t.Errorf("expected subcommand 'search'")
	}
}

func TestMarketplaceCmd_HelpOutput(t *testing.T) {
	cmd := marketplaceCmd

	if !strings.Contains(cmd.Long, "marketplace") && !strings.Contains(cmd.Short, "marketplace") {
		t.Errorf("marketplace command metadata should mention marketplace")
	}
	for _, sub := range cmd.Commands() {
		if sub.Use == "sync" {
			return
		}
	}
	t.Errorf("marketplace command should have a 'sync' subcommand registered")
}

func TestMarketplaceSyncCmd_HelpOutput(t *testing.T) {
	cmd := marketplaceSyncCmd
	if !strings.Contains(cmd.Long, "Registry") && !strings.Contains(cmd.Short, "Registry") {
		t.Errorf("sync help should mention 'Registry', got: %s", cmd.Long)
	}
}

func TestMarketplaceSyncCmd_RejectsPositionalArgs(t *testing.T) {
	cmd := marketplaceSyncCmd
	if cmd.Args == nil {
		t.Fatal("expected Args validator on sync command")
	}
	if err := cmd.Args(cmd, []string{"unexpected-positional"}); err == nil {
		t.Error("expected error when positional args are passed to sync")
	}
}
