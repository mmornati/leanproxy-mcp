package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidatePath(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name      string
		path      string
		baseDir   string
		wantErr   bool
		errMsg    string
	}{
		{
			name:    "valid path within base",
			path:    filepath.Join(tmpDir, "config.yaml"),
			baseDir: tmpDir,
			wantErr: false,
		},
		{
			name:    "valid nested path within base",
			path:    filepath.Join(tmpDir, "subdir", "config.yaml"),
			baseDir: tmpDir,
			wantErr: false,
		},
		{
			name:    "path traversal attempt",
			path:    filepath.Join(tmpDir, "..", "..", "etc", "passwd"),
			baseDir: tmpDir,
			wantErr: true,
			errMsg:  "path traversal detected",
		},
		{
			name:    "URL encoded path traversal",
			path:    "../../../etc/passwd",
			baseDir: tmpDir,
			wantErr: true,
			errMsg:  "path traversal",
		},
		{
			name:    "double encoded path traversal",
			path:    "..%2F..%2F..%2Fetc%2Fpasswd",
			baseDir: tmpDir,
			wantErr: true,
			errMsg:  "path traversal",
		},
		{
			name:    "null byte injection",
			path:    filepath.Join(tmpDir, "config.yaml\x00"),
			baseDir: tmpDir,
			wantErr: true,
			errMsg:  "null byte",
		},
		{
			name:    "sibling file outside base",
			path:    filepath.Join(tmpDir, "..", "other_config.yaml"),
			baseDir: tmpDir,
			wantErr: true,
			errMsg:  "path traversal detected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePath(tt.path, tt.baseDir)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidatePath() expected error containing %q, got nil", tt.errMsg)
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidatePath() error = %v, want error containing %q", err, tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("ValidatePath() unexpected error: %v", err)
			}
		})
	}
}

func TestValidatePath_ValidAbsolutePath(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("test"), 0644); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}

	if err := ValidatePath(configPath, tmpDir); err != nil {
		t.Errorf("ValidatePath() should not fail for valid absolute path: %v", err)
	}
}

func TestValidatePath_NonExistentFile(t *testing.T) {
	tmpDir := t.TempDir()
	nonExistent := filepath.Join(tmpDir, "nonexistent.yaml")

	if err := ValidatePath(nonExistent, tmpDir); err != nil {
		t.Errorf("ValidatePath() should not fail for non-existent file: %v", err)
	}
}