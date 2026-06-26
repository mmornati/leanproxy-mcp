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
}

func TestMarketplaceCmd_HelpOutput(t *testing.T) {
	output := captureStdout(func() {
		marketplaceCmd.SetArgs([]string{"--help"})
		marketplaceCmd.Execute()
	})

	if !strings.Contains(output, "marketplace") {
		t.Errorf("help output should contain 'marketplace', got: %s", output)
	}
}

func TestMarketplaceSyncCmd_HelpOutput(t *testing.T) {
	marketplaceCmd.SetArgs([]string{"sync", "--help"})
	err := marketplaceCmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}
