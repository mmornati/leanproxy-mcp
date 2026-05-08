package cmd

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestInitLogger_DebugLevel(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("log-level", "debug", "Log level")
	cmd.Flags().String("log-file", "", "Log file")
	cmd.Flags().Bool("verbose", false, "Verbose")

	initLogger(cmd)

	if slog.Default().Handler() == nil {
		t.Error("expected default logger to be set")
	}
}

func TestInitLogger_InfoLevel(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("log-level", "info", "Log level")
	cmd.Flags().String("log-file", "", "Log file")
	cmd.Flags().Bool("verbose", false, "Verbose")

	initLogger(cmd)
}

func TestInitLogger_WarnLevel(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("log-level", "warn", "Log level")
	cmd.Flags().String("log-file", "", "Log file")
	cmd.Flags().Bool("verbose", false, "Verbose")

	initLogger(cmd)
}

func TestInitLogger_ErrorLevel(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("log-level", "error", "Log level")
	cmd.Flags().String("log-file", "", "Log file")
	cmd.Flags().Bool("verbose", false, "Verbose")

	initLogger(cmd)
}

func TestInitLogger_InvalidLevel(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("log-level", "invalid", "Log level")
	cmd.Flags().String("log-file", "", "Log file")
	cmd.Flags().Bool("verbose", false, "Verbose")

	initLogger(cmd)
}

func TestInitLogger_VerboseEnablesDebug(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("log-level", "info", "Log level")
	cmd.Flags().String("log-file", "", "Log file")
	cmd.Flags().Bool("verbose", true, "Verbose")

	initLogger(cmd)
}

func TestInitLogger_LogFile(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	cmd := &cobra.Command{}
	cmd.Flags().String("log-level", "info", "Log level")
	cmd.Flags().String("log-file", logFile, "Log file")
	cmd.Flags().Bool("verbose", false, "Verbose")

	initLogger(cmd)

	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		t.Errorf("expected log file to be created: %v", err)
	}
}

func TestInitLogger_LogFileInvalidDir(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "nonexistent", "subdir", "test.log")

	cmd := &cobra.Command{}
	cmd.Flags().String("log-level", "info", "Log level")
	cmd.Flags().String("log-file", logFile, "Log file")
	cmd.Flags().Bool("verbose", false, "Verbose")

	initLogger(cmd)
}

func TestVerboseEnabled(t *testing.T) {
	tests := []struct {
		name     string
		verbose  bool
		expected bool
	}{
		{"verbose true", true, true},
		{"verbose false", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.Flags().BoolP("verbose", "v", tt.verbose, "Verbose")

			result := verboseEnabled(cmd)
			if result != tt.expected {
				t.Errorf("verboseEnabled() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestVerboseEnabled_FlagError(t *testing.T) {
	cmd := &cobra.Command{}

	result := verboseEnabled(cmd)
	if result != false {
		t.Errorf("expected false when flag error, got %v", result)
	}
}



func TestUserConfigPath_EnvVar(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test.yaml")
	t.Setenv("LEANPROXY_CONFIG", configPath)

	result := userConfigPath()
	if result != configPath {
		t.Errorf("userConfigPath() = %v, want %v", result, configPath)
	}
}

func TestUserConfigPath_HomeDir(t *testing.T) {
	t.Setenv("LEANPROXY_CONFIG", "")

	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("HOME not set")
	}

	result := userConfigPath()
	expected := filepath.Join(home, ".config", "leanproxy_servers.yaml")
	if result != expected {
		t.Errorf("userConfigPath() = %v, want %v", result, expected)
	}
}

func TestUserConfigPath_UserProfile(t *testing.T) {
	t.Setenv("LEANPROXY_CONFIG", "")
	t.Setenv("HOME", "")

	userProfile := os.Getenv("USERPROFILE")
	if userProfile == "" {
		t.Setenv("USERPROFILE", t.TempDir())
	}

	result := userConfigPath()
	expected := filepath.Join(os.Getenv("USERPROFILE"), ".config", "leanproxy_servers.yaml")
	if result != expected {
		t.Errorf("userConfigPath() = %v, want %v", result, expected)
	}
}