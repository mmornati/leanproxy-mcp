package bouncer

import (
	"fmt"

	"github.com/mmornati/leanproxy-mcp/pkg/utils"
)

const (
	// DefaultEstimatedTools is the default tool-count estimate when the
	// registry entry does not declare one. It matches the average used by
	// pkg/utils.TokenEstimator.CompareMCPConfigurations.
	DefaultEstimatedTools = 35

	// estimatedToolsPerKB is a coarse heuristic used to derive a tool count
	// from the registry entry's description when nothing else is available.
	estimatedToolsPerKB = 40
)

// Snapshot describes the token impact of adding a server to a LeanProxy
// deployment. It is computed from a registry feed entry before the server is
// installed so the user can preview the cost.
type Snapshot struct {
	ServerName         string
	Transport          string
	EstimatedTools     int
	NativeTokens       int
	LeanProxyTokens    int
	SavedTokens        int
	SavingsPercent     float64
	HasRegistryBudget  bool
	RegistryBudgetNote string
}

// ComputeSnapshot returns a token-cost preview for the given server definition.
//
// The estimate is derived from the entry's declared tool count when available
// (TokensPerTurn is treated as a per-turn override), otherwise from a default
// heuristic. The LeanProxy cost assumes a single gateway schema (invoke_tool +
// list_tools) regardless of how many real tools the server exposes.
//
// The returned Snapshot is safe for direct fmt.Stringer-style printing via
// FormatSnapshot.
func ComputeSnapshot(name, transport string, estimatedTools int, tokensPerTurn int64) Snapshot {
	if name == "" {
		name = "unknown"
	}
	if transport == "" {
		transport = "stdio"
	}
	if estimatedTools <= 0 {
		estimatedTools = DefaultEstimatedTools
	}

	estimator := utils.NewTokenEstimator()
	native := estimator.EstimateNativeMCPOverhead(name, estimatedTools)
	lean := estimator.EstimateLeanProxySchemaTokens()

	saved := native - lean
	if saved < 0 {
		saved = 0
	}
	var pct float64
	if native > 0 {
		pct = float64(saved) / float64(native) * 100
	}

	return Snapshot{
		ServerName:         name,
		Transport:          transport,
		EstimatedTools:     estimatedTools,
		NativeTokens:       native,
		LeanProxyTokens:    lean,
		SavedTokens:        saved,
		SavingsPercent:     pct,
		HasRegistryBudget:  tokensPerTurn > 0,
		RegistryBudgetNote: formatBudgetNote(tokensPerTurn),
	}
}

// EstimateToolsFromDescription derives a coarse tool-count estimate from the
// size of the registry entry's description. It is intentionally simple so it
// can be used as a fallback when no other signal is present; callers should
// prefer ComputeSnapshot with an explicit tool count whenever possible.
func EstimateToolsFromDescription(description string) int {
	if len(description) == 0 {
		return DefaultEstimatedTools
	}
	kb := len(description) / 1024
	if kb == 0 {
		return DefaultEstimatedTools
	}
	estimate := kb * estimatedToolsPerKB
	if estimate < DefaultEstimatedTools {
		return DefaultEstimatedTools
	}
	return estimate
}

// FormatSnapshot renders the snapshot as a short human-readable block ready to
// print to stdout. Lines are kept short for terminal readability.
func FormatSnapshot(s Snapshot) string {
	if s.SavedTokens <= 0 {
		return fmt.Sprintf(
			"Token-cost preview for %s (%s): ~%d native tokens / %d lean tokens (no savings)",
			s.ServerName, s.Transport, s.NativeTokens, s.LeanProxyTokens,
		)
	}
	return fmt.Sprintf(
		"Token-cost preview for %s (%s, ~%d tools): ~%d native tokens vs. %d lean tokens (saves ~%d tokens / %s%%)",
		s.ServerName, s.Transport, s.EstimatedTools,
		s.NativeTokens, s.LeanProxyTokens, s.SavedTokens, formatPercent(s.SavingsPercent),
	)
}

func formatPercent(p float64) string {
	if p <= 0 {
		return "0"
	}
	// One decimal place without trailing zeros.
	whole := int(p)
	if p-float64(whole) < 0.05 {
		return fmt.Sprintf("%d", whole)
	}
	return fmt.Sprintf("%.1f", p)
}

func formatBudgetNote(tokensPerTurn int64) string {
	if tokensPerTurn <= 0 {
		return ""
	}
	return fmt.Sprintf("Registry reports ~%d tokens/turn baseline.", tokensPerTurn)
}
