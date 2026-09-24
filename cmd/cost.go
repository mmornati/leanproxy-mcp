package cmd

import (
	"fmt"
	"os"

	"github.com/mmornati/leanproxy-mcp/pkg/reporter"
	"github.com/spf13/cobra"
)

// costCmd's tracker (reporter.GlobalCostTracker) is only fed by
// TrackCostFromStrings/Track, which nothing in the live pipeline calls
// (see issue #324's problem statement). Kept for backward compatibility;
// use `report` for the auditable numbers built from real counters.
var costCmd = &cobra.Command{
	Use:   "cost",
	Short: "[DEPRECATED, estimate-only] Display token cost attribution statistics",
	Long: `DEPRECATED: this command's numbers are estimated/modeled, not measured from
the live pipeline. Use 'leanproxy-mcp report' for an auditable savings
report built entirely from real counters (see docs/savings-report.md).

Display token usage broken down by tool and server for the current session.`,
	Run: runCost,
}

var costFlags struct {
	byTool   bool
	byServer bool
	jsonOut  bool
	reset    bool
}

func init() {
	costCmd.Flags().BoolVar(&costFlags.byTool, "by-tool", false, "Show cost breakdown by tool only")
	costCmd.Flags().BoolVar(&costFlags.byServer, "by-server", false, "Show cost breakdown by server only")
	costCmd.Flags().BoolVar(&costFlags.jsonOut, "json", false, "Output in JSON format")
	costCmd.Flags().BoolVar(&costFlags.reset, "reset", false, "Reset cost counters")
	RootCmd.AddCommand(costCmd)
}

func runCost(cmd *cobra.Command, args []string) {
	tracker := reporter.GlobalCostTracker()

	if !costFlags.jsonOut {
		fmt.Fprint(os.Stderr, savingsDeprecationNotice)
	}
	if costFlags.reset {
		tracker.Reset()
		fmt.Println("Cost counters reset")
		return
	}

	if costFlags.jsonOut {
		output, err := tracker.FormatJSON()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}
		fmt.Println(output)
		return
	}

	output := tracker.FormatCLI(costFlags.byTool, costFlags.byServer)
	fmt.Print(output)
}
