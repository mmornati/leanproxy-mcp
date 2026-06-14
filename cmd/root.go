package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var RootCmd = &cobra.Command{
	Use:   "leanproxy-mcp",
	Short: "LeanProxy MCP - A JSON-RPC streaming proxy with token validation",
	Long: `LeanProxy MCP provides secure JSON-RPC streaming proxy capabilities
with token validation, MCP server registry, and configurable redaction.

Features:
  - JSON-RPC streaming proxy
  - Token validation and authentication
  - MCP server registry management
  - Configurable redaction patterns

For full documentation, see: https://github.com/mmornati/leanproxy-mcp#readme`,
	SilenceUsage: true,
}

var GlobalConfigPath string
var DryRunEnabled bool

func Execute() error {
	return RootCmd.Execute()
}

func init() {
	RootCmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose logging")
	RootCmd.PersistentFlags().String("log-level", "info", "Log level (debug, info, warn, error)")
	RootCmd.PersistentFlags().StringVar(&GlobalConfigPath, "config", "", "Path to leanproxy_servers.yaml config file")
	RootCmd.PersistentFlags().BoolP("dry-run", "n", false, "Preview actions without making changes")
	RootCmd.PersistentFlags().String("log-file", "", "Path to log file (logs to stderr if not specified)")
}

func initLogger(cmd *cobra.Command) {
	logFile, _ := cmd.Flags().GetString("log-file")
	logLevelStr, _ := cmd.Flags().GetString("log-level")
	verbose, _ := cmd.Flags().GetBool("verbose")

	var level slog.Level
	switch logLevelStr {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	if verbose {
		level = slog.LevelDebug
	}

	var handler slog.Handler

	if logFile != "" {
		dir := filepath.Dir(logFile)
		if err := os.MkdirAll(dir, 0750); err != nil {
			slog.Warn("failed to create log directory", "path", dir, "error", err)
		}
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600) // #nosec G304 -- log file path from CLI flag
		if err != nil {
			slog.Warn("failed to open log file", "path", logFile, "error", err)
			handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
		} else {
			handler = slog.NewTextHandler(f, &slog.HandlerOptions{Level: level})
		}
	} else {
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
}

func verboseEnabled(cmd *cobra.Command) bool {
	v, err := cmd.Flags().GetBool("verbose")
	if err != nil {
		return false
	}
	return v
}

func logError(format string, args ...interface{}) {
	slog.Error(fmt.Sprintf(format, args...))
	os.Exit(1)
}
