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
	if len(subcommands) != 1 {
		t.Fatalf("expected 1 subcommand, got %d", len(subcommands))
	}

	if subcommands[0].Use != "sync" {
		t.Errorf("expected subcommand 'sync', got '%s'", subcommands[0].Use)
	}
	if subcommands[0].Args == nil {
		t.Errorf("sync subcommand should declare an Args validator (cobra.NoArgs)")
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
