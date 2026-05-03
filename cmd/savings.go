package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/mmornati/leanproxy-mcp/pkg/utils"
	"github.com/spf13/cobra"
)

var savingsCmd = &cobra.Command{
	Use:   "savings",
	Short: "Display token savings statistics",
	Long:  `Display cumulative token savings across all requests or filter by server.`,
	Run:   runSavings,
}

var savingsFlags struct {
	reset   bool
	server  string
	jsonOut bool
}

var globalSavingsTracker = utils.NewSavingsTracker()

func init() {
	savingsCmd.Flags().BoolVar(&savingsFlags.reset, "reset", false, "Reset cumulative counters")
	savingsCmd.Flags().StringVar(&savingsFlags.server, "server", "", "Filter savings by server name")
	savingsCmd.Flags().BoolVar(&savingsFlags.jsonOut, "json", false, "Output in JSON format")
	RootCmd.AddCommand(savingsCmd)
}

func runSavings(cmd *cobra.Command, args []string) {
	if savingsFlags.reset {
		globalSavingsTracker.Reset()
		fmt.Println("Savings counters reset")
		return
	}

	if savingsFlags.server != "" {
		displayServerSavings(savingsFlags.server)
	} else {
		displayCumulativeSavings()
	}
}

func displayCumulativeSavings() {
	cumulative := globalSavingsTracker.GetCumulativeSavings()

	if savingsFlags.jsonOut {
		output, _ := json.MarshalIndent(cumulative, "", "  ")
		fmt.Println(string(output))
		return
	}

	fmt.Printf("=== Token Savings Summary ===\n")
	fmt.Printf("Total Original Tokens:  %d\n", cumulative.TotalOriginal)
	fmt.Printf("Total Optimized Tokens: %d\n", cumulative.TotalOptimized)
	fmt.Printf("Total Saved Tokens:     %d\n", cumulative.TotalSaved)
	if cumulative.TotalOriginal > 0 {
		savingsPct := float64(cumulative.TotalSaved) / float64(cumulative.TotalOriginal) * 100
		fmt.Printf("Savings Percentage:     %.2f%%\n", savingsPct)
	}
	fmt.Printf("Session Duration:      %v\n", cumulative.SessionDuration)
	fmt.Printf("Requests Processed:    %d\n", cumulative.RequestsProcessed)

	breakdown := globalSavingsTracker.GetServerBreakdown()
	if len(breakdown) > 0 {
		fmt.Printf("\n=== Savings by Server ===\n")
		for name, ss := range breakdown {
			fmt.Printf("%s: %d tokens saved (%.2f%%)\n",
				name, ss.SavedTokens,
				float64(ss.SavedTokens)/float64(ss.OriginalTokens)*100)
		}
	}
}

func displayServerSavings(serverName string) {
	breakdown := globalSavingsTracker.GetServerBreakdown()

	if ss, exists := breakdown[serverName]; exists {
		if savingsFlags.jsonOut {
			output, _ := json.MarshalIndent(ss, "", "  ")
			fmt.Println(string(output))
			return
		}

		fmt.Printf("=== Server: %s ===\n", serverName)
		fmt.Printf("Original Tokens:  %d\n", ss.OriginalTokens)
		fmt.Printf("Optimized Tokens: %d\n", ss.OptimizedTokens)
		fmt.Printf("Saved Tokens:     %d\n", ss.SavedTokens)
		if ss.OriginalTokens > 0 {
			fmt.Printf("Savings:          %.2f%%\n",
				float64(ss.SavedTokens)/float64(ss.OriginalTokens)*100)
		}
	} else {
		if savingsFlags.jsonOut {
			fmt.Println("{}")
		} else {
			fmt.Printf("No savings data for server: %s\n", serverName)
		}
	}
}
