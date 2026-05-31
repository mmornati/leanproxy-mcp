package cmd

import (
	"testing"
)

func TestSavingsCmd_Flags(t *testing.T) {
	tests := []struct {
		name string
		flag string
		set  string
		get  interface{}
	}{
		{"reset", "reset", "true", true},
		{"server", "server", "testserver", "testserver"},
		{"json", "json", "true", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := savingsCmd.Flags().Set(tt.flag, tt.set); err != nil {
				t.Fatalf("set flag %s: %v", tt.flag, err)
			}

			switch v := tt.get.(type) {
			case bool:
				got, err := savingsCmd.Flags().GetBool(tt.flag)
				if err != nil {
					t.Fatalf("get flag %s: %v", tt.flag, err)
				}
				if got != v {
					t.Errorf("flag %s = %v, want %v", tt.flag, got, v)
				}
			case string:
				got, err := savingsCmd.Flags().GetString(tt.flag)
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

func TestSavingsCmd_HelpOutput(t *testing.T) {
	cmd := savingsCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestSavingsCmd_ResetFlag(t *testing.T) {
	cmd := savingsCmd
	cmd.SetArgs([]string{"--reset"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("reset flag should not error: %v", err)
	}
}

func TestGlobalSavingsTracker(t *testing.T) {
	if globalSavingsTracker == nil {
		t.Error("expected non-nil tracker")
	}
}

func TestDisplayCumulativeSavings(t *testing.T) {
	globalSavingsTracker.Reset()
	displayCumulativeSavings()
}

func TestDisplayServerSavings_NotFound(t *testing.T) {
	globalSavingsTracker.Reset()
	displayServerSavings("nonexistent")
}

func TestDisplayServerSavings(t *testing.T) {
	globalSavingsTracker.Reset()
	displayServerSavings("")
}
