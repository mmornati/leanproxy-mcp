package bouncer

import (
	"strings"
	"testing"
)

func TestComputeSnapshot_DefaultsApplied(t *testing.T) {
	s := ComputeSnapshot("", "", 0, 0)
	if s.ServerName != "unknown" {
		t.Errorf("ServerName = %q, want %q", s.ServerName, "unknown")
	}
	if s.Transport != "stdio" {
		t.Errorf("Transport = %q, want %q", s.Transport, "stdio")
	}
	if s.EstimatedTools != DefaultEstimatedTools {
		t.Errorf("EstimatedTools = %d, want %d", s.EstimatedTools, DefaultEstimatedTools)
	}
	if s.NativeTokens != DefaultEstimatedTools*75 {
		t.Errorf("NativeTokens = %d, want %d", s.NativeTokens, DefaultEstimatedTools*75)
	}
	if !s.HasRegistryBudget && s.RegistryBudgetNote != "" {
		t.Errorf("HasRegistryBudget false but budget note = %q", s.RegistryBudgetNote)
	}
}

func TestComputeSnapshot_BasicMath(t *testing.T) {
	s := ComputeSnapshot("github", "stdio", 20, 0)
	if s.EstimatedTools != 20 {
		t.Errorf("EstimatedTools = %d, want 20", s.EstimatedTools)
	}
	if s.NativeTokens != 1500 {
		t.Errorf("NativeTokens = %d, want 1500", s.NativeTokens)
	}
	if s.LeanProxyTokens <= 0 {
		t.Errorf("LeanProxyTokens should be positive, got %d", s.LeanProxyTokens)
	}
	if s.SavedTokens <= 0 {
		t.Errorf("SavedTokens should be positive, got %d", s.SavedTokens)
	}
	if s.SavingsPercent <= 0 || s.SavingsPercent >= 100 {
		t.Errorf("SavingsPercent out of range, got %v", s.SavingsPercent)
	}
}

func TestComputeSnapshot_RegistryBudgetReported(t *testing.T) {
	s := ComputeSnapshot("github", "stdio", 10, 1500)
	if !s.HasRegistryBudget {
		t.Error("HasRegistryBudget should be true when tokensPerTurn > 0")
	}
	if !strings.Contains(s.RegistryBudgetNote, "1500") {
		t.Errorf("RegistryBudgetNote = %q, expected it to mention 1500", s.RegistryBudgetNote)
	}
}

func TestComputeSnapshot_NegativeToolsDefaultsUp(t *testing.T) {
	s := ComputeSnapshot("foo", "http", -5, 0)
	if s.EstimatedTools != DefaultEstimatedTools {
		t.Errorf("negative tool count should default, got %d", s.EstimatedTools)
	}
}

func TestEstimateToolsFromDescription(t *testing.T) {
	tests := []struct {
		name string
		desc string
		want int
	}{
		{"empty uses default", "", DefaultEstimatedTools},
		{"short uses default", "tiny description", DefaultEstimatedTools},
		{"medium scales up", strings.Repeat("a", 5*1024), 5 * estimatedToolsPerKB},
		{"floor enforced when below default", strings.Repeat("a", 512), DefaultEstimatedTools},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EstimateToolsFromDescription(tc.desc)
			if got != tc.want {
				t.Errorf("EstimateToolsFromDescription(len=%d) = %d, want %d", len(tc.desc), got, tc.want)
			}
		})
	}
}

func TestFormatSnapshot_ZeroSavings(t *testing.T) {
	s := Snapshot{ServerName: "x", Transport: "stdio", EstimatedTools: 1, NativeTokens: 100, LeanProxyTokens: 100}
	out := FormatSnapshot(s)
	if !strings.Contains(out, "no savings") {
		t.Errorf("FormatSnapshot with zero savings should mention 'no savings', got %q", out)
	}
	if !strings.Contains(out, "x") {
		t.Errorf("FormatSnapshot should include server name, got %q", out)
	}
}

func TestFormatSnapshot_PositiveSavings(t *testing.T) {
	s := Snapshot{
		ServerName:      "github",
		Transport:       "stdio",
		EstimatedTools:  20,
		NativeTokens:    1500,
		LeanProxyTokens: 200,
		SavedTokens:     1300,
		SavingsPercent:  86.6,
	}
	out := FormatSnapshot(s)
	for _, frag := range []string{"github", "stdio", "20", "1500", "200", "1300"} {
		if !strings.Contains(out, frag) {
			t.Errorf("FormatSnapshot = %q, missing %q", out, frag)
		}
	}
}
