//go:build !codemode

package cmd

import (
	"log/slog"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

// installCodeMode: this binary has no code mode (issue #325 is an
// experimental spike, built only with `-tags codemode`). An enabled
// `code_mode:` block is ignored, loudly.
func installCodeMode(_ *mcp.Handler, cfg *migrate.Config) {
	if cfg != nil && cfg.CodeMode.IsEnabled() {
		slog.Warn("code_mode.enabled is set but this binary was built without code mode (-tags codemode): execute_code is not available")
	}
}
