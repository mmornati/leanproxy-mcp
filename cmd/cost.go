package cmd

import (
	"fmt"

	"github.com/mmornati/leanproxy-mcp/pkg/reporter"
	"github.com/spf13/cobra"
)

var costCmd = &cobra.Command{
	Use:   "cost",
	Short: "Display token cost attribution statistics",
	Long:  `Display token usage broken down by tool and server for the current session.`,
	Run:   runCost,
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
