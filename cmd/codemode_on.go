//go:build codemode

package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/mmornati/leanproxy-mcp/pkg/codemode"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

// codeModeWorkerCmd is the sandbox process of code mode (issue #325):
// the proxy starts it for each execute_code call and speaks to it over
// stdin/stdout. It is not meant to be run by hand.
var codeModeWorkerCmd = &cobra.Command{
	Use:    codemode.WorkerCommand,
	Short:  "Code mode sandbox process (internal)",
	Hidden: true,
	Args:   cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		os.Exit(codemode.ServeWorker(os.Stdin, os.Stdout))
	},
}

func init() {
	RootCmd.AddCommand(codeModeWorkerCmd)
}

// installCodeMode adds the execute_code stage (issue #325) innermost in
// the handler's pipeline when `code_mode.enabled` is set.
func installCodeMode(handler *mcp.Handler, cfg *migrate.Config) {
	if cfg == nil || !cfg.CodeMode.IsEnabled() {
		return
	}
	cm := mcp.NewCodeMode(handler, cfg.CodeMode, codemode.Options{})
	handler.Use(mcp.Traced("code_mode", cm.Middleware()))
	slog.Warn(cm.Summary())
}
