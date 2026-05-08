package cmd

import (
	"testing"
)

func TestCacheCmd_Flags(t *testing.T) {
	tests := []struct {
		name  string
		flag  string
		set   string
		isBool bool
	}{
		{"list", "list", "true", true},
		{"server", "server", "testserver", false},
		{"search", "search", "testtool", false},
		{"json", "json", "true", true},
		{"clear", "clear", "true", true},
		{"location", "location", "true", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := cacheCmd.Flags().Set(tt.flag, tt.set); err != nil {
				t.Fatalf("set flag %s: %v", tt.flag, err)
			}

			if tt.isBool {
				got, err := cacheCmd.Flags().GetBool(tt.flag)
				if err != nil {
					t.Fatalf("get flag %s: %v", tt.flag, err)
				}
				if !got {
					t.Errorf("flag %s = %v, want true", tt.flag, got)
				}
			} else {
				got, err := cacheCmd.Flags().GetString(tt.flag)
				if err != nil {
					t.Fatalf("get flag %s: %v", tt.flag, err)
				}
				if got != tt.set {
					t.Errorf("flag %s = %v, want %v", tt.flag, got, tt.set)
				}
			}
		})
	}
}

func TestCacheCmd_HelpOutput(t *testing.T) {
	cmd := cacheCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestCacheCmd_ListFlag(t *testing.T) {
	cmd := cacheCmd
	cmd.SetArgs([]string{"--list"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("list flag should not error: %v", err)
	}
}

func TestCacheCmd_LocationFlag(t *testing.T) {
	cmd := cacheCmd
	cmd.SetArgs([]string{"--location"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("location flag should not error: %v", err)
	}
}