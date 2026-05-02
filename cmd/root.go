package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

var RootCmd = &cobra.Command{
	Use:   "leanproxy",
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

func Execute() error {
	return RootCmd.Execute()
}

func init() {
	RootCmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose logging")
	RootCmd.PersistentFlags().String("log-level", "info", "Log level (debug, info, warn, error)")
	RootCmd.PersistentFlags().StringVar(&GlobalConfigPath, "config", "", "Path to leanproxy_servers.yaml config file")
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
