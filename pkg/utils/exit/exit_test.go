package exit

import (
	"testing"
)

func TestExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		code     int
		expected int
	}{
		{"Success is 0", Success, 0},
		{"General error is 1", General, 1},
		{"Misuse is 2", Misuse, 2},
		{"Configuration error is 3", ConfigurationError, 3},
		{"Token resolution failure is 4", TokenResolutionFailure, 4},
		{"Upstream error is 125", UpstreamError, 125},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.code != tt.expected {
				t.Errorf("exit code %s: got %d, want %d", tt.name, tt.code, tt.expected)
			}
		})
	}
}