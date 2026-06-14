package cmd

import (
	"testing"
)

func TestBouncerCmd_HelpOutput(t *testing.T) {
	cmd := bouncerCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestValidatePatternsCmd_HelpOutput(t *testing.T) {
	cmd := validatePatternsCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestListPatternsCmd_HelpOutput(t *testing.T) {
	cmd := listPatternsCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestBouncerCmd_ListPatterns(t *testing.T) {
	cmd := listPatternsCmd
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("list patterns should not error: %v", err)
	}
}

func TestBouncerCmd_PersistentFlags(t *testing.T) {
	if err := bouncerCmd.PersistentFlags().Set("config", "/tmp/test.yaml"); err != nil {
		t.Fatalf("set config flag: %v", err)
	}

	got, err := bouncerCmd.PersistentFlags().GetString("config")
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if got != "/tmp/test.yaml" {
		t.Errorf("config = %v, want /tmp/test.yaml", got)
	}
}
