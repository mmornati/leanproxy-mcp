package mcp

import (
	"testing"
)

func TestEnrichError(t *testing.T) {
	tests := []struct {
		name          string
		original      string
		wantContains  string
		wantHintEmoji bool
	}{
		{
			name:          "repository not found",
			original:      "Could not resolve to a Repository with the name 'anomalyco/leanproxy-mcp'",
			wantContains:  "Check the owner/repo spelling",
			wantHintEmoji: true,
		},
		{
			name:          "server not running",
			original:      "server github is not running (state: stopped)",
			wantContains:  "Run 'leanproxy server start",
			wantHintEmoji: true,
		},
		{
			name:          "no match",
			original:      "some random error",
			wantContains:  "some random error",
			wantHintEmoji: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EnrichError(tt.original)
			if tt.wantHintEmoji {
				if len(got) <= len(tt.original) {
					t.Errorf("EnrichError() = %v, original was not enriched", got)
				}
			}
			if got == "" {
				t.Error("EnrichError() should not return empty string")
			}
		})
	}
}

func TestGetErrorHint(t *testing.T) {
	hint := GetErrorHint("Could not resolve to a Repository")
	if hint == nil {
		t.Fatal("hint should not be nil for repository not found")
	}

	if hint.Suggestion == "" {
		t.Error("hint should have suggestion")
	}

	if hint.Action == "" {
		t.Error("hint should have action")
	}
}

func TestGetErrorHintNoMatch(t *testing.T) {
	hint := GetErrorHint("random unrelated error")
	if hint != nil {
		t.Errorf("GetErrorHint() = %v, want nil", hint)
	}
}

func TestFormatErrorWithHint(t *testing.T) {
	result := FormatErrorWithHint("test error", "github", "github_list_issues")
	if result == "" {
		t.Error("result should not be empty")
	}

	if len(result) <= len("test error") {
		t.Error("result should contain additional context")
	}
}

func TestAddErrorContext(t *testing.T) {
	result := AddErrorContext("original error", "github", "list_issues")
	if result == "" {
		t.Error("result should not be empty")
	}
}

func TestAddErrorContextEmpty(t *testing.T) {
	result := AddErrorContext("original error", "", "")
	if result != "original error" {
		t.Error("result should be unchanged when no context provided")
	}
}
