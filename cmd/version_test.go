package cmd

import (
	"testing"
)

func TestVersionCmd_HelpOutput(t *testing.T) {
	cmd := versionCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestRunVersion(t *testing.T) {
	runVersion(versionCmd, []string{})
}