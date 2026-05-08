package cmd

import (
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/reporter"
)

func TestCostCmd_Flags(t *testing.T) {
	tests := []struct {
		name  string
		flag  string
		set   string
		get   interface{}
	}{
		{"by-tool", "by-tool", "true", true},
		{"by-server", "by-server", "true", true},
		{"json", "json", "true", true},
		{"reset", "reset", "true", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := costCmd.Flags().Set(tt.flag, tt.set); err != nil {
				t.Fatalf("set flag %s: %v", tt.flag, err)
			}

			switch v := tt.get.(type) {
			case bool:
				got, err := costCmd.Flags().GetBool(tt.flag)
				if err != nil {
					t.Fatalf("get flag %s: %v", tt.flag, err)
				}
				if got != v {
					t.Errorf("flag %s = %v, want %v", tt.flag, got, v)
				}
			}
		})
	}
}

func TestCostCmd_HelpOutput(t *testing.T) {
	cmd := costCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestCostCmd_ResetFlag(t *testing.T) {
	cmd := costCmd
	cmd.SetArgs([]string{"--reset"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("reset flag should not error: %v", err)
	}
}

func TestCostCmd_JsonFlag(t *testing.T) {
	cmd := costCmd
	cmd.SetArgs([]string{"--json"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("json flag should not error: %v", err)
	}
}



func TestGlobalCostTracker(t *testing.T) {
	tracker := reporter.GlobalCostTracker()
	if tracker == nil {
		t.Error("expected non-nil tracker")
	}
}